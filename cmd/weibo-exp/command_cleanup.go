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
	yes := flags.Bool("yes", false, "确认已人工核验内容不存在")
	if len(args) > 0 {
		if err := parseFlags(flags, args[1:]); err != nil {
			return err
		}
		if (*kind != "comment" && *kind != "repost") || (*id == "") == (*post == "") {
			return errors.New("必须指定 kind，并且只提供 id 或 post 之一")
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
		fmt.Fprintf(output, "待清理内容：%d 条\n", len(state.PendingDeletes))
		for _, item := range state.PendingDeletes {
			id := item.ContentID
			if id == "" {
				id = "未知，需人工检查"
			}
			fmt.Fprintf(output, "  类型 %s；内容 ID %s；原帖 %s；超话 %s\n", item.Kind, id, item.SourcePostID, item.TopicID)
		}
		fmt.Fprintln(output, "仅在微博中确认内容已不存在后，执行 cleanup confirm --kind comment|repost --post 原帖ID（或 --id 内容ID）。")
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
		fmt.Fprint(output, "这不会删除微博内容。请先人工确认该内容已不存在，再输入 yes 清除这条待清理记录：")
		answer, _ := bufio.NewReader(input).ReadString('\n')
		if strings.TrimSpace(answer) != "yes" {
			return errors.New("用户取消")
		}
	}
	state.PendingDeletes = append(state.PendingDeletes[:index], state.PendingDeletes[index+1:]...)
	if err := app.SaveState(paths.State, state); err != nil {
		return err
	}
	fmt.Fprintln(output, "已确认并移除指定待清理记录；互动进度保留。")
	return nil
}
