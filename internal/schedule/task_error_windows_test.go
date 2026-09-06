package schedule

import (
	"errors"
	"strings"
	"syscall"
	"testing"
)

func TestWindowsChineseTaskError(t *testing.T) {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	cp, _, _ := kernel.NewProc("GetConsoleOutputCP").Call()
	if cp == 0 {
		t.Skip("requires an attached console")
	}
	setCP := kernel.NewProc("SetConsoleOutputCP")
	ok, _, err := setCP.Call(936)
	if ok == 0 {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if ok, _, err := setCP.Call(cp); ok == 0 {
			t.Errorf("restore console code page: %v", err)
		}
	})
	// schtasks Chinese '拒绝访问', encoded with the console's GBK code page.
	raw := []byte{0xbe, 0xdc, 0xbe, 0xf8, 0xb7, 0xc3, 0xce, 0xca}
	result := commandError("schtasks /Delete", raw, errors.New("exit status 1"))
	if !strings.Contains(result.Error(), "拒绝访问") {
		t.Fatalf("undecoded native error: %v", result)
	}
}
