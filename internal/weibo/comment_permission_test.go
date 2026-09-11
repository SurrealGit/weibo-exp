package weibo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOnlyConfirmedCommentPermissionRefusalAllowsFallback(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       bool
	}{
		{"permission", `{"ok":0,"msg":"由于对方的设置，你不能评论哦！"}`, 200, true},
		{"ascii punctuation", `{"ok":0,"msg":"由于对方的设置，你不能评论哦!"}`, 200, true},
		{"rate limit", `{"ok":0,"msg":"操作过于频繁"}`, 200, false},
		{"authentication", `{"ok":-100,"msg":"由于对方的设置，你不能评论哦！"}`, 200, false},
		{"unknown envelope", `{"msg":"由于对方的设置，你不能评论哦！"}`, 200, false},
		{"http error", `{"ok":0,"msg":"由于对方的设置，你不能评论哦！"}`, 403, false},
	} {
		for _, kind := range []string{"comment", "repost", "delete"} {
			t.Run(tc.name+"/"+kind, func(t *testing.T) {
				client := NewClient("https://example.invalid", &http.Client{Transport: fixedTransport(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
				})})
				var err error
				switch kind {
				case "comment":
					_, err = client.Comment(context.Background(), "1", "test", "st")
				case "repost":
					_, err = client.Repost(context.Background(), "1", "st")
				case "delete":
					err = client.DeleteComment(context.Background(), "1", "st")
				}
				var rejected *RejectedError
				got := errors.As(err, &rejected) && rejected.CommentRestricted
				if got != (tc.want && kind == "comment") {
					t.Fatalf("restriction=%v err=%v", got, err)
				}
			})
		}
	}
}
