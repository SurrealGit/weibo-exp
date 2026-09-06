package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomicJSONAndErrorPreservesPrevious(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "state.json")
	if err := WriteJSON(path, map[string]int{"value": 1}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(path, make(chan int), 0600); err == nil {
		t.Fatal("unsupported JSON should fail")
	}
	var state map[string]int
	if err := ReadJSON(path, &state); err != nil || state["value"] != 1 {
		t.Fatalf("%+v %v", state, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Fatal("temporary file leaked")
		}
	}
}
