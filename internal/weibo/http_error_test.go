package weibo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

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
