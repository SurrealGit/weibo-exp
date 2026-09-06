package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/runner"
	"github.com/SurrealGit/weibo-exp/internal/session"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

func configCommand(ctx context.Context, paths app.Paths, args []string, output, errorOutput io.Writer, baseURL string) error {
	if len(args) == 0 || args[0] == "show" {
		flags := flag.NewFlagSet("config show", flag.ContinueOnError)
		flags.SetOutput(errorOutput)
		asJSON := flags.Bool("json", false, "输出原始 JSON 配置")
		if len(args) > 0 {
			args = args[1:]
		}
		if err := parseFlags(flags, args); err != nil {
			return err
		}
		cfg, err := app.LoadConfig(paths.Config)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(output, cfg)
		}
		fmt.Fprintf(output, "配置位置：%s\n", paths.Config)
		printConfig(output, cfg, paths)
		return nil
	}
	if args[0] == "reset" || args[0] == "path" {
		if err := noArguments("config "+args[0], args[1:], errorOutput); err != nil {
			return err
		}
	}
	if args[0] == "reset" {
		release, err := runner.AcquireLock(paths.Lock)
		if err != nil {
			return err
		}
		defer release()
	}
	switch args[0] {
	case "path":
		fmt.Fprintln(output, paths.Config)
		return nil
	case "reset":
		if err := saveConfigAndSchedule(paths, app.DefaultConfig(), nil); err != nil {
			return err
		}
		fmt.Fprintln(output, "配置结果：已恢复默认值")
		fmt.Fprintf(output, "配置位置：%s\n", paths.Config)
		return nil
	case "set":
		flags := flag.NewFlagSet("config set", flag.ContinueOnError)
		flags.SetOutput(errorOutput)
		commentLimit := flags.Int("comment-limit", -1, "每超话每日评论数")
		repostLimit := flags.Int("repost-limit", -1, "每超话每日转发数")
		maxFeedPages := flags.Int("max-feed-pages", app.DefaultConfig().MaxFeedPages, "每个超话最多读取的帖子页数；帖子不足时可提高")
		logRetentionDays := flags.Int("log-retention-days", -1, "自动保留日志天数；0 表示不自动清理")
		taskTime := flags.String("time", "", "每日定时，HH:MM")
		topics := flags.String("topics", "", "仅执行指定超话，可使用 topics 命令中的序号、名称或 ID；逗号分隔，all 表示全部")
		templatesFile := flags.String("templates-file", "", "评论池文本文件，每行一条")
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
		provided := map[string]bool{}
		flags.Visit(func(f *flag.Flag) { provided[f.Name] = true })
		if provided["comment-limit"] {
			cfg.CommentLimit = *commentLimit
		}
		if provided["repost-limit"] {
			cfg.RepostLimit = *repostLimit
		}
		if provided["max-feed-pages"] {
			cfg.MaxFeedPages = *maxFeedPages
		}
		if provided["log-retention-days"] {
			cfg.LogRetentionDays = *logRetentionDays
		}
		if *taskTime != "" {
			cfg.ScheduledTime = *taskTime
		}
		if *topics != "" {
			if *topics == "all" {
				cfg.SelectedTopics = nil
			} else {
				jar, err := session.Load(paths.Session)
				if err != nil {
					return err
				}
				client := weibo.NewClient(baseURL, &http.Client{Jar: jar, Timeout: 30 * time.Second})
				if _, err := client.LoginStatus(ctx); err != nil {
					return fmt.Errorf("解析超话选择前需要有效登录: %w", err)
				}
				followed, err := client.FollowedTopics(ctx, cfg.MaxFollowedPages)
				if err != nil {
					return fmt.Errorf("读取关注超话以解析选择: %w", err)
				}
				cfg.SelectedTopics, err = resolveTopicSelections(*topics, followed)
				if err != nil {
					return err
				}
			}
		}
		if *templatesFile != "" {
			data, err := os.ReadFile(*templatesFile)
			if err != nil {
				return err
			}
			cfg.CommentTemplates = nonEmptyLines(string(data))
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
		if err := saveConfigAndSchedule(paths, cfg, nil); err != nil {
			return err
		}
		fmt.Fprintln(output, "配置结果：已保存")
		fmt.Fprintf(output, "配置位置：%s\n", paths.Config)
		printSelectedTopics(output, cfg.SelectedTopics)
		if *logRetentionDays >= 0 {
			printLogRetention(output, cfg.LogRetentionDays)
		}
		return nil
	default:
		return errors.New("用法：weibo-exp config [show|path|reset|set]")
	}
}

func resolveTopicSelections(value string, topics []weibo.Topic) (app.TopicSelections, error) {
	inputs := splitValues(value)
	if len(inputs) == 0 {
		return nil, errors.New("超话选择不能为空；使用 all 表示全部")
	}
	seen := make(map[string]bool, len(inputs))
	result := make(app.TopicSelections, 0, len(inputs))
	for _, input := range inputs {
		var matches []weibo.Topic
		if index, err := strconv.Atoi(input); err == nil {
			if index < 1 || index > len(topics) {
				return nil, fmt.Errorf("超话序号 %d 超出范围，当前可用序号为 1–%d", index, len(topics))
			}
			matches = []weibo.Topic{topics[index-1]}
		} else {
			inputName := normalizedTopicName(input)
			for _, topic := range topics {
				if topic.ID == input || normalizedTopicName(topic.Name) == inputName {
					matches = append(matches, topic)
				}
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("未找到超话 %q；请先执行 weibo-exp topics 查看可用序号和名称", input)
		}
		if len(matches) > 1 {
			return nil, fmt.Errorf("超话名称 %q 不唯一，请改用序号或 ID", input)
		}
		topic := matches[0]
		if seen[topic.ID] {
			continue
		}
		seen[topic.ID] = true
		result = append(result, app.TopicSelection{ID: topic.ID, Name: displayTopicName(topic.Name)})
	}
	return result, nil
}

func normalizedTopicName(name string) string {
	return strings.TrimSuffix(strings.TrimSpace(name), "超话")
}

func printSelectedTopics(output io.Writer, selected app.TopicSelections) {
	if len(selected) == 0 {
		fmt.Fprintln(output, "任务超话：全部关注超话")
		return
	}
	fmt.Fprintln(output, "任务超话：")
	for index, topic := range selected {
		name := topic.Name
		if name == "" {
			name = "旧版配置（名称待重新选择后补全）"
		}
		fmt.Fprintf(output, "  [%d] %s\n", index+1, name)
	}
}

func printConfig(output io.Writer, cfg app.Config, paths app.Paths) {
	printSelectedTopics(output, cfg.SelectedTopics)
	fmt.Fprintf(output, "每日执行时间：%s\n", cfg.ScheduledTime)
	printScheduleState(output, paths)
	printLogRetention(output, cfg.LogRetentionDays)
	fmt.Fprintf(output, "每个超话每日评论：%d 条\n", cfg.CommentLimit)
	fmt.Fprintf(output, "每个超话每日转发：%d 条\n", cfg.RepostLimit)
	fmt.Fprintf(output, "评论池：%d 条\n", len(cfg.CommentTemplates))
	for index, template := range cfg.CommentTemplates {
		fmt.Fprintf(output, "  [%d] %s\n", index+1, template)
	}
	fmt.Fprintf(output, "互动公开时间：%d–%d 秒\n", cfg.VisibleDelayMin, cfg.VisibleDelayMax)
	fmt.Fprintf(output, "互动间隔：%d–%d 秒\n", cfg.ActionDelayMin, cfg.ActionDelayMax)
	fmt.Fprintf(output, "超话间隔：%d–%d 秒\n", cfg.TopicDelayMin, cfg.TopicDelayMax)
}

func printLogRetention(output io.Writer, days int) {
	if days == 0 {
		fmt.Fprintln(output, "日志自动清理：已关闭")
	} else {
		fmt.Fprintf(output, "日志自动清理：保留最近 %d 天\n", days)
	}
}

func splitValues(value string) []string {
	var result []string
	value = strings.ReplaceAll(value, "，", ",")
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func nonEmptyLines(value string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !seen[line] {
			seen[line] = true
			result = append(result, line)
		}
	}
	return result
}
