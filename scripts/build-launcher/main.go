// Build the helper before compiling the Windows CLI that embeds it.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	output := flag.String("output", "internal/schedule/launcher/weibo-exp-launcher.exe", "output file")
	flag.Parse()
	if err := build(*output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func build(output string) error {
	abs, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-ldflags=-s -w -H=windowsgui", "-o", abs, "github.com/SurrealGit/weibo-exp/cmd/weibo-exp-launcher")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GOOS=") && !strings.HasPrefix(value, "GOARCH=") && !strings.HasPrefix(value, "CGO_ENABLED=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
