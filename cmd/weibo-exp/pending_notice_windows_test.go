package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
)

// The child only reports arguments; it never executes a CLI command or reads data.
func TestRecoveryCommandArgumentProcess(t *testing.T) {
	if os.Getenv("WEIBO_EXP_TEST_COMMAND_ARGS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			_ = json.NewEncoder(os.Stdout).Encode(os.Args[i+1:])
			os.Exit(0)
		}
	}
	os.Exit(2)
}

func TestRecoveryCommandsInPowerShell(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		t.Run(shell, func(t *testing.T) {
			binary, err := exec.LookPath(shell)
			if err != nil {
				t.Skip("PowerShell not installed")
			}
			for _, dataDir := range []string{
				`C:\Tasks\自动签到\data`, `C:\User's Data\data`,
				"C:\\Tasks\\$daily\\$(Get-Date)\\`data\\%TEMP% & !data",
				`C:\Task Data\`, `C:\`, `\\server\share name\`,
			} {
				t.Run(dataDir, func(t *testing.T) {
					paths := app.Paths{DataDir: dataDir}
					// Forward the parsed arguments to a native Go process as well,
					// exercising PowerShell's native argument serialization.
					script := "[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false); " +
						"function weibo-exp { & " + commandArgForOS(executable, "windows") +
						" -test.run=^TestRecoveryCommandArgumentProcess$ -- @args }; " + commandFor(paths, "cleanup")
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, binary, "-NoProfile", "-NonInteractive", "-Command", script)
					cmd.Env = append(os.Environ(), "WEIBO_EXP_TEST_COMMAND_ARGS=1")
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("%v: %s", err, out)
					}
					var got []string
					if err := json.Unmarshal(bytes.TrimSpace(out), &got); err != nil {
						t.Fatalf("%v: %s", err, out)
					}
					if len(got) != 3 || got[0] != "--data-dir" || got[2] != "cleanup" || filepath.Clean(got[1]) != filepath.Clean(dataDir) {
						t.Fatalf("arguments changed: %q (data directory %q)", got, dataDir)
					}
					// Do not accidentally attach explanatory text to a copyable line.
					instruction := commandInstruction(paths, "cleanup", "查看详情")
					if !reflect.DeepEqual(strings.Split(instruction, "\n"), []string{"请在 PowerShell 中执行以下命令查看详情：", commandFor(paths, "cleanup")}) {
						t.Fatal(instruction)
					}
				})
			}
		})
	}
}
