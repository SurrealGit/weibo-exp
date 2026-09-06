package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDirectoryGuardSurvivesLockUnlinkAcrossProcesses(t *testing.T) {
	if os.Getenv("WEIBO_TEST_GUARD_CHILD") != "" {
		release, err := AcquireLock(os.Getenv("WEIBO_TEST_GUARD_PATH"))
		if err == nil {
			release()
			t.Fatal("child acquired removed lock")
		}
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows disallows deleting open file; native helper tested separately")
	}
	path := filepath.Join(t.TempDir(), "run.lock")
	release, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestDirectoryGuardSurvivesLockUnlinkAcrossProcesses$")
	child.Env = append(os.Environ(), "WEIBO_TEST_GUARD_CHILD=1", "WEIBO_TEST_GUARD_PATH="+path)
	if data, err := child.CombinedOutput(); err != nil {
		t.Fatalf("%s %v", data, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("child recreated run.lock")
	}
}
