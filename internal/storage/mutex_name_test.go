package storage

import (
	"strings"
	"testing"
)

func TestDirectoryMutexAcrossSessions(t *testing.T) {
	name := DirectoryMutexName(`C:\Private\Data`)
	if !strings.HasPrefix(name, `Global\weibo-exp-`) {
		t.Fatal("mutex must span Windows sessions")
	}
	if name != DirectoryMutexName(`c:\private\data`) {
		t.Fatal("case changed mutex identity")
	}
	if name == DirectoryMutexName(`C:\Private\Other`) {
		t.Fatal("different directories share a mutex")
	}
}
