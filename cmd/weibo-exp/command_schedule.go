package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/runner"
	"github.com/SurrealGit/weibo-exp/internal/schedule"
)

func printScheduleState(output io.Writer, paths app.Paths) {
	status, err := inspectSchedule(nativeScheduleConfig(paths, "", "00:00"))
	if err != nil {
		fmt.Fprintf(output, "定时任务状态：查询失败（%v）\n", err)
		return
	}
	state := "未安装"
	if status.Registered {
		state = "已安装"
		if err := matchingScheduleData(status, paths); err != nil {
			state = "已安装，但未确认用于当前数据目录（" + err.Error() + "）；执行 weibo-exp schedule status --verbose 查看详情"
		}
	}
	fmt.Fprintf(output, "定时任务状态：%s\n", state)
}

func scheduleCommand(paths app.Paths, args []string, output, errorOutput io.Writer) error {
	if len(args) == 0 {
		return errors.New("用法：weibo-exp schedule [install|status|uninstall]")
	}
	if args[0] == "uninstall" {
		if err := noArguments("schedule uninstall", args[1:], errorOutput); err != nil {
			return err
		}
	}
	if args[0] == "uninstall" {
		release, err := runner.AcquireLock(paths.Lock)
		if err != nil {
			return err
		}
		defer release()
	}
	switch args[0] {
	case "install":
		flags := flag.NewFlagSet("schedule install", flag.ContinueOnError)
		flags.SetOutput(errorOutput)
		at := flags.String("at", "", "每日执行时间，HH:MM")
		if err := parseFlags(flags, args[1:]); err != nil {
			return err
		}
		release, err := runner.AcquireLock(paths.Lock)
		if err != nil {
			return err
		}
		defer release()
		cfg, err := app.LoadConfig(paths.Config)
		if err != nil {
			return err
		}
		if *at != "" {
			cfg.ScheduledTime = *at
			if err := cfg.Validate(); err != nil {
				return err
			}
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		executable, err = filepath.EvalSymlinks(executable)
		if err != nil {
			return err
		}
		native := nativeScheduleConfig(paths, executable, cfg.ScheduledTime)
		previous, err := inspectSchedule(native)
		if err != nil {
			return err
		}
		if (previous.Registered || previous.FileExists) && matchingScheduleData(previous, paths) != nil {
			fmt.Fprintf(output, "将替换当前用户的现有定时任务：数据目录 %q → %q\n", previous.DataDir, paths.DataDir)
		}
		if err := saveConfigAndSchedule(paths, cfg, &native); err != nil {
			return err
		}
		fmt.Fprintln(output, "定时任务：已安装")
		backend, _ := schedule.BackendName()
		fmt.Fprintf(output, "原生调度器：%s\n", backend)
		fmt.Fprintf(output, "每日执行时间：%s\n", cfg.ScheduledTime)
		fmt.Fprintln(output, "补跑检查：从每日执行时间起每 4 小时一次，至当天结束（完成后仅进行轻量状态检查）")
		fmt.Fprintf(output, "程序路径：%s\n", executable)
		fmt.Fprintf(output, "数据目录：%s\n", paths.DataDir)
		return nil
	case "status":
		flags := flag.NewFlagSet("schedule status", flag.ContinueOnError)
		flags.SetOutput(errorOutput)
		verbose := flags.Bool("verbose", false, "额外显示程序、数据目录及原生配置路径")
		if err := parseFlags(flags, args[1:]); err != nil {
			return err
		}
		status, err := inspectSchedule(nativeScheduleConfig(paths, "", "00:00"))
		if err != nil {
			return err
		}
		if status.Registered {
			if status.DailyTime == "" || status.RetryInterval == "" {
				return errors.New("定时任务已注册，但无法识别执行时间或补跑规则；执行 weibo-exp config show 确认期望时间，再执行 weibo-exp schedule install --at HH:MM 重新生成调度配置（将 HH:MM 替换为期望时间）")
			}
			fmt.Fprintln(output, "定时任务状态：已安装")
			fmt.Fprintf(output, "原生调度器：%s\n", status.Backend)
			fmt.Fprintf(output, "每日执行时间：%s\n", status.DailyTime)
			fmt.Fprintf(output, "补跑间隔：%s\n", status.RetryInterval)
			if status.Enabled != nil && !*status.Enabled {
				fmt.Fprintf(output, "注意：定时任务已禁用。执行 weibo-exp schedule install --at %s 重新启用。\n", status.DailyTime)
			} else if status.Backend == "Linux 用户级 systemd" && status.Active != nil && !*status.Active {
				fmt.Fprintf(output, "注意：定时器未启动。执行 weibo-exp schedule install --at %s 重新启动。\n", status.DailyTime)
			}
		} else if status.FileExists {
			fmt.Fprintln(output, "定时任务状态：未安装（存在未加载的残留配置）")
			fmt.Fprintln(output, "处理建议：执行 weibo-exp schedule install，重新生成并加载调度配置。")
		} else {
			fmt.Fprintln(output, "定时任务状态：未安装")
			fmt.Fprintln(output, "执行 weibo-exp schedule install 安装定时任务。")
		}
		if *verbose {
			for _, item := range [][2]string{{"程序路径", status.Executable}, {"数据目录", status.DataDir}, {"原生配置", status.ConfigPath}} {
				if item[1] != "" {
					fmt.Fprintf(output, "%s：%s\n", item[0], item[1])
				}
			}
		}
		fmt.Fprintln(output, "\n执行 weibo-exp logs 查看最近运行记录。")
		return nil
	case "uninstall":
		native := nativeScheduleConfig(paths, "", "00:00")
		status, err := inspectSchedule(native)
		if err != nil {
			return err
		}
		if err := matchingScheduleData(status, paths); err != nil {
			return err
		}
		if err := uninstallSchedule(native); err != nil {
			return err
		}
		fmt.Fprintln(output, "定时任务：已卸载")
		return nil
	default:
		return errors.New("用法：weibo-exp schedule [install|status|uninstall]")
	}
}

func nativeScheduleConfig(paths app.Paths, executable, taskTime string) schedule.Config {
	hour, minute, _ := schedule.ParseTime(taskTime)
	return schedule.Config{
		Executable: executable, DataDir: paths.DataDir, LaunchAgent: paths.LaunchAgent,
		SystemdService: paths.SystemdService, SystemdTimer: paths.SystemdTimer,
		Hour: hour, Minute: minute,
	}
}

func scheduleConfigFromStatus(paths app.Paths, status schedule.InstallStatus, taskTime string) (schedule.Config, error) {
	executable := status.Executable
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return schedule.Config{}, err
		}
		executable, err = filepath.EvalSymlinks(executable)
		if err != nil {
			return schedule.Config{}, err
		}
	}
	dataDir := status.DataDir
	if dataDir == "" {
		dataDir = paths.DataDir
	}
	nativePaths := paths
	nativePaths.DataDir = dataDir
	return nativeScheduleConfig(nativePaths, executable, taskTime), nil
}

var inspectSchedule = schedule.Inspect
var installSchedule = schedule.Install

func saveConfigAndSchedule(paths app.Paths, cfg app.Config, explicit *schedule.Config) error {
	old, err := app.LoadConfig(paths.Config)
	if err != nil {
		return err
	}
	native := explicit
	if native == nil && cfg.ScheduledTime != old.ScheduledTime {
		status, err := inspectSchedule(nativeScheduleConfig(paths, "", cfg.ScheduledTime))
		if err != nil {
			return err
		}
		if status.Registered {
			if err := matchingScheduleData(status, paths); err != nil {
				return err
			}
			value, err := scheduleConfigFromStatus(paths, status, cfg.ScheduledTime)
			if err != nil {
				return err
			}
			native = &value
		}
	}
	// Save first: RunAtLoad must not observe the previous daily time. Each native
	// backend owns rollback of its actual configuration; this layer owns JSON.
	if err := app.SaveConfig(paths.Config, cfg); err != nil {
		return err
	}
	if native != nil {
		if err := installSchedule(*native); err != nil {
			restoreErr := app.SaveConfig(paths.Config, old)
			if restoreErr != nil {
				return errors.Join(err, fmt.Errorf("配置回滚失败: %w", restoreErr))
			}
			return fmt.Errorf("配置已恢复；原生调度更新失败: %w", err)
		}
	}
	return nil
}
