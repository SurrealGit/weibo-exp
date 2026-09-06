package logging

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	OutputFile = "scheduled.log"
	ErrorFile  = "scheduled-error.log"
)

type PruneResult struct {
	RemovedLines int
	KeptLines    int
}

func Paths(logDir string) []string {
	return []string{
		filepath.Join(logDir, OutputFile),
		filepath.Join(logDir, ErrorFile),
	}
}

func Tail(path string, lineCount int) ([]byte, error) {
	if lineCount < 1 {
		return nil, errors.New("日志行数必须大于 0")
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	const blockSize int64 = 32 * 1024
	position := info.Size()
	var data []byte
	for position > 0 && bytes.Count(data, []byte("\n")) <= lineCount {
		readSize := min(blockSize, position)
		position -= readSize
		block := make([]byte, readSize)
		if _, err := file.ReadAt(block, position); err != nil {
			return nil, err
		}
		data = append(block, data...)
	}
	lines := splitLines(data)
	if len(lines) > lineCount {
		lines = lines[len(lines)-lineCount:]
	}
	return joinLines(lines), nil
}

func Clear(logDir string) error {
	for _, path := range Paths(logDir) {
		if err := truncateExisting(path); err != nil {
			return fmt.Errorf("清空 %s: %w", path, err)
		}
	}
	return nil
}

func Prune(logDir string, retentionDays int, now time.Time) (PruneResult, error) {
	if retentionDays < 0 {
		return PruneResult{}, errors.New("日志保留天数不能为负数")
	}
	if retentionDays == 0 {
		return PruneResult{}, nil
	}
	cutoff := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)
	var total PruneResult
	for _, path := range Paths(logDir) {
		result, err := pruneFile(path, cutoff, now.Location())
		if err != nil {
			return total, fmt.Errorf("清理 %s: %w", path, err)
		}
		total.RemovedLines += result.RemovedLines
		total.KeptLines += result.KeptLines
	}
	return total, nil
}

func pruneFile(path string, cutoff time.Time, location *time.Location) (PruneResult, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return PruneResult{}, nil
	}
	if err != nil {
		return PruneResult{}, err
	}
	defer file.Close()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".log-prune-*")
	if err != nil {
		return PruneResult{}, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	reader := bufio.NewReader(file)
	writer := bufio.NewWriter(tmp)
	result := PruneResult{}
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			when, ok := lineTime(line, location)
			if ok && when.Before(cutoff) {
				result.RemovedLines++
			} else {
				if _, err := writer.Write(line); err != nil {
					return PruneResult{}, err
				}
				result.KeptLines++
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return PruneResult{}, readErr
		}
	}
	if result.RemovedLines == 0 {
		return result, nil
	}
	if err := writer.Flush(); err != nil {
		return PruneResult{}, err
	}
	if err := tmp.Sync(); err != nil {
		return PruneResult{}, err
	}
	if err := tmp.Close(); err != nil {
		return PruneResult{}, err
	}
	if err := file.Close(); err != nil {
		return PruneResult{}, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return PruneResult{}, err
	}
	return result, nil
}

func lineTime(line []byte, location *time.Location) (time.Time, bool) {
	const layout = "[2006-01-02 15:04:05]"
	if len(line) < len(layout) {
		return time.Time{}, false
	}
	value, err := time.ParseInLocation(layout, string(line[:len(layout)]), location)
	return value, err == nil
}

func splitLines(data []byte) [][]byte {
	data = bytes.TrimSuffix(data, []byte("\n"))
	if len(data) == 0 {
		return nil
	}
	return bytes.Split(data, []byte("\n"))
}

func joinLines(lines [][]byte) []byte {
	if len(lines) == 0 {
		return nil
	}
	return append(bytes.Join(lines, []byte("\n")), '\n')
}

func truncateExisting(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return file.Close()
}
