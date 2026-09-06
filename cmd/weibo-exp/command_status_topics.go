package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

func statusCommand(ctx context.Context, paths app.Paths, cfg app.Config, client *weibo.Client, args []string, output, errorOutput io.Writer) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	asJSON := flags.Bool("json", false, "输出 JSON")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	status, err := client.LoginStatus(ctx)
	if err != nil {
		return err
	}
	topics, err := client.FollowedTopics(ctx, cfg.MaxFollowedPages)
	if err != nil {
		return err
	}
	state, err := app.LoadState(paths.State)
	if err != nil {
		return err
	}
	if err := state.CheckAccount(status.UID); err != nil {
		return err
	}
	today := app.Today(time.Now())
	views := topicViews(topics, cfg.SelectedTopics, cfg, state, today)
	progress := taskProgress(views)
	todayComplete := progress.Complete && len(state.PendingDeletes) == 0
	result := map[string]any{
		"login": status.Login, "uid": status.UID, "followed_topics": len(topics),
		"topics_in_scope": selectedTopicCount(topics, cfg.SelectedTopics), "selection_mode": selectionMode(cfg.SelectedTopics),
		"today": today, "today_complete": todayComplete, "today_completed_topics": progress.Completed,
		"today_remaining_topics": progress.Remaining, "pending_deletes": len(state.PendingDeletes),
	}
	if *asJSON {
		return writeJSON(output, result)
	}
	fmt.Fprintln(output, "账户状态：已登录")
	fmt.Fprintf(output, "用户 UID：%s\n", status.UID)
	fmt.Fprintf(output, "关注超话：读取到 %d 个\n", len(topics))
	if len(cfg.SelectedTopics) == 0 {
		fmt.Fprintf(output, "任务范围：全部关注超话（%d 个）\n", len(topics))
	} else {
		fmt.Fprintf(output, "任务范围：已选择 %d 个超话，当前匹配 %d 个\n", len(cfg.SelectedTopics), selectedTopicCount(topics, cfg.SelectedTopics))
	}
	if progress.Complete && len(state.PendingDeletes) > 0 {
		fmt.Fprintf(output, "今日任务：互动进度已完成 %d/%d 个超话；仍有内容待清理，整体未完成\n", progress.Completed, progress.Total)
	} else {
		printTaskProgress(output, progress)
	}
	if len(state.PendingDeletes) > 0 {
		fmt.Fprintf(output, "待清理内容：%d 条（执行 cleanup 查看；缺少 ID 的项目需人工检查）\n", len(state.PendingDeletes))
	}
	fmt.Fprintf(output, "每日执行时间：%s\n", cfg.ScheduledTime)
	printScheduleState(output, paths)
	printLogRetention(output, cfg.LogRetentionDays)
	return nil
}

func topicsCommand(ctx context.Context, paths app.Paths, cfg app.Config, client *weibo.Client, args []string, output, errorOutput io.Writer) error {
	flags := flag.NewFlagSet("topics", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	asJSON := flags.Bool("json", false, "输出 JSON")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	status, err := client.LoginStatus(ctx)
	if err != nil {
		return err
	}
	topics, err := client.FollowedTopics(ctx, cfg.MaxFollowedPages)
	if err != nil {
		return err
	}
	state, err := app.LoadState(paths.State)
	if err != nil {
		return err
	}
	if err := state.CheckAccount(status.UID); err != nil {
		return err
	}
	views := topicViews(topics, cfg.SelectedTopics, cfg, state, app.Today(time.Now()))
	if *asJSON {
		return writeJSON(output, map[string]any{"selection_mode": selectionMode(cfg.SelectedTopics), "pending_deletes": len(state.PendingDeletes), "topics": views})
	}
	if len(state.PendingDeletes) > 0 {
		fmt.Fprintf(output, "全部待清理内容：%d 条；执行 weibo-exp cleanup 查看（包括已取消关注的超话）。\n", len(state.PendingDeletes))
	}
	printTopics(output, views, cfg.SelectedTopics)
	return nil
}

type topicView struct {
	Name           string `json:"name"`
	ID             string `json:"id"`
	Selected       bool   `json:"selected"`
	Signed         bool   `json:"signed_today"`
	CanCheckin     bool   `json:"can_checkin"`
	CommentsDone   int    `json:"comments_done"`
	CommentsGoal   int    `json:"comments_goal"`
	RepostsDone    int    `json:"reposts_done"`
	RepostsGoal    int    `json:"reposts_goal"`
	PendingDeletes int    `json:"pending_deletes"`
	TaskCompleted  bool   `json:"task_completed"`
}

func topicViews(topics []weibo.Topic, selected app.TopicSelections, cfg app.Config, state app.State, today string) []topicView {
	views := make([]topicView, 0, len(topics))
	for _, topic := range topics {
		progress := state.Progress(today, topic.ID, topic.Signed, cfg)
		commentsDone, repostsDone, pendingDeletes := progress.CommentsDone, progress.RepostsDone, progress.Pending
		views = append(views, topicView{
			Name: displayTopicName(topic.Name), ID: topic.ID, Selected: selected.Includes(topic.ID),
			Signed: topic.Signed, CanCheckin: topic.CanCheckin, CommentsDone: commentsDone, CommentsGoal: cfg.CommentLimit,
			RepostsDone: repostsDone, RepostsGoal: cfg.RepostLimit, PendingDeletes: pendingDeletes,
			TaskCompleted: progress.Complete,
		})
	}
	return views
}

type topicTaskProgress struct {
	Completed int
	Remaining int
	Total     int
	Complete  bool
}

func taskProgress(topics []topicView) topicTaskProgress {
	var progress topicTaskProgress
	for _, topic := range topics {
		if !topic.Selected {
			continue
		}
		progress.Total++
		if topic.TaskCompleted {
			progress.Completed++
		}
	}
	progress.Remaining = progress.Total - progress.Completed
	progress.Complete = progress.Total > 0 && progress.Remaining == 0
	return progress
}

func printTaskProgress(output io.Writer, progress topicTaskProgress) {
	if progress.Total == 0 {
		fmt.Fprintln(output, "今日任务：当前没有纳入任务范围的超话")
	} else if progress.Complete {
		fmt.Fprintf(output, "今日任务：已完成 %d/%d 个超话\n", progress.Completed, progress.Total)
	} else {
		fmt.Fprintf(output, "今日任务：已完成 %d/%d 个超话；剩余 %d 个，执行 weibo-exp topics 查看详情\n", progress.Completed, progress.Total, progress.Remaining)
	}
}

func printTopics(output io.Writer, topics []topicView, selected app.TopicSelections) {
	fmt.Fprintf(output, "关注超话：%d 个\n", len(topics))
	if len(selected) == 0 {
		fmt.Fprintln(output, "任务范围：全部关注超话")
	} else {
		fmt.Fprintf(output, "任务范围：已选择 %d 个超话\n", len(selected))
	}
	for index, topic := range topics {
		scope := "已包含"
		if !topic.Selected {
			scope = "未包含"
		}
		checkin := "暂无法确认"
		if topic.Signed {
			checkin = "今日已签到"
		} else if topic.CanCheckin {
			checkin = "今日未签到"
		}
		fmt.Fprintf(output, "\n[%d] %s\n", index+1, topic.Name)
		fmt.Fprintf(output, "  任务范围：%s\n", scope)
		fmt.Fprintf(output, "  签到状态：%s\n", checkin)
		interaction := "未完成"
		if topic.CommentsDone >= topic.CommentsGoal && topic.RepostsDone >= topic.RepostsGoal && topic.PendingDeletes == 0 {
			interaction = "已完成"
		}
		fmt.Fprintf(output, "  互动状态：%s（评论 %d/%d，转发 %d/%d）\n", interaction, topic.CommentsDone, topic.CommentsGoal, topic.RepostsDone, topic.RepostsGoal)
		if topic.PendingDeletes > 0 {
			fmt.Fprintf(output, "  待清理内容：%d 条（下次运行会优先重试删除）\n", topic.PendingDeletes)
		}
	}
	if len(topics) > 0 {
		fmt.Fprintln(output, "\n只处理指定超话（序号、名称均可）：")
		indices := "1"
		if len(topics) > 1 {
			indices = "1,2"
		}
		fmt.Fprintf(output, "  weibo-exp config set --topics %s\n", indices)
		fmt.Fprintf(output, "  weibo-exp config set --topics %q\n", topics[0].Name)
		fmt.Fprintln(output, "恢复处理全部关注超话：")
		fmt.Fprintln(output, "  weibo-exp config set --topics all")
	}
}

func selectedTopicCount(topics []weibo.Topic, selected app.TopicSelections) int {
	count := 0
	for _, topic := range topics {
		if selected.Includes(topic.ID) {
			count++
		}
	}
	return count
}

func selectionMode(selected app.TopicSelections) string {
	if len(selected) == 0 {
		return "all"
	}
	return "selected"
}
