package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/install"
	"github.com/SurrealGit/weibo-exp/internal/logging"
	"github.com/SurrealGit/weibo-exp/internal/runner"
	"github.com/SurrealGit/weibo-exp/internal/schedule"
)

var uninstallSchedule = schedule.Uninstall
var executablePath = os.Executable
var removeInstallationFiles = install.RemoveFiles

func installationCommand(paths app.Paths, args []string, input io.Reader, output, errorOutput io.Writer) error {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	dir := flags.String("dir", "", "安装目录；默认使用当前安装位置或系统用户目录")
	yes := flags.Bool("yes", false, "确认安装/升级，不再询问")
	noPath := flags.Bool("no-path", false, "跳过 PATH 设置；升级时保留已有设置")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *dir == "" {
		if record, err := install.Read(paths.DataDir); err == nil {
			*dir = filepath.Dir(record.Executable)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		} else {
			var err error
			*dir, err = install.DefaultDir()
			if err != nil {
				return err
			}
		}
	}
	canonical, err := install.CanonicalDir(*dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "安装目录：%s\n数据目录：%s\n已有配置、登录会话和任务进度将保留。\n", canonical, paths.DataDir)
	fmt.Fprintln(output, "仅安装程序；已有定时任务保持原样。")
	if !*noPath {
		fmt.Fprintln(output, "将配置当前用户的 PATH，以便在任意目录使用 weibo-exp。")
	}
	if !*yes && !confirmLifecycle(input, output, "输入 yes 安装/升级：") {
		return errors.New("用户取消")
	}
	release, err := runner.AcquireLock(paths.Lock)
	if err != nil {
		return err
	}
	defer release()
	source, err := executablePath()
	if err != nil {
		return err
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	record, err := install.Install(source, canonical, paths.DataDir, !*noPath)
	if err != nil {
		return fmt.Errorf("安装未完成：%w", err)
	}
	fmt.Fprintf(output, "程序已安装：%s\n", record.Executable)
	entry := "weibo-exp"
	if *noPath {
		fmt.Fprintln(output, "已跳过 PATH 设置。请重新打开终端；如尚未配置 PATH，请进入安装目录运行以下命令。")
		entry = "./weibo-exp"
		if runtime.GOOS == "windows" {
			entry = ".\\weibo-exp.exe"
		}
	} else {
		fmt.Fprintln(output, "PATH 已配置。请关闭并重新打开终端，然后在任意目录使用 weibo-exp。")
		fmt.Fprintln(output, "若新终端仍找不到命令，请完全退出终端应用后重新打开。")
	}
	fmt.Fprintln(output, "接下来按需执行：")
	fmt.Fprintf(output, "  登录：%s login\n  手动运行：%s run\n  启用每日自动运行：%s schedule install\n  卸载：%s uninstall\n", entry, entry, entry, entry)
	fmt.Fprintln(output, "已有登录会话可继续使用；若定时任务指向其他程序路径，请重新执行 schedule install。")
	fmt.Fprintln(output, "下载的发行包和原始程序副本不会自动删除，可自行处理。")
	return nil
}

func confirmLifecycle(input io.Reader, output io.Writer, prompt string) bool {
	fmt.Fprint(output, prompt)
	text, _ := bufio.NewReader(input).ReadString('\n')
	return strings.TrimSpace(text) == "yes"
}

func matchingScheduleData(status schedule.InstallStatus, paths app.Paths) error {
	if !status.Registered && !status.FileExists {
		return nil
	}
	if status.DataDir == "" {
		return errors.New("现有调度配置无法确认所属数据目录；请先通过 schedule status --verbose 排查，不会覆盖或卸载未知任务")
	}
	a, err := install.CanonicalDir(status.DataDir)
	if err != nil {
		return err
	}
	b, err := install.CanonicalDir(paths.DataDir)
	if err != nil {
		return err
	}
	if a != b {
		return errors.New("现有定时任务属于另一数据目录，未修改")
	}
	return nil
}

func uninstallationCommand(paths app.Paths, args []string, input io.Reader, output, errorOutput io.Writer) error {
	flags := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	keep := flags.Bool("keep-config", false, "保留配置、登录会话、任务进度；日志仍清理")
	yes := flags.Bool("yes", false, "确认列出的卸载范围，不再询问")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	record, err := install.Read(paths.DataDir)
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("没有受管理的安装记录；便携/旧版程序不会被猜测删除。可先执行 install 建立记录；仅停止原定时任务使用 schedule uninstall")
	}
	if err != nil {
		return err
	}
	if err := record.CheckOwned(); err != nil {
		return err
	}
	// Receipts store canonical paths; normalize aliases such as macOS /tmp
	// without following individual configuration-file symlinks.
	if paths.LogDir == filepath.Join(paths.DataDir, "logs") {
		paths.LogDir = filepath.Join(record.DataDir, "logs")
	}
	paths.DataDir = record.DataDir
	paths.Config = filepath.Join(record.DataDir, "config.json")
	paths.Session = filepath.Join(record.DataDir, "session.json")
	paths.State = filepath.Join(record.DataDir, "state.json")
	paths.Lock = filepath.Join(record.DataDir, "run.lock")
	fmt.Fprintf(output, "将卸载：%s\n将清理：安装记录、定时任务、日志和工具专用二维码临时文件。\n", record.Executable)
	if *keep {
		fmt.Fprintf(output, "保留配置、登录会话和任务进度：%s\n", paths.DataDir)
	} else {
		fmt.Fprintf(output, "将永久删除配置、登录会话和任务进度：%s\n", paths.DataDir)
	}
	fmt.Fprintln(output, "不删除下载包、其他程序副本、目录内不属于本工具的文件或系统使用历史。")
	fmt.Fprintln(output, "将撤销本工具添加的 PATH 设置，保留原有环境配置。")
	if !*yes && !confirmLifecycle(input, output, "输入 yes 确认卸载：") {
		return errors.New("用户取消")
	}
	release, err := runner.AcquireLock(paths.Lock)
	if err != nil {
		return err
	}
	defer release()
	// Re-read everything after acquiring the lock; confirmation may take time.
	record, err = install.Read(paths.DataDir)
	if err != nil {
		return err
	}
	if err := record.CheckOwned(); err != nil {
		return err
	}
	state, err := app.LoadState(paths.State)
	if err != nil {
		return err
	}
	if len(state.PendingDeletes) != 0 {
		return errors.New("存在待删除或待人工检查的微博内容；先执行 cleanup 查看并处理，再卸载。尚未停止任务或删除数据")
	}
	status, err := inspectSchedule(nativeScheduleConfig(paths, "", "00:00"))
	if err != nil {
		return err
	}
	if err := matchingScheduleData(status, paths); err != nil {
		return err
	}
	qr, err := loginTemporaryFiles(paths.DataDir)
	if err != nil {
		return err
	}
	files := append(logging.Paths(paths.LogDir), qr...)
	if !*keep {
		files = append(files, paths.Config, paths.Session, paths.State)
	}
	// Preflight all deletion paths before stopping a task or deleting any data.
	for _, p := range append(append([]string{}, files...), paths.Lock, record.Sidecar(), filepath.Join(record.DataDir, install.ReceiptName)) {
		if _, err := install.CleanPath(p); err != nil {
			return err
		}
		if info, err := os.Lstat(p); err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("清理目标不是普通文件：%s", p)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if status.Registered || status.FileExists {
		if err := uninstallSchedule(nativeScheduleConfig(paths, "", "00:00")); err != nil {
			return fmt.Errorf("停止定时任务失败，尚未删除数据：%w", err)
		}
	}
	if err := install.RemovePath(record); err != nil {
		return fmt.Errorf("PATH 清理未完成，程序和数据保留，可重试 uninstall：%w", err)
	}
	fmt.Fprintln(output, "本工具添加的 PATH 设置已清理；请重新打开终端。")
	if err := removeInstallationFiles(files); err != nil {
		return fmt.Errorf("定时任务已停止，但清理未完成：%w；安装记录保留，可重试 uninstall", err)
	}
	finalFiles := []string{paths.Lock, record.Executable, record.Sidecar(), filepath.Join(record.DataDir, install.ReceiptName)}
	dirs := []string{filepath.Join(paths.DataDir, "login-tmp"), paths.LogDir}
	if !*keep {
		dirs = append(dirs, paths.DataDir)
	}
	dirs = append(dirs, record.CreatedDirs...)
	if runtime.GOOS == "windows" {
		if err := install.StartWindowsCleanup(record, finalFiles, dirs); err != nil {
			return fmt.Errorf("数据清理已执行，但最终文件清理未启动：%w；安装记录保留，可重试 uninstall", err)
		}
		// The helper waits for process exit and obtains the same native mutex
		// before deleting final files. A competing owner makes cleanup fail safely.
		fmt.Fprintln(output, "定时任务已停止，数据已按所选范围处理。程序及安装记录将在本进程退出后由系统清理；尚未确认最终删除成功，失败信息将输出到当前终端。")
		return nil
	}
	if err := removeInstallationFiles(finalFiles); err != nil {
		return fmt.Errorf("部分卸载完成，最终文件清理失败：%w", err)
	}
	remaining := install.RemoveEmpty(dirs)
	fmt.Fprintln(output, "卸载完成：已移除受管理程序、安装记录和定时任务。")
	if *keep {
		fmt.Fprintln(output, "已按要求保留配置、登录会话和任务进度；日志已清理。")
	} else {
		fmt.Fprintln(output, "配置、登录会话、任务进度及日志已清理。")
	}
	for _, d := range remaining {
		fmt.Fprintf(output, "保留目录（非空或无法删除）：%s\n", d)
	}
	return nil
}

func loginTemporaryFiles(dataDir string) ([]string, error) {
	dir := filepath.Join(dataDir, "login-tmp")
	if _, err := install.CleanPath(dir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "weibo-login-") && strings.HasSuffix(entry.Name(), ".png") {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	return paths, nil
}
