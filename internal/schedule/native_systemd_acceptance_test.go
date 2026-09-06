package schedule

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Opt-in real user-manager test. Only a uniquely named, local-only shell probe
// is registered; the production task name never reaches systemctl.
func TestNativeSystemdIsolatedLifecycle(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("WEIBO_TEST_NATIVE_SYSTEMD") != "1" {
		t.Skip("opt-in Linux acceptance: WEIBO_TEST_NATIVE_SYSTEMD=1")
	}
	unitDir := os.Getenv("WEIBO_TEST_SYSTEMD_UNIT_DIR")
	if !filepath.IsAbs(unitDir) {
		t.Fatal("explicit user-manager unit directory required")
	}
	dir := filepath.Join(t.TempDir(), "data space%$")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("weibo-exp-acceptance-%d-%d", os.Getpid(), time.Now().UnixNano())
	var createdDirs []string
	for p := unitDir; ; p = filepath.Dir(p) {
		if _, err := os.Stat(p); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		createdDirs = append(createdDirs, p)
	}
	if err := os.MkdirAll(unitDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Executable: filepath.Join(dir, "探针 space%$.sh"), DataDir: dir,
		SystemdService: filepath.Join(unitDir, name+".service"), SystemdTimer: filepath.Join(unitDir, name+".timer")}
	probe := "#!/bin/sh\nset -eu\ncd -- \"$2\"\nprintf '%s start\\n' \"$(date +%s)\" >> events.log\nwhile [ -e hold ]; do sleep 1; done\nprintf '%s end\\n' \"$(date +%s)\" >> events.log\n"
	if err := os.WriteFile(cfg.Executable, []byte(probe), 0700); err != nil {
		t.Fatal(err)
	}
	native := func(args ...string) ([]byte, error) {
		return exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	}
	oldOS, oldLook, oldRun := currentGOOS, lookPath, runCommand
	t.Cleanup(func() {
		currentGOOS, lookPath, runCommand = oldOS, oldLook, oldRun
		_, _ = native("disable", "--now", name+".timer")
		_, _ = native("stop", name+".service")
		if _, err := os.Stat(cfg.SystemdTimer); err == nil {
			out, err := native("clean", "--what=state", name+".timer")
			if err != nil {
				t.Errorf("state cleanup: %s %v", out, err)
			}
		}
		for _, p := range []string{cfg.SystemdTimer, cfg.SystemdService} {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				t.Error(err)
			}
		}
		_, _ = native("daemon-reload")
		_, _ = native("reset-failed", name+".service", name+".timer")
		for _, suffix := range []string{".service", ".timer"} {
			out, err := native("show", name+suffix, "--property=LoadState", "--value")
			if err != nil || strings.TrimSpace(string(out)) != "not-found" {
				t.Errorf("native cleanup unverified: %s %v", out, err)
			}
		}
		for _, p := range createdDirs {
			_ = os.Remove(p)
		} // empty directories only
		t.Logf("unique units cleaned: %s", name)
	})
	currentGOOS, lookPath = "linux", exec.LookPath
	failRestart := false
	runCommand = func(command string, args ...string) ([]byte, error) {
		if command != "systemctl" {
			return nil, fmt.Errorf("unexpected command %s", command)
		}
		args = append([]string(nil), args...)
		for i := range args {
			args[i] = strings.ReplaceAll(args[i], SystemdName+".timer", name+".timer")
			args[i] = strings.ReplaceAll(args[i], SystemdName+".service", name+".service")
		}
		if len(args) > 1 && args[1] == "daemon-reload" {
			if b, err := os.ReadFile(cfg.SystemdTimer); err == nil {
				if err := os.WriteFile(cfg.SystemdTimer, []byte(strings.ReplaceAll(string(b), "Unit="+SystemdName+".service", "Unit="+name+".service")), 0600); err != nil {
					return nil, err
				}
			}
		}
		if failRestart && len(args) > 1 && args[1] == "restart" {
			failRestart = false
			if err := os.WriteFile(cfg.SystemdTimer, []byte("[Timer]\nOnCalendar=invalid-calendar\n"), 0600); err != nil {
				return nil, err
			}
			_, _ = native("daemon-reload")
		}
		return exec.Command(command, args...).CombinedOutput()
	}
	events := func() string { b, _ := os.ReadFile(filepath.Join(dir, "events.log")); return string(b) }
	wait := func(deadline time.Time, ready func() bool) {
		t.Helper()
		for time.Now().Before(deadline) {
			if ready() {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
		out, _ := native("status", name+".service", name+".timer", "--no-pager")
		t.Fatalf("probe timeout: %s\n%s", events(), out)
	}
	initial := time.Now().Add(10 * time.Minute)
	cfg.Hour, cfg.Minute = initial.Hour(), initial.Minute()
	if err := Install(cfg); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(cfg)
	if err != nil || !status.Registered || status.DailyTime != initial.Format("15:04") || status.Executable != cfg.Executable || status.DataDir != dir {
		t.Fatalf("native inspect: %+v %v", status, err)
	}
	if out, err := native("start", "--no-block", name+".service"); err != nil {
		t.Fatalf("start: %s %v", out, err)
	}
	wait(time.Now().Add(15*time.Second), func() bool { return strings.Contains(events(), " end\n") })
	t.Log("native manual trigger passed with Unicode, space, percent and dollar path")
	failRestart = true
	if err := Install(cfg); err == nil {
		t.Fatal("corrupt timer update accepted")
	}
	status, err = Inspect(cfg)
	if err != nil || status.DailyTime != initial.Format("15:04") || status.Active == nil || !*status.Active {
		t.Fatalf("rollback: %+v %v", status, err)
	}
	t.Log("native failed update restored active timer")
	due := time.Now().Add(90 * time.Second).Truncate(time.Minute)
	cfg.Hour, cfg.Minute = due.Hour(), due.Minute()
	if err := Install(cfg); err != nil {
		t.Fatal(err)
	}
	t.Logf("waiting for calendar trigger: %s", due.Format(time.RFC3339))
	wait(due.Add(80*time.Second), func() bool {
		for _, line := range strings.Split(events(), "\n") {
			var epoch int64
			var kind string
			if _, err := fmt.Sscan(line, &epoch, &kind); err == nil && kind == "end" && epoch >= due.Unix() {
				return true
			}
		}
		return false
	})
	t.Logf("native events: %s", events())
	if err := os.WriteFile(filepath.Join(dir, "hold"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := native("start", "--no-block", name+".service"); err != nil {
		t.Fatalf("start held service: %s %v", out, err)
	}
	wait(time.Now().Add(15*time.Second), func() bool {
		b, _ := native("is-active", name+".service")
		return strings.TrimSpace(string(b)) == "activating"
	})
	if err := Uninstall(cfg); err != nil {
		t.Fatal(err)
	}
	b, _ := native("is-active", name+".service")
	if state := strings.TrimSpace(string(b)); state == "active" || state == "activating" {
		t.Fatal("uninstall left the service running")
	}
	t.Log("uninstall stopped running service and removed timer")
}
