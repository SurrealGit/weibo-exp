package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/logging"
	"github.com/SurrealGit/weibo-exp/internal/mailnotify"
	"github.com/SurrealGit/weibo-exp/internal/runner"
	"github.com/SurrealGit/weibo-exp/internal/storage"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

var sendNotification = mailnotify.Send

func mailNotificationStatus(paths app.Paths) string {
	cfg, err := loadMailConfig(filepath.Join(paths.DataDir, mailnotify.ConfigFile))
	if err != nil {
		return "invalid"
	}
	if cfg.Host == "" {
		return "unconfigured"
	}
	if cfg.Enabled {
		return "enabled"
	}
	return "disabled"
}

func printMailNotificationStatus(output io.Writer, paths app.Paths, status string) {
	switch status {
	case "enabled":
		fmt.Fprintln(output, "邮件通知：已启用")
	case "disabled":
		fmt.Fprintln(output, "邮件通知：已配置，未启用")
	case "invalid":
		fmt.Fprintf(output, "邮件通知：配置异常，执行 %s 查看详情\n", commandFor(paths, "notify status"))
	default:
		fmt.Fprintln(output, "邮件通知：未配置")
	}
}

func loadMailConfig(path string) (mailnotify.Config, error) {
	var cfg mailnotify.Config
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, errors.New("无法读取 SMTP 配置文件")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cfg) != nil || decoder.Decode(new(any)) != io.EOF {
		return cfg, errors.New("SMTP 配置格式无效，请检查 JSON 字段与语法")
	}
	return cfg, cfg.Validate()
}

func notifyCommand(ctx context.Context, paths app.Paths, args []string, output, errorOutput io.Writer) error {
	if len(args) == 0 {
		return errors.New("用法：weibo-exp notify configure --file FILE | status | test | enable | disable")
	}
	flags := flag.NewFlagSet("notify "+args[0], flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	var source string
	if args[0] == "configure" {
		flags.StringVar(&source, "file", "", "SMTP JSON 配置文件")
	} else if args[0] != "status" && args[0] != "test" && args[0] != "enable" && args[0] != "disable" {
		return errors.New("用法：weibo-exp notify configure --file FILE | status | test | enable | disable")
	}
	if err := parseFlags(flags, args[1:]); err != nil {
		return err
	}
	if args[0] != "status" {
		release, err := runner.AcquireLock(paths.Lock)
		if err != nil {
			return err
		}
		defer release()
	}
	configPath := filepath.Join(paths.DataDir, mailnotify.ConfigFile)
	if args[0] == "configure" {
		if source == "" {
			return errors.New("请通过 --file 指定 SMTP JSON 配置文件")
		}
		cfg, err := loadMailConfig(source)
		if err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
		previous, err := loadMailConfig(configPath)
		if err != nil {
			return err
		}
		// Import changes SMTP settings, never the existing notification switch.
		cfg.Enabled = previous.Enabled
		if err := storage.WriteJSON(configPath, cfg, 0600); err != nil {
			return errors.New("保存 SMTP 配置失败")
		}
		if err := os.Remove(filepath.Join(paths.DataDir, mailnotify.StateFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("SMTP 配置已保存，但清除旧通知记录失败；请检查数据目录权限后重新导入")
		}
		fmt.Fprintln(output, "SMTP 配置：已保存")
		if cfg.Enabled {
			fmt.Fprintln(output, "邮件通知：已启用（保持原状态）")
		} else {
			fmt.Fprintln(output, "邮件通知：已关闭")
		}
		fmt.Fprintln(output, "配置已保存在数据目录中，授权码以明文保存。请妥善保管或删除自行创建的导入源文件。")
		fmt.Fprintf(output, "执行 %s 发送测试邮件。\n", commandFor(paths, "notify test"))
		if !cfg.Enabled {
			fmt.Fprintf(output, "确认收到后，执行 %s 启用自动通知。\n", commandFor(paths, "notify enable"))
		}
		return nil
	}
	cfg, err := loadMailConfig(configPath)
	if err != nil {
		return err
	}
	switch args[0] {
	case "status":
		if cfg.Enabled {
			fmt.Fprintln(output, "邮件通知：已启用")
			fmt.Fprintln(output, "提醒内容：任务异常、每日完成")
			fmt.Fprintln(output, "通知频率：每类异常每天最多一封；每日完成最多一封")
		} else {
			fmt.Fprintln(output, "邮件通知：已关闭")
		}
		if cfg.Host != "" {
			security := "SSL/TLS（连接时加密）"
			if cfg.SecurityMode() == "starttls" {
				security = "STARTTLS（连接后升级加密）"
			}
			fmt.Fprintf(output, "SMTP 服务器：%s:%d\n加密方式：%s\n发件人：%s\n收件人：%s\n", cfg.Host, cfg.Port, security, cfg.From, cfg.To)
		}
	case "enable", "disable":
		enable := args[0] == "enable"
		if enable && cfg.Host == "" {
			return fmt.Errorf("尚未配置 SMTP，请先执行 %s", commandFor(paths, "notify configure --file FILE"))
		}
		if cfg.Host != "" && cfg.Enabled != enable {
			cfg.Enabled = enable
			if err := storage.WriteJSON(configPath, cfg, 0600); err != nil {
				return errors.New("保存 SMTP 配置失败")
			}
		}
		if enable {
			fmt.Fprintln(output, "邮件通知：已启用（任务异常、每日完成）")
		} else {
			fmt.Fprintln(output, "邮件通知：已关闭，已有配置保留。")
			if cfg.Host != "" {
				fmt.Fprintf(output, "执行 %s 可重新启用。\n", commandFor(paths, "notify enable"))
			}
		}
	case "test":
		if cfg.Host == "" {
			return errors.New("尚未配置 SMTP，请先执行 notify configure --file FILE")
		}
		if err := sendNotification(ctx, cfg, "微博超话经验助手：测试邮件", "这是一封测试邮件。\n时间："+time.Now().Format("2006-01-02 15:04:05 MST")+"\n未执行微博任务，也未改变自动通知记录。"); err != nil {
			return err
		}
		fmt.Fprintln(output, "测试邮件已提交 SMTP 服务器，请检查收件箱或垃圾邮件；实际投递可能延迟。")
	}
	return nil
}

type notificationState struct {
	ConfigHash string            `json:"config_hash"`
	Sent       map[string]string `json:"sent"`
}

func failureMail(paths app.Paths, taskErr error, now time.Time) (category, subject, body string) {
	category, description := "other", "定时任务失败或未完成"
	command := commandFor(paths, "logs --errors")
	state, stateErr := app.LoadState(paths.State)
	manual, retry := pendingCounts(state)
	if errors.Is(taskErr, weibo.ErrNotLoggedIn) {
		category, description, command = "login", "登录状态已失效", commandFor(paths, "login")
	} else if stateErr == nil && manual > 0 {
		category, description, command = "manual", "存在需要人工核查的待处理记录", commandFor(paths, "cleanup")
	}
	subject = "微博超话经验助手：" + description
	body = fmt.Sprintf("微博超话经验助手\n时间：%s\n问题：%s\n\n请执行以下命令处理或查看详情：\n%s\n", now.Format("2006-01-02 15:04:05 MST"), description, command)
	if category != "manual" && stateErr == nil && manual+retry > 0 {
		body += "\n另有待处理内容，请执行 " + commandFor(paths, "cleanup") + " 查看详情。\n"
	}
	body += "\n处理后可等待下次定时检查，或执行 " + commandFor(paths, "run") + " 立即检查。\n"
	return
}

// Caller holds the task lock. Mail errors never replace the original task result.
func notifyScheduledFailure(ctx context.Context, paths app.Paths, taskErr error, output io.Writer, now time.Time) {
	if taskErr == nil || ctx.Err() != nil || errors.Is(taskErr, context.Canceled) {
		return
	}
	category, subject, body := failureMail(paths, taskErr, now)
	sendDailyNotification(ctx, paths, category, subject, body, output, output, now)
}

func notifyScheduledSuccess(ctx context.Context, paths app.Paths, topics int, output, errorOutput io.Writer, now time.Time) {
	if topics <= 0 || ctx.Err() != nil {
		return
	}
	body := fmt.Sprintf("微博超话经验助手\n日期：%s\n完成超话：%d/%d\n确认时间：%s\n\n当前任务范围内的超话均已完成，暂无待处理内容。\n任务完成情况依据实时签到状态和本地互动记录，实际经验到账请以微博 App 为准。\n", app.Today(now), topics, topics, now.Format("2006-01-02 15:04:05 MST"))
	sendDailyNotification(ctx, paths, "success", "微博超话经验助手：今日任务已完成", body, output, errorOutput, now)
}

// Both notification types share the existing lock, config and daily records.
func sendDailyNotification(ctx context.Context, paths app.Paths, category, subject, body string, output, errorOutput io.Writer, now time.Time) {
	log := func(message string) { logNotification(paths, errorOutput, message) }
	cfg, err := loadMailConfig(filepath.Join(paths.DataDir, mailnotify.ConfigFile))
	if err != nil {
		log("邮件通知失败：无法读取有效的 SMTP 配置，原始任务结果不变。")
		return
	}
	if !cfg.Enabled {
		return
	}
	statePath := filepath.Join(paths.DataDir, mailnotify.StateFile)
	var state notificationState
	if err := storage.ReadJSON(statePath, &state); err != nil && !errors.Is(err, os.ErrNotExist) {
		log("邮件通知失败：无法读取通知记录，请检查 notification-state.json；本次未发送邮件。")
		return
	}
	encoded, _ := json.Marshal(cfg)
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(encoded))
	if state.ConfigHash != fingerprint || state.Sent == nil {
		state = notificationState{ConfigHash: fingerprint, Sent: map[string]string{}}
	}
	if state.Sent[category] == app.Today(now) {
		return
	}
	if err := sendNotification(ctx, cfg, subject, body); err != nil {
		log("邮件通知失败：" + err.Error())
		return
	}
	state.Sent[category] = app.Today(now)
	if err := storage.WriteJSON(statePath, state, 0600); err != nil {
		log("邮件已提交 SMTP 服务器，但保存通知记录失败；下次可能重复提醒。")
		return
	}
	logNotification(paths, output, "邮件通知已提交 SMTP 服务器（"+category+"）；实际投递可能延迟。")
}

func logNotification(paths app.Paths, output io.Writer, message string) {
	if output != nil {
		fmt.Fprintln(output, message)
		return
	}
	// Startup failures may happen before the ordinary scheduled logs are open.
	if os.MkdirAll(paths.LogDir, 0700) == nil {
		file, err := os.OpenFile(filepath.Join(paths.LogDir, logging.ErrorFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err == nil {
			defer file.Close()
			fmt.Fprintln(newTimestampWriter(file, time.Now), message)
			return
		}
	}
	fmt.Fprintln(os.Stderr, message)
}
