package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/mailnotify"
	"github.com/SurrealGit/weibo-exp/internal/schedule"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

func TestStatusMailNotification(t *testing.T) {
	old := inspectSchedule
	t.Cleanup(func() { inspectSchedule = old })
	inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) { return schedule.InstallStatus{}, nil }
	mockMail(t, func(context.Context, mailnotify.Config, string, string) error {
		t.Fatal("status attempted to send mail")
		return nil
	})
	for _, tc := range []struct{ state, label string }{
		{"unconfigured", "未配置"}, {"disabled", "已配置，未启用"},
		{"enabled", "已启用"}, {"invalid", "配置异常"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			paths, cfg := mailFixture(t)
			path := filepath.Join(paths.DataDir, mailnotify.ConfigFile)
			if tc.state == "unconfigured" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				cfg.Enabled = tc.state == "enabled"
				data, err := json.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				if tc.state == "invalid" {
					data = []byte(`{"password":"SECRET"`)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			for _, loggedIn := range []bool{true, false} {
				for _, asJSON := range []bool{false, true} {
					client := weibo.NewClient("https://unit.test", &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
						if r.URL.Host != "unit.test" || r.Method != "GET" {
							t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
						}
						body := `{"ok":1,"data":{"cards":[]}}`
						if r.URL.Path == "/api/config" {
							body = `{"ok":1,"data":{"login":true,"uid":"self","st":"fixture"}}`
							if !loggedIn {
								body = `{"ok":-100}`
							}
						}
						return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
					})})
					var output bytes.Buffer
					var args []string
					if asJSON {
						args = []string{"--json"}
					}
					err := statusCommand(context.Background(), paths, app.DefaultConfig(), client, args, &output, io.Discard)
					if (loggedIn && err != nil) || (!loggedIn && !errors.Is(err, weibo.ErrNotLoggedIn)) {
						t.Fatalf("unexpected error: %v", err)
					}
					if asJSON {
						var result map[string]any
						if err := json.Unmarshal(output.Bytes(), &result); err != nil {
							t.Fatal(err)
						}
						if result["mail_notification_status"] != tc.state {
							t.Fatal(result)
						}
					} else {
						want := "邮件通知：" + tc.label
						if tc.state == "invalid" {
							want += "，执行 " + commandFor(paths, "notify status") + " 查看详情"
						}
						if !strings.Contains(output.String(), "定时任务状态：未安装\n"+want+"\n") {
							t.Fatal(output.String())
						}
					}
					for _, secret := range []string{cfg.Password, cfg.Username, cfg.Host, "SECRET"} {
						if strings.Contains(output.String(), secret) {
							t.Fatal("mail configuration leaked")
						}
					}
				}
			}
			after, err := os.ReadFile(path)
			if !bytes.Equal(before, after) || (tc.state == "unconfigured" && !errors.Is(err, os.ErrNotExist)) {
				t.Fatal("status modified SMTP configuration")
			}
			if _, err := os.Stat(filepath.Join(paths.DataDir, mailnotify.StateFile)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("status created notification state")
			}
		})
	}
}
