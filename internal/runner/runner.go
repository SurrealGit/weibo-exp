package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

type API interface {
	LoginStatus(context.Context) (weibo.LoginStatus, error)
	FollowedTopics(context.Context, int) ([]weibo.Topic, error)
	TopicPosts(context.Context, string, string, int, int, []string, *weibo.PostFeed) ([]weibo.Post, error)
	Checkin(context.Context, string) error
	Comment(context.Context, string, string, string) (string, error)
	DeleteComment(context.Context, string, string) error
	Repost(context.Context, string, string) (string, error)
	DeleteRepost(context.Context, string, string) error
}

type Options struct {
	DryRun bool
	Clock  func() time.Time
	IfDue  bool
	Output io.Writer
	Sleep  func(context.Context, time.Duration) error
	Save   func(app.State) error
	Random *rand.Rand
}

type Summary struct {
	Topics        int `json:"topics"`
	Checkins      int `json:"checkins"`
	Comments      int `json:"comments"`
	Reposts       int `json:"reposts"`
	Deleted       int `json:"deleted"`
	SkippedTopics int `json:"skipped_topics"`
}

type Runner struct {
	API     API
	Config  app.Config
	State   *app.State
	Options Options
}

func (r *Runner) Run(ctx context.Context) (Summary, error) {
	if r.API == nil || r.State == nil {
		return Summary{}, errors.New("任务执行器未初始化")
	}
	if r.Options.Output == nil {
		r.Options.Output = io.Discard
	}
	if r.Options.Clock == nil {
		r.Options.Clock = time.Now
	}
	if r.Options.Sleep == nil {
		r.Options.Sleep = sleepContext
	}
	if r.Options.Random == nil {
		r.Options.Random = rand.New(rand.NewSource(r.Options.Clock().UnixNano()))
	}
	if r.Options.Save == nil {
		if !r.Options.DryRun {
			return Summary{}, errors.New("实际运行必须提供状态保存函数")
		}
	}
	date := app.Today(r.Options.Clock())
	summary := Summary{}

	status, err := r.API.LoginStatus(ctx)
	if err != nil {
		return Summary{}, err
	}
	if err := r.State.CheckAccount(status.UID); err != nil {
		return summary, err
	}
	if !r.Options.DryRun && r.State.UID == "" {
		r.State.UID = status.UID
		if err := r.Options.Save(*r.State); err != nil {
			return summary, err
		}
	}
	if len(r.State.PendingDeletes) > 0 && !r.Options.DryRun {
		cleaned, err := r.cleanupPending(ctx, status.ST)
		summary.Deleted += cleaned
		if err != nil {
			return summary, err
		}
	}
	if r.Options.IfDue && r.Options.Clock().Format("15:04") < r.Config.ScheduledTime {
		fmt.Fprintf(r.Options.Output, "定时检查：当前时间尚未达到设定时间 %s；本次不创建新任务。\n", r.Config.ScheduledTime)
		return summary, nil
	}
	allTopics, err := r.API.FollowedTopics(ctx, r.Config.MaxFollowedPages)
	if err != nil {
		return summary, err
	}
	topics := filterTopics(allTopics, r.Config.SelectedTopics)
	if len(topics) == 0 {
		if len(allTopics) == 0 {
			return summary, errors.New("未读取到已关注超话，为避免误判本次不记为成功")
		}
		return summary, errors.New("已读取关注超话，但没有任何超话匹配当前 selected_topics 配置")
	}
	summary.Topics = len(topics)
	if err := r.checkDate(date); err != nil {
		return summary, err
	}
	if r.Options.IfDue && len(r.State.PendingDeletes) == 0 {
		complete := true
		for _, topic := range topics {
			complete = complete && r.State.Progress(date, topic.ID, topic.Signed, r.Config).Complete
		}
		if complete {
			fmt.Fprintf(r.Options.Output, "定时检查：今日已完成 %d/%d 个超话；本次不执行。\n", len(topics), len(topics))
			return summary, nil
		}
	}
	if r.Options.DryRun {
		fmt.Fprintln(r.Options.Output, "只读预演：本次不会向微博发送签到、评论、转发或删除请求。")
	}
	fmt.Fprintf(r.Options.Output, "账户状态：已登录；读取到 %d 个关注超话，%d 个纳入本次检查。\n", len(allTopics), len(topics))
	if len(r.State.PendingDeletes) > 0 {
		fmt.Fprintf(r.Options.Output, "待清理内容：%d 条；正式运行时会先清理，清理完成前不会创建新互动。\n", len(r.State.PendingDeletes))
		return Summary{Topics: len(topics)}, errors.New("存在待清理内容，本次预演不规划新的评论或转发")
	}

	var partial []string
	for topicIndex := range topics {
		if err := r.checkDate(date); err != nil {
			return summary, err
		}
		topic := &topics[topicIndex]
		fmt.Fprintf(r.Options.Output, "\n[%d/%d] %s超话\n", topicIndex+1, len(topics), topic.Name)
		if topic.Signed {
			fmt.Fprintln(r.Options.Output, "  签到状态：今日已签到；无需执行")
		} else if topic.CanCheckin {
			if r.Options.DryRun {
				if topic.CheckinURL == "" {
					fmt.Fprintln(r.Options.Output, "  签到状态：未签到；当前凭证缺失，正式运行时将刷新后再判断")
				} else {
					summary.Checkins++
					fmt.Fprintln(r.Options.Output, "  签到状态：未签到；正式运行时将执行签到")
				}
			} else {
				fmt.Fprintln(r.Options.Output, "  签到状态：未签到；正在刷新签到凭证")
				freshTopic, err := r.freshTopic(ctx, topic.ID)
				if dateErr := r.checkDate(date); dateErr != nil {
					return summary, dateErr
				}
				if err != nil {
					partial = append(partial, fmt.Sprintf("[%s] 刷新签到凭证失败: %v", topic.Name, err))
					fmt.Fprintf(r.Options.Output, "  签到结果：未执行（刷新签到凭证失败：%v）\n", err)
				} else if freshTopic.Signed {
					fmt.Fprintln(r.Options.Output, "  签到状态：刷新后显示今日已签到；无需执行")
				} else if !freshTopic.CanCheckin || freshTopic.CheckinURL == "" {
					state := freshTopic.ButtonName
					if state == "" {
						state = "未提供按钮状态"
					}
					partial = append(partial, fmt.Sprintf("[%s] 刷新后无可用签到入口（%s）", topic.Name, state))
					fmt.Fprintf(r.Options.Output, "  签到结果：未执行（刷新后无可用签到入口：%s）\n", state)
				} else if err := r.API.Checkin(ctx, freshTopic.CheckinURL); err != nil {
					partial = append(partial, fmt.Sprintf("[%s] 签到失败: %v", topic.Name, err))
					fmt.Fprintf(r.Options.Output, "  签到结果：失败（%v）\n", err)
				} else {
					summary.Checkins++
					fmt.Fprintln(r.Options.Output, "  签到结果：成功")
				}
			}
		} else {
			state := topic.ButtonName
			if state == "" {
				state = "未提供按钮状态"
			}
			fmt.Fprintf(r.Options.Output, "  签到状态：无可执行入口（%s）\n", state)
			partial = append(partial, fmt.Sprintf("[%s] 无法确认签到状态或没有签到入口（%s）", topic.Name, state))
		}

		history := r.State.Topic(date, topic.ID)
		progress := r.State.Progress(date, topic.ID, topic.Signed, r.Config)
		commentRemaining, repostRemaining := progress.CommentsRemaining, progress.RepostsRemaining
		if commentRemaining+repostRemaining == 0 {
			fmt.Fprintf(r.Options.Output, "  互动状态：本地记录显示今日已完成（评论 %d/%d，转发 %d/%d）\n",
				len(history.CommentedPostIDs), r.Config.CommentLimit, len(history.RepostedPostIDs), r.Config.RepostLimit)
			continue
		}

		completed := append(append([]string(nil), history.CommentedPostIDs...), history.RepostedPostIDs...)
		feed := &weibo.PostFeed{}
		posts, err := r.API.TopicPosts(ctx, topic.ID, status.UID, r.Config.MaxFeedPages, max(r.Config.MaxPostsPerTopic, r.Config.CommentLimit+r.Config.RepostLimit), completed, feed)
		if err != nil {
			summary.SkippedTopics++
			partial = append(partial, fmt.Sprintf("[%s] 获取帖子失败: %v", topic.Name, err))
			fmt.Fprintf(r.Options.Output, "  候选帖子：读取失败，已跳过该超话（%v）\n", err)
			continue
		}
		if len(posts) == 0 {
			summary.SkippedTopics++
			partial = append(partial, fmt.Sprintf("[%s] 未读取到可用候选帖子", topic.Name))
			fmt.Fprintf(r.Options.Output, "  候选帖子：0 条可用；已排除本人及今日已互动内容。可通过 config set --max-feed-pages 调整分页上限（当前 %d 页）。\n", r.Config.MaxFeedPages)
			continue
		}
		selected := selectPosts(posts, completed, commentRemaining+repostRemaining)
		comments := selected[:min(commentRemaining, len(selected))]
		repostsStart := len(comments)
		repostsEnd := min(repostsStart+repostRemaining, len(selected))
		reposts := selected[repostsStart:repostsEnd]
		fmt.Fprintf(r.Options.Output, "  候选帖子：%d 条；剩余任务目标：评论 %d 条，转发 %d 条；已分配不同帖子 %d 条\n",
			len(posts), commentRemaining, repostRemaining, len(comments)+len(reposts))
		if r.Options.DryRun && len(selected) < commentRemaining+repostRemaining {
			partial = append(partial, fmt.Sprintf("[%s] 可用帖子不足，需要 %d 条而实际只有 %d 条", topic.Name, commentRemaining+repostRemaining, len(selected)))
			fmt.Fprintf(r.Options.Output, "  注意：可用帖子不足，目标需要 %d 条，本次只能分配 %d 条\n", commentRemaining+repostRemaining, len(selected))
			fmt.Fprintf(r.Options.Output, "  读取限制：最多 %d 页、%d 条候选；已排除本人及今日已互动内容。可通过 config set --max-feed-pages 调整分页上限；帖子不足不记为全部完成。\n",
				r.Config.MaxFeedPages, max(r.Config.MaxPostsPerTopic, r.Config.CommentLimit+r.Config.RepostLimit))
		}

		if r.Options.DryRun {
			summary.Comments += len(comments)
			summary.Reposts += len(reposts)
			continue
		}
		unfinished, err := r.runInteractions(ctx, topic.ID, date, status, posts, completed, feed, commentRemaining, repostRemaining, &summary)
		if err != nil {
			return summary, err
		}
		if unfinished != "" {
			partial = append(partial, fmt.Sprintf("[%s] %s", topic.Name, unfinished))
		}
		if topicIndex < len(topics)-1 {
			if err := r.delay(ctx, r.Config.TopicDelayMin, r.Config.TopicDelayMax); err != nil {
				return summary, err
			}
		}
	}

	if len(partial) > 0 {
		return summary, errors.New(strings.Join(partial, "; "))
	}
	if r.Options.DryRun {
		return summary, nil
	}
	if err := r.Options.Save(*r.State); err != nil {
		return summary, err
	}
	return summary, nil
}

func (r *Runner) cleanupPending(ctx context.Context, st string) (int, error) {
	fmt.Fprintf(r.Options.Output, "发现 %d 条待清理内容；正在优先重试删除。\n", len(r.State.PendingDeletes))
	cleaned := 0
	for len(r.State.PendingDeletes) > 0 {
		item := r.State.PendingDeletes[0]
		if item.ContentID == "" {
			return cleaned, fmt.Errorf("存在无内容 ID 的待清理%s（原帖 %s）；请人工检查后执行 cleanup confirm", pendingKindName(item.Kind), item.SourcePostID)
		}
		err := r.deleteContent(ctx, item.Kind, item.ContentID, st)
		if err != nil {
			return cleaned, fmt.Errorf("重试删除%s %s 失败；清理完成前不会创建新互动: %w", pendingKindName(item.Kind), item.ContentID, err)
		}
		r.State.RemovePendingDelete(item.Kind, item.ContentID)
		cleaned++
		if err := r.Options.Save(*r.State); err != nil {
			return cleaned, fmt.Errorf("保存待删除清理结果失败: %w", err)
		}
		fmt.Fprintf(r.Options.Output, "待清理%s：已删除（%s）\n", pendingKindName(item.Kind), item.ContentID)
	}
	return cleaned, nil
}

func pendingKindName(kind string) string {
	if kind == "comment" {
		return "评论"
	}
	if kind == "repost" {
		return "转发"
	}
	return "内容"
}

func (r *Runner) freshTopic(ctx context.Context, topicID string) (weibo.Topic, error) {
	topics, err := r.API.FollowedTopics(ctx, r.Config.MaxFollowedPages)
	if err != nil {
		return weibo.Topic{}, err
	}
	for _, topic := range topics {
		if topic.ID == topicID {
			return topic, nil
		}
	}
	return weibo.Topic{}, errors.New("刷新后的关注列表中未找到该超话")
}

func filterTopics(topics []weibo.Topic, selected app.TopicSelections) []weibo.Topic {
	if len(selected) == 0 {
		return topics
	}
	result := make([]weibo.Topic, 0, len(topics))
	for _, topic := range topics {
		if selected.Includes(topic.ID) {
			result = append(result, topic)
		}
	}
	return result
}

func (r *Runner) delay(ctx context.Context, low, high int) error {
	if high < low {
		high = low
	}
	seconds := low
	if high > low {
		seconds += r.Options.Random.Intn(high - low + 1)
	}
	if seconds <= 0 {
		return nil
	}
	return r.Options.Sleep(ctx, time.Duration(seconds)*time.Second)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
