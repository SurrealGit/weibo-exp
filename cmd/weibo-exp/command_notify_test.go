package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/mailnotify"
	"github.com/SurrealGit/weibo-exp/internal/runner"
	"github.com/SurrealGit/weibo-exp/internal/storage"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

func mailFixture(t *testing.T) (app.Paths, mailnotify.Config) {
	t.Helper()
	paths, err := app.ResolvePaths(filepath.Join(t.TempDir(), "custom data"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := mailnotify.Config{Enabled: true, Host: "smtp.example.com", Port: 465, Security: "tls", Username: "sender@example.com", Password: "secret-password", From: "sender@example.com", To: "recipient@example.com"}
	if err := storage.WriteJSON(filepath.Join(paths.DataDir, mailnotify.ConfigFile), cfg, 0600); err != nil {
		t.Fatal(err)
	}
	return paths, cfg
}

func mockMail(t *testing.T, send func(context.Context, mailnotify.Config, string, string) error) {
	t.Helper()
	old := sendNotification
	sendNotification = send
	t.Cleanup(func() { sendNotification = old })
}

func TestNotifyConfigurationAndCommands(t *testing.T) {
	paths, cfg := mailFixture(t)
	source := filepath.Join(t.TempDir(), "source.json")
	if err := storage.WriteJSON(source, cfg, 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(paths.DataDir, mailnotify.ConfigFile)
	statePath := filepath.Join(paths.DataDir, mailnotify.StateFile)
	os.WriteFile(statePath, []byte(`{"sent":{"login":"2026-10-01"}}`), 0600)
	count := 0
	mockMail(t, func(context.Context, mailnotify.Config, string, string) error { count++; return nil })
	var output bytes.Buffer
	call := func(args ...string) error {
		return notifyCommand(context.Background(), paths, args, &output, &output)
	}
	if err := call("configure", "--file", source); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("configuration sent mail")
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("configuration did not clear dedup state")
	}
	original, _ := os.ReadFile(configPath)
	if !bytes.Contains(original, []byte(cfg.Password)) {
		t.Fatal("credential not stored")
	}
	if err := call("status"); err != nil || !strings.Contains(output.String(), cfg.To) || strings.Contains(output.String(), cfg.Password) {
		t.Fatalf("unsafe status: %v", err)
	}
	if !strings.Contains(output.String(), "提醒内容：任务异常、每日完成") || !strings.Contains(output.String(), "每日完成最多一封") {
		t.Fatal("status omitted completion notifications")
	}
	if !strings.Contains(output.String(), commandFor(paths, "notify test")) {
		t.Fatal("custom data dir absent from guidance")
	}
	os.WriteFile(source, []byte(`{"password":"secret-password",`), 0600)
	if err := call("configure", "--file", source); err == nil || strings.Contains(err.Error(), cfg.Password) {
		t.Fatal("invalid configuration accepted or leaked")
	}
	after, _ := os.ReadFile(configPath)
	if !bytes.Equal(original, after) {
		t.Fatal("invalid import changed existing configuration")
	}
	if err := call("disable"); err != nil {
		t.Fatal(err)
	}
	saved, err := loadMailConfig(configPath)
	if err != nil || saved.Enabled || saved.Password != cfg.Password {
		t.Fatal("disable did not preserve credentials")
	}
	if err := call("test"); err != nil || count != 1 {
		t.Fatalf("explicit test did not send: %v", err)
	}
	saved, err = loadMailConfig(configPath)
	if err != nil || saved.Enabled {
		t.Fatal("test enabled automatic notifications")
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("test modified notification state")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("source removed")
	}
	if strings.Contains(output.String(), cfg.Password) {
		t.Fatal("credential leaked")
	}
}

func TestNotifyConfigurePreservesSwitch(t *testing.T) {
	for _, mode := range []string{"first", "enabled", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			paths, cfg := mailFixture(t)
			configPath := filepath.Join(paths.DataDir, mailnotify.ConfigFile)
			enabled := mode == "enabled"
			if mode == "first" {
				if err := os.Remove(configPath); err != nil {
					t.Fatal(err)
				}
			} else {
				cfg.Enabled = enabled
				if err := storage.WriteJSON(configPath, cfg, 0600); err != nil {
					t.Fatal(err)
				}
			}
			// Even an explicitly opposite switch in the source cannot toggle it.
			cfg.Enabled = !enabled
			cfg.To = "updated@example.com"
			source := filepath.Join(t.TempDir(), "smtp.json")
			if err := storage.WriteJSON(source, cfg, 0600); err != nil {
				t.Fatal(err)
			}
			mockMail(t, func(context.Context, mailnotify.Config, string, string) error {
				t.Fatal("configure sent mail")
				return nil
			})
			var output bytes.Buffer
			if err := notifyCommand(context.Background(), paths, []string{"configure", "--file", source}, &output, io.Discard); err != nil {
				t.Fatal(err)
			}
			saved, err := loadMailConfig(configPath)
			if err != nil || saved.Enabled != enabled || saved.To != cfg.To {
				t.Fatalf("import failed to preserve switch: mode=%s err=%v", mode, err)
			}
			if !enabled && !strings.Contains(output.String(), commandFor(paths, "notify enable")) {
				t.Fatal("missing custom-directory enable guidance")
			}
		})
	}
}

func TestNotifySecurityAliases(t *testing.T) {
	for _, tc := range []struct{ security, label string }{
		{"ssl", "SSL/TLS（连接时加密）"},
		{" SSL ", "SSL/TLS（连接时加密）"},
		{" SSL/TLS ", "SSL/TLS（连接时加密）"},
		{"tls", "SSL/TLS（连接时加密）"},
		{" TLS ", "SSL/TLS（连接时加密）"},
		{" STARTTLS ", "STARTTLS（连接后升级加密）"},
	} {
		t.Run(tc.security, func(t *testing.T) {
			paths, cfg := mailFixture(t)
			cfg.Security = tc.security
			source := filepath.Join(t.TempDir(), "source.json")
			if err := storage.WriteJSON(source, cfg, 0600); err != nil {
				t.Fatal(err)
			}
			var sent int
			mockMail(t, func(_ context.Context, actual mailnotify.Config, _, _ string) error {
				sent++
				if actual != cfg {
					t.Fatal("configuration changed before sending")
				}
				return nil
			})
			var output bytes.Buffer
			for _, args := range [][]string{{"configure", "--file", source}, {"status"}, {"test"}} {
				if err := notifyCommand(context.Background(), paths, args, &output, io.Discard); err != nil {
					t.Fatal(err)
				}
			}
			if sent != 1 || !strings.Contains(output.String(), "加密方式："+tc.label) || strings.Contains(output.String(), cfg.Password) {
				t.Fatal("unexpected sends, status label or exposed credentials")
			}
			configPath := filepath.Join(paths.DataDir, mailnotify.ConfigFile)
			before, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Security = "plain"
			if err := storage.WriteJSON(source, cfg, 0600); err != nil {
				t.Fatal(err)
			}
			err = notifyCommand(context.Background(), paths, []string{"configure", "--file", source}, io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "ssl（连接时加密") || !strings.Contains(err.Error(), "starttls（连接后升级加密") {
				t.Fatalf("missing configuration guidance: %v", err)
			}
			after, err := os.ReadFile(configPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("invalid security overwrote configuration")
			}
		})
	}
}

func TestNotifyEnableDisablePreserveDedup(t *testing.T) {
	paths, original := mailFixture(t)
	count := 0
	mockMail(t, func(context.Context, mailnotify.Config, string, string) error { count++; return nil })
	now := time.Now()
	notifyScheduledFailure(context.Background(), paths, weibo.ErrNotLoggedIn, io.Discard, now)
	statePath := filepath.Join(paths.DataDir, mailnotify.StateFile)
	before, err := os.ReadFile(statePath)
	if err != nil || count != 1 {
		t.Fatalf("first notification failed: %v", err)
	}
	for _, command := range []string{"disable", "disable", "enable", "enable"} {
		if err := notifyCommand(context.Background(), paths, []string{command}, io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
		saved, err := loadMailConfig(filepath.Join(paths.DataDir, mailnotify.ConfigFile))
		expected := original
		expected.Enabled = command == "enable"
		if err != nil || saved != expected {
			t.Fatalf("toggle changed SMTP settings: %s err=%v", command, err)
		}
		notifyScheduledFailure(context.Background(), paths, weibo.ErrNotLoggedIn, io.Discard, now)
		after, err := os.ReadFile(statePath)
		if err != nil || !bytes.Equal(before, after) || count != 1 {
			t.Fatalf("%s reset dedup or resent mail: err=%v sends=%d", command, err, count)
		}
	}
}

func TestNotifyEnableRequiresValidConfig(t *testing.T) {
	paths, _ := mailFixture(t)
	configPath := filepath.Join(paths.DataDir, mailnotify.ConfigFile)
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	mockMail(t, func(context.Context, mailnotify.Config, string, string) error {
		t.Fatal("toggle sent mail")
		return nil
	})
	err := notifyCommand(context.Background(), paths, []string{"enable"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), commandFor(paths, "notify configure --file FILE")) {
		t.Fatalf("missing configure guidance: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := notifyCommand(context.Background(), paths, []string{"disable"}, io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("toggle without configuration created a file")
	}
	invalid := []byte(`{"host":"smtp.example.com","password":"secret-password"}`)
	if err := os.WriteFile(configPath, invalid, 0600); err != nil {
		t.Fatal(err)
	}
	err = notifyCommand(context.Background(), paths, []string{"enable"}, io.Discard, io.Discard)
	if err == nil || strings.Contains(err.Error(), "secret-password") {
		t.Fatal("invalid configuration enabled or leaked")
	}
	after, _ := os.ReadFile(configPath)
	if !bytes.Equal(invalid, after) {
		t.Fatal("failed enable modified configuration")
	}
}

func TestNotificationDedupAndClassification(t *testing.T) {
	paths, _ := mailFixture(t)
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.Local)
	count := 0
	var subjects []string
	mockMail(t, func(_ context.Context, _ mailnotify.Config, subject, body string) error {
		count++
		subjects = append(subjects, subject)
		if strings.Contains(body, "SECRET") || !strings.Contains(body, "--data-dir") {
			t.Fatal("mail leaked raw error or lost custom directory")
		}
		return nil
	})
	var output bytes.Buffer
	call := func(err error, when time.Time) {
		notifyScheduledFailure(context.Background(), paths, err, &output, when)
	}
	call(nil, now)
	call(context.Canceled, now)
	if count != 0 {
		t.Fatal("notified for success or cancellation")
	}
	call(fmt.Errorf("SECRET: %w", weibo.ErrNotLoggedIn), now)
	call(weibo.ErrNotLoggedIn, now.Add(time.Hour))
	call(errors.New("HTTP 403 SECRET"), now)
	call(errors.New("network SECRET"), now)
	if count != 2 || !strings.Contains(subjects[0], "登录状态") || strings.Contains(subjects[1], "登录状态") {
		t.Fatal("wrong daily category dedup")
	}
	state := app.NewState()
	state.AddPendingDelete(app.PendingDelete{Kind: "repost", SourcePostID: "1001"})
	if err := app.SaveState(paths.State, state); err != nil {
		t.Fatal(err)
	}
	call(errors.New("SECRET"), now)
	call(errors.New("SECRET"), now)
	call(weibo.ErrNotLoggedIn, now)
	if count != 3 || !strings.Contains(subjects[2], "人工核查") {
		t.Fatal("manual category or login priority incorrect")
	}
	call(weibo.ErrNotLoggedIn, now.AddDate(0, 0, 1))
	if count != 4 {
		t.Fatal("no next day notification")
	}
	state.PendingDeletes[0].ContentID = "2001"
	app.SaveState(paths.State, state)
	category, _, body := failureMail(paths, errors.New("failure"), now)
	if category != "other" || !strings.Contains(body, "cleanup") {
		t.Fatal("known delete recovery guidance missing")
	}
}

func TestNotificationSendAndStateFailures(t *testing.T) {
	paths, cfg := mailFixture(t)
	count := 0
	mockMail(t, func(context.Context, mailnotify.Config, string, string) error {
		count++
		if count == 1 {
			return errors.New("模拟发送失败")
		}
		return nil
	})
	var output bytes.Buffer
	now := time.Now()
	for i := 0; i < 3; i++ {
		notifyScheduledFailure(context.Background(), paths, weibo.ErrNotLoggedIn, &output, now)
	}
	if count != 2 {
		t.Fatal("failed send consumed dedup allowance")
	}
	statePath := filepath.Join(paths.DataDir, mailnotify.StateFile)
	os.WriteFile(statePath, []byte("invalid SECRET"), 0600)
	notifyScheduledFailure(context.Background(), paths, weibo.ErrNotLoggedIn, &output, now)
	if count != 2 || strings.Contains(output.String(), "SECRET") {
		t.Fatal("invalid state leaked or sent")
	}
	cfg.Enabled = false
	storage.WriteJSON(filepath.Join(paths.DataDir, mailnotify.ConfigFile), cfg, 0600)
	notifyScheduledFailure(context.Background(), paths, errors.New("failure"), &output, now)
	if count != 2 {
		t.Fatal("disabled notification sent")
	}
}

func TestScheduledNotificationsPreserveTaskFailure(t *testing.T) {
	paths, _ := mailFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":1,"data":{"login":false}}`)
	}))
	defer server.Close()
	count := 0
	mockMail(t, func(context.Context, mailnotify.Config, string, string) error {
		count++
		return errors.New("模拟 SMTP 故障")
	})
	err := scheduledCommand(context.Background(), paths, server.URL)
	if !errors.Is(err, weibo.ErrNotLoggedIn) || strings.Contains(err.Error(), "SMTP") || count != 1 {
		t.Fatalf("task failure changed: %v; sends=%d", err, count)
	}
	logData, _ := os.ReadFile(filepath.Join(paths.LogDir, "scheduled-error.log"))
	if !bytes.Contains(logData, []byte("邮件通知失败")) || !bytes.Contains(logData, []byte("登录态无效")) {
		t.Fatal("original failure or notification diagnostic absent")
	}
	// A corrupt business configuration still allows an independent mail notice.
	os.WriteFile(paths.Config, []byte("invalid"), 0600)
	err = scheduledCommand(context.Background(), paths, server.URL)
	if err == nil || count != 2 {
		t.Fatal("startup failure not notified")
	}
	release, err := runner.AcquireLock(paths.Lock)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := scheduledCommand(context.Background(), paths, server.URL); err == nil || count != 2 {
		t.Fatal("lock conflict sent mail")
	}
}

func TestNoAutomaticMailForManualCommands(t *testing.T) {
	paths, _ := mailFixture(t)
	mockMail(t, func(context.Context, mailnotify.Config, string, string) error {
		t.Fatal("manual command sent mail")
		return nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":1,"data":{"login":false}}`)
	}))
	defer server.Close()
	for _, command := range [][]string{{"status"}, {"topics"}, {"run", "--yes"}, {"run", "--dry-run"}, {"notify", "status"}} {
		args := append([]string{"--data-dir", paths.DataDir, "--base-url", server.URL}, command...)
		_ = runCLI(context.Background(), args, strings.NewReader(""), io.Discard, io.Discard)
	}
}

func TestScheduledSuccessAndInvalidMailConfig(t *testing.T) {
	paths, _ := mailFixture(t)
	cfg := app.DefaultConfig()
	cfg.ScheduledTime = "00:00"
	cfg.CommentLimit, cfg.RepostLimit = 0, 0
	if err := app.SaveConfig(paths.Config, cfg); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/config" {
			fmt.Fprint(w, `{"ok":1,"data":{"login":true,"uid":"test","st":"test"}}`)
		} else {
			fmt.Fprint(w, `{"ok":1,"data":{"cards":[{"title_sub":"示例超话","scheme":"https://m.weibo.cn/p/index?containerid=100808aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","itemid":"follow_super_follow_x","buttons":[{"name":"已签到"}]}]}}`)
		}
	}))
	defer server.Close()
	count := 0
	mockMail(t, func(_ context.Context, _ mailnotify.Config, subject, body string) error {
		count++
		if !strings.Contains(subject, "今日任务已完成") || !strings.Contains(body, "完成超话：1/1") {
			t.Fatal("incorrect completion mail")
		}
		if count == 1 {
			return errors.New("模拟 SMTP 故障")
		}
		return nil
	})
	for i := 0; i < 3; i++ {
		if err := scheduledCommand(context.Background(), paths, server.URL); err != nil {
			t.Fatalf("mail error changed successful task: %v", err)
		}
	}
	if count != 2 {
		t.Fatalf("completion retry/dedup failed: sends=%d", count)
	}
	regularLog, _ := os.ReadFile(filepath.Join(paths.LogDir, "scheduled.log"))
	errorLog, _ := os.ReadFile(filepath.Join(paths.LogDir, "scheduled-error.log"))
	if !bytes.Contains(regularLog, []byte("（success）")) || bytes.Contains(errorLog, []byte("（success）")) || !bytes.Contains(errorLog, []byte("邮件通知失败")) {
		t.Fatal("completion and error messages logged to wrong files")
	}
	os.WriteFile(filepath.Join(paths.DataDir, mailnotify.ConfigFile), []byte("invalid SECRET"), 0600)
	if err := scheduledCommand(context.Background(), paths, server.URL); err != nil {
		t.Fatalf("invalid mail config blocked business: %v", err)
	}
	var log bytes.Buffer
	notifyScheduledFailure(context.Background(), paths, errors.New("business failed"), &log, time.Now())
	if !strings.Contains(log.String(), "SMTP 配置") || strings.Contains(log.String(), "SECRET") {
		t.Fatal("invalid config diagnostic unsafe or missing")
	}
}

func TestCompletionNotificationDailyRules(t *testing.T) {
	paths, _ := mailFixture(t)
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.Local)
	var subjects []string
	mockMail(t, func(_ context.Context, _ mailnotify.Config, subject, body string) error {
		subjects = append(subjects, subject)
		if strings.Contains(body, "secret-password") || strings.Contains(body, "SECRET") {
			t.Fatal("sensitive content in mail")
		}
		return nil
	})
	notifyScheduledFailure(context.Background(), paths, weibo.ErrNotLoggedIn, io.Discard, now)
	notifyScheduledSuccess(context.Background(), paths, 3, io.Discard, io.Discard, now)
	notifyScheduledSuccess(context.Background(), paths, 4, io.Discard, io.Discard, now.Add(4*time.Hour))
	if len(subjects) != 2 || !strings.Contains(subjects[1], "今日任务已完成") {
		t.Fatal("recovery or expanded-scope dedup failed")
	}
	for _, action := range []string{"disable", "enable"} {
		if err := notifyCommand(context.Background(), paths, []string{action}, io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
		notifyScheduledSuccess(context.Background(), paths, 3, io.Discard, io.Discard, now)
	}
	if len(subjects) != 2 {
		t.Fatal("toggle reset completion dedup")
	}
	next := now.AddDate(0, 0, 1)
	notifyScheduledSuccess(context.Background(), paths, 0, io.Discard, io.Discard, next)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	notifyScheduledSuccess(ctx, paths, 3, io.Discard, io.Discard, next)
	if len(subjects) != 2 {
		t.Fatal("empty/canceled result sent completion")
	}
	notifyScheduledSuccess(context.Background(), paths, 3, io.Discard, io.Discard, next)
	if len(subjects) != 3 {
		t.Fatal("missing next-day completion")
	}
	if err := notifyCommand(context.Background(), paths, []string{"disable"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	notifyScheduledSuccess(context.Background(), paths, 3, io.Discard, io.Discard, next.AddDate(0, 0, 1))
	if len(subjects) != 3 {
		t.Fatal("disabled completion sent mail")
	}
}

func TestScheduledCompletionOutcomes(t *testing.T) {
	for _, mode := range []string{"checkin", "incomplete", "empty", "early", "manual", "dry-run"} {
		t.Run(mode, func(t *testing.T) {
			paths, _ := mailFixture(t)
			cfg := app.DefaultConfig()
			cfg.ScheduledTime = "00:00"
			cfg.CommentLimit, cfg.RepostLimit = 0, 0
			if mode == "early" {
				if time.Now().Format("15:04") == "23:59" {
					t.Skip("no later daily time available")
				}
				cfg.ScheduledTime = "23:59"
			}
			if err := app.SaveConfig(paths.Config, cfg); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/config":
					fmt.Fprint(w, `{"ok":1,"data":{"login":true,"uid":"test","st":"test"}}`)
				case "/checkin":
					fmt.Fprint(w, `{"ok":1,"data":{}}`)
				default:
					button := "已签到"
					if mode == "checkin" {
						button = "签到"
					} else if mode == "incomplete" {
						button = "查看"
					}
					if mode == "empty" {
						fmt.Fprint(w, `{"ok":1,"data":{"cards":[]}}`)
						return
					}
					fmt.Fprintf(w, `{"ok":1,"data":{"cards":[{"title_sub":"示例超话","scheme":"https://m.weibo.cn/p/index?containerid=100808aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","itemid":"follow_super_follow_x","buttons":[{"name":%q,"scheme":"/checkin"}]}]}}`, button)
				}
			}))
			defer server.Close()
			var subjects []string
			mockMail(t, func(_ context.Context, _ mailnotify.Config, subject, _ string) error {
				subjects = append(subjects, subject)
				return nil
			})
			var err error
			if mode == "manual" || mode == "dry-run" {
				flag := "--yes"
				if mode == "dry-run" {
					flag = "--dry-run"
				}
				err = runCLI(context.Background(), []string{"--data-dir", paths.DataDir, "--base-url", server.URL, "run", flag}, strings.NewReader(""), io.Discard, io.Discard)
			} else {
				err = scheduledCommand(context.Background(), paths, server.URL)
			}
			wantFailure := mode == "incomplete" || mode == "empty"
			if (err != nil) != wantFailure {
				t.Fatalf("unexpected task result: %v", err)
			}
			if mode == "early" || mode == "manual" || mode == "dry-run" {
				if len(subjects) != 0 {
					t.Fatal("skipped or manual task sent mail")
				}
			} else if len(subjects) != 1 || strings.Contains(subjects[0], "今日任务已完成") == wantFailure {
				t.Fatalf("wrong notification: %v", subjects)
			}
		})
	}
}

func TestNotificationStateSaveFailure(t *testing.T) {
	paths, _ := mailFixture(t)
	mockMail(t, func(context.Context, mailnotify.Config, string, string) error {
		return os.Mkdir(filepath.Join(paths.DataDir, mailnotify.StateFile), 0700)
	})
	var log bytes.Buffer
	notifyScheduledFailure(context.Background(), paths, weibo.ErrNotLoggedIn, &log, time.Now())
	if !strings.Contains(log.String(), "保存通知记录失败") {
		t.Fatal("missing duplicate-delivery warning")
	}
}

func TestScheduledManualPendingNotifiesWithoutWeiboRequests(t *testing.T) {
	paths, _ := mailFixture(t)
	state := app.NewState()
	state.AddPendingDelete(app.PendingDelete{Kind: "comment", SourcePostID: "1001"})
	if err := app.SaveState(paths.State, state); err != nil {
		t.Fatal(err)
	}
	count := 0
	mockMail(t, func(_ context.Context, _ mailnotify.Config, subject, body string) error {
		count++
		if !strings.Contains(subject, "人工核查") || !strings.Contains(body, commandFor(paths, "cleanup")) {
			t.Fatal("incorrect manual-recovery email")
		}
		return nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("blocked task made a Weibo request")
	}))
	defer server.Close()
	for i := 0; i < 2; i++ {
		var blocked *manualCheckError
		if err := scheduledCommand(context.Background(), paths, server.URL); !errors.As(err, &blocked) {
			t.Fatalf("manual block lost: %v", err)
		}
	}
	if count != 1 {
		t.Fatal("scheduled notice not deduplicated")
	}
	after, err := app.LoadState(paths.State)
	if err != nil || len(after.PendingDeletes) != 1 {
		t.Fatal("notification modified pending content")
	}
}
