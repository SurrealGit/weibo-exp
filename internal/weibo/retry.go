package weibo

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DeleteRetry is only for the same known content ID, never for creation.
func DeleteRetry(err error) (time.Duration, bool) {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, ErrNotLoggedIn) {
		return 0, false
	}
	var h *HTTPFailure
	if errors.As(err, &h) {
		switch h.Status {
		case 429, 500, 502, 503, 504:
			return h.RetryAfter, true
		}
		return 0, false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return 0, true
	}
	var n net.Error
	if errors.As(err, &n) {
		return 0, n.Timeout() || n.Temporary()
	}
	return 0, false
}

func retryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	// Parse and cap without overflow. A long/invalid value means defer until
	// the next scheduled run rather than retry earlier than the server permits.
	if seconds, err := strconv.ParseUint(value, 10, 32); err == nil {
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil {
		return max(0, date.Sub(now))
	}
	return 24 * time.Hour
}
