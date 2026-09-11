package runner

import (
	"context"
	"errors"
	"fmt"

	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

// Returned only after a definite refusal and successful removal of the saved
// creation intent. Persistence/deletion errors must never permit fallback.
type postRestrictionError struct{ error }

func (r *Runner) runInteractions(ctx context.Context, topicID, date string, status weibo.LoginStatus, posts []weibo.Post, completed []string, feed *weibo.PostFeed, comments, reposts int, summary *Summary) (string, error) {
	excluded := append([]string(nil), completed...)
	queue := selectPosts(posts, excluded, len(posts))
	// Keep the initially allocated repost candidates available even when every
	// comment attempt is refused. Fallback consumes only unallocated candidates.
	commentEnd := min(comments, len(queue))
	repostEnd := min(commentEnd+reposts, len(queue))
	reservedReposts := append([]weibo.Post(nil), queue[commentEnd:repostEnd]...)
	queue = append(queue[:commentEnd:commentEnd], queue[repostEnd:]...)
	for _, post := range reservedReposts {
		excluded = append(excluded, post.MID)
	}
	remaining := []int{comments, reposts}
	exhausted := false
	for group, kind := range []string{"comment", "repost"} {
		if kind == "repost" {
			queue = append(reservedReposts, queue...)
		}
		target := remaining[group]
		for remaining[group] > 0 {
			if err := r.checkDate(date); err != nil {
				return "", err
			}
			if len(queue) == 0 && !exhausted {
				if err := r.delay(ctx, r.Config.ActionDelayMin, r.Config.ActionDelayMax); err != nil {
					return "", err
				}
				var err error
				posts, err = r.API.TopicPosts(ctx, topicID, status.UID, r.Config.MaxFeedPages,
					max(r.Config.MaxPostsPerTopic, r.Config.CommentLimit+r.Config.RepostLimit), excluded, feed)
				if err != nil {
					return "", fmt.Errorf("补充候选帖子失败，队列已停止: %w", err)
				}
				queue = selectPosts(posts, excluded, len(posts))
				exhausted = len(queue) == 0
				if !exhausted {
					fmt.Fprintf(r.Options.Output, "  补充候选：%d 条；继续处理剩余任务。\n", len(queue))
				}
			}
			if len(queue) == 0 {
				break
			}
			if err := r.checkDate(date); err != nil {
				return "", err
			}
			post := queue[0]
			queue = queue[1:]
			excluded = append(excluded, post.MID)
			err := r.interact(ctx, topicID, post, kind, date, status.ST, target-remaining[group]+1, target, summary)
			if err != nil {
				var restricted *postRestrictionError
				if !errors.As(err, &restricted) {
					return "", err
				}
				fmt.Fprintf(r.Options.Output, "  评论已跳过（原帖 %s）：该帖限制评论；将尝试其他候选。\n", post.MID)
				if err := r.delay(ctx, r.Config.ActionDelayMin, r.Config.ActionDelayMax); err != nil {
					return "", err
				}
				continue
			}
			remaining[group]--
		}
	}
	if remaining[0]+remaining[1] > 0 {
		message := fmt.Sprintf("可用候选已耗尽，仍需评论 %d 条、转发 %d 条", remaining[0], remaining[1])
		fmt.Fprintf(r.Options.Output, "  本超话未完成：%s；已无更多内容或达到分页上限（最多 %d 页）。可通过 config set --max-feed-pages 调整上限；继续检查其他超话。\n", message, r.Config.MaxFeedPages)
		return message, nil
	}
	return "", nil
}
