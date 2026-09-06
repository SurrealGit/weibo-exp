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

// Opt in explicitly: this test registers a uniquely named native job, never
// the user's real task. Ordinary go test runs skip it even on macOS.
func TestNativeLaunchdIsolatedLifecycle(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getenv("WEIBO_TEST_NATIVE_LAUNCHD") != "1" {
		t.Skip("opt-in macOS acceptance: WEIBO_TEST_NATIVE_LAUNCHD=1")
	}
	dir, err := os.MkdirTemp("", "weibo-launchd-acceptance-")
	if err != nil {
		t.Fatal(err)
	}
	label := fmt.Sprintf("local.weibo-exp.acceptance.%d.%d", os.Getpid(), time.Now().UnixNano())
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
	t.Logf("isolated target: %s; temporary directory: %s", target, dir)
	probe, err := os.ReadFile("testdata/launchd-probe.sh")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Executable: filepath.Join(dir, "probe.sh"), DataDir: dir, LaunchAgent: filepath.Join(dir, "test.plist")}
	if err := os.WriteFile(cfg.Executable, probe, 0700); err != nil {
		t.Fatal(err)
	}
	oldOS, oldLook, oldRun := currentGOOS, lookPath, runCommand
	t.Cleanup(func() {
		currentGOOS, lookPath, runCommand = oldOS, oldLook, oldRun
		// Exact unique target only. Preserve test files if cleanup cannot be proved.
		_, _ = exec.Command("launchctl", "bootout", target).CombinedOutput()
		out, err := exec.Command("launchctl", "print", target).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "Could not find service") {
			t.Errorf("cannot confirm cleanup; retained %s, target %s: %s %v", dir, target, out, err)
			return
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
		t.Logf("cleanup verified: %s absent; temporary files removed", target)
	})
	currentGOOS, lookPath = "darwin", exec.LookPath
	// Exercise production install/inspect/uninstall logic. Rewrite only its fixed
	// label at the OS boundary so no production task can be registered or removed.
	runCommand = func(name string, args ...string) ([]byte, error) {
		if name != "launchctl" {
			return nil, fmt.Errorf("unexpected command %q", name)
		}
		isolated := append([]string(nil), args...)
		for i := range isolated {
			isolated[i] = strings.ReplaceAll(isolated[i], Label, label)
		}
		if len(isolated) == 3 && isolated[0] == "bootstrap" {
			if isolated[2] != cfg.LaunchAgent {
				return nil, fmt.Errorf("unexpected plist path")
			}
			data, err := os.ReadFile(cfg.LaunchAgent)
			if err != nil {
				return nil, err
			}
			text := strings.ReplaceAll(string(data), Label, label)
			if !strings.Contains(text, "<string>"+label+"</string>") || strings.Contains(text, Label) {
				return nil, fmt.Errorf("unsafe test label")
			}
			if err := os.WriteFile(cfg.LaunchAgent, []byte(text), 0600); err != nil {
				return nil, err
			}
		}
		for _, arg := range isolated {
			if strings.Contains(arg, Label) {
				return nil, fmt.Errorf("production label must never reach launchctl")
			}
		}
		return exec.Command(name, isolated...).CombinedOutput()
	}
	logPath := filepath.Join(dir, "events.log")
	waitFor := func(description string, timeout time.Duration, ready func(string) bool) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			data, _ := os.ReadFile(logPath)
			if ready(string(data)) {
				t.Log(description)
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
		out, _ := exec.Command("launchctl", "print", target).CombinedOutput()
		data, _ := os.ReadFile(logPath)
		t.Fatalf("timeout: %s\nevents: %s\nlaunchd: %s", description, data, out)
	}
	initial := time.Now().Add(10 * time.Minute)
	cfg.Hour, cfg.Minute = initial.Hour(), initial.Minute()
	if err := Install(cfg); err != nil {
		t.Fatal(err)
	}
	waitFor("RunAtLoad started isolated probe", 15*time.Second, func(log string) bool { return strings.Count(log, " start\n") >= 1 })
	if err := os.WriteFile(filepath.Join(dir, "hold"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	// launchd throttles very frequent launches; allow it to start the held probe.
	if out, err := exec.Command("launchctl", "kickstart", target).CombinedOutput(); err != nil {
		t.Fatalf("kickstart: %s %v", out, err)
	}
	waitFor("probe running before configuration update", 20*time.Second, func(log string) bool { return strings.Contains(log, " holding\n") })
	if out, err := exec.Command("launchctl", "print", target).CombinedOutput(); err != nil || !strings.Contains(string(out), "state = running") {
		t.Fatalf("expected running probe: %s %v", out, err)
	}
	if err := os.Remove(filepath.Join(dir, "hold")); err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(75 * time.Second).Truncate(time.Minute)
	cfg.Hour, cfg.Minute = due.Hour(), due.Minute()
	if err := Install(cfg); err != nil {
		t.Fatal(err)
	}
	waitFor("running task replaced and new RunAtLoad executed", 20*time.Second, func(log string) bool { return strings.Count(log, " start\n") >= 3 })
	status, err := Inspect(cfg)
	if err != nil || !status.Registered || status.DailyTime != due.Format("15:04") || status.Executable != cfg.Executable || status.DataDir != dir || status.RetryInterval != RetryDisplay {
		t.Fatalf("updated config: %+v %v", status, err)
	}
	t.Logf("awaiting real calendar trigger at %s (original %02d:%02d)", due.Format(time.RFC3339), initial.Hour(), initial.Minute())
	waitFor("updated calendar triggered probe", time.Until(due)+25*time.Second, func(log string) bool {
		return !time.Now().Before(due) && strings.Count(log, " start\n") >= 4
	})
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("isolated events:\n%s", data)
	if err := Uninstall(cfg); err != nil {
		t.Fatal(err)
	}
	status, err = Inspect(cfg)
	if err != nil || status.Registered || status.FileExists {
		t.Fatalf("uninstall: %+v %v", status, err)
	}
	t.Log("native uninstall verified")
}
