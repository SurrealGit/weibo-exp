package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"
	"text/tabwriter"
)

// No command accepts positional arguments after its flags.
func parseFlags(flags *flag.FlagSet, args []string) error {
	if err := parseFlagSet(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%s 不接受额外参数：%q\n\n%s", flags.Name(), flags.Args(), flagUsage(flags))
	}
	return nil
}

// Keep flag's parsing semantics, but format errors once at the CLI boundary.
func parseFlagSet(flags *flag.FlagSet, args []string) error {
	output, usage := flags.Output(), flags.Usage
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	err := flags.Parse(args)
	flags.SetOutput(output)
	flags.Usage = usage
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(output, flagUsage(flags))
		return err
	}
	if err == nil {
		return nil
	}
	message := err.Error()
	switch {
	case strings.HasPrefix(message, "flag provided but not defined: "):
		message = "未知参数 --" + strings.TrimLeft(strings.TrimPrefix(message, "flag provided but not defined: "), "-")
	case strings.HasPrefix(message, "flag needs an argument: "):
		message = "参数 --" + strings.TrimLeft(strings.TrimPrefix(message, "flag needs an argument: "), "-") + " 缺少值"
	case strings.HasPrefix(message, "bad flag syntax: "):
		message = "参数格式错误：" + strings.TrimPrefix(message, "bad flag syntax: ")
	default:
		if match := invalidFlagValue.FindStringSubmatch(message); match != nil {
			message = fmt.Sprintf("参数 --%s 的值无效：%s", match[2], match[1])
		} else {
			message = "参数解析失败：" + message
		}
	}
	return fmt.Errorf("%s\n\n%s", message, flagUsage(flags))
}

var invalidFlagValue = regexp.MustCompile(`^invalid (?:boolean )?value (".*") for (?:flag )?-([^: ]+):`)

func flagUsage(flags *flag.FlagSet) string {
	var out bytes.Buffer
	command := "weibo-exp " + flags.Name()
	if flags.Name() == "weibo-exp" {
		command = "weibo-exp"
	}
	fmt.Fprintf(&out, "用法：%s [选项]", command)
	if flags.Name() == "weibo-exp" {
		fmt.Fprint(&out, " <命令> [命令选项]")
	}
	fmt.Fprintln(&out)
	count := 0
	flags.VisitAll(func(*flag.Flag) { count++ })
	if count > 0 {
		fmt.Fprintln(&out, "\n选项：")
		columns := tabwriter.NewWriter(&out, 0, 0, 2, ' ', 0)
		flags.VisitAll(func(f *flag.Flag) {
			name, description := flag.UnquoteUsage(f)
			if name != "" {
				name = " " + name
			}
			fmt.Fprintf(columns, "  --%s%s\t%s\n", f.Name, name, description)
		})
		_ = columns.Flush()
	}
	return out.String()
}

func noArguments(name string, args []string, output io.Writer) error {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(output)
	return parseFlags(flags, args)
}
