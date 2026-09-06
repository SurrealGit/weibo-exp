package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTopicSelectionsReadLegacyIDsAndWriteNamedObjects(t *testing.T) {
	var legacy struct {
		Selected TopicSelections `json:"selected_topics"`
	}
	if err := json.Unmarshal([]byte(`{"selected_topics":["100808aaa"]}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if len(legacy.Selected) != 1 || legacy.Selected[0].ID != "100808aaa" {
		t.Fatalf("旧版 ID 配置迁移错误：%#v", legacy.Selected)
	}

	value := struct {
		Selected TopicSelections `json:"selected_topics"`
	}{Selected: TopicSelections{{ID: "100808aaa", Name: "甲超话"}}}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"id":"100808aaa"`) || !strings.Contains(text, `"name":"甲超话"`) {
		t.Fatalf("新版配置未同时保存稳定 ID 和名称：%s", text)
	}
}

func TestTopicSelectionsIncludes(t *testing.T) {
	if !(TopicSelections(nil)).Includes("anything") {
		t.Fatal("空选择应表示全部超话")
	}
	selected := TopicSelections{{ID: "a", Name: "甲超话"}}
	if !selected.Includes("a") || selected.Includes("b") {
		t.Fatal("指定超话匹配错误")
	}
}

func TestDefaultLogRetention(t *testing.T) {
	if got := DefaultConfig().ScheduledTime; got != "10:00" {
		t.Fatalf("默认每日执行时间应为 10:00，实际为 %s", got)
	}
	if got := DefaultConfig().LogRetentionDays; got != 7 {
		t.Fatalf("默认日志保留天数应为 7，实际为 %d", got)
	}
	cfg := DefaultConfig()
	cfg.LogRetentionDays = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("未拒绝负数日志保留天数")
	}
}

func TestAlternateDataDirectoryOwnsLogs(t *testing.T) {
	t.Setenv("WEIBO_EXP_DATA_DIR", "")
	dir := t.TempDir()
	paths, err := ResolvePaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	if paths.LogDir != filepath.Join(dir, "logs") {
		t.Fatalf("%s", paths.LogDir)
	}
}

func TestDefaultPathsUseProjectName(t *testing.T) {
	t.Setenv("WEIBO_EXP_DATA_DIR", "")
	paths, err := ResolvePaths("")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.DataDir, paths.LogDir} {
		if filepath.Base(path) != "weibo-exp" {
			t.Fatalf("inconsistent project directory: %s", path)
		}
	}
	for path, want := range map[string]string{
		paths.LaunchAgent:    "com.surrealgit.weibo-exp.plist",
		paths.SystemdService: "weibo-exp.service",
		paths.SystemdTimer:   "weibo-exp.timer",
	} {
		if filepath.Base(path) != want {
			t.Fatalf("%s, want %s", path, want)
		}
	}
}
func TestSystemdUsesUserConfigDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux XDG path behavior")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(paths.SystemdTimer) != filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "systemd", "user") {
		t.Fatal(paths.SystemdTimer)
	}
}
