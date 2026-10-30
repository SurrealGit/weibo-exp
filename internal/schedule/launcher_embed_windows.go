package schedule

import "embed"

//go:embed launcher/*
var launcherFiles embed.FS

func embeddedLauncher() ([]byte, error) {
	return launcherFiles.ReadFile("launcher/weibo-exp-launcher.exe")
}
