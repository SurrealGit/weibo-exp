//go:build !darwin && !linux && !windows

package runner

import (
	"errors"
	"os"
)

func lockFile(*os.File) error { return errors.New("当前平台尚不支持安全任务锁") }

func acquireDirectoryGuard(string) (func(), error) {
	return nil, errors.New("当前平台尚不支持安全任务锁")
}
