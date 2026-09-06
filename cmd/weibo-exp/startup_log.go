package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/logging"
)

// Caller holds the task lock. Invalid configuration must not suppress its own
// diagnostic; append only, without relying on retention settings.
func logScheduledStartupError(paths app.Paths, cause error) error {
	if err := os.MkdirAll(paths.LogDir, 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(paths.LogDir, logging.ErrorFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintln(newTimestampWriter(file, time.Now), "定时启动失败：", cause)
	return errors.Join(writeErr, file.Close())
}
