package schedule

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/SurrealGit/weibo-exp/internal/storage"
)

func RenderLaunchAgent(cfg Config) ([]byte, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	var calendarIntervals strings.Builder
	for hour := cfg.Hour; hour < 24; hour += 4 {
		fmt.Fprintf(&calendarIntervals, "    <dict><key>Hour</key><integer>%d</integer><key>Minute</key><integer>%d</integer></dict>\n", hour, cfg.Minute)
	}
	document := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string><string>--data-dir</string><string>%s</string><string>scheduled-run</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>StartCalendarInterval</key>
  <array>
%s  </array>
  <key>ProcessType</key><string>Background</string>
</dict>
</plist>
`, escapeXML(Label), escapeXML(cfg.Executable), escapeXML(cfg.DataDir), calendarIntervals.String())
	if err := validateXML([]byte(document)); err != nil {
		return nil, fmt.Errorf("生成的 launchd 配置不是有效 XML: %w", err)
	}
	return []byte(document), nil
}

func installLaunchd(cfg Config) error {
	if cfg.LaunchAgent == "" {
		return errors.New("缺少 launchd 配置路径")
	}
	data, err := RenderLaunchAgent(cfg)
	if err != nil {
		return err
	}
	old, oldExists, err := readOptional(cfg.LaunchAgent)
	if err != nil {
		return err
	}
	oldStatus, err := inspectLaunchd(cfg)
	if err != nil {
		return err
	}
	if oldStatus.Registered && !oldExists {
		return errors.New("任务已注册但缺少原 plist，无法安全更新或回滚；请先确认系统任务配置")
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	if oldStatus.Registered {
		if output, err := runCommand("launchctl", "bootout", domain+"/"+Label); err != nil {
			return commandError("launchctl bootout", output, err)
		}
	}
	if err := storage.WriteFileAtomic(cfg.LaunchAgent, data, 0o600); err != nil {
		if oldStatus.Registered && oldExists {
			_, restoreErr := runCommand("launchctl", "bootstrap", domain, cfg.LaunchAgent)
			if restoreErr != nil {
				return errors.Join(err, fmt.Errorf("恢复旧 launchd 任务失败: %w", restoreErr))
			}
		}
		return err
	}
	if output, err := runCommand("launchctl", "bootstrap", domain, cfg.LaunchAgent); err != nil {
		restoreErr := restoreFile(cfg.LaunchAgent, old, oldExists)
		if restoreErr != nil {
			return errors.Join(commandError("launchctl bootstrap", output, err), restoreErr)
		}
		if oldStatus.Registered && oldExists {
			_, restoreErr := runCommand("launchctl", "bootstrap", domain, cfg.LaunchAgent)
			if restoreErr != nil {
				return errors.Join(commandError("launchctl bootstrap", output, err), fmt.Errorf("恢复旧 launchd 任务失败: %w", restoreErr))
			}
		}
		return commandError("launchctl bootstrap", output, err)
	}
	return nil
}

func uninstallLaunchd(cfg Config) error {
	status, err := inspectLaunchd(cfg)
	if err != nil {
		return err
	}
	if status.Registered {
		domain := fmt.Sprintf("gui/%d", os.Getuid())
		if output, err := runCommand("launchctl", "bootout", domain+"/"+Label); err != nil {
			return commandError("launchctl bootout", output, err)
		}
	}
	if status.FileExists {
		return os.Remove(cfg.LaunchAgent)
	}
	return nil
}

func inspectLaunchd(cfg Config) (InstallStatus, error) {
	backend, _ := BackendName()
	status := InstallStatus{Backend: backend, ConfigPath: cfg.LaunchAgent}
	data, exists, err := readOptional(cfg.LaunchAgent)
	if err != nil {
		return status, err
	}
	status.FileExists = exists
	if exists {
		status.DailyTime = launchdTime(string(data))
		status.Executable, status.DataDir = scheduledArguments(string(data))
		if launchdFourHourly(string(data)) {
			status.RetryInterval = RetryDisplay
		}
	}
	if _, err := lookPath("launchctl"); err != nil {
		if !status.FileExists {
			return status, nil
		}
		return status, errors.New("系统缺少 launchctl，无法检查 macOS 定时任务")
	}
	domainTarget := fmt.Sprintf("gui/%d/%s", os.Getuid(), Label)
	output, queryErr := runCommand("launchctl", "print", domainTarget)
	if queryErr != nil {
		if strings.Contains(string(output), "Could not find service") {
			return status, nil
		}
		return status, commandError("launchctl print", output, queryErr)
	}
	// Registration alone proves neither enabled nor currently running.
	status.Registered = true
	return status, nil
}

func launchdTime(text string) string {
	hour := regexpValue(text, `<key>Hour</key>\s*<integer>(\d+)</integer>`)
	minute := regexpValue(text, `<key>Minute</key>\s*<integer>(\d+)</integer>`)
	return formatTime(hour, minute)
}

func launchdFourHourly(text string) bool {
	matches := regexp.MustCompile(`<key>Hour</key>\s*<integer>(\d+)</integer>\s*<key>Minute</key>\s*<integer>(\d+)</integer>`).FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return false
	}
	first, err := strconv.Atoi(matches[0][1])
	if err != nil || first > 23 || len(matches) != (23-first)/4+1 {
		return false
	}
	minute, err := strconv.Atoi(matches[0][2])
	if err != nil || minute > 59 {
		return false
	}
	for i, m := range matches {
		h, e := strconv.Atoi(m[1])
		if e != nil || h != first+4*i || m[2] != matches[0][2] {
			return false
		}
	}
	return true
}
