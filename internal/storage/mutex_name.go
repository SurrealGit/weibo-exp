package storage

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// DirectoryMutexName is shared across Windows sessions by the CLI and cleanup
// helper, including desktop tasks and SSH sessions using the same data directory.
// The caller supplies the canonical directory recorded by installation.
func DirectoryMutexName(canonical string) string {
	return fmt.Sprintf("Global\\weibo-exp-%x", sha256.Sum256([]byte(strings.ToLower(canonical))))
}
