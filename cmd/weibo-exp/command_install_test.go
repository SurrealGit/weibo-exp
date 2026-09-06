package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/install"
	"github.com/SurrealGit/weibo-exp/internal/logging"
	"github.com/SurrealGit/weibo-exp/internal/runner"
	"github.com/SurrealGit/weibo-exp/internal/schedule"
)

func installedFixture(t *testing.T) (app.Paths, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "data")
	paths, err := app.ResolvePaths(data)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "download")
	if err := os.WriteFile(source, []byte("fake program"), 0700); err != nil {
		t.Fatal(err)
	}
	oldExe, oldInspect, oldInstall, oldUninstall := executablePath, inspectSchedule, installSchedule, uninstallSchedule
	t.Cleanup(func() {
		executablePath, inspectSchedule, installSchedule, uninstallSchedule = oldExe, oldInspect, oldInstall, oldUninstall
	})
	executablePath = func() (string, error) { return source, nil }
	inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) { return schedule.InstallStatus{}, nil }
	installSchedule = func(schedule.Config) error { t.Fatal("unexpected native write"); return nil }
	uninstallSchedule = func(schedule.Config) error { t.Fatal("unexpected native write"); return nil }
	dir := filepath.Join(root, "installed")
	err = installationCommand(paths, []string{"--dir", dir, "--yes", "--no-path"}, strings.NewReader(""), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.Config, paths.Session, paths.State} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("installation touched runtime data: %s: %v", path, err)
		}
	}
	// Seed configuration for uninstall fixtures, independently of installation.
	if err := app.SaveConfig(paths.Config, app.DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	return paths, dir
}

func TestUninstallCleanOrKeepConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("real file deletion test; Windows helper is separately inspected")
	}
	for _, keep := range []bool{false, true} {
		name := "clean"
		if keep {
			name = "keep"
		}
		t.Run(name, func(t *testing.T) {
			paths, dir := installedFixture(t)
			os.WriteFile(paths.Session, []byte("{}"), 0600)
			app.SaveState(paths.State, app.NewState())
			os.MkdirAll(paths.LogDir, 0700)
			os.WriteFile(filepath.Join(paths.LogDir, logging.OutputFile), []byte("logs"), 0600)
			os.MkdirAll(filepath.Join(paths.DataDir, "login-tmp"), 0700)
			os.WriteFile(filepath.Join(paths.DataDir, "login-tmp", "weibo-login-test.png"), []byte("qr"), 0600)
			args := []string{"--yes"}
			if keep {
				args = append(args, "--keep-config")
			}
			var output bytes.Buffer
			if err := uninstallationCommand(paths, args, strings.NewReader(""), &output, io.Discard); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("installation remains: %v", err)
			}
			if _, err := os.Stat(paths.Config); keep != (err == nil) {
				t.Fatalf("keep=%v config=%v", keep, err)
			}
			if !keep {
				if _, err := os.Stat(paths.DataDir); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("data directory remains: %v", err)
				}
			}
		})
	}
}

func TestUninstallPreservesUnknownFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses deferred cleanup")
	}
	paths, dir := installedFixture(t)
	unknown := filepath.Join(dir, "my-document.txt")
	os.WriteFile(unknown, []byte("user"), 0600)
	var output bytes.Buffer
	if err := uninstallationCommand(paths, []string{"--yes"}, strings.NewReader(""), &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(unknown); err != nil || string(b) != "user" {
		t.Fatal("deleted unrelated file")
	}
	if !strings.Contains(output.String(), "保留目录") {
		t.Fatal("did not report residual directory")
	}
}

func TestUninstallPendingOrSchedulerFailurePreservesData(t *testing.T) {
	for _, pending := range []bool{true, false} {
		t.Run(map[bool]string{true: "pending", false: "scheduler-error"}[pending], func(t *testing.T) {
			paths, _ := installedFixture(t)
			if pending {
				s := app.NewState()
				s.AddPendingDelete(app.PendingDelete{Kind: "comment", ContentID: "unfinished"})
				app.SaveState(paths.State, s)
			} else {
				inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) {
					return schedule.InstallStatus{Registered: true, DataDir: paths.DataDir}, nil
				}
				uninstallSchedule = func(schedule.Config) error { return errors.New("denied") }
			}
			if err := uninstallationCommand(paths, []string{"--yes"}, strings.NewReader(""), io.Discard, io.Discard); err == nil {
				t.Fatal("uninstall should stop")
			}
			if _, err := os.Stat(paths.Config); err != nil {
				t.Fatal("deleted config on failure")
			}
			if _, err := install.Read(paths.DataDir); err != nil {
				t.Fatal("lost receipt")
			}
		})
	}
}

func TestInstallOnlyPreservesRuntimeDataAndSchedule(t *testing.T) {
	paths, dir := installedFixture(t)
	// Installing/upgrading must not inspect the scheduler or parse login data.
	inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) {
		t.Fatal("installation inspected native scheduler")
		return schedule.InstallStatus{}, nil
	}
	contents := map[string]string{
		paths.Config:  "{\"scheduled_time\":\"11:23\",\"custom_field\":true}\n",
		paths.Session: "existing session",
		paths.State:   "existing progress",
	}
	for path, content := range contents {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := installationCommand(paths, []string{"--yes", "--no-path"}, strings.NewReader(""), &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	for path, want := range contents {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("changed %s: %q %v", path, got, err)
		}
	}
	record, err := install.Read(paths.DataDir)
	if err != nil || record.Executable != filepath.Join(dir, install.EntryName()) {
		t.Fatalf("default upgrade destination: %+v %v", record, err)
	}
	entry := "weibo-exp"
	if runtime.GOOS == "windows" {
		entry = `.\weibo-exp.exe`
	}
	for _, hint := range []string{entry + " login", entry + " run", entry + " schedule install"} {
		if !strings.Contains(output.String(), hint) {
			t.Fatalf("missing next-step hint %q: %s", hint, output.String())
		}
	}
}

func TestInstallDefaultPathAndUninstallKeepConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native registry acceptance is separate")
	}
	paths, dir := installedFixture(t)
	root := filepath.Dir(dir)
	t.Setenv("HOME", root)
	t.Setenv("ZDOTDIR", root)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", root) // No real commands or shell settings are consulted.
	var output bytes.Buffer
	if err := installationCommand(paths, []string{"--yes"}, strings.NewReader(""), &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"重新打开终端", "任意目录", "weibo-exp login", "weibo-exp run", "weibo-exp schedule install"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q: %s", want, output.String())
		}
	}
	record, err := install.Read(paths.DataDir)
	if err != nil || record.Path == nil {
		t.Fatalf("%+v %v", record, err)
	}
	file := filepath.Join(root, ".zshrc")
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
	if err := uninstallationCommand(paths, []string{"--yes", "--keep-config"}, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("PATH residue: %v", err)
	}
	if _, err := os.Stat(paths.Config); err != nil {
		t.Fatal("configuration was not retained")
	}
}

func TestUninstallEditedPathRetainsProgramAndData(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native registry acceptance is separate")
	}
	paths, dir := installedFixture(t)
	root := filepath.Dir(dir)
	t.Setenv("HOME", root)
	t.Setenv("ZDOTDIR", root)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", root)
	if err := installationCommand(paths, []string{"--yes"}, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, ".zshrc")
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(strings.ReplaceAll(string(b), "export PATH", "export CUSTOM")), 0600); err != nil {
		t.Fatal(err)
	}
	if err := uninstallationCommand(paths, []string{"--yes"}, strings.NewReader(""), io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "手动移除") {
		t.Fatalf("%v", err)
	}
	for _, file := range []string{paths.Config, filepath.Join(dir, install.EntryName()), filepath.Join(paths.DataDir, install.ReceiptName)} {
		if _, err := os.Stat(file); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUninstallCancellationLockAndForeignSchedule(t *testing.T) {
	for _, scenario := range []string{"cancel", "active-lock", "other-data", "query-failure"} {
		t.Run(scenario, func(t *testing.T) {
			paths, _ := installedFixture(t)
			args := []string{"--yes"}
			switch scenario {
			case "cancel":
				args = nil
			case "active-lock":
				release, err := runner.AcquireLock(paths.Lock)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			case "other-data":
				inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) {
					return schedule.InstallStatus{Registered: true, DataDir: filepath.Join(paths.DataDir, "other")}, nil
				}
			case "query-failure":
				inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) {
					return schedule.InstallStatus{}, errors.New("cannot query scheduler")
				}
			}
			if err := uninstallationCommand(paths, args, strings.NewReader("no\n"), io.Discard, io.Discard); err == nil {
				t.Fatal("expected refusal")
			}
			r, err := install.Read(paths.DataDir)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.CheckOwned(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(paths.Config); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInstallRejectsRemovedSetupOptions(t *testing.T) {
	for _, args := range [][]string{{"--at", "12:34"}, {"--no-login"}, {"--no-schedule"}} {
		if err := installationCommand(app.Paths{}, args, strings.NewReader(""), io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted removed option: %v", args)
		}
	}
}

func TestUninstallRejectsLinkedConfigBeforeStoppingSchedule(t *testing.T) {
	paths, _ := installedFixture(t)
	target := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(target, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.Config); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, paths.Config); err != nil {
		t.Skip(err)
	}
	inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) {
		return schedule.InstallStatus{Registered: true, DataDir: paths.DataDir}, nil
	}
	if err := uninstallationCommand(paths, []string{"--yes"}, strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Fatal("accepted symlink")
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "private" {
		t.Fatal("changed symlink target")
	}
}

// Run the real command dispatcher in a child executable. Only the native
// scheduler is replaced: no formal task, user data, or network is touched.
func TestInstalledExecutableHelper(t *testing.T) {
	payload := os.Getenv("WEIBO_EXP_TEST_INSTALL_ARGS")
	if payload == "" {
		return
	}
	oldTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	http.DefaultTransport = testTransport(func(*http.Request) (*http.Response, error) {
		t.Error("program installation/uninstallation must not make HTTP requests")
		return nil, errors.New("unexpected network request")
	})
	inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) { return schedule.InstallStatus{}, nil }
	installSchedule = func(schedule.Config) error { return errors.New("unexpected native install") }
	uninstallSchedule = func(schedule.Config) error { return errors.New("unexpected native uninstall") }
	var args []string
	if err := json.Unmarshal([]byte(payload), &args); err != nil {
		t.Fatal(err)
	}
	if err := runCLI(context.Background(), args, strings.NewReader(""), os.Stdout, os.Stderr); err != nil {
		t.Fatal(err)
	}
}

func TestRealExecutableInstallDiscoverAndSelfUninstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows self-deletion needs native acceptance")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	t.Setenv("ZDOTDIR", root)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", root)
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, dir := filepath.Join(root, "custom data"), filepath.Join(root, "自定义程序")
	invoke := func(executable string, args ...string) string {
		t.Helper()
		payload, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(executable, "-test.run=^TestInstalledExecutableHelper$")
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "WEIBO_EXP_DATA_DIR=") && !strings.HasPrefix(value, "WEIBO_EXP_TEST_INSTALL_ARGS=") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		cmd.Env = append(cmd.Env, "WEIBO_EXP_TEST_INSTALL_ARGS="+string(payload))
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v\n%s", err, output)
		}
		return string(output)
	}
	if output := invoke(source, "--data-dir", data, "install", "--dir", dir, "--yes"); !strings.Contains(output, "重新打开终端") {
		t.Fatalf("missing terminal restart hint: %s", output)
	}
	entry := filepath.Join(dir, install.EntryName())
	if output := invoke(entry, "config", "show"); !strings.Contains(output, data) {
		t.Fatalf("data discovery failed: %s", output)
	}
	invoke(entry, "uninstall", "--yes")
	for _, path := range []string{data, dir, filepath.Join(root, ".zshrc")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("residue: %s %v", path, err)
		}
	}
}
