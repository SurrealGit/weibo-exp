package weibo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var ErrNotLoggedIn = errors.New("微博登录态无效，请重新执行 login")

// RejectedError proves the operation was not submitted or was explicitly
// rejected. Other transport/response errors have an unknown remote outcome.
type RejectedError struct{ Err error }

func (e *RejectedError) Error() string { return e.Err.Error() }
func (e *RejectedError) Unwrap() error { return e.Err }

type Client struct {
	BaseURL   string
	HTTP      *http.Client
	UserAgent string
}

func NewClient(baseURL string, httpClient *http.Client) *Client {
	if baseURL == "" {
		baseURL = "https://m.weibo.cn"
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"), HTTP: httpClient,
		UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
	}
}

func (c *Client) LoginStatus(ctx context.Context) (LoginStatus, error) {
	raw, err := c.getData(ctx, "/api/config")
	if err != nil {
		return LoginStatus{}, err
	}
	var value struct {
		Login bool `json:"login"`
		ST    any  `json:"st"`
		UID   any  `json:"uid"`
	}
	if err := decodeNumbers(raw, &value); err != nil {
		return LoginStatus{}, fmt.Errorf("解析登录状态: %w", err)
	}
	status := LoginStatus{Login: value.Login, ST: stringValue(value.ST), UID: stringValue(value.UID)}
	if !status.Login || status.ST == "" {
		return status, ErrNotLoggedIn
	}
	return status, nil
}

func (c *Client) FollowedTopics(ctx context.Context, maxPages int) ([]Topic, error) {
	var result []Topic
	seenTopics := make(map[string]bool)
	seenCursors := make(map[string]bool)
	cursor := ""
	for page := 0; page < maxPages; page++ {
		path := "/api/container/getIndex?containerid=100803_-_followsuper"
		if cursor != "" {
			path += "&since_id=" + url.QueryEscape(cursor)
		}
		data, err := c.getData(ctx, path)
		if err != nil {
			return nil, err
		}
		topics, next, err := ParseTopics(data)
		if err != nil {
			return nil, err
		}
		for _, topic := range topics {
			if !seenTopics[topic.ID] {
				seenTopics[topic.ID] = true
				result = append(result, topic)
			}
		}
		if next == "" || next == cursor || seenCursors[next] {
			break
		}
		seenCursors[next] = true
		cursor = next
		if page+1 == maxPages {
			return nil, errors.New("关注列表达到分页上限但仍有下一页；请提高 max_followed_pages 后重试")
		}
	}
	return result, nil
}

func (c *Client) TopicPosts(ctx context.Context, topicID, selfUID string, maxPages, maxPosts int, excluded []string) ([]Post, error) {
	var result []Post
	seenPosts := make(map[string]bool)
	for _, id := range excluded {
		seenPosts[id] = true
	}
	seenCursors := make(map[string]bool)
	cursor := ""
	for page := 0; page < maxPages; page++ {
		path := "/api/container/getIndex?containerid=" + url.QueryEscape(topicID)
		if cursor != "" {
			path += "&since_id=" + url.QueryEscape(cursor)
		}
		attempts := 1
		if page == 0 {
			attempts = 3
		}
		var posts []Post
		var next string
		for attempt := 0; attempt < attempts; attempt++ {
			requestPath := path
			if attempt > 0 {
				requestPath += "&__rnd=" + strconv.FormatInt(time.Now().UnixMilli()+int64(attempt), 10)
			}
			data, err := c.getData(ctx, requestPath)
			if err != nil {
				return nil, err
			}
			posts, next, err = ParsePosts(data, selfUID)
			if err != nil {
				return nil, err
			}
			if len(posts) > 0 || next != "" {
				break
			}
		}
		for _, post := range posts {
			if !seenPosts[post.MID] {
				seenPosts[post.MID] = true
				// Count usable candidates, not self-authored or already completed posts.
				if post.MID == "" || post.IsSelf {
					continue
				}
				result = append(result, post)
				if len(result) >= maxPosts {
					return result, nil
				}
			}
		}
		if next == "" || next == cursor || seenCursors[next] {
			break
		}
		seenCursors[next] = true
		cursor = next
	}
	return result, nil
}

func (c *Client) Checkin(ctx context.Context, target string) error {
	_, err := c.getData(ctx, target)
	return err
}

func (c *Client) Comment(ctx context.Context, mid, content, st string) (string, error) {
	raw, err := c.postForm(ctx, "/api/comments/create", url.Values{
		"content": {content}, "mid": {mid}, "st": {st},
	})
	if err != nil {
		return "", err
	}
	return CreatedID(raw), nil
}

func (c *Client) DeleteComment(ctx context.Context, id, st string) error {
	_, err := c.postForm(ctx, "/comments/destroy", url.Values{
		"cid": {id}, "st": {st}, "_spr": {"screen:1920x1080"},
	})
	return err
}

func (c *Client) Repost(ctx context.Context, mid, st string) (string, error) {
	// Keep reposts separate from comments; each task owns its deletion record.
	raw, err := c.postForm(ctx, "/api/statuses/repost", url.Values{
		"id": {mid}, "mid": {mid}, "content": {"转发微博"}, "dualPost": {"0"}, "st": {st},
	})
	if err != nil {
		return "", err
	}
	return CreatedID(raw), nil
}

func (c *Client) DeleteRepost(ctx context.Context, id, st string) error {
	_, err := c.postForm(ctx, "/profile/delMyblog", url.Values{
		"mid": {id}, "st": {st}, "_spr": {"screen:1920x1080"},
	})
	return err
}

func (c *Client) getData(ctx context.Context, target string) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, target, nil)
}

func (c *Client) postForm(ctx context.Context, target string, form url.Values) (json.RawMessage, error) {
	return c.do(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
}

func (c *Client) do(ctx context.Context, method, target string, body io.Reader) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, &RejectedError{Err: err}
	}
	resolved, err := c.resolve(target)
	if err != nil {
		return nil, &RejectedError{Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, method, resolved, body)
	if err != nil {
		return nil, &RejectedError{Err: err}
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Referer", c.BaseURL+"/")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	}
	var connected atomic.Bool
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { connected.Store(true) }}))
	resp, err := c.HTTP.Do(req)
	if err != nil {
		cause := fmt.Errorf("%s %s: %w", method, endpointLabel(resolved), err)
		var connectionErr *net.OpError
		if !connected.Load() && errors.As(err, &connectionErr) && connectionErr.Op == "dial" {
			return nil, &RejectedError{Err: cause}
		}
		return nil, cause
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: HTTP %d", method, endpointLabel(resolved), resp.StatusCode)
	}
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("<")) {
		return nil, fmt.Errorf("%s %s: 服务器返回了 HTML，可能需要重新登录或解除风控", method, endpointLabel(resolved))
	}
	var envelope struct {
		OK   *int            `json:"ok"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("解析微博响应: %w", err)
	}
	if envelope.OK == nil {
		return nil, errors.New("微博响应缺少 ok，无法确认操作结果")
	}
	if *envelope.OK == -100 {
		return nil, &RejectedError{Err: ErrNotLoggedIn}
	}
	if *envelope.OK == 0 {
		if envelope.Msg == "" {
			envelope.Msg = "微博接口返回失败"
		}
		return nil, &RejectedError{Err: errors.New(envelope.Msg)}
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return json.RawMessage(data), nil
	}
	return envelope.Data, nil
}

func endpointLabel(target string) string {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Path == "" {
		return "微博接口"
	}
	return parsed.Path
}

func (c *Client) resolve(target string) (string, error) {
	if parsed, err := url.Parse(target); err == nil && parsed.IsAbs() {
		return target, nil
	}
	if !strings.HasPrefix(target, "/") {
		target = "/" + target
	}
	return c.BaseURL + target, nil
}
