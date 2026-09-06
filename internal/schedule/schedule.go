package schedule

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/SurrealGit/weibo-exp/internal/storage"
)

const (
	Label        = "com.surrealgit.weibo-exp"
	SystemdName  = "weibo-exp"
	WindowsName  = "weibo-exp"
	RetryDisplay = "每 4 小时一次"
)

type Config struct {
	Executable     string
	DataDir        string
	LaunchAgent    string
	SystemdService string
	SystemdTimer   string
	Hour           int
	Minute         int
}

type InstallStatus struct {
	Backend       string
	Registered    bool
	FileExists    bool
	DailyTime     string
	Enabled       *bool // nil means this backend did not verify the state.
	Active        *bool
	RetryInterval string
	Executable    string
	DataDir       string
	ConfigPath    string
}

func knownBool(value bool) *bool { return &value }

var (
	currentGOOS = runtime.GOOS
	lookPath    = exec.LookPath
	runCommand  = func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).CombinedOutput()
	}
)

func BackendName() (string, error) {
	switch currentGOOS {
	case "darwin":
		return "macOS launchd", nil
	case "linux":
		return "Linux 用户级 systemd", nil
	case "windows":
		return "Windows Task Scheduler（仅用户登录期间）", nil
	default:
		return "", fmt.Errorf("当前系统 %s 不支持原生定时任务；支持 macOS、Linux(systemd --user) 和 Windows", currentGOOS)
	}
}

func ParseTime(value string) (int, int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, 0, errors.New("时间必须使用 HH:MM 格式")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, errors.New("小时必须在 00–23 之间")
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, 0, errors.New("分钟必须在 00–59 之间")
	}
	return hour, minute, nil
}

func Install(cfg Config) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}
	switch currentGOOS {
	case "darwin":
		return installLaunchd(cfg)
	case "linux":
		return installSystemd(cfg)
	case "windows":
		return installWindows(cfg)
	default:
		_, err := BackendName()
		return err
	}
}

func Uninstall(cfg Config) error {
	switch currentGOOS {
	case "darwin":
		return uninstallLaunchd(cfg)
	case "linux":
		return uninstallSystemd(cfg)
	case "windows":
		return uninstallWindows()
	default:
		_, err := BackendName()
		return err
	}
}

func Inspect(cfg Config) (InstallStatus, error) {
	switch currentGOOS {
	case "darwin":
		return inspectLaunchd(cfg)
	case "linux":
		return inspectSystemd(cfg)
	case "windows":
		return inspectWindows()
	default:
		_, err := BackendName()
		return InstallStatus{}, err
	}
}

func validateConfig(cfg Config) error {
	if strings.ContainsAny(cfg.Executable+cfg.DataDir, "\x00\r\n") {
		return errors.New("调度路径不得含换行或空字符")
	}
	if cfg.Executable == "" || !absolutePath(cfg.Executable) {
		return errors.New("可执行文件必须是绝对路径")
	}
	if cfg.DataDir == "" || !absolutePath(cfg.DataDir) {
		return errors.New("数据目录必须是绝对路径")
	}
	if cfg.Hour < 0 || cfg.Hour > 23 || cfg.Minute < 0 || cfg.Minute > 59 {
		return errors.New("定时时间无效")
	}
	return nil
}

func absolutePath(value string) bool {
	return filepath.IsAbs(value) || regexp.MustCompile(`^[A-Za-z]:[\\/]`).MatchString(value) || strings.HasPrefix(value, `\\`)
}

func readOptional(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}

func restoreFile(path string, data []byte, existed bool) error {
	if existed {
		return storage.WriteFileAtomic(path, data, 0600)
	}
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func commandError(action string, output []byte, err error) error {
	if strings.HasPrefix(action, "schtasks ") {
		output = decodeTaskText(output)
	}
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, message)
}

func scheduledArguments(text string) (string, string) {
	values := regexp.MustCompile(`<string>([^<]*)</string>`).FindAllStringSubmatch(text, -1)
	for index := 0; index+2 < len(values); index++ {
		if values[index+1][1] == "--data-dir" {
			return xmlUnescape(values[index][1]), xmlUnescape(values[index+2][1])
		}
	}
	return "", ""
}

func regexpValue(text, pattern string) string {
	match := regexp.MustCompile(pattern).FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func formatTime(hour, minute string) string {
	h, hErr := strconv.Atoi(hour)
	m, mErr := strconv.Atoi(minute)
	if hErr != nil || mErr != nil {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", h, m)
}

func escapeXML(value string) string {
	var output bytes.Buffer
	_ = xml.EscapeText(&output, []byte(value))
	return output.String()
}

func xmlUnescape(value string) string {
	return html.UnescapeString(value)
}

func validateXML(data []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		if _, err := decoder.Token(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
