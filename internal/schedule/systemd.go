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

func RenderSystemdService(cfg Config) ([]byte, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	// systemd expands environment variables in arguments, but not the executable.
	line := strings.Join([]string{systemdQuote(cfg.Executable), "--data-dir", systemdQuote(strings.ReplaceAll(cfg.DataDir, "$", "$$")), "scheduled-run"}, " ")
	return []byte("[Unit]\nDescription=Weibo Super Topic Experience Helper\n\n[Service]\nType=oneshot\nExecStart=" + line + "\n"), nil
}

func RenderSystemdTimer(cfg Config) ([]byte, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	var calendars strings.Builder
	for hour := cfg.Hour; hour < 24; hour += 4 {
		fmt.Fprintf(&calendars, "OnCalendar=*-*-* %02d:%02d:00\n", hour, cfg.Minute)
	}
	return []byte(fmt.Sprintf("[Unit]\nDescription=Weibo daily tasks and four-hour recovery\n\n[Timer]\n%sPersistent=true\nUnit=%s.service\n\n[Install]\nWantedBy=timers.target\n", calendars.String(), SystemdName)), nil
}

func installSystemd(cfg Config) error {
	if cfg.SystemdService == "" || cfg.SystemdTimer == "" {
		return errors.New("缺少 systemd 用户单元路径")
	}
	if err := requireUserSystemd(); err != nil {
		return err
	}
	service, err := RenderSystemdService(cfg)
	if err != nil {
		return err
	}
	timer, err := RenderSystemdTimer(cfg)
	if err != nil {
		return err
	}
	oldStatus, err := inspectSystemd(cfg)
	if err != nil {
		return err
	}
	oldService, serviceExists, err := readOptional(cfg.SystemdService)
	if err != nil {
		return err
	}
	oldTimer, timerExists, err := readOptional(cfg.SystemdTimer)
	if err != nil {
		return err
	}
	if err := storage.WriteFileAtomic(cfg.SystemdService, service, 0o600); err != nil {
		return err
	}
	if err := storage.WriteFileAtomic(cfg.SystemdTimer, timer, 0o600); err != nil {
		return errors.Join(err, restoreFile(cfg.SystemdService, oldService, serviceExists))
	}

	rollback := func(cause error) error {
		errs := []error{cause, restoreFile(cfg.SystemdService, oldService, serviceExists), restoreFile(cfg.SystemdTimer, oldTimer, timerExists)}
		errs = append(errs, systemctl("daemon-reload"))
		if oldStatus.Enabled != nil && *oldStatus.Enabled {
			errs = append(errs, systemctl("enable", SystemdName+".timer"))
		} else {
			errs = append(errs, systemctl("disable", SystemdName+".timer"))
		}
		if oldStatus.Active != nil && *oldStatus.Active {
			errs = append(errs, systemctl("restart", SystemdName+".timer"))
		} else {
			errs = append(errs, systemctl("stop", SystemdName+".timer"))
		}
		return errors.Join(errs...)
	}
	if err := systemctl("daemon-reload"); err != nil {
		return rollback(err)
	}
	if err := systemctl("enable", "--now", SystemdName+".timer"); err != nil {
		return rollback(err)
	}
	// --now does not restart an already active timer after changing its schedule.
	if err := systemctl("restart", SystemdName+".timer"); err != nil {
		return rollback(err)
	}

	return nil
}

func uninstallSystemd(cfg Config) error {
	status, err := inspectSystemd(cfg)
	if err != nil {
		return err
	}
	if !status.Registered && !status.FileExists {
		return nil
	}
	if status.Registered {
		if output, err := runCommand("systemctl", "--user", "disable", "--now", SystemdName+".timer"); err != nil {
			return commandError("systemctl --user disable --now", output, err)
		}
	}
	// Disabling a timer does not stop a oneshot service it already started.
	if output, err := runCommand("systemctl", "--user", "stop", SystemdName+".service"); err != nil {
		state, queryErr := runCommand("systemctl", "--user", "show", SystemdName+".service", "--property=LoadState", "--value")
		if queryErr != nil || strings.TrimSpace(string(state)) != "not-found" {
			return commandError("systemctl --user stop service", output, err)
		}
	}
	if status.Registered {
		if err := systemctl("clean", "--what=state", SystemdName+".timer"); err != nil {
			return err
		}
	}
	for _, path := range []string{cfg.SystemdTimer, cfg.SystemdService} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if output, err := runCommand("systemctl", "--user", "daemon-reload"); err != nil {
		return commandError("systemctl --user daemon-reload", output, err)
	}
	return nil
}

func inspectSystemd(cfg Config) (InstallStatus, error) {
	backend, _ := BackendName()
	status := InstallStatus{Backend: backend, ConfigPath: cfg.SystemdTimer}
	service, serviceExists, err := readOptional(cfg.SystemdService)
	if err != nil {
		return status, err
	}
	timer, timerExists, err := readOptional(cfg.SystemdTimer)
	if err != nil {
		return status, err
	}
	status.FileExists = serviceExists || timerExists
	if serviceExists {
		status.Executable, status.DataDir = systemdArguments(string(service))
	}
	if timerExists {
		status.DailyTime = systemdTime(string(timer))
		if systemdFourHourly(string(timer)) {
			status.RetryInterval = RetryDisplay
		}
	}
	if _, err := lookPath("systemctl"); err != nil {
		if !status.FileExists {
			return status, nil
		}
		return status, errors.New("当前 Linux 缺少 systemctl；仅支持用户级 systemd，不会生成无效定时任务")
	}

	if err := requireUserSystemd(); err != nil {
		// With no managed unit files, configuration and program removal do not
		// require a running user manager. Existing files still require inspection.
		if !status.FileExists {
			return status, nil
		}
		return status, err
	}
	output, err := runCommand("systemctl", "--user", "is-enabled", SystemdName+".timer")
	value := strings.TrimSpace(string(output))
	switch value {
	case "enabled", "enabled-runtime", "linked", "linked-runtime", "static", "indirect":
		status.Registered = true
		status.Enabled = knownBool(err == nil)
	case "disabled", "masked", "masked-runtime":
		status.Registered = true
		status.Enabled = knownBool(false)
	case "not-found":
		return status, nil
	default:
		return status, commandError("systemctl is-enabled", output, errors.Join(err, errors.New("无法识别启用状态")))
	}
	output, err = runCommand("systemctl", "--user", "is-active", SystemdName+".timer")
	switch strings.TrimSpace(string(output)) {
	case "active", "activating":
		status.Active = knownBool(true)
	case "inactive", "failed", "deactivating":
		status.Active = knownBool(false)
	default:
		return status, commandError("systemctl is-active", output, errors.Join(err, errors.New("无法识别运行状态")))
	}
	return status, nil
}

func requireUserSystemd() error {
	if _, err := lookPath("systemctl"); err != nil {
		return errors.New("当前 Linux 缺少 systemctl；仅支持用户级 systemd")
	}
	if output, err := runCommand("systemctl", "--user", "show-environment"); err != nil {
		return commandError("用户级 systemd 不可用", output, err)
	}
	return nil
}

func systemdTime(text string) string {
	match := regexpValue(text, `(?m)^OnCalendar=.*\s(\d{2}:\d{2}):\d{2}\s*$`)
	return match
}

func systemdArguments(text string) (string, string) {
	line := regexpValue(text, `(?m)^ExecStart=(.+)$`)
	parts := splitSystemdLine(line)
	if len(parts) == 4 && parts[1] == "--data-dir" && parts[3] == "scheduled-run" {
		return strings.ReplaceAll(parts[0], "%%", "%"), strings.NewReplacer("%%", "%", "$$", "$").Replace(parts[2])
	}
	return "", ""
}

func systemdQuote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(value) + `"`
}

func splitSystemdLine(value string) []string {
	var result []string
	for len(value) > 0 {
		value = strings.TrimSpace(value)
		if value == "" {
			break
		}
		if value[0] != '"' {
			index := strings.IndexByte(value, ' ')
			if index < 0 {
				result = append(result, value)
				break
			}
			result = append(result, value[:index])
			value = value[index+1:]
			continue
		}
		var item strings.Builder
		escaped := false
		end := 1
		for ; end < len(value); end++ {
			character := value[end]
			if escaped {
				item.WriteByte(character)
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == '"' {
				end++
				break
			} else {
				item.WriteByte(character)
			}
		}
		result = append(result, item.String())
		value = value[end:]
	}
	return result
}

func systemctl(args ...string) error {
	output, err := runCommand("systemctl", append([]string{"--user"}, args...)...)
	if err != nil {
		return commandError("systemctl "+strings.Join(args, " "), output, err)
	}
	return nil
}

func systemdFourHourly(text string) bool {
	matches := regexp.MustCompile(`(?m)^OnCalendar=\*-\*-\* (\d{2}):(\d{2}):00$`).FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return false
	}
	first, _ := strconv.Atoi(matches[0][1])
	minute, _ := strconv.Atoi(matches[0][2])
	if first > 23 || minute > 59 || len(matches) != (23-first)/4+1 {
		return false
	}
	for i, m := range matches {
		h, _ := strconv.Atoi(m[1])
		if h != first+4*i || m[2] != matches[0][2] {
			return false
		}
	}
	return true
}
