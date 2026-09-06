package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadOldStateAndSaveMigratesToV4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	legacy := `{"version":2,"last_success_date":"2026-09-05","last_success_topics":["old"],"history":{"2026-09-05":{"topic":{"comments":["1"]}}}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if state.Version != StateVersion || len(state.Topic("2026-09-05", "topic").CommentedPostIDs) != 1 {
		t.Fatalf("旧状态读取错误：%#v", state)
	}
	if err := SaveState(path, state); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "last_success") || !strings.Contains(string(data), `"version": 4`) {
		t.Fatalf("状态未迁移到 v4：%s", data)
	}
}

func TestPendingDeleteAndSevenDayHistoryPrune(t *testing.T) {
	state := NewState()
	state.Mark("2026-08-29", "topic", "comment", "old")
	state.Mark("2026-08-30", "topic", "comment", "kept")
	state.Mark("2026-09-05", "topic", "repost", "today")
	item := PendingDelete{Kind: "comment", ContentID: "123", TopicID: "topic", CreatedAt: time.Now()}
	state.AddPendingDelete(item)
	state.AddPendingDelete(item)
	if state.PendingForTopic("topic") != 1 {
		t.Fatalf("待删除去重失败：%#v", state.PendingDeletes)
	}
	state.PruneHistory(time.Date(2026, 9, 5, 12, 0, 0, 0, time.Local), 7)
	if _, ok := state.History["2026-08-29"]; ok {
		t.Fatal("七天外历史未清理")
	}
	if _, ok := state.History["2026-08-30"]; !ok {
		t.Fatal("七天窗口边界被错误清理")
	}
	if !state.RemovePendingDelete("comment", "123") || len(state.PendingDeletes) != 0 {
		t.Fatal("待删除移除失败")
	}
}

func TestSaveStateAutomaticallyPrunesOldHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := NewState()
	state.Mark("2000-01-01", "topic", "comment", "old")
	state.Mark(Today(time.Now()), "topic", "comment", "today")
	if err := SaveState(path, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := loaded.History["2000-01-01"]; exists {
		t.Fatal("保存状态时未自动清理七天外历史")
	}
	if len(loaded.Topic(Today(time.Now()), "topic").CommentedPostIDs) != 1 {
		t.Fatal("保存状态时误删今日历史")
	}
}
