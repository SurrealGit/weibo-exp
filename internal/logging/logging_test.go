package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTailReturnsLastLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), OutputFile)
	if err := os.WriteFile(path, []byte("一\n二\n三\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := Tail(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "二\n三\n" {
		t.Fatalf("日志末尾错误：%q", data)
	}
}

func TestTailReadsEndOfLargeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), OutputFile)
	var content strings.Builder
	for index := 0; index < 20000; index++ {
		content.WriteString("历史行\n")
	}
	content.WriteString("倒数第二行\n最后一行\n")
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := Tail(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "倒数第二行\n最后一行\n" {
		t.Fatalf("大日志末尾读取错误：%q", data)
	}
}

func TestPruneKeepsRetentionWindowAndUnknownLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, OutputFile)
	data := strings.Join([]string{
		"[2026-08-28 12:00:00] 过期",
		"无法识别时间的历史记录",
		"[2026-08-29 13:00:01] 保留",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 13, 0, 0, 0, time.Local)
	result, err := Prune(dir, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedLines != 1 || result.KeptLines != 2 {
		t.Fatalf("清理统计错误：%#v", result)
	}
	kept, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(kept), "过期") || !strings.Contains(string(kept), "历史记录") || !strings.Contains(string(kept), "保留") {
		t.Fatalf("清理结果错误：%s", kept)
	}
}

func TestClearTruncatesBothLogs(t *testing.T) {
	dir := t.TempDir()
	for _, path := range Paths(dir) {
		if err := os.WriteFile(path, []byte("内容\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Clear(dir); err != nil {
		t.Fatal(err)
	}
	for _, path := range Paths(dir) {
		info, err := os.Stat(path)
		if err != nil || info.Size() != 0 {
			t.Fatalf("日志未清空：%s", path)
		}
	}
}
