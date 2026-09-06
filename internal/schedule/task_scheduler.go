package schedule

import (
	"encoding/binary"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf16"
)

func RenderWindowsTaskXML(cfg Config) ([]byte, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if strings.ContainsAny(cfg.Executable+cfg.DataDir, `"`) {
		return nil, errors.New("Windows 调度路径不得含双引号")
	}
	arguments := "--data-dir " + windowsQuote(cfg.DataDir) + " scheduled-run"
	// Separate daily triggers avoid repeating past midnight into the next day.
	var triggers strings.Builder
	for hour := cfg.Hour; hour < 24; hour += 4 {
		fmt.Fprintf(&triggers, "    <CalendarTrigger><Enabled>true</Enabled><StartBoundary>2000-01-01T%02d:%02d:00</StartBoundary><ScheduleByDay><DaysInterval>1</DaysInterval></ScheduleByDay></CalendarTrigger>\n", hour, cfg.Minute)
	}
	document := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>微博超话经验助手每日任务与每 4 小时补跑</Description></RegistrationInfo>
  <Triggers>
%s  </Triggers>
  <Principals><Principal id="Author"><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
  <Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><StartWhenAvailable>true</StartWhenAvailable><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><ExecutionTimeLimit>PT0S</ExecutionTimeLimit></Settings>
  <Actions Context="Author"><Exec><Command>%s</Command><Arguments>%s</Arguments></Exec></Actions>
</Task>
`, triggers.String(), escapeXML(cfg.Executable), escapeXML(arguments))
	if err := validateXML([]byte(document)); err != nil {
		return nil, err
	}
	return []byte(document), nil
}

func installWindows(cfg Config) error {
	if _, err := lookPath("schtasks.exe"); err != nil {
		return errors.New("系统缺少 schtasks.exe，无法安装 Windows 定时任务")
	}
	data, err := RenderWindowsTaskXML(cfg)
	if err != nil {
		return err
	}
	old, oldErr := queryWindowsXML()
	if oldErr != nil && !errors.Is(oldErr, os.ErrNotExist) {
		return oldErr
	}
	if err := installWindowsXML(data); err != nil {
		if oldErr == nil {
			if restoreErr := installWindowsXML(old); restoreErr != nil {
				return errors.Join(err, fmt.Errorf("恢复旧 Windows 任务失败: %w", restoreErr))
			}
		} else if cleanupErr := uninstallWindows(); cleanupErr != nil {
			return errors.Join(err, fmt.Errorf("清理新建 Windows 任务失败: %w", cleanupErr))
		}
		return err
	}
	return nil
}

func uninstallWindows() error {
	if _, err := lookPath("schtasks.exe"); err != nil {
		return errors.New("系统缺少 schtasks.exe，无法卸载 Windows 定时任务")
	}
	if _, err := queryWindowsXML(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if output, err := runCommand("schtasks.exe", "/Delete", "/TN", WindowsName, "/F"); err != nil {
		return fmt.Errorf("%w；请确认有权删除定时任务。如提示拒绝访问，可在管理员 PowerShell 中执行 schtasks /Delete /TN %s /F，再回到普通终端重试卸载", commandError("schtasks /Delete", output, err), WindowsName)
	}
	return nil
}

func inspectWindows() (InstallStatus, error) {
	backend, _ := BackendName()
	status := InstallStatus{Backend: backend, ConfigPath: "Task Scheduler: " + WindowsName}
	if _, err := lookPath("schtasks.exe"); err != nil {
		return status, err
	}
	data, err := queryWindowsXML()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return status, nil
		}
		return status, err
	}
	status.Registered, status.FileExists = true, true
	var task struct {
		Settings struct {
			Enabled *bool `xml:"Enabled"`
		} `xml:"Settings"`
	}
	if err := xml.Unmarshal(data, &task); err != nil {
		return status, err
	}
	status.Enabled = knownBool(task.Settings.Enabled == nil || *task.Settings.Enabled)
	status.DailyTime = windowsTime(string(data))
	status.Executable, status.DataDir = windowsArguments(string(data))
	if windowsFourHourly(data) {
		status.RetryInterval = RetryDisplay
	}
	return status, nil
}

func queryWindowsXML() ([]byte, error) {
	data, err := runCommand("schtasks.exe", "/Query", "/TN", WindowsName, "/XML")
	if err == nil {
		return decodeTaskXML(data), nil
	}
	// Error messages are localized. Only a successful full listing proves absence.
	listing, listErr := runCommand("schtasks.exe", "/Query", "/FO", "CSV", "/NH")
	if listErr != nil {
		return nil, errors.Join(commandError("schtasks /Query", data, err), commandError("schtasks /Query list", listing, listErr))
	}
	records, parseErr := csv.NewReader(strings.NewReader(string(listing))).ReadAll()
	if parseErr != nil {
		return nil, parseErr
	}
	for _, row := range records {
		if len(row) > 0 && strings.TrimPrefix(row[0], "\\") == WindowsName {
			return nil, commandError("schtasks /Query", data, err)
		}
	}
	return nil, os.ErrNotExist
}

func installWindowsXML(data []byte) error {
	// Keep UTF-8 in memory; schtasks imports a UTF-16LE file with a BOM.
	text := strings.Replace(string(data), `encoding="UTF-8"`, `encoding="UTF-16"`, 1)
	encoded := []byte{0xff, 0xfe}
	for _, unit := range utf16.Encode([]rune(text)) {
		encoded = binary.LittleEndian.AppendUint16(encoded, unit)
	}
	tmp, err := os.CreateTemp("", "weibo-exp-task-*.xml")
	if err != nil {
		return err
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	output, err := runCommand("schtasks.exe", "/Create", "/TN", WindowsName, "/XML", path, "/F")
	if err != nil {
		return commandError("schtasks /Create", output, err)
	}
	return nil
}

func windowsFourHourly(data []byte) bool {
	var task struct {
		Triggers []struct {
			StartBoundary string    `xml:"StartBoundary"`
			Enabled       *bool     `xml:"Enabled"`
			Repetition    *struct{} `xml:"Repetition"`
			DaysInterval  int       `xml:"ScheduleByDay>DaysInterval"`
		} `xml:"Triggers>CalendarTrigger"`
	}
	if xml.Unmarshal(data, &task) != nil || len(task.Triggers) == 0 {
		return false
	}
	firstHour, firstMinute, err := ParseTime(windowsTime(string(data)))
	if err != nil || len(task.Triggers) != (23-firstHour)/4+1 {
		return false
	}
	for i, trigger := range task.Triggers {
		time := regexpValue(trigger.StartBoundary, `T(\d{2}:\d{2}):00(?:[+-]\d{2}:\d{2}|Z)?$`)
		hour, minute, err := ParseTime(time)
		if err != nil || hour != firstHour+4*i || minute != firstMinute ||
			trigger.Repetition != nil || trigger.DaysInterval != 1 ||
			(trigger.Enabled != nil && !*trigger.Enabled) {
			return false
		}
	}
	return true
}

func windowsTime(text string) string {
	return regexpValue(text, `<StartBoundary>[^T]*T(\d{2}:\d{2}):[^<]+</StartBoundary>`)
}

func windowsArguments(text string) (string, string) {
	executable := xmlUnescape(regexpValue(text, `<Command>(.*?)</Command>`))
	arguments := xmlUnescape(regexpValue(text, `<Arguments>(.*?)</Arguments>`))
	marker := "--data-dir "
	index := strings.Index(arguments, marker)
	if index < 0 {
		return executable, ""
	}
	value := strings.TrimSpace(strings.TrimSuffix(arguments[index+len(marker):], "scheduled-run"))
	if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		value = value[1 : len(value)-1]
		// The renderer doubles backslashes immediately before the closing quote.
		trimmed := strings.TrimRight(value, `\`)
		trailing := len(value) - len(trimmed)
		if trailing%2 != 0 || strings.Contains(value, `"`) {
			return executable, ""
		}
		value = trimmed + strings.Repeat(`\`, trailing/2)
	}
	return executable, value
}

func windowsQuote(value string) string {
	// Paths cannot contain quotes. Only trailing backslashes need escaping;
	// otherwise the last backslash escapes the closing quote at process startup.
	trailing := len(value) - len(strings.TrimRight(value, `\`))
	return `"` + value + strings.Repeat(`\`, trailing) + `"`
}

func decodeTaskXML(data []byte) []byte {
	if len(data) >= 2 && ((data[0] == 0xff && data[1] == 0xfe) || (data[0] == 0xfe && data[1] == 0xff)) {
		var order binary.ByteOrder = binary.LittleEndian
		if data[0] == 0xfe {
			order = binary.BigEndian
		}
		values := make([]uint16, 0, (len(data)-2)/2)
		for i := 2; i+1 < len(data); i += 2 {
			values = append(values, order.Uint16(data[i:i+2]))
		}
		data = []byte(string(utf16.Decode(values)))
	} else {
		data = decodeTaskText(data)
	}
	text := strings.ReplaceAll(string(data), "UTF-16", "UTF-8")
	text = strings.ReplaceAll(text, "utf-16", "utf-8")
	return []byte(text)
}
