package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/logging"
)

func logsCommand(paths app.Paths, args []string, input io.Reader, output, errorOutput io.Writer) error {
	if len(args) > 0 && args[0] == "clear" {
		flags := flag.NewFlagSet("logs clear", flag.ContinueOnError)
		flags.SetOutput(errorOutput)
		yes := flags.Bool("yes", false, "跳过清空确认")
		if err := parseFlags(flags, args[1:]); err != nil {
			return err
		}
		if !*yes {
			fmt.Fprintln(output, "即将清空定时任务的普通日志和错误日志；此操作无法撤销。")
			fmt.Fprint(output, "输入 yes 继续：")
			answer, _ := bufio.NewReader(input).ReadString('\n')
			if strings.TrimSpace(answer) != "yes" {
				return errors.New("用户取消")
			}
		}
		if err := logging.Clear(paths.LogDir); err != nil {
			return err
		}
		fmt.Fprintln(output, "日志清理：已清空普通日志和错误日志")
		fmt.Fprintf(output, "日志目录：%s\n", paths.LogDir)
		return nil
	}

	flags := flag.NewFlagSet("logs", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	lineCount := flags.Int("lines", 100, "每个日志文件显示的末尾行数")
	errorsOnly := flags.Bool("errors", false, "只显示错误日志")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *lineCount < 1 || *lineCount > 10000 {
		return errors.New("--lines 必须在 1–10000 之间")
	}
	if !*errorsOnly {
		if err := printLogTail(output, "定时任务日志", filepath.Join(paths.LogDir, logging.OutputFile), *lineCount); err != nil {
			return err
		}
	}
	return printLogTail(output, "定时任务错误日志", filepath.Join(paths.LogDir, logging.ErrorFile), *lineCount)
}

func printLogTail(output io.Writer, title, path string, lineCount int) error {
	data, err := logging.Tail(path, lineCount)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "%s（最近 %d 行）\n", title, lineCount)
	fmt.Fprintf(output, "文件：%s\n", path)
	if len(data) == 0 {
		fmt.Fprintln(output, "无记录")
	} else if _, err := output.Write(data); err != nil {
		return err
	}
	fmt.Fprintln(output)
	return nil
}
