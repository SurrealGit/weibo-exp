package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/SurrealGit/weibo-exp/internal/storage"
)

var lockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")

func acquireDirectoryGuard(path string) (func(), error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	name, err := syscall.UTF16PtrFromString(storage.DirectoryMutexName(canonical))
	if err != nil {
		return nil, err
	}
	dll := syscall.NewLazyDLL("kernel32.dll")
	create := dll.NewProc("CreateMutexW")
	wait := dll.NewProc("WaitForSingleObject")
	release := dll.NewProc("ReleaseMutex")
	// Windows mutex ownership is thread-affine.
	runtime.LockOSThread()
	handle, _, callErr := create.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		runtime.UnlockOSThread()
		return nil, callErr
	}
	result, _, waitErr := wait.Call(handle, 0)
	if result != 0 && result != 0x80 {
		syscall.CloseHandle(syscall.Handle(handle))
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("数据目录正在使用（等待结果 %d）: %v", result, waitErr)
	}
	return func() { release.Call(handle); syscall.CloseHandle(syscall.Handle(handle)); runtime.UnlockOSThread() }, nil
}

func lockFile(file *os.File) error {
	var overlapped syscall.Overlapped
	result, _, err := lockFileEx.Call(file.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if result == 0 {
		return err
	}
	return nil
}
