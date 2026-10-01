package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/SurrealGit/weibo-exp/internal/app"
)

func pendingCounts(state app.State) (manual, retry int) {
	for _, item := range state.PendingDeletes {
		if item.ContentID == "" {
			manual++
		} else {
			retry++
		}
	}
	return
}

var simpleCommandArg = regexp.MustCompile(`^[a-zA-Z0-9_./:-]+$`)

func commandArg(value string) string {
	if simpleCommandArg.MatchString(value) {
		return value
	}
	if runtime.GOOS == "windows" {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func commandFor(paths app.Paths, command string) string {
	prefix := "weibo-exp"
	defaultDir, err := os.UserConfigDir()
	if paths.DataDir != "" && (err != nil || filepath.Clean(paths.DataDir) != filepath.Join(defaultDir, "weibo-exp")) {
		prefix += " --data-dir " + commandArg(paths.DataDir)
	}
	return prefix + " " + command
}

func pendingNotice(paths app.Paths, state app.State) string {
	manual, retry := pendingCounts(state)
	if manual > 0 {
		comments, reposts := 0, 0
		for _, item := range state.PendingDeletes {
			if item.ContentID != "" {
				continue
			}
			if item.Kind == "comment" {
				comments++
			} else if item.Kind == "repost" {
				reposts++
			}
		}
		what := fmt.Sprintf("%d 条内容", manual)
		if comments == manual {
			what = fmt.Sprintf("%d 条评论", manual)
		}
		if reposts == manual {
			what = fmt.Sprintf("%d 条转发", manual)
		}
		return fmt.Sprintf("注意：任务执行受阻，有 %s需要人工核查。\n执行 %s 查看详情和处理命令。", what, commandFor(paths, "cleanup"))
	}
	if retry > 0 {
		return fmt.Sprintf("提醒：有 %d 条待清理内容，下次定时任务会先重试删除。\n也可执行 %s 立即重试，或执行 %s 查看详情。", retry, commandFor(paths, "run"), commandFor(paths, "cleanup"))
	}
	return ""
}

func printPendingNotice(output io.Writer, paths app.Paths, state app.State) {
	if notice := pendingNotice(paths, state); notice != "" {
		fmt.Fprintln(output, "\n"+notice)
	}
}

func printStoredPendingNotice(output io.Writer, paths app.Paths) {
	state, err := app.LoadState(paths.State)
	if err != nil {
		fmt.Fprintln(output, "提醒：无法读取任务记录：", err)
		return
	}
	printPendingNotice(output, paths, state)
}

type manualCheckError struct{ notice string }

func (e *manualCheckError) Error() string { return e.notice }

func checkManualPending(output io.Writer, paths app.Paths, state app.State, dryRun bool) error {
	manual, _ := pendingCounts(state)
	if manual == 0 {
		return nil
	}
	if dryRun {
		fmt.Fprint(output, "只读预演：本次不会向微博发送签到、评论、转发或删除请求。\n\n")
	}
	notice := pendingNotice(paths, state)
	fmt.Fprintln(output, notice)
	if dryRun {
		fmt.Fprintln(output, "\n本次未生成任务计划。")
	} else {
		fmt.Fprintln(output, "\n本次未执行签到、评论、转发或删除。")
	}
	return &manualCheckError{notice: notice}
}
