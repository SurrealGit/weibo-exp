package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/runner"
)

func cleanupCommand(paths app.Paths, args []string, input io.Reader, output, errorOutput io.Writer) error {
	if len(args) > 0 && args[0] != "confirm" {
		return noArguments("cleanup", args, errorOutput)
	}
	flags := flag.NewFlagSet("cleanup confirm", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	kind := flags.String("kind", "", "内容类型")
	id := flags.String("id", "", "已清理的内容 ID")
	post := flags.String("post", "", "缺少内容 ID 时使用原帖 ID")
	yes := flags.Bool("yes", false, "确认已完成人工核查，并移除待处理记录")
	if len(args) > 0 {
		if err := parseFlags(flags, args[1:]); err != nil {
			return err
		}
		if len(args) == 1 {
			return fmt.Errorf("未指定需要确认的记录，本次未作任何修改。\n执行 %s 查看详情和完整确认命令。", commandFor(paths, "cleanup"))
		}
		if (*kind != "comment" && *kind != "repost") || (*id == "") == (*post == "") {
			return fmt.Errorf("未正确指定需要确认的记录，本次未作任何修改。\n执行 %s 查看详情和完整确认命令。", commandFor(paths, "cleanup"))
		}
	}
	release, err := runner.AcquireLock(paths.Lock)
	if err != nil {
		return err
	}
	defer release()
	state, err := app.LoadState(paths.State)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		if len(state.PendingDeletes) == 0 {
			fmt.Fprintln(output, "暂无待清理内容，无需操作。")
			return nil
		}
		fmt.Fprintf(output, "待处理内容：%d 条\n", len(state.PendingDeletes))
		for i, item := range state.PendingDeletes {
			id := item.ContentID
			label := "评论"
			if item.Kind == "repost" {
				label = "转发"
			}
			description := "待重试删除"
			selector := "--id " + commandArg(id)
			if id == "" {
				id = "未知"
				description = "发布结果未知，需要人工核查"
				selector = "--post " + commandArg(item.SourcePostID)
			}
			fmt.Fprintf(output, "\n[%d] %s：%s\n", i+1, label, description)
			if !item.CreatedAt.IsZero() {
				fmt.Fprintf(output, "记录时间：%s\n", item.CreatedAt.Local().Format("2006-01-02 15:04:05"))
			}
			fmt.Fprintf(output, "原帖 ID：%s\n%s ID：%s\n", item.SourcePostID, label, id)
			fmt.Fprintf(output, "\n核查后，执行以下命令移除待处理记录：\n%s\n", commandFor(paths, "cleanup confirm --kind "+commandArg(item.Kind)+" "+selector))
		}
		fmt.Fprintln(output, "\n请在微博 App 中检查对应内容。")
		if len(state.PendingDeletes) == 1 && state.PendingDeletes[0].Kind == "repost" {
			fmt.Fprintln(output, "若存在，可在微博 App 中手动删除所转发的微博。")
		} else {
			fmt.Fprintln(output, "若存在，可在微博 App 中手动删除对应评论或所转发的微博。")
		}
		fmt.Fprintln(output, "以上命令只移除本地记录，不会删除微博内容。")
		_, retry := pendingCounts(state)
		if retry > 0 {
			fmt.Fprintf(output, "\n有内容 ID 的记录可自动重试删除；需要人工核查的记录处理后，\n可等待定时执行，或执行 %s 立即重试。\n", commandFor(paths, "run"))
		}
		return nil
	}
	index := -1
	for i, item := range state.PendingDeletes {
		if item.Kind == *kind && ((*id != "" && item.ContentID == *id) || (*post != "" && item.ContentID == "" && item.SourcePostID == *post)) {
			if index >= 0 {
				return errors.New("匹配到多条记录，无法唯一确认")
			}
			index = i
		}
	}
	if index < 0 {
		return errors.New("未找到匹配的待清理记录")
	}
	if !*yes {
		fmt.Fprintln(output, "移除记录后，将不再跟进这条内容的清理。")
		fmt.Fprint(output, "确认已完成人工核查后，输入 yes 继续：")
		answer, _ := bufio.NewReader(input).ReadString('\n')
		if strings.TrimSpace(answer) != "yes" {
			return errors.New("用户取消")
		}
	}
	state.PendingDeletes = append(state.PendingDeletes[:index], state.PendingDeletes[index+1:]...)
	if err := app.SaveState(paths.State, state); err != nil {
		return err
	}
	fmt.Fprintln(output, "已移除指定待处理记录，互动进度保留。")
	if len(state.PendingDeletes) == 0 {
		fmt.Fprintf(output, "暂无其他待处理记录。\n\n可以等待下次定时任务重新检查，\n也可执行 %s 立即检查。\n", commandFor(paths, "run"))
	} else {
		manual, _ := pendingCounts(state)
		fmt.Fprintf(output, "剩余待处理内容：%d 条，其中 %d 条需要人工核查。\n执行 %s 查看详情和处理命令。\n", len(state.PendingDeletes), manual, commandFor(paths, "cleanup"))
	}
	return nil
}
