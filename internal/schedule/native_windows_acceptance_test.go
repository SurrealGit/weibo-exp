package schedule

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in only. The probe writes local events; it never calls Weibo.
// Rewrite the production task name at the OS boundary to isolate the native job.
func TestNativeWindowsIsolatedLifecycle(t *testing.T) {
	if runtime.GOOS != "windows" || os.Getenv("WEIBO_TEST_NATIVE_WINDOWS") != "1" {
		t.Skip("opt-in Windows acceptance: WEIBO_TEST_NATIVE_WINDOWS=1")
	}
	dir := t.TempDir()
	probe, err := os.ReadFile(os.Getenv("WEIBO_TEST_WINDOWS_PROBE"))
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "探针 space.exe")
	if err := os.WriteFile(exe, probe, 0700); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("weibo-exp-acceptance-%d-%d", os.Getpid(), time.Now().UnixNano())
	t.Logf("isolated native task: %s", name)
	oldOS, oldLook, oldRun := currentGOOS, lookPath, runCommand
	t.Cleanup(func() {
		currentGOOS, lookPath, runCommand = oldOS, oldLook, oldRun
		_, _ = exec.Command("schtasks.exe", "/End", "/TN", name).CombinedOutput()
		out, err := exec.Command("schtasks.exe", "/Delete", "/TN", name, "/F").CombinedOutput()
		// A query returning an error alone is not proof of deletion; confirm via
		// the native task listing as well, including after a test failure.
		listing, listErr := exec.Command("schtasks.exe", "/Query", "/FO", "CSV", "/NH").CombinedOutput()
		if listErr != nil || strings.Contains(string(listing), name) {
			t.Errorf("cleanup unverified for %s: %s %v %v", name, out, err, listErr)
		} else {
			t.Logf("cleanup verified: %s absent", name)
		}
	})
	currentGOOS, lookPath = "windows", exec.LookPath
	failNextCreate := false
	runCommand = func(command string, args ...string) ([]byte, error) {
		if command != "schtasks.exe" {
			return nil, fmt.Errorf("unexpected command: %s", command)
		}
		isolated := append([]string(nil), args...)
		for i, arg := range isolated {
			if arg == WindowsName {
				isolated[i] = name
			}
		}
		if failNextCreate && len(isolated) > 0 && isolated[0] == "/Create" {
			failNextCreate = false
			if err := os.WriteFile(isolated[4], []byte("invalid XML for rollback acceptance"), 0600); err != nil {
				return nil, err
			}
		}
		output, err := exec.Command(command, isolated...).CombinedOutput()
		if err == nil && len(isolated) > 1 && isolated[0] == "/Query" && isolated[1] == "/FO" {
			// The test maps our task into the production task's namespace. The
			// listing must use that same mapping, even when a real task exists.
			return isolatedWindowsListing(decodeTaskText(output), name)
		}
		return output, err
	}
	initial := time.Now().Add(10 * time.Minute)
	cfg := Config{Executable: exe, DataDir: dir, Hour: initial.Hour(), Minute: initial.Minute()}
	if err := Install(cfg); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(cfg)
	if err != nil || !status.Registered || status.DailyTime != initial.Format("15:04") || status.DataDir != dir || status.Executable != exe {
		t.Fatalf("initial native config: %+v %v", status, err)
	}
	failNextCreate = true
	if err := Install(cfg); err == nil {
		t.Fatal("invalid native update unexpectedly succeeded")
	}
	status, err = Inspect(cfg)
	if err != nil || !status.Registered || status.DailyTime != initial.Format("15:04") || status.Executable != exe || status.DataDir != dir {
		t.Fatalf("native rollback did not restore original task: %+v %v", status, err)
	}
	t.Log("native failed update restored original task")
	events := func() []struct {
		Time     time.Time
		Elevated bool
	} {
		data, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
		var result []struct {
			Time     time.Time
			Elevated bool
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var event struct {
				Time     time.Time
				Elevated bool
			}
			if json.Unmarshal([]byte(line), &event) == nil {
				result = append(result, event)
			}
		}
		return result
	}
	waitFor := func(deadline time.Time, ready func() bool) {
		t.Helper()
		for time.Now().Before(deadline) {
			if ready() {
				return
			}
			time.Sleep(300 * time.Millisecond)
		}
		t.Fatalf("native trigger timed out; events: %+v", events())
	}
	if out, err := exec.Command("schtasks.exe", "/Run", "/TN", name).CombinedOutput(); err != nil {
		t.Fatalf("run: %s %v", out, err)
	}
	waitFor(time.Now().Add(20*time.Second), func() bool { return len(events()) > 0 })
	for _, event := range events() {
		if event.Elevated {
			t.Fatal("LeastPrivilege probe ran elevated")
		}
	}
	t.Log("native manual trigger executed with standard-user token")
	due := time.Now().Add(90 * time.Second).Truncate(time.Minute)
	cfg.Hour, cfg.Minute = due.Hour(), due.Minute()
	if err := Install(cfg); err != nil {
		t.Fatal(err)
	}
	status, err = Inspect(cfg)
	if err != nil || status.DailyTime != due.Format("15:04") || status.RetryInterval != RetryDisplay {
		t.Fatalf("updated config: %+v %v", status, err)
	}
	t.Logf("waiting for real calendar trigger: %s", due.Format(time.RFC3339))
	waitFor(due.Add(30*time.Second), func() bool {
		for _, event := range events() {
			if !event.Time.Before(due) {
				return true
			}
		}
		return false
	})
	t.Logf("native events: %+v", events())
	if err := Uninstall(cfg); err != nil {
		t.Fatal(err)
	}
	status, err = Inspect(cfg)
	if err != nil || status.Registered {
		t.Fatalf("native uninstall: %+v %v", status, err)
	}
	t.Log("production uninstall verified")
}

func isolatedWindowsListing(data []byte, name string) ([]byte, error) {
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	w := csv.NewWriter(&output)
	for _, row := range rows {
		if len(row) > 0 && strings.TrimPrefix(row[0], `\`) == name {
			row[0] = `\` + WindowsName
			if err := w.Write(row); err != nil {
				return nil, err
			}
		}
	}
	w.Flush()
	return output.Bytes(), w.Error()
}

func TestNativeWindowsListingIsolation(t *testing.T) {
	for _, tc := range []struct{ listing, want string }{
		{"\"\\weibo-exp\",\"N/A\",\"Disabled\"\n", ""},
		{"\"\\weibo-exp\",\"N/A\",\"Disabled\"\n\"\\fixture-task\",\"N/A\",\"Ready\"\n", "\\weibo-exp,N/A,Ready\n"},
	} {
		got, err := isolatedWindowsListing([]byte(tc.listing), "fixture-task")
		if err != nil || string(got) != tc.want {
			t.Fatalf("listing=%q: got %q %v, want %q", tc.listing, got, err, tc.want)
		}
	}
}
