package schedule

import (
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func fakePlatform(t *testing.T, platform string, command func(string, ...string) ([]byte, error)) {
	t.Helper()
	oldOS, oldLook, oldRun := currentGOOS, lookPath, runCommand
	t.Cleanup(func() { currentGOOS, lookPath, runCommand = oldOS, oldLook, oldRun })
	currentGOOS = platform
	lookPath = func(s string) (string, error) { return s, nil }
	runCommand = command
}

func TestLaunchdRegistrationDoesNotInventEnabledOrActiveState(t *testing.T) {
	fakePlatform(t, "darwin", func(string, ...string) ([]byte, error) { return []byte("registered"), nil })
	status, err := Inspect(Config{LaunchAgent: filepath.Join(t.TempDir(), "absent.plist")})
	if err != nil || !status.Registered || status.Enabled != nil || status.Active != nil {
		t.Fatalf("将注册状态误认为启用/运行：%+v %v", status, err)
	}
}

func TestSystemdDisabledInactiveStatesAreKnown(t *testing.T) {
	fakePlatform(t, "linux", func(_ string, args ...string) ([]byte, error) {
		switch args[1] {
		case "is-enabled":
			return []byte("disabled"), errors.New("exit 1")
		case "is-active":
			return []byte("inactive"), errors.New("exit 3")
		default:
			return nil, nil
		}
	})
	dir := t.TempDir()
	status, err := Inspect(Config{SystemdService: filepath.Join(dir, "task.service"), SystemdTimer: filepath.Join(dir, "task.timer")})
	if err != nil || !status.Registered || status.Enabled == nil || *status.Enabled || status.Active == nil || *status.Active {
		t.Fatalf("已查明的禁用/非活动状态错误：%+v %v", status, err)
	}
}

func TestWindowsDisabledDoesNotImplyKnownActivity(t *testing.T) {
	fakePlatform(t, "windows", func(string, ...string) ([]byte, error) {
		return []byte(`<Task><Settings><Enabled>false</Enabled></Settings></Task>`), nil
	})
	status, err := Inspect(Config{})
	if err != nil || !status.Registered || status.Enabled == nil || *status.Enabled || status.Active != nil {
		t.Fatalf("Windows 未区分禁用与未知活动状态：%+v %v", status, err)
	}
}
func TestWindowsQueryFailureIsNotSuccess(t *testing.T) {
	deleted := false
	fakePlatform(t, "windows", func(_ string, args ...string) ([]byte, error) {
		if args[0] == "/Delete" {
			deleted = true
		}
		return []byte("access denied"), errors.New("exit 1")
	})
	if _, err := Inspect(Config{}); err == nil {
		t.Fatal("query error swallowed")
	}
	if err := Uninstall(Config{}); err == nil || deleted {
		t.Fatal("uninstall falsely successful")
	}
}
func TestWindowsTargetQueryFailureWithListedTaskIsError(t *testing.T) {
	fakePlatform(t, "windows", func(_ string, args ...string) ([]byte, error) {
		if args[1] == "/FO" {
			return []byte(`"\weibo-exp","N/A","Ready"`), nil
		}
		return []byte("access denied"), errors.New("exit 1")
	})
	if _, err := queryWindowsXML(); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatal("existing task query failure swallowed")
	}
}
func TestWindowsXMLTriggerOrderAndPower(t *testing.T) {
	data, err := RenderWindowsTaskXML(Config{Executable: filepath.Join(t.TempDir(), "exp"), DataDir: filepath.Join(t.TempDir(), "data"), Hour: 10})
	if err != nil {
		t.Fatal(err)
	}
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	depth := 0
	inTrigger := false
	var children []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch item := token.(type) {
		case xml.StartElement:
			if item.Name.Local == "CalendarTrigger" {
				inTrigger = true
				depth = 0
				children = nil
			} else if inTrigger {
				if depth == 0 {
					children = append(children, item.Name.Local)
				}
				depth++
			}
		case xml.EndElement:
			if item.Name.Local == "CalendarTrigger" {
				if strings.Join(children, ",") != "Enabled,StartBoundary,ScheduleByDay" {
					t.Fatalf("%v", children)
				}
				inTrigger = false
			} else if inTrigger {
				depth--
			}
		}
	}
	if strings.Join(children, ",") != "Enabled,StartBoundary,ScheduleByDay" {
		t.Fatalf("%v", children)
	}
	for _, part := range []string{"<DisallowStartIfOnBatteries>false", "<StopIfGoingOnBatteries>false", "<ExecutionTimeLimit>PT0S"} {
		if !strings.Contains(string(data), part) {
			t.Fatalf("missing %s", part)
		}
	}
}
func TestLaunchdQueryFailureAndRollbackFailureReported(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Executable: filepath.Join(t.TempDir(), "exp"), DataDir: filepath.Join(t.TempDir(), "data"), LaunchAgent: filepath.Join(dir, "task.plist")}
	if err := os.WriteFile(cfg.LaunchAgent, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	fakePlatform(t, "darwin", func(_ string, args ...string) ([]byte, error) {
		if args[0] == "print" {
			return nil, nil
		}
		if args[0] == "bootstrap" {
			return []byte("fail"), errors.New("restore bootstrap failure")
		}
		return nil, nil
	})
	err := Install(cfg)
	if err == nil || !strings.Contains(err.Error(), "恢复旧 launchd") {
		t.Fatalf("%v", err)
	}
	data, _ := os.ReadFile(cfg.LaunchAgent)
	if string(data) != "original" {
		t.Fatal("old file lost")
	}
	runCommand = func(string, ...string) ([]byte, error) { return []byte("access denied"), errors.New("denied") }
	if _, err := Inspect(cfg); err == nil {
		t.Fatal("query failure swallowed")
	}
}
func TestSystemdRollbackRestoresDisabledInactiveState(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Executable: filepath.Join(t.TempDir(), "exp"), DataDir: filepath.Join(t.TempDir(), "data"), SystemdService: filepath.Join(dir, "test.service"), SystemdTimer: filepath.Join(dir, "test.timer")}
	os.WriteFile(cfg.SystemdService, []byte("old-service"), 0600)
	os.WriteFile(cfg.SystemdTimer, []byte("old-timer"), 0600)
	var calls []string
	fakePlatform(t, "linux", func(_ string, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		if strings.Contains(call, "is-enabled") {
			return []byte("disabled"), errors.New("exit 1")
		}
		if strings.Contains(call, "is-active") {
			return []byte("inactive"), errors.New("exit 3")
		}
		if strings.Contains(call, "enable --now") {
			return nil, errors.New("partial enable failure")
		}
		return nil, nil
	})
	if err := Install(cfg); err == nil {
		t.Fatal("expected failure")
	}
	data, _ := os.ReadFile(cfg.SystemdTimer)
	if string(data) != "old-timer" {
		t.Fatal("old timer lost")
	}
	if !strings.Contains(strings.Join(calls, "\n"), "--user stop ") {
		t.Fatal("active state not restored")
	}
}
func TestRetryDetectionDoesNotGuess(t *testing.T) {
	cfg := Config{Executable: filepath.Join(t.TempDir(), "exp"), DataDir: filepath.Join(t.TempDir(), "data"), Hour: 9, Minute: 7}
	plist, _ := RenderLaunchAgent(cfg)
	if !launchdFourHourly(string(plist)) {
		t.Fatal("valid launchd interval rejected")
	}
	broken := strings.Replace(string(plist), "<integer>13</integer>", "<integer>14</integer>", 1)
	if launchdFourHourly(broken) {
		t.Fatal("invalid launchd interval accepted")
	}
	timer, _ := RenderSystemdTimer(cfg)
	if !systemdFourHourly(string(timer)) {
		t.Fatal("invalid systemd detection")
	}
}

func TestTaskXMLUTF16AndTimestampOffset(t *testing.T) {
	text := `<?xml version="1.0" encoding="UTF-16"?><Task><StartBoundary>2026-09-05T10:00:00+08:00</StartBoundary></Task>`
	data := []byte{0xff, 0xfe}
	for _, value := range utf16.Encode([]rune(text)) {
		data = binary.LittleEndian.AppendUint16(data, value)
	}
	decoded := decodeTaskXML(data)
	if err := validateXML(decoded); err != nil {
		t.Fatal(err)
	}
	if windowsTime(string(decoded)) != "10:00" {
		t.Fatal("offset timestamp not parsed")
	}
}
func TestSystemdEscapesExpansionCharacters(t *testing.T) {
	cfg := Config{Executable: filepath.Join(t.TempDir(), "a%$literal/weibo-exp"), DataDir: filepath.Join(t.TempDir(), "user$100%"), Hour: 10}
	data, err := RenderSystemdService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	executable, dataDir := systemdArguments(string(data))
	if executable != cfg.Executable || dataDir != cfg.DataDir {
		t.Fatalf("%s %s", executable, dataDir)
	}
	if !strings.Contains(string(data), "$$") || !strings.Contains(string(data), "%%") {
		t.Fatal("systemd expansion not escaped")
	}
	if strings.Contains(string(data), "$$literal") || !strings.Contains(string(data), "$literal") {
		t.Fatal("executable dollar must remain literal")
	}
}
