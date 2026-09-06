package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/logging"
	"github.com/SurrealGit/weibo-exp/internal/runner"
	"github.com/SurrealGit/weibo-exp/internal/schedule"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

func TestConfigFeedPageLimit(t *testing.T) {
	for _, value := range []int{-1, 0, 20} {
		t.Run(strconv.Itoa(value), func(t *testing.T) {
			dir := t.TempDir()
			paths := app.Paths{DataDir: dir, Config: filepath.Join(dir, "config.json"), Lock: filepath.Join(dir, "run.lock")}
			err := configCommand(context.Background(), paths, []string{"set", "--max-feed-pages", strconv.Itoa(value)}, io.Discard, io.Discard, "https://unit.test")
			if value <= 0 {
				if err == nil {
					t.Fatal("无效分页上限被接受")
				}
				return
			}
			cfg, loadErr := app.LoadConfig(paths.Config)
			if err != nil || loadErr != nil || cfg.MaxFeedPages != value {
				t.Fatalf("未保存分页上限：%+v %v %v", cfg, err, loadErr)
			}
		})
	}
}

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type confirmInput struct {
	before func()
	done   bool
}

func (r *confirmInput) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	r.before()
	return copy(p, "yes\n"), nil
}

func TestRunLoadsProgressAfterConfirmation(t *testing.T) {
	dir := t.TempDir()
	paths := app.Paths{State: filepath.Join(dir, "state.json"), Lock: filepath.Join(dir, "run.lock")}
	cfg := app.DefaultConfig()
	cfg.CommentLimit = 1
	cfg.RepostLimit = 0
	const topic = "100808aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	writes := 0
	client := weibo.NewClient("https://unit.test", &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"ok":1,"data":{"login":true,"st":"fake","uid":"self"}}`
		if r.URL.Path == "/api/container/getIndex" {
			body = `{"ok":1,"data":{"cards":[{"title_sub":"test","scheme":"https://m.weibo.cn/p/index?containerid=` + topic + `","itemid":"follow_super_follow_x","buttons":[{"name":"已签到"}]}]}}`
		}
		if r.Method == "POST" {
			writes++
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})})
	input := &confirmInput{before: func() {
		state := app.NewState()
		state.Mark(app.Today(time.Now()), topic, "comment", "previous")
		if err := app.SaveState(paths.State, state); err != nil {
			t.Fatal(err)
		}
	}}
	if err := runCommand(context.Background(), paths, cfg, client, nil, input, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	state, err := app.LoadState(paths.State)
	if err != nil {
		t.Fatal(err)
	}
	if writes != 0 || state.Topic(app.Today(time.Now()), topic).CommentedPostIDs[0] != "previous" {
		t.Fatal("stale progress overwrote current state")
	}
}

func TestScheduledLockProtectsLogBeforePruning(t *testing.T) {
	dir := t.TempDir()
	paths := app.Paths{Lock: filepath.Join(dir, "run.lock"), LogDir: dir}
	path := filepath.Join(dir, logging.OutputFile)
	before := "[2000-01-01 00:00:00] old\n"
	if err := os.WriteFile(path, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	release, err := runner.AcquireLock(paths.Lock)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := scheduledCommand(context.Background(), paths, "https://unit.test"); err == nil {
		t.Fatal("concurrent run accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != before {
		t.Fatal("log modified before locking")
	}
}

func TestScheduledWordAsParameterIsNotInternalCommand(t *testing.T) {
	dir := t.TempDir()
	err := runCLI(context.Background(), []string{"--data-dir", dir, "config", "set", "--templates-file", "scheduled-run"}, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil || strings.Contains(err.Error(), "scheduled-run 不接受") {
		t.Fatalf("wrong command dispatch: %v", err)
	}
}

func TestConfigResetSyncsScheduleAndFailureRestoresJSON(t *testing.T) {
	dir := t.TempDir()
	paths := app.Paths{Config: filepath.Join(dir, "config.json"), Lock: filepath.Join(dir, "run.lock"), DataDir: dir}
	cfg := app.DefaultConfig()
	cfg.ScheduledTime = "18:00"
	if err := app.SaveConfig(paths.Config, cfg); err != nil {
		t.Fatal(err)
	}
	oldInspect, oldInstall := inspectSchedule, installSchedule
	t.Cleanup(func() { inspectSchedule, installSchedule = oldInspect, oldInstall })
	inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) {
		return schedule.InstallStatus{Registered: true, Executable: "/tmp/weibo-exp", DataDir: dir}, nil
	}
	called := false
	installSchedule = func(c schedule.Config) error {
		called = true
		if c.Hour != 10 {
			t.Fatal("wrong reset time")
		}
		return errors.New("fake scheduler failure")
	}
	err := configCommand(context.Background(), paths, []string{"reset"}, io.Discard, io.Discard, "https://unit.test")
	restored, loadErr := app.LoadConfig(paths.Config)
	if err == nil || loadErr != nil || !called || restored.ScheduledTime != "18:00" {
		t.Fatalf("%v %+v", err, restored)
	}
	installSchedule = func(c schedule.Config) error { return nil }
	if err := configCommand(context.Background(), paths, []string{"reset"}, io.Discard, io.Discard, "https://unit.test"); err != nil {
		t.Fatal(err)
	}
	restored, _ = app.LoadConfig(paths.Config)
	if restored.ScheduledTime != "10:00" {
		t.Fatal("reset did not save")
	}
}

func TestNegativeConfigFlagsRejected(t *testing.T) {
	dir := t.TempDir()
	paths := app.Paths{Config: filepath.Join(dir, "config.json"), Lock: filepath.Join(dir, "run.lock")}
	for _, flag := range []string{"--comment-limit", "--repost-limit", "--log-retention-days"} {
		if err := configCommand(context.Background(), paths, []string{"set", flag, "-5"}, io.Discard, io.Discard, ""); err == nil {
			t.Fatalf("accepted %s -5", flag)
		}
	}
}

func TestCleanupListingGuidance(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(strconv.FormatBool(pending), func(t *testing.T) {
			dir := t.TempDir()
			paths := app.Paths{State: filepath.Join(dir, "state.json"), Lock: filepath.Join(dir, "run.lock")}
			state := app.NewState()
			if pending {
				state.AddPendingDelete(app.PendingDelete{Kind: "comment", ContentID: "content", SourcePostID: "post"})
			}
			if err := app.SaveState(paths.State, state); err != nil {
				t.Fatal(err)
			}
			var output strings.Builder
			if err := cleanupCommand(paths, nil, strings.NewReader(""), &output, io.Discard); err != nil {
				t.Fatal(err)
			}
			if pending {
				for _, want := range []string{"待清理内容：1 条", "内容 ID content", "cleanup confirm"} {
					if !strings.Contains(output.String(), want) {
						t.Fatalf("missing %q: %s", want, output.String())
					}
				}
			} else if output.String() != "暂无待清理内容，无需操作。\n" {
				t.Fatalf("unexpected empty-state output: %q", output.String())
			}
		})
	}
}

func TestCleanupConfirmsOnlyExplicitItem(t *testing.T) {
	dir := t.TempDir()
	paths := app.Paths{State: filepath.Join(dir, "state.json"), Lock: filepath.Join(dir, "run.lock")}
	state := app.NewState()
	state.AddPendingDelete(app.PendingDelete{Kind: "comment", SourcePostID: "post"})
	state.AddPendingDelete(app.PendingDelete{Kind: "repost", ContentID: "keep"})
	if err := app.SaveState(paths.State, state); err != nil {
		t.Fatal(err)
	}
	if err := cleanupCommand(paths, []string{"confirm", "--kind", "comment", "--post", "post", "--yes"}, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	state, _ = app.LoadState(paths.State)
	if len(state.PendingDeletes) != 1 || state.PendingDeletes[0].ContentID != "keep" {
		t.Fatal("wrong item cleared")
	}
}
