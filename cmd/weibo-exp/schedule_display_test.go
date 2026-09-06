package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/schedule"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

func TestScheduleStateDisplay(t *testing.T) {
	yes, no := true, false
	old := inspectSchedule
	t.Cleanup(func() { inspectSchedule = old })
	dir := t.TempDir()
	for _, tc := range []struct {
		status schedule.InstallStatus
		err    error
		want   string
	}{
		{schedule.InstallStatus{}, nil, "未安装"},
		{schedule.InstallStatus{FileExists: true}, nil, "未安装"},
		{schedule.InstallStatus{Registered: true, DataDir: dir}, nil, "已安装"},
		{schedule.InstallStatus{Registered: true, DataDir: dir, Enabled: &yes}, nil, "已安装"},
		{schedule.InstallStatus{Registered: true, DataDir: dir, Enabled: &no}, nil, "已安装"},
		{schedule.InstallStatus{Registered: true, DataDir: dir, Enabled: &yes, Active: &no}, nil, "已安装"},
		{schedule.InstallStatus{}, errors.New("系统调度器不可用"), "查询失败（系统调度器不可用）"},
	} {
		inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) { return tc.status, tc.err }
		var out bytes.Buffer
		printScheduleState(&out, app.Paths{DataDir: dir})
		if out.String() != "定时任务状态："+tc.want+"\n" {
			t.Fatal(out.String())
		}
	}
}

func TestScheduleStateChecksDataDirectory(t *testing.T) {
	old := inspectSchedule
	t.Cleanup(func() { inspectSchedule = old })
	dir := t.TempDir()
	for _, dataDir := range []string{"", filepath.Join(dir, "other-account")} {
		inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) {
			return schedule.InstallStatus{Registered: true, DataDir: dataDir}, nil
		}
		var out bytes.Buffer
		printScheduleState(&out, app.Paths{DataDir: dir})
		if out.String() == "定时任务状态：已安装\n" || !strings.Contains(out.String(), "当前数据目录") || !strings.Contains(out.String(), "schedule status --verbose") {
			t.Fatalf("misleading schedule state: %s", &out)
		}
	}
}

func TestStatusAndConfigScheduleTextOnly(t *testing.T) {
	old := inspectSchedule
	t.Cleanup(func() { inspectSchedule = old })
	queries := 0
	inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) {
		queries++
		return schedule.InstallStatus{}, nil
	}
	dir := t.TempDir()
	paths := app.Paths{DataDir: dir, Config: filepath.Join(dir, "config.json"), State: filepath.Join(dir, "state.json")}
	cfg := app.DefaultConfig()
	if err := app.SaveConfig(paths.Config, cfg); err != nil {
		t.Fatal(err)
	}
	client := weibo.NewClient("https://unit.test", &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"ok":1,"data":{"login":true,"uid":"42","st":"fixture"}}`
		if r.URL.Path == "/api/container/getIndex" {
			body = `{"ok":1,"data":{"cards":[]}}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})})
	for _, name := range []string{"status", "config show"} {
		for _, asJSON := range []bool{false, true} {
			var out bytes.Buffer
			var args []string
			if asJSON {
				args = []string{"--json"}
			}
			queries = 0
			var err error
			if name == "status" {
				err = statusCommand(context.Background(), paths, cfg, client, args, &out, io.Discard)
			} else {
				err = configCommand(context.Background(), paths, append([]string{"show"}, args...), &out, io.Discard, "https://unit.test")
			}
			if err != nil {
				t.Fatal(err)
			}
			if asJSON {
				if queries != 0 || !json.Valid(out.Bytes()) || strings.Contains(out.String(), "定时任务状态") {
					t.Fatalf("JSON changed: %s queries=%d", &out, queries)
				}
			} else if queries != 1 || !strings.Contains(out.String(), "每日执行时间：10:00\n定时任务状态：未安装\n日志自动清理：") {
				t.Fatalf("%s: %s queries=%d", name, &out, queries)
			}
		}
	}
}
