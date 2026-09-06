package weibo

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

var shellExecuteW = syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")
var ole32 = syscall.NewLazyDLL("ole32.dll")
var coInitializeEx = ole32.NewProc("CoInitializeEx")
var coUninitialize = ole32.NewProc("CoUninitialize")

// OpenImage opens the local QR image with the user's associated application.
// Use the documented Shell API directly instead of a rundll32 intermediary.
func OpenImage(path string) error {
	file, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	verb, _ := syscall.UTF16PtrFromString("open")
	// Shell extensions can require STA COM. Initialization and teardown must
	// occur on the same OS thread as ShellExecuteW.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := coInitializeEx.Call(0, 0x2|0x4) // APARTMENTTHREADED | DISABLE_OLE1DDE
	if int32(hr) < 0 {
		return fmt.Errorf("初始化图片打开组件失败：HRESULT 0x%08x", uint32(hr))
	}
	defer coUninitialize.Call()
	result, _, _ := shellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, 1) // SW_SHOWNORMAL
	return shellOpenResult(result)
}

func shellOpenResult(result uintptr) error {
	// This API returns a legacy status, not a process handle; GetLastError alone
	// cannot distinguish success. Values above 32 mean the request succeeded.
	if result <= 32 {
		return fmt.Errorf("打开二维码图片失败：ShellExecuteW 返回 %d；请检查图片关联应用", result)
	}
	return nil
}
