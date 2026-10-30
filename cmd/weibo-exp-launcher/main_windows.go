// The GUI subsystem keeps Task Scheduler from allocating a console. The main
// CLI remains a console application, including when used directly from CMD.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/SurrealGit/weibo-exp/internal/app"
)

var kernel = syscall.NewLazyDLL("kernel32.dll")

func main() {
	code, err := run(os.Args[1:])
	if err != nil {
		if len(os.Args) == 6 {
			logFailure(os.Args[4], err)
		}
		os.Exit(1)
	}
	os.Exit(int(code))
}

func run(args []string) (uint32, error) {
	if len(args) != 5 || args[0] != "--executable" || args[2] != "--data-dir" || args[4] != "scheduled-run" {
		return 0, errors.New("后台启动器参数无效")
	}
	executable, dir := args[1], args[3]
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	if !filepath.IsAbs(executable) || !filepath.IsAbs(dir) || strings.EqualFold(filepath.Clean(self), filepath.Clean(executable)) {
		return 0, errors.New("后台启动器需要独立的主程序绝对路径及数据目录")
	}
	return launch(executable, dir)
}

// A kill-on-close job prevents orphaned CLI processes when Task Scheduler stops
// the launcher. Start suspended so no task work runs before assignment to it.
func launch(executable, dir string) (uint32, error) {
	job, _, callErr := kernel.NewProc("CreateJobObjectW").Call(0, 0)
	if job == 0 {
		return 0, fmt.Errorf("创建任务进程组: %w", callErr)
	}
	defer syscall.CloseHandle(syscall.Handle(job))
	limits := jobLimits{}
	limits.Basic.Flags = 0x2000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	ok, _, callErr := kernel.NewProc("SetInformationJobObject").Call(job, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if ok == 0 {
		return 0, fmt.Errorf("设置任务进程组: %w", callErr)
	}
	application, err := syscall.UTF16PtrFromString(executable)
	if err != nil {
		return 0, err
	}
	command, err := syscall.UTF16PtrFromString(syscall.EscapeArg(executable) + " --data-dir " + syscall.EscapeArg(dir) + " scheduled-run")
	if err != nil {
		return 0, err
	}
	startup := syscall.StartupInfo{Cb: uint32(unsafe.Sizeof(syscall.StartupInfo{}))}
	var process syscall.ProcessInformation
	const createNoWindow, createSuspended = 0x08000000, 0x00000004
	if err := syscall.CreateProcess(application, command, nil, nil, false, createNoWindow|createSuspended, nil, nil, &startup, &process); err != nil {
		return 0, fmt.Errorf("启动定时任务: %w", err)
	}
	defer syscall.CloseHandle(process.Process)
	defer syscall.CloseHandle(process.Thread)
	ok, _, callErr = kernel.NewProc("AssignProcessToJobObject").Call(job, uintptr(process.Process))
	if ok == 0 {
		_ = syscall.TerminateProcess(process.Process, 1)
		_, _ = syscall.WaitForSingleObject(process.Process, syscall.INFINITE)
		return 0, fmt.Errorf("加入任务进程组: %w", callErr)
	}
	resumed, _, callErr := kernel.NewProc("ResumeThread").Call(uintptr(process.Thread))
	if uint32(resumed) == 0xffffffff {
		return 0, fmt.Errorf("启动任务线程: %w", callErr)
	}
	if _, err := syscall.WaitForSingleObject(process.Process, syscall.INFINITE); err != nil {
		return 0, err
	}
	var code uint32
	if err := syscall.GetExitCodeProcess(process.Process, &code); err != nil {
		return 0, err
	}
	return code, nil
}

// Windows JOBOBJECT_EXTENDED_LIMIT_INFORMATION, using pointer-width fields.
type jobLimits struct {
	Basic struct {
		ProcessTime, JobTime int64
		Flags                uint32
		Minimum, Maximum     uintptr
		ActiveProcesses      uint32
		Affinity             uintptr
		Priority, Scheduling uint32
	}
	IO                                                         [6]uint64
	ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory uintptr
}

func logFailure(dir string, failure error) {
	paths, err := app.ResolvePaths(dir)
	if err != nil {
		return
	}
	if os.MkdirAll(paths.LogDir, 0700) != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(paths.LogDir, "scheduled-error.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "[%s] 后台启动器失败：%v\n", time.Now().Format("2006-01-02 15:04:05"), failure)
}
