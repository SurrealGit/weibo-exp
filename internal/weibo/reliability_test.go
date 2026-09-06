package weibo

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fixedTransport func(*http.Request) (*http.Response, error)

func (f fixedTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixtureClient(body string) *Client {
	return NewClient("https://unit.test", &http.Client{Transport: fixedTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})})
}
func TestCommentParsesNestedIDAfterEnvelopeUnwrap(t *testing.T) {
	c := fixtureClient(`{"ok":1,"data":{"comment":{"id":5339546700223599}}}`)
	id, err := c.Comment(context.Background(), "source", "test", "token")
	if err != nil || id != "5339546700223599" {
		t.Fatalf("%s %v", id, err)
	}
}
func TestFollowedTopicsReportsTruncation(t *testing.T) {
	c := fixtureClient(`{"ok":1,"data":{"cards":[],"cardlistInfo":{"since_id":"next"}}}`)
	if _, err := c.FollowedTopics(context.Background(), 1); err == nil {
		t.Fatal("truncated list reported complete")
	}
}
