package weibo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/session"
)

// ErrRenewUnavailable means the long-lived passport credential (ALC) is gone,
// so only a new QR login can restore the session.
var ErrRenewUnavailable = errors.New("长期登录凭证已失效，请执行 weibo-exp login")

const (
	maxRenewHops       = 10
	restoreSuccess     = 20000000
	visitorReturnURL   = "https://weibo.com/"
	defaultVisitorVer  = "20250916"
	renewResponseLimit = 2 << 20
)

var (
	visitorRequestIDPattern = regexp.MustCompile(`request_id\s*=\s*"([0-9A-Za-z]+)"`)
	visitorVersionPattern   = regexp.MustCompile(`cb=visitor_gray_callback&ver=(\d+)`)
)

type RenewOptions struct {
	PassportURL string // defaults to https://passport.weibo.com
	ReturnURL   string // defaults to https://m.weibo.cn/
	Timeout     time.Duration
}

// RenewSession restores the login the way weibo.com's visitor page does: the
// passport ALC in base is exchanged for a restore ticket (alt), which signs in
// again and sets fresh cookies on every Weibo domain. The ALC is single-use, so
// the returned jar must be verified with LoginStatus and saved. base is not
// modified.
func RenewSession(ctx context.Context, base *session.Jar, options RenewOptions) (*session.Jar, error) {
	if options.PassportURL == "" {
		options.PassportURL = "https://passport.weibo.com"
	}
	if options.ReturnURL == "" {
		options.ReturnURL = "https://m.weibo.cn/"
	}
	if options.Timeout <= 0 {
		options.Timeout = 30 * time.Second
	}
	candidate, err := session.New(withoutLoginCookies(base.Snapshot()))
	if err != nil {
		return nil, err
	}
	passport := strings.TrimRight(options.PassportURL, "/")
	allowed := renewHostPolicy(passport, options.ReturnURL)
	client := &http.Client{Jar: candidate, Timeout: options.Timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRenewHops {
			return errors.New("跳转次数过多")
		}
		if !allowed(req.URL) {
			return errors.New("跳转地址不符合预期")
		}
		return nil
	}}

	visitorURL := passport + "/visitor/visitor?" + url.Values{
		"entry": {"miniblog"}, "a": {"enter"}, "url": {visitorReturnURL}, "domain": {"weibo.com"},
		"ua": {desktopUserAgent}, "_rand": {randParam(time.Now())}, "sudaref": {""},
	}.Encode()
	page, err := renewRequest(ctx, client, "访客页面", http.MethodGet, visitorURL, nil, "")
	if err != nil {
		return nil, err
	}
	requestID, version := visitorParams(page)
	if requestID == "" {
		return nil, errors.New("访客页面格式已变化")
	}

	form := url.Values{
		"cb": {"visitor_gray_callback"}, "ver": {version}, "request_id": {requestID}, "tid": {""},
		"from": {"weibo"}, "webdriver": {"false"}, "rid": {strconv.FormatInt(time.Now().UnixMilli(), 10)},
		"return_url": {visitorReturnURL},
	}
	raw, err := renewRequest(ctx, client, "访客凭证", http.MethodPost, passport+"/visitor/genvisitor2", form, visitorURL)
	if err != nil {
		return nil, err
	}
	alt, err := restoreTicket(raw)
	if err != nil {
		return nil, err
	}

	loginURL := passport + "/sso/v2/login?" + url.Values{
		"entry": {"sso"}, "source": {"visitor_restore"}, "type": {"3"}, "alt": {alt}, "url": {options.ReturnURL},
	}.Encode()
	if _, err := renewRequest(ctx, client, "恢复登录", http.MethodGet, loginURL, nil, visitorURL); err != nil {
		return nil, err
	}
	return candidate, nil
}

// renewRequest sends one renewal step. The URLs carry the restore ticket, so
// errors name the step and never include a URL, cookie or response body.
func renewRequest(ctx context.Context, client *http.Client, step, method, target string, form url.Values, referer string) ([]byte, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("%s: 请求地址无效", step)
	}
	req.Header.Set("User-Agent", desktopUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	if form != nil {
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", req.URL.Scheme+"://"+req.URL.Host)
	}
	resp, err := client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("%s: %s", step, urlInText.ReplaceAllString(err.Error(), "<url>"))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s HTTP %d", step, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, renewResponseLimit))
	if err != nil {
		return nil, fmt.Errorf("%s: 读取响应失败", step)
	}
	return data, nil
}

var urlInText = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s"']+`)

// withoutLoginCookies drops SUB/SUBP so the visitor page sees a signed-out
// browser, which is the condition the restore flow was verified under.
func withoutLoginCookies(cookies []session.StoredCookie) []session.StoredCookie {
	kept := make([]session.StoredCookie, 0, len(cookies))
	for _, cookie := range cookies {
		domain := strings.ToLower(strings.TrimPrefix(cookie.Domain, "."))
		if (cookie.Name == "SUB" || cookie.Name == "SUBP") && (domainWithin(domain, "weibo.com") || domainWithin(domain, "weibo.cn")) {
			continue
		}
		kept = append(kept, cookie)
	}
	return kept
}

func visitorParams(page []byte) (requestID, version string) {
	version = defaultVisitorVer
	if match := visitorRequestIDPattern.FindSubmatch(page); match != nil {
		requestID = string(match[1])
	}
	if match := visitorVersionPattern.FindSubmatch(page); match != nil {
		version = string(match[1])
	}
	return requestID, version
}

// restoreTicket reads the JSONP reply of genvisitor2, e.g.
// visitor_gray_callback({"retcode":20000000,"data":{"alt":"..."}}).
func restoreTicket(raw []byte) (string, error) {
	text := string(raw)
	if start, end := strings.Index(text, "("), strings.LastIndex(text, ")"); start >= 0 && end > start {
		text = text[start+1 : end]
	}
	var reply struct {
		RetCode any `json:"retcode"`
		Data    struct {
			Alt string `json:"alt"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &reply); err != nil {
		return "", errors.New("访客凭证响应格式已变化")
	}
	if code := integerValue(reply.RetCode); code != restoreSuccess {
		return "", fmt.Errorf("访客凭证 retcode=%d", code)
	}
	if reply.Data.Alt == "" {
		return "", ErrRenewUnavailable
	}
	return reply.Data.Alt, nil
}

// renewHostPolicy allows https redirects within the Weibo and Sina domains,
// plus the configured passport and return origins (used by tests).
func renewHostPolicy(passport, returnURL string) func(*url.URL) bool {
	origins := make(map[string]bool)
	for _, raw := range []string{passport, returnURL} {
		if parsed, err := url.Parse(raw); err == nil && parsed.Host != "" {
			origins[parsed.Scheme+"://"+parsed.Host] = true
		}
	}
	return func(target *url.URL) bool {
		if origins[target.Scheme+"://"+target.Host] {
			return true
		}
		host := strings.ToLower(target.Hostname())
		return target.Scheme == "https" && (domainWithin(host, "weibo.com") || domainWithin(host, "weibo.cn") || domainWithin(host, "sina.com.cn"))
	}
}

func domainWithin(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func randParam(now time.Time) string {
	return strconv.FormatFloat(float64(now.UnixMilli())/1000, 'f', 3, 64)
}
