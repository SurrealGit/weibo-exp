package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/schedule"
)

// Exercise the real CLI dispatch while replacing only the OS scheduler boundary.
// Invalid session/state fixtures make accidental entry into login/run fail.
func TestScheduleInstallIsIndependentOfLoginAndRun(t *testing.T) {
	paths, err := app.ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := app.SaveConfig(paths.Config, app.DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.Session, paths.State} {
		if err := os.WriteFile(path, []byte("invalid fixture; must not be loaded"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var requests atomic.Int32
	oldTransport := http.DefaultTransport
	http.DefaultTransport = testTransport(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("network forbidden in schedule-only test")
	})
	oldInspect, oldInstall, oldUninstall := inspectSchedule, installSchedule, uninstallSchedule
	t.Cleanup(func() {
		http.DefaultTransport = oldTransport
		inspectSchedule, installSchedule, uninstallSchedule = oldInspect, oldInstall, oldUninstall
	})
	inspects, installs := 0, 0
	inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) {
		inspects++
		return schedule.InstallStatus{}, nil
	}
	installSchedule = func(cfg schedule.Config) error {
		installs++
		if cfg.DataDir != paths.DataDir || cfg.Hour != 11 || cfg.Minute != 30 || cfg.Executable == "" {
			t.Fatalf("unexpected native config: %+v", cfg)
		}
		return nil
	}
	uninstallSchedule = func(schedule.Config) error {
		t.Fatal("unexpected uninstall")
		return nil
	}
	var out bytes.Buffer
	err = runCLI(context.Background(), []string{"--data-dir", paths.DataDir, "schedule", "install", "--at", "11:30"}, strings.NewReader(""), &out, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 || inspects != 1 || installs != 1 {
		t.Fatalf("requests=%d inspect=%d install=%d", requests.Load(), inspects, installs)
	}
	for _, path := range []string{paths.Session, paths.State} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "invalid fixture; must not be loaded" {
			t.Fatalf("fixture changed: %s: %v", path, err)
		}
	}
}
