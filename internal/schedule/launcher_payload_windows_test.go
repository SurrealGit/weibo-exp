package schedule

import (
	"bytes"
	"debug/pe"
	"testing"
)

func TestEmbeddedLauncherIsGUI(t *testing.T) {
	data, err := embeddedLauncher()
	if err != nil {
		t.Skip("run go generate ./internal/schedule to test the Windows payload")
	}
	file, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	header, ok := file.OptionalHeader.(*pe.OptionalHeader64)
	if !ok || header.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		t.Fatal("embedded helper is not a GUI executable")
	}
}
