//go:build !windows

package install

import "errors"

func nativeReadUserPath() (userPathValue, error) {
	return userPathValue{}, errors.New("当前系统没有 Windows 用户 PATH")
}

func nativeWriteUserPath(userPathValue) error {
	return errors.New("当前系统没有 Windows 用户 PATH")
}
