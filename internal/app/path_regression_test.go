package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvironmentAndFlagUseIdenticalLogs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WEIBO_EXP_DATA_DIR", dir)
	a, err := ResolvePaths("")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEIBO_EXP_DATA_DIR", "")
	b, err := ResolvePaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a != b || a.LogDir != filepath.Join(dir, "logs") {
		t.Fatalf("inconsistent paths %+v %+v", a, b)
	}
	other, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if other.LogDir == a.LogDir {
		t.Fatal("different directories share logs")
	}
}

func TestLoadMissingConfigDoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if _, err := LoadConfig(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("read created configuration")
	}
}

func TestDefaultDirectoryAliasesKeepDefaultLogs(t *testing.T) {
	t.Setenv("WEIBO_EXP_DATA_DIR", "")
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("APPDATA", root)
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	dir, err := systemDataDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	a, err := ResolvePaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ResolvePaths(alias)
	if err != nil {
		t.Fatal(err)
	}
	if a.LogDir != b.LogDir {
		t.Fatalf("alias changed logs: %s %s", a.LogDir, b.LogDir)
	}
}
