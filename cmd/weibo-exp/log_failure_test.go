package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/logging"
)

func TestLogPruneReadFailureStillAllowsRunOutput(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix owner permission checks")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, logging.OutputFile)
	if err := os.WriteFile(path, []byte("[2000-01-01 00:00:00] old\n"), 0200); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })
	if _, err := os.ReadFile(path); err == nil {
		t.Fatal("fault injection did not deny reads")
	}
	out, _, closeLogs, err := openScheduledLogs(app.Paths{LogDir: dir}, app.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := fmt.Fprintln(out, "task continued")
	closeLogs()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "task continued") || !strings.Contains(string(data), "old") {
		t.Fatalf("%s %v", data, err)
	}
	warning, err := os.ReadFile(filepath.Join(dir, logging.ErrorFile))
	if err != nil || !strings.Contains(string(warning), "旧日志清理失败，本次仍继续") {
		t.Fatalf("%s %v", warning, err)
	}
}

func TestLogOpenFailureDoesNotReturnRunnableWriters(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, logging.ErrorFile), 0700); err != nil {
		t.Fatal(err)
	}
	out, errOut, closeLogs, err := openScheduledLogs(app.Paths{LogDir: dir}, app.DefaultConfig())
	if err == nil || out != nil || errOut != nil || closeLogs != nil {
		t.Fatal("logging setup failure allowed execution")
	}
}
