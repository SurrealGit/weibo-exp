package install

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"unicode/utf16"

	"github.com/SurrealGit/weibo-exp/internal/storage"
)

// The system PowerShell process waits for this process to exit. The script is
// passed in memory, so it does not leave a helper executable or batch file.
func WindowsCleanupScript(r Record, files, dirs []string, pid int) (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	for _, path := range append(append([]string{}, files...), dirs...) {
		if _, err := CleanPath(path); err != nil {
			return "", err
		}
	}
	payload, err := json.Marshal(struct {
		Executable string
		MutexName  string
		Hash       string
		Files      []string
		Dirs       []string
	}{r.Executable, storage.DirectoryMutexName(r.DataDir), r.SHA256, files, CleanupDirs(dirs)})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$p = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s')) | ConvertFrom-Json
$guard = $null
$owned = $false
try {
  Wait-Process -Id %d -Timeout 30 -ErrorAction SilentlyContinue
  if (Get-Process -Id %d -ErrorAction SilentlyContinue) { throw '主程序仍运行，未删除程序和安装记录' }
  $guard = [Threading.Mutex]::new($false, $p.MutexName)
  try { $owned = $guard.WaitOne(0) } catch [Threading.AbandonedMutexException] { $owned = $true }
  if (-not $owned) { throw '数据目录正在使用，最终清理已停止；请结束其他命令后重试卸载' }
  foreach ($f in $p.Files) {
    if (Test-Path -LiteralPath $f) {
      $item = Get-Item -LiteralPath $f -Force
      if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw "拒绝删除非普通文件：$f" }
    }
  }
  if (Test-Path -LiteralPath $p.Executable) {
    if ((Get-FileHash -LiteralPath $p.Executable -Algorithm SHA256).Hash -ne $p.Hash) { throw '程序校验不符，拒绝删除' }
    Remove-Item -LiteralPath $p.Executable -Force
  }
  foreach ($f in $p.Files) {
    if (Test-Path -LiteralPath $f) { Remove-Item -LiteralPath $f -Force }
  }
  foreach ($d in $p.Dirs) {
    if (Test-Path -LiteralPath $d) {
      $item = Get-Item -LiteralPath $d -Force
      if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -or $d -eq $env:USERPROFILE -or $d -eq [IO.Path]::GetPathRoot($d)) { throw "拒绝清理目录：$d" }
      if (@(Get-ChildItem -LiteralPath $d -Force).Count -eq 0) { Remove-Item -LiteralPath $d }
      else { Write-Output "保留非空目录：$d" }
    }
  }
  Write-Output '后台卸载清理完成。下载包和其他副本不在清理范围内。'
} catch { Write-Error "卸载未完成：$_"; exit 1 }
finally { if ($owned) { $guard.ReleaseMutex() }; if ($null -ne $guard) { $guard.Dispose() } }
`, base64.StdEncoding.EncodeToString(payload), pid, pid), nil
}

func StartWindowsCleanup(r Record, files, dirs []string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("此清理入口仅适用于 Windows")
	}
	script, err := WindowsCleanupScript(r, files, dirs, os.Getpid())
	if err != nil {
		return err
	}
	units := utf16.Encode([]rune(script))
	data := make([]byte, len(units)*2)
	for i, v := range units {
		binary.LittleEndian.PutUint16(data[i*2:], v)
	}
	command := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-OutputFormat", "Text", "-EncodedCommand", base64.StdEncoding.EncodeToString(data))
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
