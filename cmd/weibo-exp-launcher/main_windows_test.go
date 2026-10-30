package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

type probeResult struct {
	PID     int
	Console uintptr
}

func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "--data-dir" && os.Args[3] == "scheduled-run" {
		dir := os.Args[2]
		window, _, _ := kernel.NewProc("GetConsoleWindow").Call()
		data, _ := json.Marshal(probeResult{os.Getpid(), window})
		if os.WriteFile(filepath.Join(dir, "probe.json"), data, 0600) != nil {
			os.Exit(2)
		}
		if _, err := os.Stat(filepath.Join(dir, "hold")); err == nil {
			time.Sleep(30 * time.Second)
		} else {
			time.Sleep(500 * time.Millisecond)
		}
		os.Exit(7)
	}
	os.Exit(m.Run())
}

func TestLaunchWaitsAndReturnsExitCodeWithoutConsole(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) == 8 && unsafe.Sizeof(jobLimits{}) != 144 {
		t.Fatal("incorrect job structure layout")
	}
	dir := t.TempDir()
	start := time.Now()
	code, err := launch(os.Args[0], dir)
	if err != nil || code != 7 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Fatal("did not wait")
	}
	var event probeResult
	data, err := os.ReadFile(filepath.Join(dir, "probe.json"))
	if err != nil || json.Unmarshal(data, &event) != nil || event.Console != 0 {
		t.Fatalf("console probe: %s %v", data, err)
	}
}

func TestGUIExitAndForcedStop(t *testing.T) {
	helper := os.Getenv("WEIBO_TEST_LAUNCHER")
	if helper == "" {
		t.Skip("set WEIBO_TEST_LAUNCHER to the generated GUI executable")
	}
	for _, hold := range []bool{false, true} {
		dir := t.TempDir()
		if hold {
			if err := os.WriteFile(filepath.Join(dir, "hold"), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command(helper, "--executable", os.Args[0], "--data-dir", dir, "scheduled-run")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill() })
		var event probeResult
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			data, _ := os.ReadFile(filepath.Join(dir, "probe.json"))
			if json.Unmarshal(data, &event) == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if event.PID == 0 || event.Console != 0 {
			t.Fatalf("%+v", event)
		}
		if !hold {
			err := cmd.Wait()
			if err == nil || cmd.ProcessState.ExitCode() != 7 {
				t.Fatalf("GUI exit: %v", err)
			}
			continue
		}
		child, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(event.PID))
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = cmd.Wait()
		result, err := syscall.WaitForSingleObject(child, 5000)
		_ = syscall.CloseHandle(child)
		if err != nil || result != syscall.WAIT_OBJECT_0 {
			t.Fatalf("orphaned CLI: result=%d err=%v", result, err)
		}
	}
}

func TestLauncherRejectsUnexpectedCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"--executable", os.Args[0], "--data-dir", t.TempDir(), "run"}, {"--executable", os.Args[0], "--data-dir", t.TempDir(), "scheduled-run"}} {
		if _, err := run(args); err == nil {
			t.Fatal("accepted invalid or recursive command")
		}
	}
}

func TestActualCLIStartupFailureIsLogged(t *testing.T) {
	helper, cli := os.Getenv("WEIBO_TEST_LAUNCHER"), os.Getenv("WEIBO_TEST_MAIN")
	if helper == "" || cli == "" {
		t.Skip("set WEIBO_TEST_LAUNCHER and WEIBO_TEST_MAIN")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("invalid test config"), 0600); err != nil {
		t.Fatal(err)
	}
	// Invalid configuration stops before any account or network access.
	cmd := exec.Command(helper, "--executable", cli, "--data-dir", dir, "scheduled-run")
	if err := cmd.Run(); err == nil || cmd.ProcessState.ExitCode() != 1 {
		t.Fatalf("startup exit: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "logs", "scheduled-error.log"))
	if err != nil || !strings.Contains(string(data), "config.json") {
		t.Fatalf("startup log missing: %q %v", data, err)
	}
}
