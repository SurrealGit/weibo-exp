package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/runner"
	"github.com/SurrealGit/weibo-exp/internal/session"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

func runCommand(ctx context.Context, paths app.Paths, cfg app.Config, client *weibo.Client, args []string, input io.Reader, output, errorOutput io.Writer) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	dryRun := flags.Bool("dry-run", false, "只生成计划，不发送写请求")
	yes := flags.Bool("yes", false, "跳过本次执行确认")
	ifDue := flags.Bool("if-due", false, "仅在已达设定时间且今日未完成时执行")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if !*dryRun && !*yes {
		fmt.Fprintln(output, "即将执行实际写操作：")
		fmt.Fprintln(output, "  - 为未签到的超话执行签到")
		fmt.Fprintln(output, "  - 评论和转发将短暂公开，随后请求删除")
		fmt.Fprintln(output, "  - 任一删除失败时立即停止整个队列")
		fmt.Fprint(output, "输入 yes 继续：")
		answer, _ := bufio.NewReader(input).ReadString('\n')
		if strings.TrimSpace(answer) != "yes" {
			return errors.New("用户取消")
		}
	}
	release, err := runner.AcquireLock(paths.Lock)
	if err != nil {
		return err
	}
	defer release()
	if paths.Config != "" {
		cfg, err = app.LoadConfig(paths.Config)
		if err != nil {
			return err
		}
	}
	return executeRun(ctx, paths, cfg, client, runner.Options{DryRun: *dryRun, IfDue: *ifDue, Clock: time.Now, Output: output})
}

func executeRun(ctx context.Context, paths app.Paths, cfg app.Config, client *weibo.Client, options runner.Options) (resultErr error) {
	state, err := app.LoadState(paths.State)
	if err != nil {
		return err
	}
	if paths.Session != "" {
		jar, err := session.Load(paths.Session)
		if err != nil {
			return err
		}
		client.HTTP.Jar = jar
		if !options.DryRun {
			defer func() { resultErr = errors.Join(resultErr, session.Save(paths.Session, jar)) }()
		}
	}
	options.Save = func(value app.State) error { return app.SaveState(paths.State, value) }
	taskRunner := runner.Runner{API: client, Config: cfg, State: &state, Options: options}
	summary, err := taskRunner.Run(ctx)
	if !options.IfDue || summary.Comments+summary.Reposts+summary.Deleted+summary.Checkins > 0 || err != nil {
		printRunSummary(options.Output, summary, options.DryRun, err)
	}
	return err
}

func scheduledCommand(ctx context.Context, paths app.Paths, baseURL string) error {
	release, err := runner.AcquireLock(paths.Lock)
	if err != nil {
		return err
	}
	defer release()
	cfg, err := app.LoadConfig(paths.Config)
	if err != nil {
		return errors.Join(err, logScheduledStartupError(paths, err))
	}
	output, errorOutput, closeLogs, err := openScheduledLogs(paths, cfg)
	if err != nil {
		return err
	}
	defer closeLogs()
	client := weibo.NewClient(baseURL, &http.Client{Timeout: 30 * time.Second})
	err = executeRun(ctx, paths, cfg, client, runner.Options{IfDue: true, Clock: time.Now, Output: output})
	if err != nil {
		fmt.Fprintln(errorOutput, "操作失败：", err)
	}
	return err
}

func printRunSummary(output io.Writer, summary runner.Summary, dryRun bool, runErr error) {
	if dryRun {
		heading := "预演汇总（未执行任何微博写操作）："
		if runErr != nil {
			heading = "预演汇总（存在失败或未完成项；未执行任何微博写操作）："
		}
		fmt.Fprintln(output, "\n"+heading)
		fmt.Fprintf(output, "  检查超话：%d\n", summary.Topics)
		fmt.Fprintf(output, "  预计签到：%d\n", summary.Checkins)
		fmt.Fprintf(output, "  预计评论：%d\n", summary.Comments)
		fmt.Fprintf(output, "  预计转发：%d\n", summary.Reposts)
		fmt.Fprintf(output, "  预计删除：%d（评论和转发创建后各删除一次）\n", summary.Comments+summary.Reposts)
		fmt.Fprintf(output, "  跳过超话：%d\n", summary.SkippedTopics)
		return
	}
	heading := "执行汇总（仅统计已实际成功的操作）："
	if runErr != nil {
		heading = "执行汇总（存在失败或未完成项，仅统计已实际成功的操作）："
	}
	fmt.Fprintln(output, "\n"+heading)
	fmt.Fprintf(output, "  检查超话：%d\n", summary.Topics)
	fmt.Fprintf(output, "  签到成功：%d\n", summary.Checkins)
	fmt.Fprintf(output, "  评论成功：%d\n", summary.Comments)
	fmt.Fprintf(output, "  转发成功：%d\n", summary.Reposts)
	fmt.Fprintf(output, "  删除成功：%d\n", summary.Deleted)
	fmt.Fprintf(output, "  跳过超话：%d\n", summary.SkippedTopics)
}
