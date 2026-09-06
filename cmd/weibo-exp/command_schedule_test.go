package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/schedule"
)

func TestScheduleStatusConciseAndVerbose(t *testing.T) {
	oldInspect := inspectSchedule
	t.Cleanup(func() { inspectSchedule = oldInspect })
	status := schedule.InstallStatus{
		Registered: true, Backend: "macOS launchd", DailyTime: "10:00", RetryInterval: "每 4 小时一次",
		Executable: "/test/weibo-exp", DataDir: "/test/data", ConfigPath: "/test/task.plist",
	}
	inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) { return status, nil }
	const summary = "定时任务状态：已安装\n原生调度器：macOS launchd\n每日执行时间：10:00\n补跑间隔：每 4 小时一次\n"
	const footer = "\n执行 weibo-exp logs 查看最近运行记录。\n"
	for _, verbose := range []bool{false, true} {
		args := []string{"status"}
		want := summary
		if verbose {
			args = append(args, "--verbose")
			want += "程序路径：/test/weibo-exp\n数据目录：/test/data\n原生配置：/test/task.plist\n"
		}
		var output bytes.Buffer
		if err := scheduleCommand(app.Paths{}, args, &output, io.Discard); err != nil {
			t.Fatal(err)
		}
		if output.String() != want+footer {
			t.Fatalf("verbose=%v 输出不符：\n%s", verbose, output.String())
		}
	}
	status.Executable, status.DataDir, status.ConfigPath = "", "", ""
	var output bytes.Buffer
	if err := scheduleCommand(app.Paths{}, []string{"status", "--verbose"}, &output, io.Discard); err != nil || output.String() != summary+footer {
		t.Fatalf("空路径不应显示占位信息：%v\n%s", err, output.String())
	}
}

func TestScheduleStatusProblemsRemainActionable(t *testing.T) {
	oldInspect := inspectSchedule
	t.Cleanup(func() { inspectSchedule = oldInspect })
	no := false
	for _, tc := range []struct {
		name     string
		status   schedule.InstallStatus
		queryErr error
		want     string
		wantErr  bool
	}{
		{"query failure", schedule.InstallStatus{}, errors.New("launchctl print: access denied"), "access denied", true},
		{"missing time", schedule.InstallStatus{Registered: true, RetryInterval: schedule.RetryDisplay}, nil, "config show", true},
		{"unsupported retry", schedule.InstallStatus{Registered: true, DailyTime: "10:00"}, nil, "schedule install --at HH:MM", true},
		{"disabled", schedule.InstallStatus{Registered: true, DailyTime: "10:00", RetryInterval: schedule.RetryDisplay, Enabled: &no}, nil, "已禁用。执行 weibo-exp schedule install --at 10:00", false},
		{"stopped timer", schedule.InstallStatus{Registered: true, Backend: "Linux 用户级 systemd", DailyTime: "10:00", RetryInterval: schedule.RetryDisplay, Active: &no}, nil, "定时器未启动。执行 weibo-exp schedule install --at 10:00", false},
		{"absent", schedule.InstallStatus{}, nil, "执行 weibo-exp schedule install 安装定时任务", false},
		{"unloaded", schedule.InstallStatus{FileExists: true}, nil, "重新生成并加载调度配置", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) { return tc.status, tc.queryErr }
			var output bytes.Buffer
			err := scheduleCommand(app.Paths{}, []string{"status"}, &output, io.Discard)
			if (err != nil) != tc.wantErr {
				t.Fatalf("错误状态不符：%v", err)
			}
			text := output.String()
			if err != nil {
				if output.Len() != 0 {
					t.Fatalf("查询失败却输出正常摘要：%s", text)
				}
				text = err.Error()
			}
			if !strings.Contains(text, tc.want) {
				t.Fatalf("缺少具体错误或处理建议：%s", text)
			}
		})
	}
}

func TestScheduleStatusRejectsUnexpectedArguments(t *testing.T) {
	oldInspect := inspectSchedule
	t.Cleanup(func() { inspectSchedule = oldInspect })
	inspectSchedule = func(schedule.Config) (schedule.InstallStatus, error) {
		t.Fatal("参数错误不应查询调度器")
		return schedule.InstallStatus{}, nil
	}
	for _, args := range [][]string{{"status", "extra"}, {"status", "--typo"}} {
		if err := scheduleCommand(app.Paths{}, args, io.Discard, io.Discard); err == nil {
			t.Fatalf("未拒绝多余参数：%v", args)
		}
	}
}
