package weibo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/SurrealGit/weibo-exp/internal/session"
)

const visitorPage = `<script>var request_id = "req123";
ufp.util.postData('https://' + window.location.host + '/visitor/genvisitor2', 'cb=visitor_gray_callback&ver=20250916&request_id='+request_id, function (res) {});</script>`

// fakePassport mimics the verified restore flow. reply overrides the
// genvisitor2 body and login overrides the /sso/v2/login handler.
func fakePassport(t *testing.T, reply string, login http.HandlerFunc) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/visitor/visitor":
			if r.URL.Query().Get("entry") != "miniblog" || r.URL.Query().Get("a") != "enter" {
				t.Errorf("访客页面参数错误: %s", r.URL.RawQuery)
			}
			fmt.Fprint(w, visitorPage)
		case "/visitor/genvisitor2":
			if err := r.ParseForm(); err != nil || r.Method != http.MethodPost {
				t.Errorf("访客凭证请求错误: method=%s err=%v", r.Method, err)
			}
			if r.PostForm.Get("request_id") != "req123" || r.PostForm.Get("ver") != "20250916" || r.PostForm.Get("cb") != "visitor_gray_callback" {
				t.Errorf("访客凭证表单错误: %v", r.PostForm)
			}
			if cookie, err := r.Cookie("ALC"); err != nil || cookie.Value != "alc-old" {
				t.Errorf("访客凭证请求未携带 ALC")
			}
			http.SetCookie(w, &http.Cookie{Name: "ALC", Value: "deleted", Path: "/", MaxAge: -1})
			fmt.Fprint(w, reply)
		case "/sso/v2/login":
			if login != nil {
				login(w, r)
				return
			}
			query := r.URL.Query()
			if query.Get("alt") != "alt-secret" || query.Get("source") != "visitor_restore" || query.Get("type") != "3" || query.Get("entry") != "sso" {
				t.Errorf("恢复登录参数错误: %s", r.URL.RawQuery)
			}
			http.SetCookie(w, &http.Cookie{Name: "ALC", Value: "alc-new", Path: "/"})
			http.Redirect(w, r, "/crossdomain?ticket=ticket-secret&url="+url.QueryEscape(query.Get("url")), http.StatusFound)
		case "/crossdomain":
			http.SetCookie(w, &http.Cookie{Name: "SUB", Value: "sub-new", Path: "/"})
			http.Redirect(w, r, r.URL.Query().Get("url"), http.StatusFound)
		case "/m/":
			fmt.Fprint(w, "home")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

const restoreReply = `window.visitor_gray_callback && visitor_gray_callback({"retcode":20000000,"msg":"succ","data":{"alt":"alt-secret","tid":"t"}});`

func baseJar(t *testing.T) *session.Jar {
	t.Helper()
	jar, err := session.New([]session.StoredCookie{
		{Name: "ALC", Value: "alc-old", Domain: "127.0.0.1", Path: "/", HostOnly: true},
		{Name: "SUB", Value: "sub-old", Domain: "weibo.cn", Path: "/", Secure: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return jar
}

func cookieIn(jar http.CookieJar, rawURL, name string) string {
	u, _ := url.Parse(rawURL)
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func TestRenewSessionRestoresLogin(t *testing.T) {
	server := fakePassport(t, restoreReply, nil)
	base := baseJar(t)
	renewed, err := RenewSession(context.Background(), base, RenewOptions{PassportURL: server.URL, ReturnURL: server.URL + "/m/"})
	if err != nil {
		t.Fatal(err)
	}
	if got := cookieIn(renewed, server.URL, "SUB"); got != "sub-new" {
		t.Fatalf("续期后 SUB=%q，期望 sub-new", got)
	}
	if got := cookieIn(renewed, server.URL, "ALC"); got != "alc-new" {
		t.Fatalf("续期后 ALC=%q，期望 alc-new", got)
	}
	if cookieIn(renewed, "https://m.weibo.cn/", "SUB") != "" {
		t.Fatal("续期副本不应保留旧的 weibo.cn SUB")
	}
	if cookieIn(base, server.URL, "ALC") != "alc-old" || cookieIn(base, "https://m.weibo.cn/", "SUB") != "sub-old" {
		t.Fatal("RenewSession 修改了原会话")
	}
}

func TestRenewSessionFailures(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		login http.HandlerFunc
		want  string
	}{
		{name: "no alt", reply: `visitor_gray_callback({"retcode":20000000,"data":{"alt":"","tid":"t"}});`, want: ErrRenewUnavailable.Error()},
		{name: "retcode", reply: `visitor_gray_callback({"retcode":50111261,"msg":"fail"});`, want: "访客凭证 retcode=50111261"},
		{name: "not jsonp", reply: `<html>busy</html>`, want: "访客凭证响应格式已变化"},
		{name: "foreign redirect", reply: restoreReply, login: func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://collector.example/steal?alt=alt-secret", http.StatusFound)
		}, want: "恢复登录: 跳转地址不符合预期"},
		{name: "redirect loop", reply: restoreReply, login: func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/sso/v2/login?alt=alt-secret", http.StatusFound)
		}, want: "恢复登录: 跳转次数过多"},
		{name: "http status", reply: restoreReply, login: func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "ticket-secret", http.StatusServiceUnavailable)
		}, want: "恢复登录 HTTP 503"},
		{name: "transport", reply: restoreReply, login: func(w http.ResponseWriter, r *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
		}, want: "恢复登录: "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := fakePassport(t, tc.reply, tc.login)
			_, err := RenewSession(context.Background(), baseJar(t), RenewOptions{PassportURL: server.URL, ReturnURL: server.URL + "/m/"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误为 %v，期望包含 %q", err, tc.want)
			}
			for _, secret := range []string{"alt-secret", "ticket-secret", "alc-old", "sub-old", "127.0.0.1:", "collector.example"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("错误信息泄露了 %q: %v", secret, err)
				}
			}
		})
	}
	if _, err := RenewSession(context.Background(), baseJar(t), RenewOptions{PassportURL: "http://127.0.0.1:1"}); err == nil {
		t.Fatal("passport 不可达时应返回错误")
	}
}

func TestRenewSessionNoAltIsUnavailable(t *testing.T) {
	server := fakePassport(t, `visitor_gray_callback({"retcode":20000000,"data":{"alt":""}});`, nil)
	_, err := RenewSession(context.Background(), baseJar(t), RenewOptions{PassportURL: server.URL, ReturnURL: server.URL + "/m/"})
	if !errors.Is(err, ErrRenewUnavailable) {
		t.Fatalf("期望 ErrRenewUnavailable，实际 %v", err)
	}
}

func TestRenewSessionRejectsChangedVisitorPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/visitor/visitor" {
			t.Errorf("访客页面格式变化后不应继续请求 %s", r.URL.Path)
		}
		fmt.Fprint(w, "<html>new page</html>")
	}))
	defer server.Close()
	_, err := RenewSession(context.Background(), baseJar(t), RenewOptions{PassportURL: server.URL, ReturnURL: server.URL + "/m/"})
	if err == nil || err.Error() != "访客页面格式已变化" {
		t.Fatalf("错误为 %v", err)
	}
}

func TestWithoutLoginCookies(t *testing.T) {
	kept := withoutLoginCookies([]session.StoredCookie{
		{Name: "SUB", Domain: "weibo.com"}, {Name: "SUBP", Domain: ".weibo.cn"}, {Name: "SUB", Domain: "passport.weibo.com"},
		{Name: "SUB", Domain: "sina.com.cn"}, {Name: "ALC", Domain: "passport.weibo.com"}, {Name: "SCF", Domain: "weibo.cn"},
	})
	var names []string
	for _, cookie := range kept {
		names = append(names, cookie.Name+"@"+cookie.Domain)
	}
	if got := strings.Join(names, ","); got != "SUB@sina.com.cn,ALC@passport.weibo.com,SCF@weibo.cn" {
		t.Fatalf("过滤结果为 %s", got)
	}
}

func TestRenewHostPolicy(t *testing.T) {
	allowed := renewHostPolicy("http://127.0.0.1:8080", "https://m.weibo.cn/")
	for raw, want := range map[string]bool{
		"https://login.sina.com.cn/sso/v2/crossdomain": true,
		"https://passport.weibo.cn/sso/crossdomain":    true,
		"https://m.weibo.cn/":                          true,
		"https://weibo.com/":                           true,
		"http://127.0.0.1:8080/sso/v2/login":           true,
		"http://login.sina.com.cn/":                    false,
		"http://127.0.0.1:9090/":                       false,
		"https://weibo.com.evil.example/":              false,
		"https://evilweibo.com/":                       false,
	} {
		target, _ := url.Parse(raw)
		if allowed(target) != want {
			t.Errorf("%s: allowed=%v，期望 %v", raw, !want, want)
		}
	}
}
