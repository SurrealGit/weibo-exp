package schedule

import (
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderLaunchAgent(t *testing.T) {
	hour, minute, err := ParseTime("09:07")
	if err != nil || hour != 9 || minute != 7 {
		t.Fatalf("时间解析错误: %d:%d %v", hour, minute, err)
	}
	cfg := Config{
		Executable: filepath.Join(t.TempDir(), "Weibo & Exp", "weibo-exp"),
		DataDir:    filepath.Join(t.TempDir(), "Application Support", "weibo-exp"),
		Hour:       hour, Minute: minute,
	}
	data, err := RenderLaunchAgent(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"<?xml version=", "<integer>9</integer>", "<integer>7</integer>", "<integer>13</integer>", "<integer>17</integer>", "<integer>21</integer>", "<string>scheduled-run</string>", "<key>RunAtLoad</key>", "Weibo &amp; Exp"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("launchd 配置缺少 %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "StartInterval") || strings.Count(text, "<key>Hour</key>") != 4 {
		t.Fatalf("launchd 补跑时间未按每日时间每 4 小时对齐：\n%s", text)
	}
	decoder := xml.NewDecoder(strings.NewReader(text))
	for {
		if _, err := decoder.Token(); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("launchd 配置不是有效 XML：%v\n%s", err, text)
		}
	}
	if strings.HasPrefix(text, "&lt;") {
		t.Fatalf("XML 声明被错误转义")
	}
	if executable, dataDir := scheduledArguments(text); executable != cfg.Executable || dataDir != cfg.DataDir {
		t.Fatalf("launchd 参数反解析错误：%q %q", executable, dataDir)
	}
}

func TestRenderSystemdAndWindows(t *testing.T) {
	cfg := Config{Executable: filepath.Join(t.TempDir(), "微博 助手/weibo-exp"), DataDir: filepath.Join(t.TempDir(), "data dir"), Hour: 10, Minute: 30}
	service, err := RenderSystemdService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	timer, err := RenderSystemdTimer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(service), "scheduled-run") || !systemdFourHourly(string(timer)) || systemdTime(string(timer)) != "10:30" {
		t.Fatalf("systemd 配置错误：\n%s\n%s", service, timer)
	}
	if executable, dataDir := systemdArguments(string(service)); executable != cfg.Executable || dataDir != cfg.DataDir {
		t.Fatalf("systemd 参数反解析错误：%q %q", executable, dataDir)
	}

	windows, err := RenderWindowsTaskXML(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(windows)
	for _, expected := range []string{"InteractiveToken", "ScheduleByDay", "scheduled-run", "IgnoreNew"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("Windows 配置缺少 %q：\n%s", expected, text)
		}
	}
	if windowsTime(text) != "10:30" {
		t.Fatalf("Windows 时间反解析错误：%q", windowsTime(text))
	}
}

func TestLaunchdUpdateFailureRestoresPreviousFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task.plist")
	old := []byte("old configuration")
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	oldOS, oldLook, oldRun := currentGOOS, lookPath, runCommand
	defer func() { currentGOOS, lookPath, runCommand = oldOS, oldLook, oldRun }()
	currentGOOS = "darwin"
	lookPath = func(string) (string, error) { return "/bin/launchctl", nil }
	bootstrapCalls := 0
	runCommand = func(_ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "print" {
			return nil, nil
		}
		if len(args) > 0 && args[0] == "bootstrap" {
			bootstrapCalls++
			if bootstrapCalls == 1 {
				return []byte("failed"), errors.New("exit 5")
			}
		}
		return nil, nil
	}
	err := Install(Config{Executable: filepath.Join(t.TempDir(), "weibo-exp"), DataDir: filepath.Join(t.TempDir(), "data"), LaunchAgent: path, Hour: 10, Minute: 30})
	if err == nil {
		t.Fatal("bootstrap 失败未返回错误")
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != string(old) {
		t.Fatalf("更新失败未恢复旧配置：%q %v", got, readErr)
	}
}

func TestSystemdLifecycleWithFakeCommands(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		Executable: filepath.Join(t.TempDir(), "weibo-exp"), DataDir: filepath.Join(t.TempDir(), "data"), Hour: 9, Minute: 7,
		SystemdService: filepath.Join(dir, SystemdName+".service"),
		SystemdTimer:   filepath.Join(dir, SystemdName+".timer"),
	}
	oldOS, oldLook, oldRun := currentGOOS, lookPath, runCommand
	defer func() { currentGOOS, lookPath, runCommand = oldOS, oldLook, oldRun }()
	currentGOOS = "linux"
	lookPath = func(string) (string, error) { return "/bin/systemctl", nil }
	enabled := false
	runCommand = func(_ string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "show-environment"), strings.Contains(joined, "daemon-reload"):
			return nil, nil
		case strings.Contains(joined, "enable --now"):
			enabled = true
			return nil, nil
		case strings.Contains(joined, "disable --now"):
			enabled = false
			return nil, nil
		case strings.Contains(joined, "is-active"):
			if enabled {
				return []byte("active"), nil
			}
			return []byte("inactive"), errors.New("exit 3")
		case strings.Contains(joined, "is-enabled"):
			if enabled {
				return []byte("enabled"), nil
			}
			return []byte("disabled"), errors.New("exit 1")
		}
		return nil, nil
	}
	if err := Install(cfg); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(cfg)
	if err != nil || !status.Registered || status.DailyTime != "09:07" || status.Executable != cfg.Executable || status.DataDir != cfg.DataDir {
		t.Fatalf("systemd 状态错误：%#v %v", status, err)
	}
	if err := Uninstall(cfg); err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Fatal("systemd timer 未停用")
	}
	if _, err := os.Stat(cfg.SystemdTimer); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("systemd timer 文件未删除：%v", err)
	}
}

func TestWindowsLifecycleWithFakeCommands(t *testing.T) {
	cfg := Config{Executable: `C:\Apps\Weibo Exp\weibo-exp.exe`, DataDir: `C:\Users\test\Data`, Hour: 8, Minute: 5}
	oldOS, oldLook, oldRun := currentGOOS, lookPath, runCommand
	defer func() { currentGOOS, lookPath, runCommand = oldOS, oldLook, oldRun }()
	currentGOOS = "windows"
	lookPath = func(string) (string, error) { return `C:\Windows\System32\schtasks.exe`, nil }
	var installed []byte
	runCommand = func(_ string, args ...string) ([]byte, error) {
		if len(args) > 1 && args[0] == "/Query" && args[1] == "/FO" {
			return nil, nil
		}
		if len(args) > 0 && args[0] == "/Query" {
			if installed == nil {
				return []byte("not found"), errors.New("exit 1")
			}
			return installed, nil
		}
		if len(args) > 0 && args[0] == "/Create" {
			path := args[4]
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			installed = data
			return nil, nil
		}
		if len(args) > 0 && args[0] == "/Delete" {
			installed = nil
			return nil, nil
		}
		return nil, nil
	}
	if err := Install(cfg); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(cfg)
	if err != nil || !status.Registered || status.DailyTime != "08:05" || status.Executable != cfg.Executable || status.DataDir != cfg.DataDir {
		t.Fatalf("Windows 状态错误：%#v %v", status, err)
	}
	if err := Uninstall(cfg); err != nil || installed != nil {
		t.Fatalf("Windows 卸载失败：%v", err)
	}
}

func TestParseTimeRejectsInvalidInput(t *testing.T) {
	for _, value := range []string{"9", "24:00", "10:60", "aa:bb"} {
		if _, _, err := ParseTime(value); err == nil {
			t.Fatalf("未拒绝无效时间 %q", value)
		}
	}
}
