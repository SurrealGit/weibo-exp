package weibo

import "testing"

func TestShellOpenResult(t *testing.T) {
	for _, code := range []uintptr{0, 2, 5, 31, 32, 33, 42} {
		if err := shellOpenResult(code); (err == nil) != (code > 32) {
			t.Errorf("code %d: %v", code, err)
		}
	}
}

func TestOpenImageRejectsNUL(t *testing.T) {
	if err := OpenImage("bad\x00path.png"); err == nil {
		t.Fatal("invalid Windows path accepted")
	}
}
