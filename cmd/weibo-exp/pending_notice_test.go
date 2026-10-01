package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/SurrealGit/weibo-exp/internal/logging"
	"github.com/SurrealGit/weibo-exp/internal/schedule"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

func pendingFixture(t *testing.T) (app.Paths, app.State) {
	t.Helper()
	paths, err := app.ResolvePaths(filepath.Join(t.TempDir(), "data with ' quote"))
	if err != nil {
		t.Fatal(err)
	}
	state := app.NewState()
	state.UID = "test-account"
	state.PendingDeletes = []app.PendingDelete{
		{Kind: "repost", SourcePostID: "1001", CreatedAt: time.Date(2026, 9, 30, 14, 25, 41, 0, time.Local)},
		{Kind: "comment", ContentID: "2002", SourcePostID: "1002"},
	}
	state.Mark(app.Today(time.Now()), "topic", "comment", "1002")
	if err := app.SaveState(paths.State, state); err != nil {
		t.Fatal(err)
	}
	return paths, state
}

func TestManualPendingBlocksBeforePromptNetworkAndWrites(t *testing.T) {
	for _, args := range [][]string{{"run"}, {"run", "--yes"}, {"run", "--dry-run"}, {"scheduled-run"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			paths, _ := pendingFixture(t)
			before, _ := os.ReadFile(paths.State)
			old := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = old })
			http.DefaultTransport = testTransport(func(*http.Request) (*http.Response, error) {
				t.Error("unexpected network request")
				return nil, errors.New("network prohibited")
			})
			var output bytes.Buffer
			err := runCLI(context.Background(), append([]string{"--data-dir", paths.DataDir}, args...), strings.NewReader(""), &output, io.Discard)
			var blocked *manualCheckError
			if !errors.As(err, &blocked) {
				t.Fatalf("err=%v output=%s", err, &output)
			}
			text := output.String()
			if args[0] == "scheduled-run" {
				data, readErr := os.ReadFile(filepath.Join(paths.LogDir, logging.OutputFile))
				if readErr != nil {
					t.Fatal(readErr)
				}
				text = string(data)
				errorsLog, _ := os.ReadFile(filepath.Join(paths.LogDir, logging.ErrorFile))
				if !strings.Contains(string(errorsLog), "cleanup") {
					t.Fatal("error log lacks recovery command")
				}
			}
			if !strings.Contains(text, "注意：任务执行受阻，有 1 条转发需要人工核查。") || !strings.Contains(text, commandFor(paths, "cleanup")) {
				t.Fatal(text)
			}
			if strings.Contains(text, "输入 yes") || strings.Contains(text, "执行汇总") || strings.Contains(text, "本工具") {
				t.Fatal(text)
			}
			after, _ := os.ReadFile(paths.State)
			if !bytes.Equal(before, after) {
				t.Fatal("state changed")
			}
		})
	}
}

func TestStatusAndTopicsNoticeAndJSONEvenWhenLoggedOut(t *testing.T) {
	paths, _ := pendingFixture(t)
	old := inspectSchedule
	t.Cleanup(func() { inspectSchedule = old })
	inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) { return schedule.InstallStatus{}, nil }
	for _, loggedIn := range []bool{true, false} {
		for _, asJSON := range []bool{true, false} {
			for _, command := range []string{"status", "topics"} {
				t.Run(command+"/"+map[bool]string{true: "logged-in", false: "expired"}[loggedIn]+"/"+map[bool]string{true: "json", false: "text"}[asJSON], func(t *testing.T) {
					client := weibo.NewClient("https://example.invalid", &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
						body := `{"ok":1,"data":{"cards":[]}}`
						if req.URL.Path == "/api/config" {
							body = `{"ok":1,"data":{"login":true,"uid":"test-account","st":"test"}}`
							if !loggedIn {
								body = `{"ok":-100}`
							}
						}
						return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
					})})
					var output bytes.Buffer
					var args []string
					if asJSON {
						args = []string{"--json"}
					}
					fn := statusCommand
					if command == "topics" {
						fn = topicsCommand
					}
					err := fn(context.Background(), paths, app.DefaultConfig(), client, args, &output, io.Discard)
					if (err == nil) != loggedIn {
						t.Fatalf("err=%v", err)
					}
					if asJSON {
						var result map[string]any
						if err := json.Unmarshal(output.Bytes(), &result); err != nil {
							t.Fatalf("%v: %s", err, &output)
						}
						if result["pending_deletes"] != float64(2) || result["pending_manual_checks"] != float64(1) || result["pending_retry_deletes"] != float64(1) {
							t.Fatal(result)
						}
					} else if strings.Count(output.String(), "注意：任务执行受阻") != 1 || !strings.Contains(output.String(), commandFor(paths, "cleanup")) {
						t.Fatal(output.String())
					}
				})
			}
		}
	}
}

func TestCleanupCommandsAndExplicitConfirmation(t *testing.T) {
	paths, _ := pendingFixture(t)
	var output bytes.Buffer
	if err := cleanupCommand(paths, nil, strings.NewReader(""), &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"待处理内容：2 条", "2026-09-30 14:25:41", commandFor(paths, "cleanup confirm --kind repost --post 1001"), commandFor(paths, "cleanup confirm --kind comment --id 2002"), "不会删除微博内容"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %s: %s", want, &output)
		}
	}
	before, _ := os.ReadFile(paths.State)
	if err := cleanupCommand(paths, []string{"confirm"}, strings.NewReader(""), io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "完整确认命令") {
		t.Fatal(err)
	}
	if err := cleanupCommand(paths, []string{"confirm", "--kind", "repost", "--post", "1001"}, strings.NewReader("no\n"), io.Discard, io.Discard); err == nil {
		t.Fatal("missing cancellation")
	}
	after, _ := os.ReadFile(paths.State)
	if !bytes.Equal(before, after) {
		t.Fatal("unconfirmed removal")
	}
	output.Reset()
	if err := cleanupCommand(paths, []string{"confirm", "--kind", "repost", "--post", "1001"}, strings.NewReader("yes\n"), &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "剩余待处理内容：1 条，其中 0 条需要人工核查") || !strings.Contains(output.String(), "将不再跟进") {
		t.Fatal(output.String())
	}
	state, _ := app.LoadState(paths.State)
	if len(state.PendingDeletes) != 1 || state.PendingDeletes[0].ContentID != "2002" || len(state.Topic(app.Today(time.Now()), "topic").CommentedPostIDs) != 1 {
		t.Fatalf("%+v", state)
	}
	output.Reset()
	if err := cleanupCommand(paths, []string{"confirm", "--kind", "comment", "--id", "2002", "--yes"}, strings.NewReader(""), &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "暂无其他待处理记录") || !strings.Contains(output.String(), commandFor(paths, "run")) {
		t.Fatal(output.String())
	}
}

func TestScheduleNoticesDoNotRunTasks(t *testing.T) {
	paths, _ := pendingFixture(t)
	oldInspect, oldInstall := inspectSchedule, installSchedule
	t.Cleanup(func() { inspectSchedule, installSchedule = oldInspect, oldInstall })
	inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) {
		return schedule.InstallStatus{Registered: true, DailyTime: "10:00", RetryInterval: "每 4 小时一次", DataDir: paths.DataDir}, nil
	}
	installSchedule = func(schedule.Config) error { return nil }
	for _, command := range []string{"status", "install"} {
		var output bytes.Buffer
		if err := scheduleCommand(paths, []string{command}, &output, io.Discard); err != nil {
			t.Fatal(err)
		}
		if strings.Count(output.String(), "注意：任务执行受阻") != 1 {
			t.Fatal(output.String())
		}
	}
}

func TestAutomaticRetryNoticeDoesNotClaimManualBlock(t *testing.T) {
	paths, state := pendingFixture(t)
	state.PendingDeletes = state.PendingDeletes[1:]
	notice := pendingNotice(paths, state)
	if strings.Contains(notice, "受阻") || !strings.Contains(notice, "下次定时任务会先重试删除") {
		t.Fatal(notice)
	}
	if err := checkManualPending(io.Discard, paths, state, false); err != nil {
		t.Fatal(err)
	}
}

func TestLoginSuccessPreservesPendingAndShowsNotice(t *testing.T) {
	paths, _ := pendingFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sso/signin":
			http.SetCookie(w, &http.Cookie{Name: "X-CSRF-TOKEN", Value: "test-csrf", Path: "/"})
			fmt.Fprint(w, "signin")
		case "/sso/v2/qrcode/image":
			fmt.Fprint(w, `{"retcode":20000000,"data":{"qrid":"test-qr","image":"/qr.png"}}`)
		case "/qr.png":
			fmt.Fprint(w, "test image")
		case "/sso/v2/qrcode/check":
			fmt.Fprint(w, `{"retcode":20000000,"data":{"url":"/cross"}}`)
		case "/cross":
			http.SetCookie(w, &http.Cookie{Name: "SUB", Value: "test-session", Path: "/"})
			fmt.Fprint(w, "ok")
		case "/api/config":
			fmt.Fprint(w, `{"ok":1,"data":{"login":true,"uid":"test-account","st":"test-st"}}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	var output bytes.Buffer
	err := runCLI(context.Background(), []string{"--data-dir", paths.DataDir, "--base-url", server.URL, "--passport-url", server.URL, "login", "--no-open"}, strings.NewReader(""), &output, io.Discard)
	if err != nil {
		t.Fatalf("%v: %s", err, &output)
	}
	if !strings.Contains(output.String(), "登录结果：成功") || !strings.Contains(output.String(), "注意：任务执行受阻") || !strings.Contains(output.String(), commandFor(paths, "cleanup")) {
		t.Fatal(output.String())
	}
	state, err := app.LoadState(paths.State)
	if err != nil || len(state.PendingDeletes) != 2 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}
