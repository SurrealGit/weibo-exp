//go:build !windows

package schedule

import "errors"

func embeddedLauncher() ([]byte, error) {
	return nil, errors.New("后台启动器仅用于 Windows")
}
