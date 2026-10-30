package schedule

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func launcherTestDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLauncherOwnershipAndRestoration(t *testing.T) {
	fakeLauncher(t)
	dir := launcherTestDir(t)
	original, err := readLauncher(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeLauncher(original); err != nil {
		t.Fatal(err)
	}
	installed, err := readLauncher(dir)
	if err != nil || !installed.hasBinary || !installed.hasReceipt {
		t.Fatalf("%+v %v", installed, err)
	}
	launcherPayload = func() ([]byte, error) { return []byte("updated launcher"), nil }
	if err := writeLauncher(installed); err != nil {
		t.Fatal(err)
	}
	if err := installed.restore(); err != nil {
		t.Fatal(err)
	}
	restored, err := readLauncher(dir)
	if err != nil || !bytes.Equal(restored.binary, installed.binary) {
		t.Fatalf("restore: %v", err)
	}
	if err := os.WriteFile(installed.path, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLauncher(dir); err == nil {
		t.Fatal("accepted foreign content")
	}
	if err := installed.restore(); err != nil {
		t.Fatal(err)
	}
	if err := original.restore(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(installed.path); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestLauncherMissingAndSymlink(t *testing.T) {
	fakeLauncher(t)
	dir := launcherTestDir(t)
	s, _ := readLauncher(dir)
	launcherPayload = func() ([]byte, error) { return nil, os.ErrNotExist }
	if err := writeLauncher(s); err == nil || !strings.Contains(err.Error(), "go generate") {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "elsewhere"), s.path); err != nil {
		t.Skip(err)
	}
	if _, err := readLauncher(dir); err == nil {
		t.Fatal("accepted symlink")
	}
}

func TestWindowsLauncherLifecycleRollbackAndDirectorySwitch(t *testing.T) {
	var registered []byte
	failCreate, failDelete := false, false
	fakePlatform(t, "windows", func(_ string, args ...string) ([]byte, error) {
		switch args[0] {
		case "/Query":
			if args[1] == "/FO" {
				return nil, nil
			}
			if registered == nil {
				return nil, os.ErrNotExist
			}
			return registered, nil
		case "/Create":
			if failCreate {
				failCreate = false
				return nil, errors.New("create failed")
			}
			data, err := os.ReadFile(args[4])
			registered = decodeTaskXML(data)
			return nil, err
		case "/Delete":
			if failDelete {
				return nil, errors.New("delete failed")
			}
			registered = nil
			return nil, nil
		}
		return nil, errors.New("unexpected command")
	})
	cfg := Config{Executable: filepath.Join(launcherTestDir(t), "main.exe"), DataDir: launcherTestDir(t), Hour: 10}
	failCreate = true
	if Install(cfg) == nil {
		t.Fatal("expected creation failure")
	}
	s, err := readLauncher(cfg.DataDir)
	if err != nil || s.hasBinary || s.hasReceipt {
		t.Fatalf("failed install left files: %+v %v", s, err)
	}
	if err := Install(cfg); err != nil {
		t.Fatal(err)
	}
	old, err := readLauncher(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	oldTask := append([]byte(nil), registered...)
	// Equivalent spellings must not remove the newly installed helper.
	alias := cfg
	alias.DataDir += string(filepath.Separator) + "."
	if err := Install(alias); err != nil {
		t.Fatal(err)
	}
	if _, err := readLauncher(cfg.DataDir); err != nil {
		t.Fatal(err)
	}
	if err := Install(cfg); err != nil {
		t.Fatal(err)
	}
	launcherPayload = func() ([]byte, error) { return []byte("new helper"), nil }
	failCreate = true
	if Install(cfg) == nil {
		t.Fatal("expected update failure")
	}
	restored, err := readLauncher(cfg.DataDir)
	if err != nil || !bytes.Equal(old.binary, restored.binary) || !bytes.Equal(registered, oldTask) {
		t.Fatalf("rollback: %v", err)
	}
	failDelete = true
	if Uninstall(cfg) == nil {
		t.Fatal("expected uninstall failure")
	}
	if _, err := os.Stat(old.path); err != nil {
		t.Fatal("removed helper before task")
	}
	failDelete = false
	cfg.DataDir = launcherTestDir(t)
	if err := Install(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old.path); !os.IsNotExist(err) {
		t.Fatal("old helper remains")
	}
	// A task removed externally still leaves a discoverable, removable helper.
	registered = nil
	status, err := Inspect(cfg)
	if err != nil || status.Registered || !status.FileExists || status.DataDir != cfg.DataDir {
		t.Fatalf("%+v %v", status, err)
	}
	if err := Uninstall(cfg); err != nil {
		t.Fatal(err)
	}
	status, err = Inspect(cfg)
	if err != nil || status.FileExists {
		t.Fatalf("residual helper: %+v %v", status, err)
	}
}

func TestWindowsLegacyTaskArguments(t *testing.T) {
	text := `<Task><Command>C:\Apps\weibo-exp.exe</Command><Arguments>--data-dir &quot;C:\Data space&quot; scheduled-run</Arguments></Task>`
	exe, dir := windowsArguments(text)
	if exe != `C:\Apps\weibo-exp.exe` || dir != `C:\Data space` {
		t.Fatalf("%q %q", exe, dir)
	}
}
