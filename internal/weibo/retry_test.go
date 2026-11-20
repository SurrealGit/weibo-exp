package weibo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRetryAfterMetadata(t *testing.T) {
	now := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"", 0}, {"5", 5 * time.Second}, {"999999999999999999999", 24 * time.Hour}, {"invalid", 24 * time.Hour},
		{now.Add(8 * time.Second).Format(http.TimeFormat), 8 * time.Second}, {now.Add(-time.Minute).Format(http.TimeFormat), 0},
	} {
		if got := retryAfter(tc.value, now); got != tc.want {
			t.Fatalf("%q: %v", tc.value, got)
		}
	}
	c := NewClient("https://example.invalid", &http.Client{Transport: fixedTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"7"}}, Body: io.NopCloser(strings.NewReader(`{"error":"secret"}`))}, nil
	})})
	err := c.DeleteComment(context.Background(), "known", "st")
	delay, retry := DeleteRetry(err)
	if !retry || delay != 7*time.Second || strings.Contains(err.Error(), "secret") {
		t.Fatal(err, delay, retry)
	}
	if _, ok := DeleteRetry(context.Canceled); ok {
		t.Fatal("retry cancellation")
	}
	if _, ok := DeleteRetry(context.DeadlineExceeded); !ok {
		t.Fatal("per-request timeout should be retryable; caller checks parent context")
	}
	if _, ok := DeleteRetry(errors.New("unknown")); ok {
		t.Fatal("retry unknown response")
	}
}
