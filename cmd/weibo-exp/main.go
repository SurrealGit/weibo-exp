package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/install"
	"github.com/SurrealGit/weibo-exp/internal/logging"
	"github.com/SurrealGit/weibo-exp/internal/runner"
	"github.com/SurrealGit/weibo-exp/internal/session"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

func main() {
	ctx := context.Background()
	if err := runCLI(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "操作失败：", err)
		if errors.Is(err, weibo.ErrNotLoggedIn) {
			os.Exit(3)
		}
		os.Exit(1)
	}
}

// Caller holds the task lock until both files are closed.
func openScheduledLogs(paths app.Paths, cfg app.Config) (io.Writer, io.Writer, func(), error) {
	_, pruneErr := logging.Prune(paths.LogDir, cfg.LogRetentionDays, time.Now())
	if err := os.MkdirAll(paths.LogDir, 0700); err != nil {
		return nil, nil, nil, err
	}
	out, err := os.OpenFile(filepath.Join(paths.LogDir, logging.OutputFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, nil, nil, err
	}
	errFile, err := os.OpenFile(filepath.Join(paths.LogDir, logging.ErrorFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		out.Close()
		return nil, nil, nil, err
	}
	output, errorOutput := newTimestampWriter(out, time.Now), newTimestampWriter(errFile, time.Now)
	if pruneErr != nil {
		fmt.Fprintln(errorOutput, "警告：旧日志清理失败，本次仍继续：", pruneErr)
	}
	return output, errorOutput, func() { out.Close(); errFile.Close() }, nil
}

type timestampWriter struct {
	target    io.Writer
	now       func() time.Time
	lineStart bool
	mu        sync.Mutex
}

func newTimestampWriter(target io.Writer, now func() time.Time) *timestampWriter {
	return &timestampWriter{target: target, now: now, lineStart: true}
}

func (w *timestampWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	written := 0
	for len(data) > 0 {
		if w.lineStart {
			if _, err := fmt.Fprintf(w.target, "[%s] ", w.now().Local().Format("2006-01-02 15:04:05")); err != nil {
				return written, err
			}
			w.lineStart = false
		}
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			end = len(data) - 1
		}
		chunk := data[:end+1]
		count, err := w.target.Write(chunk)
		written += count
		if count > 0 && chunk[count-1] == '\n' {
			w.lineStart = true
		}
		if err != nil {
			return written, err
		}
		if count != len(chunk) {
			return written, io.ErrShortWrite
		}
		data = data[count:]
	}
	return written, nil
}

func runCLI(ctx context.Context, args []string, input io.Reader, output, errorOutput io.Writer) error {
	global := flag.NewFlagSet("weibo-exp", flag.ContinueOnError)
	global.SetOutput(errorOutput)
	dataDir := global.String("data-dir", "", "配置和会话数据目录")
	baseURL := global.String("base-url", envOr("WEIBO_EXP_BASE_URL", "https://m.weibo.cn"), "微博 API 根地址")
	passportURL := global.String("passport-url", envOr("WEIBO_EXP_PASSPORT_URL", "https://passport.weibo.com"), "微博登录根地址")
	if err := parseFlagSet(global, args); err != nil {
		return err
	}
	rest := global.Args()
	if len(rest) == 0 {
		printHelp(output)
		return nil
	}
	paths, err := app.ResolvePaths(*dataDir)
	if *dataDir == "" && os.Getenv("WEIBO_EXP_DATA_DIR") == "" {
		if exe, exeErr := os.Executable(); exeErr == nil {
			exe, exeErr = filepath.EvalSymlinks(exe)
			if exeErr != nil {
				return exeErr
			}
			managed, readErr := install.DataDirForExecutable(exe)
			if readErr != nil {
				return readErr
			}
			if managed != "" {
				paths, err = app.ResolvePaths(managed)
			}
		}
	}
	if err != nil {
		return err
	}
	command := rest[0]
	commandArgs := rest[1:]

	switch command {
	case "install":
		return installationCommand(paths, commandArgs, input, output, errorOutput)
	case "uninstall":
		return uninstallationCommand(paths, commandArgs, input, output, errorOutput)
	case "help", "--help", "-h":
		if err := noArguments("help", commandArgs, errorOutput); err != nil {
			return err
		}
		printHelp(output)
		return nil
	case "version":
		if err := noArguments("version", commandArgs, errorOutput); err != nil {
			return err
		}
		fmt.Fprintf(output, "weibo-exp %s (%s)\n", app.Version, app.PlatformName())
		return nil
	case "config":
		return configCommand(ctx, paths, commandArgs, output, errorOutput, *baseURL)
	case "schedule":
		return scheduleCommand(paths, commandArgs, output, errorOutput)
	case "scheduled-run":
		if len(commandArgs) != 0 {
			return errors.New("scheduled-run 不接受额外参数")
		}
		return scheduledCommand(ctx, paths, *baseURL)
	case "cleanup":
		return cleanupCommand(paths, commandArgs, input, output, errorOutput)
	case "logs":
		return logsCommand(paths, commandArgs, input, output, errorOutput)
	}

	var loginTimeout time.Duration
	var loginNoOpen bool
	if command == "login" {
		loginFlags := flag.NewFlagSet("login", flag.ContinueOnError)
		loginFlags.SetOutput(errorOutput)
		loginFlags.DurationVar(&loginTimeout, "timeout", 4*time.Minute, "扫码超时时间")
		loginFlags.BoolVar(&loginNoOpen, "no-open", false, "不自动打开二维码图片")
		if err := parseFlags(loginFlags, commandArgs); err != nil {
			return err
		}
		release, err := runner.AcquireLock(paths.Lock)
		if err != nil {
			return err
		}
		defer release()
	}
	cfg, err := app.LoadConfig(paths.Config)
	if err != nil {
		return err
	}
	jar, err := session.Load(paths.Session)
	if err != nil {
		return err
	}
	httpClient := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	client := weibo.NewClient(*baseURL, httpClient)

	switch command {
	case "login":
		opener := weibo.OpenImage
		if loginNoOpen {
			opener = func(string) error { return errors.New("已禁用自动打开") }
		}
		state, err := app.LoadState(paths.State)
		if err != nil {
			return err
		}
		if state.UID == "" {
			if oldStatus, err := client.LoginStatus(ctx); err == nil {
				state.UID = oldStatus.UID
				if err := app.SaveState(paths.State, state); err != nil {
					return err
				}
			} else if len(state.History)+len(state.PendingDeletes) > 0 {
				fmt.Fprintln(output, "提示：旧版任务记录尚未绑定账号，请扫描原账号；切换账号请使用 --data-dir 指定新目录。")
			}
		}
		if err := weibo.QRLogin(ctx, jar, weibo.QRLoginOptions{
			PassportURL: *passportURL, RedirectURL: *baseURL + "/", Timeout: loginTimeout,
			TempDir: filepath.Join(paths.DataDir, "login-tmp"),
			Output:  output, OpenImage: opener,
		}); err != nil {
			return err
		}
		status, err := client.LoginStatus(ctx)
		if err != nil {
			return fmt.Errorf("微博状态校验失败，未替换原会话: %w", err)
		}
		if err := state.CheckAccount(status.UID); err != nil {
			return err
		}
		state.UID = status.UID
		if err := app.SaveState(paths.State, state); err != nil {
			return err
		}
		if err := session.Save(paths.Session, jar); err != nil {
			return err
		}
		fmt.Fprintln(output, "登录结果：成功")
		fmt.Fprintf(output, "用户 UID：%s\n", status.UID)
		fmt.Fprintf(output, "会话文件：%s\n", paths.Session)
		return nil
	case "status":
		return statusCommand(ctx, paths, cfg, client, commandArgs, output, errorOutput)
	case "topics":
		return topicsCommand(ctx, paths, cfg, client, commandArgs, output, errorOutput)
	case "run":
		return runCommand(ctx, paths, cfg, client, commandArgs, input, output, errorOutput)
	default:
		return fmt.Errorf("未知命令 %q，请执行 weibo-exp help", command)
	}
}

func printHelp(output io.Writer) {
	fmt.Fprintln(output, `微博超话经验助手

用法：weibo-exp [--data-dir DIR] <command>

命令：
  install [--dir DIR]   安装/升级程序并配置用户 PATH；--no-path 跳过 PATH
  uninstall             卸载并清除数据；--keep-config 保留配置、会话和进度
  login                 使用微博 App 扫码登录
  status [--json]       只读检查账户、关注超话和本地任务状态
  topics [--json]       列出关注超话、序号和是否纳入任务
  run --dry-run         只读预演今日剩余任务，不发送微博写请求
  run [--yes]           执行今日剩余任务；--yes 跳过确认
  config show [--json]  查看易读配置；--json 显示原始配置
  config set            修改配置；超话可使用序号、名称或 ID
  schedule install      安装定时任务
  schedule status       查看定时状态；--verbose 显示路径详情
  schedule uninstall    卸载定时任务
  logs [--lines N]      查看定时任务日志；--errors 只看错误日志
  logs clear [--yes]    清空定时任务日志
  cleanup               查看待清理内容；confirm 确认已人工清理指定项
  version               显示版本`)
}

func displayTopicName(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasSuffix(name, "超话") {
		return name
	}
	return name + "超话"
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
