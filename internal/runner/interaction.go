package runner

import (
	"context"
	"errors"
	"fmt"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

func (r *Runner) checkDate(date string) error {
	if app.Today(r.Options.Clock()) != date {
		return errors.New("任务执行期间日期已改变；已停止创建新任务，请重新运行以检查当天进度")
	}
	return nil
}

func (r *Runner) deleteContent(ctx context.Context, kind, id, st string) error {
	switch kind {
	case "comment":
		return r.API.DeleteComment(ctx, id, st)
	case "repost":
		return r.API.DeleteRepost(ctx, id, st)
	default:
		return fmt.Errorf("未知内容类型 %q", kind)
	}
}

func (r *Runner) interact(ctx context.Context, topicID string, post weibo.Post, kind, date, st string, index, total int, summary *Summary) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Persist intent before a request can reach Weibo. An interrupted or ambiguous
	// request remains blocked for manual inspection, without claiming success.
	item := app.PendingDelete{Kind: kind, TopicID: topicID, SourcePostID: post.MID, CreatedAt: r.Options.Clock()}
	r.State.AddPendingDelete(item)
	pendingIndex := len(r.State.PendingDeletes) - 1
	if err := r.Options.Save(*r.State); err != nil {
		r.State.PendingDeletes = r.State.PendingDeletes[:pendingIndex]
		return fmt.Errorf("保存创建前记录失败，尚未发送请求: %w", err)
	}
	var id string
	var err error
	content := ""
	if kind == "comment" {
		content = chooseTemplate(post.MID, date+":"+topicID, r.Config.CommentTemplates)
		id, err = r.API.Comment(ctx, post.MID, content, st)
	} else {
		id, err = r.API.Repost(ctx, post.MID, st)
	}
	if err != nil {
		var rejected *weibo.RejectedError
		if errors.As(err, &rejected) {
			r.State.PendingDeletes = r.State.PendingDeletes[:pendingIndex]
			return errors.Join(fmt.Errorf("%s未创建（原帖 %s）: %w", pendingKindName(kind), post.MID, err), r.Options.Save(*r.State))
		}
		return fmt.Errorf("%s创建结果无法确认（原帖 %s）；已保留待检查记录，执行 cleanup 查看，请勿重复发布: %w", pendingKindName(kind), post.MID, err)
	}
	if kind == "comment" {
		summary.Comments++
	} else {
		summary.Reposts++
	}
	// Record against the actual creation day even if the response crossed midnight.
	r.State.Mark(app.Today(r.Options.Clock()), topicID, kind, post.MID)
	item.ContentID = id
	r.State.PendingDeletes[pendingIndex] = item
	label := fmt.Sprintf("%s [%d/%d]", pendingKindName(kind), index, total)
	fmt.Fprintf(r.Options.Output, "  %s：已创建（原帖 %s，内容 %s，新内容 %s）\n", label, post.MID, content, id)
	if err := r.Options.Save(*r.State); err != nil {
		var cleanupErr error
		if id != "" {
			cleanupErr = r.deleteContent(ctx, kind, id, st)
			if cleanupErr == nil {
				summary.Deleted++
				r.State.RemovePendingDelete(kind, id)
			} else {
				cleanupErr = fmt.Errorf("补偿删除失败，请人工检查内容 %s: %w", id, cleanupErr)
			}
		}
		return errors.Join(fmt.Errorf("保存待清理记录失败（%s，原帖 %s，内容 ID %s）；请人工检查: %w", kind, post.MID, id, err), cleanupErr)
	}
	if id == "" {
		return fmt.Errorf("%s已提交但缺少内容 ID；已持久化待人工检查记录，执行 cleanup 查看", pendingKindName(kind))
	}
	if err := r.delay(ctx, r.Config.VisibleDelayMin, r.Config.VisibleDelayMax); err != nil {
		return err
	}
	if err := r.deleteContent(ctx, kind, id, st); err != nil {
		return fmt.Errorf("删除%s %s 失败，队列已停止: %w", pendingKindName(kind), id, err)
	}
	summary.Deleted++
	r.State.RemovePendingDelete(kind, id)
	if err := r.Options.Save(*r.State); err != nil {
		return fmt.Errorf("保存清理结果失败（%s %s）: %w", kind, id, err)
	}
	fmt.Fprintf(r.Options.Output, "  %s：已删除（%s）\n", label, id)
	return r.delay(ctx, r.Config.ActionDelayMin, r.Config.ActionDelayMax)
}
