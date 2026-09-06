//go:build !windows

package schedule

func decodeTaskText(data []byte) []byte { return data }
