package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/logging"
	"github.com/SurrealGit/weibo-exp/internal/schedule"
)

func TestCommandArgumentsCannotTurnHelpOrPreviewIntoWrites(t *testing.T) {
	originalTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	http.DefaultTransport = testTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected network request")
		return nil, errors.New("unexpected request")
	})
	oldInstall, oldUninstall, oldInspect := installSchedule, uninstallSchedule, inspectSchedule
	t.Cleanup(func() { installSchedule, uninstallSchedule, inspectSchedule = oldInstall, oldUninstall, oldInspect })
	installSchedule = func(schedule.Config) error { t.Fatal("native write"); return nil }
	uninstallSchedule = func(schedule.Config) error { t.Fatal("native deletion"); return nil }
	inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) {
		t.Fatal("unexpected native inspection")
		return schedule.InstallStatus{}, nil
	}
	for _, args := range [][]string{
		{"run", "--yes", "unexpected", "--dry-run"},
		{"config", "reset", "--help"}, {"schedule", "uninstall", "--help"},
		{"config", "set", "--comment-limit", "8", "unexpected"},
		{"logs", "clear", "--yes", "unexpected"},
		{"schedule", "install", "unexpected", "--at", "11:00"},
		{"install", "--yes", "unexpected"}, {"uninstall", "--yes", "unexpected"},
		{"run", "--help"}, {"login", "--help"}, {"cleanup", "confirm", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			paths, err := app.ResolvePaths(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			cfg := app.DefaultConfig()
			cfg.CommentLimit = 9
			if err := app.SaveConfig(paths.Config, cfg); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(paths.Config)
			var output bytes.Buffer
			err = runCLI(context.Background(), append([]string{"--data-dir", paths.DataDir, "--base-url", "http://127.0.0.1:1"}, args...), strings.NewReader(""), &output, &output)
			if err == nil {
				t.Fatal("invalid arguments accepted")
			}
			if args[len(args)-1] == "--help" && !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("help not handled: %v", err)
			}
			after, _ := os.ReadFile(paths.Config)
			if !bytes.Equal(before, after) {
				t.Fatal("config changed")
			}
			if _, err := os.Stat(paths.State); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("state created")
			}
		})
	}
}

func TestHelpDoesNotCreateRuntimeFiles(t *testing.T) {
	for _, args := range [][]string{{"run"}, {"login"}, {"config", "set"}, {"config", "reset"}, {"schedule", "install"}, {"schedule", "uninstall"}, {"cleanup", "confirm"}, {"install"}, {"uninstall"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "new-data")
			arguments := append([]string{"--data-dir", dir}, args...)
			err := runCLI(context.Background(), append(arguments, "--help"), strings.NewReader(""), io.Discard, io.Discard)
			if !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("help: %v", err)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("help created data directory")
			}
		})
	}
}

func TestScheduleOwnershipRules(t *testing.T) {
	paths, err := app.ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	oldInspect, oldInstall, oldUninstall := inspectSchedule, installSchedule, uninstallSchedule
	t.Cleanup(func() { inspectSchedule, installSchedule, uninstallSchedule = oldInspect, oldInstall, oldUninstall })
	inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) {
		return schedule.InstallStatus{Registered: true, DataDir: other}, nil
	}
	uninstallSchedule = func(schedule.Config) error { t.Fatal("foreign task deleted"); return nil }
	if err := scheduleCommand(paths, []string{"uninstall"}, io.Discard, io.Discard); err == nil {
		t.Fatal("foreign uninstall accepted")
	}
	called := false
	installSchedule = func(cfg schedule.Config) error { called = cfg.DataDir == paths.DataDir; return nil }
	var output bytes.Buffer
	if err := scheduleCommand(paths, []string{"install"}, &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !called || !strings.Contains(output.String(), fmt.Sprintf("%q", other)) || !strings.Contains(output.String(), fmt.Sprintf("%q", paths.DataDir)) {
		t.Fatal("switch not disclosed")
	}
}

func TestScheduledInvalidConfigurationIsLogged(t *testing.T) {
	for _, contents := range []string{"{invalid", `{"scheduled_time":"25:00"}`} {
		t.Run(contents, func(t *testing.T) {
			p, err := app.ResolvePaths(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p.Config, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			if err := scheduledCommand(context.Background(), p, "http://127.0.0.1:1"); err == nil {
				t.Fatal("accepted invalid config")
			}
			data, err := os.ReadFile(filepath.Join(p.LogDir, logging.ErrorFile))
			if err != nil || !bytes.HasPrefix(data, []byte("[")) || !bytes.Contains(data, []byte("定时启动失败")) {
				t.Fatalf("missing diagnostic %s %v", data, err)
			}
		})
	}
}
