package weibo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCreationForbidden100006RejectionIsNarrow(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		status                   int
		rejected                 bool
	}{
		{"numeric code", "POST", "/api/statuses/repost", `{"error_code":100006,"msg":"secret-cookie"}`, 403, true},
		{"string code", "POST", "/api/statuses/repost", `{"errno":"100006"}`, 403, true},
		{"code alias", "POST", "/api/statuses/repost", `{"code":100006}`, 403, true},
		{"other status", "POST", "/api/statuses/repost", `{"error_code":100006}`, 500, false},
		{"other code", "POST", "/api/statuses/repost", `{"error_code":100007}`, 403, false},
		{"missing code", "POST", "/api/statuses/repost", `{}`, 403, false},
		{"nested code", "POST", "/api/statuses/repost", `{"data":{"error_code":100006}}`, 403, false},
		{"html", "POST", "/api/statuses/repost", `<html>100006</html>`, 403, false},
		{"comment", "POST", "/api/comments/create", `{"error_code":100006}`, 403, true},
		{"comment string code", "POST", "/api/comments/create", `{"errno":"100006"}`, 403, true},
		{"comment other status", "POST", "/api/comments/create", `{"error_code":100006}`, 500, false},
		{"comment other code", "POST", "/api/comments/create", `{"error_code":100007}`, 403, false},
		{"comment no code", "POST", "/api/comments/create", `{}`, 403, false},
		{"comment read", "GET", "/api/comments/create", `{"error_code":100006}`, 403, false},
		{"delete repost", "POST", "/profile/delMyblog", `{"error_code":100006}`, 403, false},
		{"delete comment", "POST", "/comments/destroy", `{"error_code":100006}`, 403, false},
		{"read", "GET", "/api/statuses/repost", `{"error_code":100006}`, 403, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient("https://example.invalid", &http.Client{Transport: fixedTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})})
			_, err := client.do(context.Background(), tc.method, tc.path, nil)
			var rejected *RejectedError
			if errors.As(err, &rejected) != tc.rejected {
				t.Fatalf("rejected=%v: %v", tc.rejected, err)
			}
			var failure *HTTPFailure
			if !errors.As(err, &failure) || failure.Status != tc.status {
				t.Fatalf("lost HTTP diagnostic: %v", err)
			}
			if strings.Contains(err.Error(), "secret") || errors.Is(err, ErrNotLoggedIn) {
				t.Fatalf("unsafe diagnostic or assumed expired login: %v", err)
			}
		})
	}
}

type interruptedResponseBody struct{ io.Reader }

func (b interruptedResponseBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}

func TestCreationForbiddenIncompleteResponseRemainsUnknown(t *testing.T) {
	c := NewClient("https://example.invalid", &http.Client{Transport: fixedTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Body: io.NopCloser(interruptedResponseBody{strings.NewReader(`{"error_code":100006}`)}), Header: make(http.Header)}, nil
	})})
	for _, path := range []string{"/api/statuses/repost", "/api/comments/create"} {
		_, err := c.postForm(context.Background(), path, nil)
		var rejected *RejectedError
		if err == nil || errors.As(err, &rejected) {
			t.Fatalf("%s incomplete response classified as rejection: %v", path, err)
		}
	}
}

func TestHTTPDiagnosticNeverExposesResponseSecrets(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"safe message", `{"error_code":21301,"msg":"请重新登录！","cookie":"secret-cookie"}`, "接口错误码：21301；接口说明：请重新登录"},
		{"unsafe message", `{"errno":"403","msg":"请重新登录 token=secret-token email=person@example.com uid=123456789","data":{"st":"secret-st"}}`, "接口错误码：403；接口说明：未获得可安全展示的错误说明"},
		{"unsafe code", `{"code":"secret-code","msg":"secret-message"}`, "未获得可安全展示的错误说明"},
		{"html", `<html>secret-cookie person@example.com https://example.com/?token=secret-token</html>`, "响应格式：HTML/XML"},
		{"long", strings.Repeat("secret", 10000), "响应格式：其他"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient("https://example.invalid", &http.Client{Transport: fixedTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{"Set-Cookie": []string{"secret-cookie"}}}, nil
			})})
			_, err := client.postForm(context.Background(), "/api/statuses/repost?sign=secret-sign", nil)
			if err == nil || !strings.Contains(err.Error(), "POST /api/statuses/repost: HTTP 403") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v", err)
			}
			for _, secret := range []string{"secret", "person@example.com", "123456789", "https://", "Set-Cookie"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("leaked %q: %v", secret, err)
				}
			}
			var rejected *RejectedError
			if errors.As(err, &rejected) {
				t.Fatal("HTTP error incorrectly treated as confirmed rejection")
			}
			if len(err.Error()) > 600 {
				t.Fatal("unbounded diagnostic")
			}
		})
	}
}
