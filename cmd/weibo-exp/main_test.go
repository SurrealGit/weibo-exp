package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/logging"
	"github.com/SurrealGit/weibo-exp/internal/runner"
	"github.com/SurrealGit/weibo-exp/internal/schedule"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

func TestHelpCommandDescriptionsAlign(t *testing.T) {
	var output bytes.Buffer
	printHelp(&output)
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.HasPrefix(line, "  ") {
			if column := strings.IndexFunc(line, func(r rune) bool { return r > 127 }); column != 24 {
				t.Errorf("description starts at column %d, want 24: %q", column, line)
			}
		}
	}
}

func TestTimestampWriterPrefixesEveryLine(t *testing.T) {
	var output bytes.Buffer
	fixed := func() time.Time {
		return time.Date(2026, 9, 5, 10, 30, 45, 0, time.Local)
	}
	writer := newTimestampWriter(&output, fixed)
	if _, err := writer.Write([]byte("第一行\n第二")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("行\n")); err != nil {
		t.Fatal(err)
	}
	expected := "[2026-09-05 10:30:45] 第一行\n[2026-09-05 10:30:45] 第二行\n"
	if output.String() != expected {
		t.Fatalf("时间戳输出错误：\n%s", output.String())
	}
}

func TestPrepareScheduledRunPrunesBeforeOpeningLogs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, "data")
	paths, err := app.ResolvePaths(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.SaveConfig(paths.Config, app.DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.LogDir, 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(paths.LogDir, logging.OutputFile)
	if err := os.WriteFile(logPath, []byte("[2000-01-01 00:00:00] 过期\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output, _, closeLogs, err := openScheduledLogs(paths, app.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(output, "定时测试"); err != nil {
		t.Fatal(err)
	}
	closeLogs()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "过期") || !strings.Contains(string(data), "定时测试") || !strings.HasPrefix(string(data), "[") {
		t.Fatalf("定时日志准备错误：%s", data)
	}
}

func TestLogsCommandViewsAndClearsLogs(t *testing.T) {
	logDir := t.TempDir()
	path := filepath.Join(logDir, logging.OutputFile)
	if err := os.WriteFile(path, []byte("一\n二\n三\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := app.Paths{LogDir: logDir}
	var output bytes.Buffer
	var errorOutput bytes.Buffer
	if err := logsCommand(paths, []string{"--lines", "2"}, strings.NewReader(""), &output, &errorOutput); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "一\n") || !strings.Contains(output.String(), "二\n三\n") {
		t.Fatalf("日志查看结果错误：\n%s", output.String())
	}
	output.Reset()
	if err := logsCommand(paths, []string{"clear"}, strings.NewReader("yes\n"), &output, &errorOutput); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != 0 {
		t.Fatalf("日志未清空：%v", err)
	}
}

func TestTaskProgressRespondsToLimitAndPendingChanges(t *testing.T) {
	topics := []weibo.Topic{{ID: "a", Name: "甲", Signed: true}}
	state := app.NewState()
	for _, post := range []string{"1", "2", "3", "4"} {
		state.Mark("2026-09-05", "a", "comment", post)
	}
	for _, post := range []string{"5", "6"} {
		state.Mark("2026-09-05", "a", "repost", post)
	}
	cfg := app.DefaultConfig()
	if progress := taskProgress(topicViews(topics, nil, cfg, state, "2026-09-05")); !progress.Complete {
		t.Fatalf("4/2 应判为完成：%#v", progress)
	}
	cfg.CommentLimit, cfg.RepostLimit = 6, 3
	if progress := taskProgress(topicViews(topics, nil, cfg, state, "2026-09-05")); progress.Complete {
		t.Fatalf("上限提高后仍误判完成：%#v", progress)
	}
	cfg.CommentLimit, cfg.RepostLimit = 4, 2
	state.AddPendingDelete(app.PendingDelete{Kind: "comment", ContentID: "pending", TopicID: "a"})
	if progress := taskProgress(topicViews(topics, nil, cfg, state, "2026-09-05")); progress.Complete {
		t.Fatalf("存在待删除内容时仍误判完成：%#v", progress)
	}
}

func TestConfigShowJSONIsMachineReadable(t *testing.T) {
	paths, err := app.ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	var errorOutput bytes.Buffer
	if err := configCommand(context.Background(), paths, []string{"show", "--json"}, &output, &errorOutput, "http://invalid.local"); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(output.Bytes()) {
		t.Fatalf("config show --json 混入了非 JSON 文本：\n%s", output.String())
	}
}

func TestPrintRunSummaryDistinguishesPlanFromResults(t *testing.T) {
	summary := runner.Summary{Topics: 2, Checkins: 2, Comments: 8, Reposts: 4}
	var output bytes.Buffer
	printRunSummary(&output, summary, true, nil)
	text := output.String()
	for _, expected := range []string{
		"预演汇总（未执行任何微博写操作）",
		"预计签到：2",
		"预计评论：8",
		"预计转发：4",
		"预计删除：12",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("预演汇总缺少 %q：\n%s", expected, text)
		}
	}
	if strings.Contains(text, "签到成功") {
		t.Fatalf("预演汇总不应声称操作成功：\n%s", text)
	}
}

func TestPrintTopicsShowsIDsAndScope(t *testing.T) {
	topics := []weibo.Topic{
		{ID: "100808aaa", Name: "甲", Signed: true},
		{ID: "100808bbb", Name: "乙", CanCheckin: true},
	}
	selected := app.TopicSelections{{ID: "100808bbb", Name: "乙超话"}}
	cfg := app.DefaultConfig()
	state := app.NewState()
	state.Mark("2026-09-05", "100808bbb", "comment", "post-1")
	views := topicViews(topics, selected, cfg, state, "2026-09-05")
	var output bytes.Buffer
	printTopics(&output, views, selected)
	text := output.String()
	for _, expected := range []string{
		"关注超话：2 个",
		"[1] 甲超话",
		"任务范围：未包含",
		"签到状态：今日已签到",
		"[2] 乙超话",
		"任务范围：已包含",
		"签到状态：今日未签到",
		"互动状态：未完成",
		"weibo-exp config set --topics 1,2",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("超话列表输出缺少 %q：\n%s", expected, text)
		}
	}
	if selectedTopicCount(topics, app.TopicSelections{{ID: "100808bbb"}, {ID: "missing"}}) != 1 {
		t.Fatal("指定超话匹配计数错误")
	}
}

func TestTaskProgressCountsOnlyFullyCompletedSelectedTopics(t *testing.T) {
	topics := []topicView{
		{Selected: true, Signed: true, TaskCompleted: true},
		{Selected: true, Signed: false, TaskCompleted: false},
		{Selected: false, Signed: true, TaskCompleted: true},
	}
	progress := taskProgress(topics)
	if progress.Completed != 1 || progress.Remaining != 1 || progress.Total != 2 || progress.Complete {
		t.Fatalf("任务进度错误：%#v", progress)
	}
}

func TestPrintTaskProgressUsesDirectInstruction(t *testing.T) {
	var output bytes.Buffer
	printTaskProgress(&output, topicTaskProgress{Completed: 2, Remaining: 1, Total: 3})
	expected := "今日任务：已完成 2/3 个超话；剩余 1 个，执行 weibo-exp topics 查看详情\n"
	if output.String() != expected {
		t.Fatalf("任务进度文案错误：%q", output.String())
	}
}

func TestPrintConfigPlacesLogRetentionAfterSchedule(t *testing.T) {
	old := inspectSchedule
	t.Cleanup(func() { inspectSchedule = old })
	inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) { return schedule.InstallStatus{}, nil }
	var output bytes.Buffer
	printConfig(&output, app.DefaultConfig(), app.Paths{DataDir: t.TempDir()})
	text := output.String()
	scheduleIndex := strings.Index(text, "每日执行时间：")
	retentionIndex := strings.Index(text, "日志自动清理：")
	commentIndex := strings.Index(text, "每个超话每日评论：")
	if scheduleIndex < 0 || retentionIndex < scheduleIndex || commentIndex < retentionIndex {
		t.Fatalf("配置输出顺序错误：\n%s", text)
	}
}

func TestResolveTopicSelectionsAcceptsIndexAndName(t *testing.T) {
	topics := []weibo.Topic{
		{ID: "100808aaa", Name: "甲"},
		{ID: "100808bbb", Name: "乙"},
	}
	selected, err := resolveTopicSelections("2，甲超话", topics)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || selected[0].ID != "100808bbb" || selected[0].Name != "乙超话" || selected[1].ID != "100808aaa" || selected[1].Name != "甲超话" {
		t.Fatalf("超话选择解析错误：%#v", selected)
	}
	if _, err := resolveTopicSelections("3", topics); err == nil {
		t.Fatal("越界序号未返回错误")
	}
}

func TestPrintRunSummaryMarksIncompleteDryRun(t *testing.T) {
	var output bytes.Buffer
	printRunSummary(&output, runner.Summary{Topics: 2, SkippedTopics: 1}, true, errors.New("读取失败"))
	if !strings.Contains(output.String(), "预演汇总（存在失败或未完成项；未执行任何微博写操作）") {
		t.Fatalf("预演失败状态不明确：\n%s", output.String())
	}
}

func TestPrintRunSummaryMarksPartialExecution(t *testing.T) {
	summary := runner.Summary{Topics: 2, Checkins: 1, Comments: 3, Reposts: 1, Deleted: 4, SkippedTopics: 1}
	var output bytes.Buffer
	printRunSummary(&output, summary, false, errors.New("部分失败"))
	text := output.String()
	for _, expected := range []string{
		"存在失败或未完成项",
		"仅统计已实际成功的操作",
		"签到成功：1",
		"评论成功：3",
		"转发成功：1",
		"删除成功：4",
		"跳过超话：1",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("实际执行汇总缺少 %q：\n%s", expected, text)
		}
	}
}
