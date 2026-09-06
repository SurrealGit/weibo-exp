package weibo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/session"
)

func TestClientSendsExpectedWriteRequests(t *testing.T) {
	const userAgent = "WeiboExp-Test/1.0"
	var mu sync.Mutex
	forms := make(map[string]url.Values)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != userAgent || r.Header.Get("User-UserAgent") != "" {
			t.Errorf("HTTP User-Agent 请求头错误：%v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			if got := r.Header.Get("X-Requested-With"); got != "XMLHttpRequest" {
				t.Errorf("缺少 X-Requested-With：%q", got)
			}
			_ = r.ParseForm()
			mu.Lock()
			forms[r.URL.Path] = r.PostForm
			mu.Unlock()
		}
		switch r.URL.Path {
		case "/api/config":
			fmt.Fprint(w, `{"ok":1,"data":{"login":true,"st":"token","uid":"42"}}`)
		case "/api/comments/create":
			fmt.Fprint(w, `{"ok":1,"data":{"idstr":"comment-created"}}`)
		case "/comments/destroy":
			fmt.Fprint(w, `{"ok":1,"data":{}}`)
		case "/api/statuses/repost":
			fmt.Fprint(w, `{"ok":1,"data":{"id":"repost-created"}}`)
		case "/profile/delMyblog":
			fmt.Fprint(w, `{"ok":1,"data":{}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	client.UserAgent = userAgent
	status, err := client.LoginStatus(context.Background())
	if err != nil || status.ST != "token" {
		t.Fatalf("登录状态错误: %#v %v", status, err)
	}
	commentID, err := client.Comment(context.Background(), "100", "打卡", "token")
	if err != nil || commentID != "comment-created" {
		t.Fatalf("评论请求失败: %q %v", commentID, err)
	}
	if err := client.DeleteComment(context.Background(), commentID, "token"); err != nil {
		t.Fatal(err)
	}
	repostID, err := client.Repost(context.Background(), "200", "token")
	if err != nil || repostID != "repost-created" {
		t.Fatalf("转发请求失败: %q %v", repostID, err)
	}
	if err := client.DeleteRepost(context.Background(), repostID, "token"); err != nil {
		t.Fatal(err)
	}

	if forms["/api/comments/create"].Get("mid") != "100" || forms["/api/comments/create"].Get("content") != "打卡" {
		t.Fatalf("评论表单错误: %#v", forms["/api/comments/create"])
	}
	if forms["/comments/destroy"].Get("cid") != "comment-created" {
		t.Fatalf("删除评论表单错误: %#v", forms["/comments/destroy"])
	}
	if forms["/api/statuses/repost"].Get("dualPost") != "0" || forms["/api/statuses/repost"].Get("id") != "200" {
		t.Fatalf("转发表单错误: %#v", forms["/api/statuses/repost"])
	}
	if forms["/profile/delMyblog"].Get("mid") != "repost-created" {
		t.Fatalf("删除转发表单错误: %#v", forms["/profile/delMyblog"])
	}
}

func TestTopicPostsRetriesTransientEmptyFirstPage(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			fmt.Fprint(w, `{"ok":1,"data":{"cards":[],"cardlistInfo":{"since_id":0}}}`)
			return
		}
		fmt.Fprint(w, `{"ok":1,"data":{"cards":[{"mblog":{"mid":"123","user":{"id":"456"}}}],"cardlistInfo":{"since_id":0}}}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	posts, err := client.TopicPosts(context.Background(), "topic", "self", 1, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(posts) != 1 || posts[0].MID != "123" {
		t.Fatalf("空响应重试结果错误：requests=%d posts=%#v", requests, posts)
	}
}

func TestTopicPostsAppliesLimitAfterEligibility(t *testing.T) {
	for _, pages := range []int{8, 16, 32} {
		t.Run(strconv.Itoa(pages), func(t *testing.T) {
			requests := 0
			var excluded []string
			for i := 1; i <= 80; i += 2 {
				excluded = append(excluded, strconv.Itoa(i))
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				page, _ := strconv.Atoi(r.URL.Query().Get("since_id"))
				if page == 0 {
					page = 1
				}
				cards := []any{}
				for i := 1; i <= 10; i++ {
					uid := "other"
					if page <= 8 && i%2 == 0 {
						uid = "self"
					}
					cards = append(cards, map[string]any{"mblog": map[string]any{
						"mid": strconv.Itoa((page-1)*10 + i), "user": map[string]any{"id": uid},
					}})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": 1, "data": map[string]any{
					"cards": cards, "cardlistInfo": map[string]any{"since_id": strconv.Itoa(page + 1)},
				}})
			}))
			defer server.Close()
			client := NewClient(server.URL, server.Client())
			posts, err := client.TopicPosts(context.Background(), "topic", "self", pages, 6, excluded)
			wantCount, wantRequests := 6, 9
			if pages == 8 {
				wantCount, wantRequests = 0, 8
			}
			if err != nil || len(posts) != wantCount || requests != wantRequests {
				t.Fatalf("requests=%d posts=%v err=%v", requests, posts, err)
			}
			if len(posts) > 0 && (posts[0].MID != "81" || posts[5].MID != "86") {
				t.Fatalf("ineligible posts counted: %v", posts)
			}
		})
	}
}

func TestHTTPErrorOmitsSignedQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	err := client.Checkin(context.Background(), "/api/container/button?sign=secret")
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "GET /api/container/button: HTTP 403") {
		t.Fatalf("HTTP 错误未正确脱敏：%v", err)
	}
}

func TestQRLoginWithoutBrowser(t *testing.T) {
	var server *httptest.Server
	checkCount := 0
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sso/signin":
			http.SetCookie(w, &http.Cookie{Name: "X-CSRF-TOKEN", Value: "csrf", Path: "/"})
			fmt.Fprint(w, "signin")
		case "/sso/v2/qrcode/image":
			if r.Header.Get("X-CSRF-TOKEN") != "csrf" {
				t.Errorf("二维码请求缺少 CSRF")
			}
			fmt.Fprintf(w, `{"retcode":20000000,"data":{"qrid":"qr-1","image":%q}}`, server.URL+"/qr.png")
		case "/qr.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("fake png"))
		case "/sso/v2/qrcode/check":
			checkCount++
			if checkCount <= 2 {
				fmt.Fprint(w, `{"retcode":50114002,"msg":"scanned"}`)
			} else {
				fmt.Fprintf(w, `{"retcode":20000000,"data":{"url":%q}}`, server.URL+"/cross")
			}
		case "/cross":
			http.SetCookie(w, &http.Cookie{Name: "SUB", Value: "authenticated", Path: "/"})
			fmt.Fprint(w, "ok")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	jar, err := session.Empty()
	if err != nil {
		t.Fatal(err)
	}
	opened := false
	qrDir := filepath.Join(t.TempDir(), "login-tmp")
	var output strings.Builder
	err = QRLogin(context.Background(), jar, QRLoginOptions{
		TempDir:     qrDir,
		PassportURL: server.URL,
		RedirectURL: server.URL + "/",
		PollEvery:   time.Millisecond,
		Timeout:     time.Second,
		Output:      &output,
		OpenImage: func(path string) error {
			opened = true
			if filepath.Dir(path) != qrDir {
				t.Errorf("unexpected QR directory: %s", path)
			}
			if _, err := os.Stat(path); err != nil {
				t.Errorf("二维码临时文件不存在: %v", err)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(qrDir); !os.IsNotExist(err) {
		t.Fatalf("QR directory not cleaned: %v", err)
	}
	if !opened || !jar.Has("SUB") {
		t.Fatalf("扫码登录结果错误: opened=%v cookies=%#v", opened, jar.Snapshot())
	}
	if strings.Count(output.String(), "扫码状态：已扫描") != 1 {
		t.Fatalf("扫码状态应只输出一次：\n%s", output.String())
	}
	for _, expected := range []string{"登录方式：", "二维码文件：", "扫码状态：确认成功。"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("登录输出缺少 %q：\n%s", expected, output.String())
		}
	}
}
