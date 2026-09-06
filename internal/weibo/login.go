package weibo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/session"
)

const (
	qrSuccess    = 20000000
	qrNotScanned = 50114001
	qrScanned    = 50114002
	qrExpired    = 50114004
)

type QRLoginOptions struct {
	TempDir     string
	PassportURL string
	RedirectURL string
	PollEvery   time.Duration
	Timeout     time.Duration
	Output      io.Writer
	OpenImage   func(string) error
}

func QRLogin(ctx context.Context, jar *session.Jar, options QRLoginOptions) error {
	if options.PassportURL == "" {
		options.PassportURL = "https://passport.weibo.com"
	}
	if options.RedirectURL == "" {
		options.RedirectURL = "https://weibo.com/"
	}
	if options.PollEvery <= 0 {
		options.PollEvery = 2 * time.Second
	}
	if options.Timeout <= 0 {
		options.Timeout = 4 * time.Minute
	}
	if options.Output == nil {
		options.Output = io.Discard
	}
	if options.OpenImage == nil {
		options.OpenImage = OpenImage
	}

	httpClient := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	base := strings.TrimRight(options.PassportURL, "/")
	signinURL := base + "/sso/signin?" + url.Values{
		"entry": {"miniblog"}, "source": {"miniblog"}, "url": {options.RedirectURL},
	}.Encode()
	if _, err := loginRequest(ctx, httpClient, http.MethodGet, signinURL, ""); err != nil {
		return fmt.Errorf("获取登录页: %w", err)
	}
	csrf := cookieValue(jar, base, "X-CSRF-TOKEN")
	if csrf == "" {
		return errors.New("微博登录页未返回 X-CSRF-TOKEN")
	}

	imageURL := base + "/sso/v2/qrcode/image?" + url.Values{"entry": {"miniblog"}, "size": {"180"}}.Encode()
	raw, err := loginRequest(ctx, httpClient, http.MethodGet, imageURL, csrf)
	if err != nil {
		return fmt.Errorf("获取登录二维码: %w", err)
	}
	var imageResponse struct {
		RetCode any    `json:"retcode"`
		Message string `json:"msg"`
		Data    struct {
			QRID  string `json:"qrid"`
			Image string `json:"image"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &imageResponse); err != nil {
		return err
	}
	imageRetCode := integerValue(imageResponse.RetCode)
	if imageRetCode != qrSuccess || imageResponse.Data.QRID == "" || imageResponse.Data.Image == "" {
		return fmt.Errorf("获取二维码失败: %s (%d)", imageResponse.Message, imageRetCode)
	}

	resolvedImageURL, err := resolveReference(base, imageResponse.Data.Image)
	if err != nil {
		return fmt.Errorf("解析二维码地址: %w", err)
	}
	if options.TempDir == "" {
		options.TempDir, err = os.MkdirTemp("", "weibo-login-")
		if err != nil {
			return err
		}
	} else if err := os.MkdirAll(options.TempDir, 0700); err != nil {
		return err
	}
	defer os.Remove(options.TempDir) // Only remove an empty directory.
	imagePath, err := downloadQR(ctx, httpClient, resolvedImageURL, options.TempDir)
	if err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(imagePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(options.Output, "警告：二维码临时文件未能删除：%s（%v）\n", imagePath, err)
		}
	}()
	fmt.Fprintln(options.Output, "登录方式：微博 App 扫码（不依赖浏览器）")
	fmt.Fprintf(options.Output, "二维码文件：%s\n", imagePath)
	fmt.Fprintln(options.Output, "操作提示：请扫描二维码，并在微博 App 中确认登录。")
	if err := options.OpenImage(imagePath); err != nil {
		fmt.Fprintf(options.Output, "二维码未自动打开，请手动打开：%s\n", imagePath)
	}

	deadline := time.Now().Add(options.Timeout)
	lastRetCode := -1
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(options.PollEvery):
		}
		checkURL := base + "/sso/v2/qrcode/check?" + url.Values{
			"entry": {"miniblog"}, "source": {"miniblog"}, "url": {options.RedirectURL},
			"qrid": {imageResponse.Data.QRID}, "rid": {""}, "ver": {"20250520"},
		}.Encode()
		raw, err := loginRequest(ctx, httpClient, http.MethodGet, checkURL, csrf)
		if err != nil {
			continue
		}
		var check struct {
			RetCode any    `json:"retcode"`
			Message string `json:"msg"`
			Data    struct {
				URL string `json:"url"`
				Alt string `json:"alt"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &check) != nil {
			continue
		}
		retCode := integerValue(check.RetCode)
		switch retCode {
		case qrSuccess:
			if check.Data.URL != "" {
				crossURL, err := resolveReference(base, check.Data.URL)
				if err != nil {
					return fmt.Errorf("解析跨域登录地址: %w", err)
				}
				if _, err := loginRequest(ctx, httpClient, http.MethodGet, crossURL, ""); err != nil {
					return fmt.Errorf("完成跨域登录: %w", err)
				}
			}
			if check.Data.Alt != "" {
				altURL := "https://login.sina.com.cn/sso/login.php?" + url.Values{
					"entry": {"miniblog"}, "alt": {check.Data.Alt}, "returntype": {"TEXT"},
				}.Encode()
				_, _ = loginRequest(ctx, httpClient, http.MethodGet, altURL, "")
			}
			if !jar.Has("SUB") {
				return errors.New("扫码成功，但未取得 SUB Cookie")
			}
			fmt.Fprintln(options.Output, "扫码状态：确认成功。")
			return nil
		case qrScanned:
			if retCode != lastRetCode {
				fmt.Fprintln(options.Output, "扫码状态：已扫描；请在微博 App 中确认。")
			}
		case qrExpired:
			return errors.New("登录二维码已过期，请重试")
		case qrNotScanned:
			// 继续轮询。
		default:
			if check.Message != "" && retCode != lastRetCode {
				fmt.Fprintf(options.Output, "登录状态：%s\n", check.Message)
			}
		}
		lastRetCode = retCode
	}
	return errors.New("等待扫码超时")
}

func loginRequest(ctx context.Context, client *http.Client, method, target, csrf string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://passport.weibo.com/sso/signin?entry=miniblog&source=miniblog&url=https://weibo.com/")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if csrf != "" {
		req.Header.Set("X-CSRF-TOKEN", csrf)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}

func downloadQR(ctx context.Context, client *http.Client, target, directory string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载登录二维码: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("下载登录二维码: HTTP %d", resp.StatusCode)
	}
	file, err := os.CreateTemp(directory, "weibo-login-*.png")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := io.Copy(file, io.LimitReader(resp.Body, 2<<20)); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return filepath.Clean(path), nil
}

func cookieValue(jar *session.Jar, rawURL, name string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func resolveReference(baseURL, reference string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(reference)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}

func integerValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case string:
		result, _ := strconv.Atoi(typed)
		return result
	case json.Number:
		result, _ := strconv.Atoi(typed.String())
		return result
	default:
		return 0
	}
}
