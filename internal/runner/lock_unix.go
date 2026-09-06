//go:build darwin || linux

package runner

import (
	"errors"
	"os"
	"syscall"
)

// The directory inode remains locked while uninstall removes run.lock.
// Validate the opened inode in case a previous uninstall removed the directory.
func acquireDirectoryGuard(path string) (func(), error) {
	dir, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if err := lockFile(dir); err != nil {
		dir.Close()
		return nil, err
	}
	opened, err := dir.Stat()
	current, statErr := os.Stat(path)
	if err != nil || statErr != nil || !os.SameFile(opened, current) {
		dir.Close()
		return nil, errors.New("数据目录已改变，请重新执行命令")
	}
	return func() { _ = dir.Close() }, nil
}

func lockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
