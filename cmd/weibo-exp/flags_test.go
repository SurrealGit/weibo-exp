package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestFlagErrorsAreLocalizedAndPrintedOnce(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--dry-rum"}, "未知参数 --dry-rum"},
		{[]string{"-dry-rum"}, "未知参数 --dry-rum"},
		{[]string{"--lines"}, "参数 --lines 缺少值"},
		{[]string{"--lines=bad"}, `参数 --lines 的值无效："bad"`},
		{[]string{"--dry-run=bad"}, `参数 --dry-run 的值无效："bad"`},
		{[]string{"---bad"}, "参数格式错误：---bad"},
		{[]string{"extra"}, "run 不接受额外参数"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			f := flag.NewFlagSet("run", flag.ContinueOnError)
			var output bytes.Buffer
			f.SetOutput(&output)
			f.Bool("dry-run", false, "只生成计划")
			f.Int("lines", 20, "显示最近 `N` 行")
			err := parseFlags(f, tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "用法：weibo-exp run [选项]") {
				t.Fatalf("unexpected error: %v", err)
			}
			if output.Len() != 0 {
				t.Fatalf("error printed before CLI boundary: %s", &output)
			}
			if strings.Contains(err.Error(), "Usage of") || strings.Contains(err.Error(), "flag provided") || !strings.Contains(err.Error(), "--lines N") {
				t.Fatal(err)
			}
		})
	}
}

func TestFlagsKeepAliasesAndHelp(t *testing.T) {
	for _, arg := range []string{"-dry-run", "--dry-run"} {
		f := flag.NewFlagSet("run", flag.ContinueOnError)
		value := f.Bool("dry-run", false, "预演")
		if err := parseFlags(f, []string{arg}); err != nil || !*value {
			t.Fatalf("%s: %v", arg, err)
		}
	}
	f := flag.NewFlagSet("status", flag.ContinueOnError)
	f.Bool("json", false, "输出 JSON")
	var output bytes.Buffer
	f.SetOutput(&output)
	if err := parseFlags(f, []string{"--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "--json") || strings.Count(output.String(), "用法：") != 1 {
		t.Fatal(output.String())
	}
}

func TestGlobalFlagsKeepCommandArguments(t *testing.T) {
	f := flag.NewFlagSet("weibo-exp", flag.ContinueOnError)
	f.String("data-dir", "", "数据目录")
	if err := parseFlagSet(f, []string{"--data-dir", "test", "run", "--dry-run"}); err != nil || strings.Join(f.Args(), " ") != "run --dry-run" {
		t.Fatalf("lost command arguments: %v %v", f.Args(), err)
	}
}
