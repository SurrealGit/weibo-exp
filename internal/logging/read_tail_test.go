package logging

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadTail(t *testing.T) {
	for _, tc := range []struct {
		name, output, errors, want string
		limit                      int
		errorsOnly                 bool
	}{
		{"interleaved", "[2026-10-15 10:00:00] start\n[2026-10-15 19:00:00] done\n", "[2026-10-15 14:00:00] failed\n", "[2026-10-15 14:00:00] [错误] failed\n[2026-10-15 19:00:00] done\n", 2, false},
		{"old errors do not hide success", "[2026-10-15 19:00:00] done\n", "[2026-10-14 14:00:00] old failure\n", "[2026-10-15 19:00:00] done\n", 1, false},
		{"same second stable", "[2026-10-15 19:00:00] first\n[2026-10-15 19:00:00] second\n", "[2026-10-15 19:00:00] failed\n", "[2026-10-15 19:00:00] first\n[2026-10-15 19:00:00] second\n[2026-10-15 19:00:00] [错误] failed\n", 10, false},
		{"errors only", "[2026-10-15 20:00:00] done\n", "[2026-10-15 14:00:00] old\n[2026-10-15 19:00:00] failed", "[2026-10-15 19:00:00] [错误] failed\n", 1, true},
		{"legacy and continuation", "legacy\n[2026-10-15 19:00:00] start\ndetails\n", "[2026-10-15 19:00:01] failed\n", "legacy\n[2026-10-15 19:00:00] start\ndetails\n[2026-10-15 19:00:01] [错误] failed\n", 10, false},
		{"undated errors", "", "old error\nnext error\n", "[错误] old error\n[错误] next error\n", 10, false},
		{"missing files", "", "", "", 10, false},
		{"missing error file", "latest\n", "", "latest\n", 10, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for file, content := range map[string]string{OutputFile: tc.output, ErrorFile: tc.errors} {
				if content != "" {
					if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			got, err := ReadTail(dir, tc.limit, tc.errorsOnly)
			if err != nil || string(got) != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestReadTailErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadTail(dir, 0, false); err == nil {
		t.Fatal("accepted zero limit")
	}
	if err := os.Mkdir(filepath.Join(dir, ErrorFile), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTail(dir, 10, false); err == nil {
		t.Fatal("ignored unreadable error log")
	}
}
