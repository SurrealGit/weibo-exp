package weibo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var diagnosticCode = regexp.MustCompile(`^-?[0-9]{1,8}$`)

// Error responses may echo credentials, personal data or entire requests.
// Extract only short numeric error codes and known, non-personal messages;
// never log arbitrary server text, HTML, headers or request bodies.
func httpFailure(method, endpoint string, status int, data []byte) error {
	format := "其他"
	message := "未获得可安全展示的错误说明"
	var fields map[string]json.RawMessage
	code := ""
	if json.Unmarshal(data, &fields) == nil && fields != nil {
		format = "JSON"
		for _, key := range []string{"error_code", "errno", "code"} {
			value := strings.Trim(string(fields[key]), `"`)
			if diagnosticCode.MatchString(value) {
				code = value
				break
			}
		}
		for _, key := range []string{"msg", "message", "error"} {
			var value string
			if json.Unmarshal(fields[key], &value) != nil {
				continue
			}
			value = strings.TrimRight(strings.TrimSpace(value), "！!。.")
			switch value {
			case "请先登录", "请重新登录", "登录已失效", "登录态失效", "登录超时", "未登录", "需要登录", "访问被拒绝", "没有权限", "无权限", "权限不足", "操作过于频繁", "操作频繁", "请求过于频繁", "请稍后再试", "需要验证码", "请完成验证", "由于对方的设置，你不能评论哦", "Forbidden", "Unauthorized", "Too Many Requests":
				message = value
			}
		}
	} else if bytes.HasPrefix(bytes.TrimSpace(data), []byte("<")) {
		format = "HTML/XML"
	}
	codeText := ""
	if code != "" {
		codeText = "；接口错误码：" + code
	}
	return fmt.Errorf("%s %s: HTTP %d；响应格式：%s%s；接口说明：%s", method, endpoint, status, format, codeText, message)
}
