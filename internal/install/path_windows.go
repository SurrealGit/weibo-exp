package install

import (
	"encoding/binary"
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

var registryAPI = syscall.NewLazyDLL("advapi32.dll")
var regCreateKey = registryAPI.NewProc("RegCreateKeyExW")
var regSetValue = registryAPI.NewProc("RegSetValueExW")
var regDeleteValue = registryAPI.NewProc("RegDeleteValueW")
var sendSettingChange = syscall.NewLazyDLL("user32.dll").NewProc("SendMessageTimeoutW")

func nativeReadUserPath() (userPathValue, error) {
	var key syscall.Handle
	name, _ := syscall.UTF16PtrFromString("Environment")
	err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, name, 0, syscall.KEY_QUERY_VALUE, &key)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return userPathValue{}, nil
	}
	if err != nil {
		return userPathValue{}, err
	}
	defer syscall.RegCloseKey(key)
	value, _ := syscall.UTF16PtrFromString("Path")
	var kind, size uint32
	err = syscall.RegQueryValueEx(key, value, nil, &kind, nil, &size)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return userPathValue{}, nil
	}
	if err != nil {
		return userPathValue{}, err
	}
	if kind != syscall.REG_SZ && kind != syscall.REG_EXPAND_SZ {
		return userPathValue{}, errors.New("用户 Path 注册表值不是字符串")
	}
	// A concurrently expanded value returns ERROR_MORE_DATA and the caller retries.
	data := make([]byte, size+2)
	if err := syscall.RegQueryValueEx(key, value, nil, &kind, &data[0], &size); err != nil {
		return userPathValue{}, err
	}
	if size%2 != 0 {
		return userPathValue{}, errors.New("用户 Path 注册表值编码无效")
	}
	chars := make([]uint16, size/2)
	for i := range chars {
		chars[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	return userPathValue{Exists: true, Value: syscall.UTF16ToString(chars), Type: kind}, nil
}

func nativeWriteUserPath(value userPathValue) error {
	name, _ := syscall.UTF16PtrFromString("Environment")
	var key syscall.Handle
	result, _, _ := regCreateKey.Call(uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(name)), 0, 0, 0, syscall.KEY_SET_VALUE, 0, uintptr(unsafe.Pointer(&key)), 0)
	if result != 0 {
		return fmt.Errorf("打开用户环境变量：%w", syscall.Errno(result))
	}
	defer syscall.RegCloseKey(key)
	pathName, _ := syscall.UTF16PtrFromString("Path")
	if value.Exists {
		if value.Type != syscall.REG_SZ && value.Type != syscall.REG_EXPAND_SZ {
			return errors.New("用户 Path 注册表类型无效")
		}
		chars, err := syscall.UTF16FromString(value.Value)
		if err != nil {
			return err
		}
		result, _, _ = regSetValue.Call(uintptr(key), uintptr(unsafe.Pointer(pathName)), 0, uintptr(value.Type), uintptr(unsafe.Pointer(&chars[0])), uintptr(len(chars)*2))
	} else {
		result, _, _ = regDeleteValue.Call(uintptr(key), uintptr(unsafe.Pointer(pathName)))
		if result == uintptr(syscall.ERROR_FILE_NOT_FOUND) {
			result = 0
		}
	}
	if result != 0 {
		return fmt.Errorf("更新用户 PATH：%w", syscall.Errno(result))
	}
	// Notify desktop applications; registry persistence does not depend on a
	// particular window responding. Existing terminals retain their environment.
	sendSettingChange.Call(0xffff, 0x1a, 0, uintptr(unsafe.Pointer(name)), 2, 1000, 0)
	return nil
}
