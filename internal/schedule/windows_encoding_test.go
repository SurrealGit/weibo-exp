package schedule

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsInstallAndRollbackUseUTF16(t *testing.T) {
	cfg := Config{Executable: filepath.Join(t.TempDir(), "程序 😀.exe"), DataDir: t.TempDir(), Hour: 10}
	old, err := RenderWindowsTaskXML(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Hour = 14
	creates := 0
	var temporary []string
	fakePlatform(t, "windows", func(_ string, args ...string) ([]byte, error) {
		if args[0] == "/Query" {
			return old, nil
		}
		if args[0] != "/Create" {
			t.Fatalf("unexpected command %v", args)
		}
		creates++
		path := args[4]
		temporary = append(temporary, path)
		encoded, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(encoded, []byte{0xff, 0xfe}) {
			t.Fatal("XML lacks UTF-16LE BOM")
		}
		decoded := decodeTaskXML(encoded)
		if err := validateXML(decoded); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(decoded), "程序 😀.exe") {
			t.Fatal("Unicode path lost")
		}
		if creates == 1 {
			if windowsTime(string(decoded)) != "14:00" {
				t.Fatal("wrong update")
			}
			return []byte("injected native error"), errors.New("exit 1")
		}
		if !bytes.Equal(decoded, old) {
			t.Fatal("rollback XML differs")
		}
		return nil, nil
	})
	if err := Install(cfg); err == nil || !strings.Contains(err.Error(), "injected native error") {
		t.Fatalf("lost original failure: %v", err)
	}
	if creates != 2 {
		t.Fatalf("create calls: %d", creates)
	}
	for _, path := range temporary {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("temporary XML remains: %s", path)
		}
	}
}
