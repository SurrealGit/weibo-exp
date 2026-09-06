package main

import (
	"context"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/install"
	"github.com/SurrealGit/weibo-exp/internal/runner"
)

func TestUninstallExcludesCommandsAfterRemovingLockFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses native mutex/deferred helper")
	}
	paths, _ := installedFixture(t)
	old := removeInstallationFiles
	t.Cleanup(func() { removeInstallationFiles = old })
	checked := false
	removeInstallationFiles = func(files []string) error {
		if len(files) == 0 || files[0] != paths.Lock {
			return install.RemoveFiles(files)
		}
		if err := install.RemoveFiles(files[:1]); err != nil {
			return err
		}
		checked = true
		// This is the exact former release/unlink window in the real uninstall.
		if release, err := runner.AcquireLock(paths.Lock); err == nil {
			release()
			t.Fatal("lock recreated during uninstall")
		}
		if err := runCommand(context.Background(), paths, app.DefaultConfig(), nil, []string{"--yes"}, strings.NewReader(""), io.Discard, io.Discard); err == nil {
			t.Fatal("run entered uninstall")
		}
		if err := configCommand(context.Background(), paths, []string{"set", "--comment-limit", "7"}, io.Discard, io.Discard, ""); err == nil {
			t.Fatal("config write entered uninstall")
		}
		if err := installationCommand(paths, []string{"--yes", "--no-path"}, strings.NewReader(""), io.Discard, io.Discard); err == nil {
			t.Fatal("installation entered uninstall")
		}
		if _, err := os.Stat(paths.Lock); !os.IsNotExist(err) {
			t.Fatal("blocked command recreated lock")
		}
		return install.RemoveFiles(files[1:])
	}
	if err := uninstallationCommand(paths, []string{"--yes"}, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !checked {
		t.Fatal("final deletion not exercised")
	}
	if _, err := os.Stat(paths.DataDir); !os.IsNotExist(err) {
		t.Fatal("data directory recreated")
	}
}
