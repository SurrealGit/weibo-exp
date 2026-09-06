package schedule

import (
	"errors"
	"strings"
	"testing"
)

func TestWindowsDeleteFailureProvidesRecovery(t *testing.T) {
	cause := errors.New("exit status 1")
	fakePlatform(t, "windows", func(_ string, args ...string) ([]byte, error) {
		switch args[0] {
		case "/Query":
			return []byte("<Task/>"), nil
		case "/Delete":
			return []byte("错误: 拒绝访问。"), cause
		default:
			t.Fatalf("unexpected command: %v", args)
			return nil, nil
		}
	})
	err := uninstallWindows()
	if !errors.Is(err, cause) {
		t.Fatalf("lost cause: %v", err)
	}
	for _, want := range []string{"拒绝访问", "管理员 PowerShell", "schtasks /Delete /TN weibo-exp /F", "普通终端重试卸载"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q: %v", want, err)
		}
	}
}
