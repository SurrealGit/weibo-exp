package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/mailnotify"
	"github.com/SurrealGit/weibo-exp/internal/session"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

// renewServer plays m.weibo.cn and the passport visitor system on one host.
// valid maps a SUB value to the UID that /api/config reports for it; the
// passport accepts only the current ALC, once, like the real single-use token.
type renewServer struct {
	*httptest.Server
	mu       sync.Mutex
	valid    map[string]string
	alc      string
	passport int
	onTasks  func()
}

func newRenewServer(t *testing.T, valid map[string]string) *renewServer {
	t.Helper()
	s := &renewServer{valid: valid, alc: "alc-1"}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

func (s *renewServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cookie := func(name string) string {
		if c, err := r.Cookie(name); err == nil {
			return c.Value
		}
		return ""
	}
	switch r.URL.Path {
	case "/visitor/visitor":
		s.passport++
		fmt.Fprint(w, `<script>var request_id = "req1"; postData('/visitor/genvisitor2', 'cb=visitor_gray_callback&ver=20250916&request_id='+request_id)</script>`)
	case "/visitor/genvisitor2":
		s.passport++
		alt := ""
		if s.alc != "" && cookie("ALC") == s.alc {
			alt, s.alc = "alt-1", ""
		}
		fmt.Fprintf(w, `visitor_gray_callback({"retcode":20000000,"data":{"alt":%q}});`, alt)
	case "/sso/v2/login":
		s.passport++
		s.alc = "alc-2"
		http.SetCookie(w, &http.Cookie{Name: "SUB", Value: "sub-new", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "ALC", Value: "alc-2", Path: "/"})
		http.Redirect(w, r, r.URL.Query().Get("url"), http.StatusFound)
	case "/":
		fmt.Fprint(w, "home")
	case "/api/config":
		if uid, ok := s.valid[cookie("SUB")]; ok {
			fmt.Fprintf(w, `{"ok":1,"data":{"login":true,"uid":%q,"st":"st"}}`, uid)
		} else {
			fmt.Fprint(w, `{"ok":1,"data":{"login":false}}`)
		}
	default:
		if s.onTasks != nil {
			s.onTasks()
			s.onTasks = nil
		}
		fmt.Fprint(w, `{"ok":1,"data":{"cards":[{"title_sub":"示例超话","scheme":"https://m.weibo.cn/p/index?containerid=100808aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","itemid":"follow_super_follow_x","buttons":[{"name":"已签到"}]}]}}`)
	}
}

func (s *renewServer) passportRequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.passport
}

func (s *renewServer) setOnTasks(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onTasks = f
}

func renewJar(t *testing.T, server *renewServer, sub, alc string, renewedAt time.Time) *session.Jar {
	t.Helper()
	u, _ := url.Parse(server.URL)
	jar, err := session.New([]session.StoredCookie{
		{Name: "SUB", Value: sub, Domain: u.Hostname(), Path: "/", HostOnly: true},
		{Name: "ALC", Value: alc, Domain: u.Hostname(), Path: "/", HostOnly: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	jar.SetRenewedAt(renewedAt)
	return jar
}

func jarCookie(jar *session.Jar, rawURL, name string) string {
	u, _ := url.Parse(rawURL)
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

// scheduledRenewFixture prepares a data directory whose scheduled run reaches
// the task phase: bound UID "test", start time 00:00, no interactions.
func scheduledRenewFixture(t *testing.T, server *renewServer, sub, alc string, renewedAt time.Time) app.Paths {
	t.Helper()
	paths, _ := mailFixture(t)
	cfg := app.DefaultConfig()
	cfg.ScheduledTime = "00:00"
	cfg.CommentLimit, cfg.RepostLimit = 0, 0
	if err := app.SaveConfig(paths.Config, cfg); err != nil {
		t.Fatal(err)
	}
	state, err := app.LoadState(paths.State)
	if err != nil {
		t.Fatal(err)
	}
	state.UID = "test"
	if err := app.SaveState(paths.State, state); err != nil {
		t.Fatal(err)
	}
	if err := session.Save(paths.Session, renewJar(t, server, sub, alc, renewedAt)); err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestScheduledRunRenewsExpiredSession(t *testing.T) {
	server := newRenewServer(t, map[string]string{"sub-new": "test"})
	paths := scheduledRenewFixture(t, server, "sub-old", "alc-1", time.Now().Add(-time.Hour))
	var checked atomic.Bool
	server.setOnTasks(func() {
		checked.Store(true)
		saved, err := session.Load(paths.Session)
		if err != nil || jarCookie(saved, server.URL, "SUB") != "sub-new" {
			t.Errorf("任务开始前续期后的会话未保存: err=%v", err)
		}
	})
	var subjects []string
	mockMail(t, func(_ context.Context, _ mailnotify.Config, subject, _ string) error {
		subjects = append(subjects, subject)
		return nil
	})
	if err := scheduledCommand(context.Background(), paths, server.URL, server.URL); err != nil {
		t.Fatalf("续期后任务应成功: %v", err)
	}
	if !checked.Load() {
		t.Fatal("定时任务没有进入任务阶段，无法确认续期后的会话已提前保存")
	}
	saved, err := session.Load(paths.Session)
	if err != nil {
		t.Fatal(err)
	}
	if jarCookie(saved, server.URL, "SUB") != "sub-new" || jarCookie(saved, server.URL, "ALC") != "alc-2" {
		t.Fatalf("会话未换成续期结果: %#v", saved.Snapshot())
	}
	if time.Since(saved.RenewedAt()) > time.Minute {
		t.Fatalf("renewed_at 未更新: %v", saved.RenewedAt())
	}
	for _, subject := range subjects {
		if strings.Contains(subject, "登录状态已失效") {
			t.Fatalf("续期成功后不应发送登录失效邮件: %v", subjects)
		}
	}
	logData, _ := os.ReadFile(filepath.Join(paths.LogDir, "scheduled.log"))
	if !bytes.Contains(logData, []byte("登录状态：已自动续期。")) {
		t.Fatalf("定时日志缺少续期记录:\n%s", logData)
	}
}

func TestScheduledRunRenewalFailureSendsOneLoginNotice(t *testing.T) {
	server := newRenewServer(t, map[string]string{})
	paths := scheduledRenewFixture(t, server, "sub-old", "alc-used", time.Now().Add(-time.Hour))
	logins := 0
	mockMail(t, func(_ context.Context, _ mailnotify.Config, subject, _ string) error {
		if strings.Contains(subject, "登录状态已失效") {
			logins++
		}
		return nil
	})
	for i := 0; i < 2; i++ {
		if err := scheduledCommand(context.Background(), paths, server.URL, server.URL); !errors.Is(err, weibo.ErrNotLoggedIn) {
			t.Fatalf("续期失败后应报告登录失效，实际 %v", err)
		}
	}
	if logins != 1 {
		t.Fatalf("登录失效邮件应只发送一封，实际 %d", logins)
	}
	logData, _ := os.ReadFile(filepath.Join(paths.LogDir, "scheduled.log"))
	if !bytes.Contains(logData, []byte("登录状态：已失效，自动续期未成功")) {
		t.Fatalf("定时日志缺少续期失败原因:\n%s", logData)
	}
}

func renewClient(server *renewServer) *weibo.Client {
	client := weibo.NewClient(server.URL, &http.Client{Timeout: 5 * time.Second})
	client.PassportURL = server.URL
	return client
}

func TestEnsureSessionProactiveRenewal(t *testing.T) {
	server := newRenewServer(t, map[string]string{"sub-old": "test", "sub-new": "test"})
	state := app.State{UID: "test"}
	now := time.Now()
	var output strings.Builder
	var saved []*session.Jar
	r := renewal{Now: now, Output: &output, Save: func(jar *session.Jar) error { saved = append(saved, jar); return nil }}

	disabled := weibo.NewClient(server.URL, &http.Client{Timeout: 5 * time.Second})
	due := renewJar(t, server, "sub-old", "alc-1", now.Add(-25*time.Hour))
	if got := ensureSession(context.Background(), disabled, due, state, r); got != due || server.passportRequests() != 0 {
		t.Fatal("未配置 PassportURL 时不应续期")
	}
	recent := renewJar(t, server, "sub-old", "alc-1", now.Add(-23*time.Hour))
	if got := ensureSession(context.Background(), renewClient(server), recent, state, r); got != recent || server.passportRequests() != 0 {
		t.Fatal("距上次续期不足 24 小时时不应续期")
	}
	got := ensureSession(context.Background(), renewClient(server), due, state, r)
	if got == due || jarCookie(got, server.URL, "SUB") != "sub-new" || !got.RenewedAt().Equal(now) {
		t.Fatalf("主动续期结果错误: %#v", got.Snapshot())
	}
	if len(saved) != 1 || saved[0] != got || !strings.Contains(output.String(), "登录状态：已自动续期。") {
		t.Fatalf("续期后应立即保存一次: saved=%d output=%s", len(saved), output.String())
	}
}

func TestEnsureSessionFailedProactiveRenewalWaitsADay(t *testing.T) {
	server := newRenewServer(t, map[string]string{"sub-old": "test"})
	now := time.Now()
	jar := renewJar(t, server, "sub-old", "alc-used", now.Add(-25*time.Hour))
	var output strings.Builder
	r := renewal{Now: now, Output: &output, Save: func(*session.Jar) error {
		t.Fatal("续期失败时不应保存新会话")
		return nil
	}}
	if got := ensureSession(context.Background(), renewClient(server), jar, app.State{UID: "test"}, r); got != jar {
		t.Fatal("续期失败时应返回原会话")
	}
	if !jar.RenewedAt().Equal(now) {
		t.Fatalf("续期尝试时间未记录: %v", jar.RenewedAt())
	}
	if !strings.Contains(output.String(), "提示：登录自动续期未成功") || !strings.Contains(output.String(), weibo.ErrRenewUnavailable.Error()) {
		t.Fatalf("续期失败提示错误: %s", output.String())
	}
	requests := server.passportRequests()
	r.Now = now.Add(4 * time.Hour)
	ensureSession(context.Background(), renewClient(server), jar, app.State{UID: "test"}, r)
	if server.passportRequests() != requests {
		t.Fatal("主动续期失败后 24 小时内不应再次请求 passport")
	}
}

func TestEnsureSessionRejectsOtherAccount(t *testing.T) {
	server := newRenewServer(t, map[string]string{"sub-new": "other"})
	now := time.Now()
	jar := renewJar(t, server, "sub-old", "alc-1", now.Add(-time.Hour))
	var output strings.Builder
	r := renewal{Now: now, Output: &output, Save: func(*session.Jar) error {
		t.Fatal("账户不一致时不应保存新会话")
		return nil
	}}
	if got := ensureSession(context.Background(), renewClient(server), jar, app.State{UID: "test"}, r); got != jar {
		t.Fatal("账户不一致时应保留原会话")
	}
	if !strings.Contains(output.String(), "登录状态：已失效，自动续期未成功") {
		t.Fatalf("账户不一致的提示错误: %s", output.String())
	}
}

func TestDryRunNeverRenews(t *testing.T) {
	server := newRenewServer(t, map[string]string{"sub-new": "test"})
	paths := scheduledRenewFixture(t, server, "sub-old", "alc-1", time.Time{})
	args := []string{"--data-dir", paths.DataDir, "--base-url", server.URL, "--passport-url", server.URL, "run", "--dry-run"}
	_ = runCLI(context.Background(), args, strings.NewReader(""), io.Discard, io.Discard)
	if server.passportRequests() != 0 {
		t.Fatal("dry-run 不应请求 passport")
	}
	saved, err := session.Load(paths.Session)
	if err != nil {
		t.Fatal(err)
	}
	if jarCookie(saved, server.URL, "SUB") != "sub-old" || jarCookie(saved, server.URL, "ALC") != "alc-1" {
		t.Fatalf("dry-run 修改了会话: %#v", saved.Snapshot())
	}
}
