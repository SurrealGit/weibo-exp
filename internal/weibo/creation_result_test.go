package weibo

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

type brokenResponseBody struct{}

func (brokenResponseBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (brokenResponseBody) Close() error             { return nil }

func TestCreationFailureClassification(t *testing.T) {
	for _, kind := range []string{"comment", "repost"} {
		for _, tc := range []struct {
			name, body                  string
			status                      int
			transportErr                error
			broken, cancelled, rejected bool
		}{
			{name: "refused", body: `{"ok":0,"msg":"refused"}`, rejected: true},
			{name: "expired", body: `{"ok":-100}`, rejected: true},
			{name: "empty-envelope", body: `{}`},
			{name: "invalid-json", body: `not json`},
			{name: "server-error", status: 500, body: "error"},
			{name: "response-lost", transportErr: io.ErrUnexpectedEOF},
			{name: "response-body-lost", broken: true},
			{name: "dial-failed", transportErr: &net.OpError{Op: "dial", Err: errors.New("refused")}, rejected: true},
			{name: "cancelled-before-request", cancelled: true, rejected: true},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				called := false
				client := NewClient("https://example.invalid", &http.Client{Transport: fixedTransport(func(r *http.Request) (*http.Response, error) {
					called = true
					if tc.transportErr != nil {
						return nil, tc.transportErr
					}
					status := tc.status
					if status == 0 {
						status = 200
					}
					var body io.ReadCloser = io.NopCloser(strings.NewReader(tc.body))
					if tc.broken {
						body = brokenResponseBody{}
					}
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: body, Request: r}, nil
				})})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if tc.cancelled {
					cancel()
				}
				var err error
				if kind == "comment" {
					_, err = client.Comment(ctx, "post", "tt", "st")
				} else {
					_, err = client.Repost(ctx, "post", "st")
				}
				var rejected *RejectedError
				if err == nil || errors.As(err, &rejected) != tc.rejected {
					t.Fatalf("classification %v", err)
				}
				if tc.cancelled && called {
					t.Fatal("cancelled request sent")
				}
			})
		}
	}
}
