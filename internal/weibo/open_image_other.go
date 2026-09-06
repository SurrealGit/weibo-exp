//go:build !windows

package weibo

import (
	"os/exec"
	"runtime"
)

func OpenImage(path string) error {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	return exec.Command(command, path).Start()
}
