package runner

import (
	"fmt"
	"os"
	"path/filepath"
)

// A directory/native guard outlives run.lock during uninstall. Keep the file
// lock as well so existing versions still participate in normal exclusion.
func AcquireLock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	guard, err := acquireDirectoryGuard(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("无法取得任务锁（可能有另一个任务正在运行）: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		guard()
		return nil, err
	}
	if err := lockFile(file); err != nil {
		file.Close()
		guard()
		return nil, fmt.Errorf("无法取得任务锁（可能有另一个任务正在运行）: %w", err)
	}
	return func() { _ = file.Close(); guard() }, nil
}
