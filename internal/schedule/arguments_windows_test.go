package schedule

import (
	"reflect"
	"syscall"
	"testing"
	"unsafe"
)

// Validate the generated quoting against Windows, not our inspection parser.
// This calls a string parser only: no process or native task is started.
func TestWindowsTaskArgumentsNativeParser(t *testing.T) {
	parse := func(command string) []string {
		t.Helper()
		value, err := syscall.UTF16PtrFromString(command)
		if err != nil {
			t.Fatal(err)
		}
		var count int32
		argv, err := syscall.CommandLineToArgv(value, &count)
		if err != nil {
			t.Fatal(err)
		}
		defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(argv)))
		result := make([]string, count)
		for i := range result {
			result[i] = syscall.UTF16ToString(argv[i][:])
		}
		return result
	}
	for _, path := range []string{`C:\data`, `C:\数据 space`, `C:\`, `C:\data space\`, `\\server\share\`} {
		want := []string{"weibo-exp.exe", "--data-dir", path, "scheduled-run"}
		got := parse("weibo-exp.exe --data-dir " + windowsQuote(path) + " scheduled-run")
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("parsed = %q, want %q", got, want)
		}
	}
	// Regression control: the previous literal quoting consumes scheduled-run.
	old := parse(`weibo-exp.exe --data-dir "C:\" scheduled-run`)
	if len(old) == 4 && old[3] == "scheduled-run" {
		t.Fatalf("expected old trailing-backslash construction to fail: %q", old)
	}
}
