package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/storage"
)

const Version = "0.7.0"

var DefaultCommentTemplates = []string{
	"打卡",
	"来啦",
	"tt",
	"TT",
	"[打call]",
	"[打call][打call][打call]",
	"[哇]",
	"[哇][哇][哇]",
	"[送花花]",
	"[期待]",
}

type Paths struct {
	DataDir        string
	Config         string
	Session        string
	State          string
	Lock           string
	LogDir         string
	LaunchAgent    string
	SystemdService string
	SystemdTimer   string
}

func DefaultDataDir() (string, error) {
	if value := os.Getenv("WEIBO_EXP_DATA_DIR"); value != "" {
		return filepath.Abs(value)
	}
	return systemDataDir()
}

func systemDataDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "weibo-exp"), nil
}

func ResolvePaths(dataDir string) (Paths, error) {
	if dataDir == "" {
		var err error
		dataDir, err = DefaultDataDir()
		if err != nil {
			return Paths{}, err
		}
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return Paths{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	logDir := filepath.Join(abs, "logs")
	defaultDir, defaultErr := systemDataDir()
	if defaultErr == nil && !sameDirectory(abs, defaultDir) {
		// Explicit alternate data directories own their logs as well.
	} else if runtime.GOOS == "darwin" {
		logDir = filepath.Join(home, "Library", "Logs", "weibo-exp")
	} else if cacheDir, cacheErr := os.UserCacheDir(); cacheErr == nil {
		logDir = filepath.Join(cacheDir, "weibo-exp")
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, err
	}
	systemdDir := filepath.Join(configDir, "systemd", "user")
	return Paths{
		DataDir: abs, Config: filepath.Join(abs, "config.json"), Session: filepath.Join(abs, "session.json"),
		State: filepath.Join(abs, "state.json"), Lock: filepath.Join(abs, "run.lock"), LogDir: logDir,
		LaunchAgent:    filepath.Join(home, "Library", "LaunchAgents", "com.surrealgit.weibo-exp.plist"),
		SystemdService: filepath.Join(systemdDir, "weibo-exp.service"),
		SystemdTimer:   filepath.Join(systemdDir, "weibo-exp.timer"),
	}, nil
}

// Compare existing aliases without changing the paths exposed to callers.
func sameDirectory(a, b string) bool {
	if a == b {
		return true
	}
	left, le := os.Stat(a)
	right, re := os.Stat(b)
	return le == nil && re == nil && os.SameFile(left, right)
}

type Config struct {
	Version          int             `json:"version"`
	ScheduledTime    string          `json:"scheduled_time"`
	CommentLimit     int             `json:"comment_limit"`
	RepostLimit      int             `json:"repost_limit"`
	CommentTemplates []string        `json:"comment_templates"`
	SelectedTopics   TopicSelections `json:"selected_topics,omitempty"`
	MaxFollowedPages int             `json:"max_followed_pages"`
	MaxFeedPages     int             `json:"max_feed_pages"`
	MaxPostsPerTopic int             `json:"max_posts_per_topic"`
	VisibleDelayMin  int             `json:"visible_delay_min_seconds"`
	VisibleDelayMax  int             `json:"visible_delay_max_seconds"`
	ActionDelayMin   int             `json:"action_delay_min_seconds"`
	ActionDelayMax   int             `json:"action_delay_max_seconds"`
	TopicDelayMin    int             `json:"topic_delay_min_seconds"`
	TopicDelayMax    int             `json:"topic_delay_max_seconds"`
	LogRetentionDays int             `json:"log_retention_days"`
}

type TopicSelection struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type TopicSelections []TopicSelection

func (s *TopicSelections) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*s = nil
		return nil
	}
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	result := make(TopicSelections, 0, len(values))
	for _, value := range values {
		var legacyID string
		if json.Unmarshal(value, &legacyID) == nil {
			result = append(result, TopicSelection{ID: legacyID})
			continue
		}
		var selection TopicSelection
		if err := json.Unmarshal(value, &selection); err != nil {
			return errors.New("selected_topics 必须是超话对象数组或旧版 ID 数组")
		}
		result = append(result, selection)
	}
	*s = result
	return nil
}

func (s TopicSelections) Includes(topicID string) bool {
	if len(s) == 0 {
		return true
	}
	for _, topic := range s {
		if topic.ID == topicID {
			return true
		}
	}
	return false
}

func DefaultConfig() Config {
	return Config{
		Version:          2,
		ScheduledTime:    "10:00",
		CommentLimit:     4,
		RepostLimit:      2,
		CommentTemplates: append([]string(nil), DefaultCommentTemplates...),
		MaxFollowedPages: 30,
		MaxFeedPages:     8,
		MaxPostsPerTopic: 80,
		VisibleDelayMin:  6,
		VisibleDelayMax:  9,
		ActionDelayMin:   8,
		ActionDelayMax:   14,
		TopicDelayMin:    12,
		TopicDelayMax:    20,
		LogRetentionDays: 7,
	}
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if err := storage.ReadJSON(path, &cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	if cfg.Version < 2 {
		cfg.Version = 2
	}
	return cfg, nil
}

func SaveConfig(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	return storage.WriteJSON(path, cfg, 0o600)
}

func (c Config) Validate() error {
	if _, err := time.Parse("15:04", c.ScheduledTime); err != nil {
		return errors.New("定时时间必须使用 HH:MM 格式")
	}
	if c.CommentLimit < 0 || c.CommentLimit > 100 || c.RepostLimit < 0 || c.RepostLimit > 100 {
		return errors.New("评论和转发上限必须在 0–100 之间")
	}
	if len(c.CommentTemplates) == 0 {
		return errors.New("评论池不能为空")
	}
	seenTopics := make(map[string]bool, len(c.SelectedTopics))
	for _, topic := range c.SelectedTopics {
		if topic.ID == "" {
			return errors.New("已选超话缺少 ID")
		}
		if seenTopics[topic.ID] {
			return errors.New("已选超话中存在重复 ID")
		}
		seenTopics[topic.ID] = true
	}
	for _, pair := range [][2]int{
		{c.VisibleDelayMin, c.VisibleDelayMax},
		{c.ActionDelayMin, c.ActionDelayMax},
		{c.TopicDelayMin, c.TopicDelayMax},
	} {
		if pair[0] < 0 || pair[1] < pair[0] {
			return errors.New("延时配置必须为非负数且最大值不得小于最小值")
		}
	}
	if c.MaxFollowedPages < 1 || c.MaxFeedPages < 1 || c.MaxPostsPerTopic < 1 {
		return errors.New("分页与帖子数上限必须大于 0")
	}
	if c.LogRetentionDays < 0 || c.LogRetentionDays > 3650 {
		return errors.New("日志保留天数必须在 0–3650 之间；0 表示不自动清理")
	}
	return nil
}

func Today(now time.Time) string {
	return now.Local().Format("2006-01-02")
}

func PlatformName() string {
	return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
}
