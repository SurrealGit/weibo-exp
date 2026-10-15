package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/logging"
)

func TestLogsMergedLimitAndCombinedFlags(t *testing.T) {
	dir := t.TempDir()
	for _, file := range []string{logging.OutputFile, logging.ErrorFile} {
		var data strings.Builder
		for i := 0; i < 120; i++ {
			fmt.Fprintf(&data, "[2026-10-15 19:%02d:%02d] %s %d\n", i/60, i%60, file, i)
		}
		if err := os.WriteFile(filepath.Join(dir, file), []byte(data.String()), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		args       []string
		count      int
		errorsOnly bool
	}{
		{nil, 100, false},
		{[]string{"--lines", "30"}, 30, false},
		{[]string{"--errors"}, 100, true},
		{[]string{"--errors", "--lines", "30"}, 30, true},
		{[]string{"--lines", "30", "--errors"}, 30, true},
	} {
		var output bytes.Buffer
		if err := logsCommand(app.Paths{LogDir: dir}, tc.args, strings.NewReader(""), &output, io.Discard); err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, line := range strings.Split(output.String(), "\n") {
			if !strings.HasPrefix(line, "[2026-") {
				continue
			}
			count++
			if tc.errorsOnly && !strings.Contains(line, "[错误]") {
				t.Fatalf("ordinary line in error-only output: %s", line)
			}
		}
		if count != tc.count {
			t.Fatalf("args=%v got %d lines, want %d", tc.args, count, tc.count)
		}
	}
}
