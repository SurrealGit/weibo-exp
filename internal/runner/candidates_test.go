package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

type feedTestAPI struct {
	*weibo.Client
	topics []weibo.Topic
}

func (a *feedTestAPI) LoginStatus(context.Context) (weibo.LoginStatus, error) {
	return weibo.LoginStatus{Login: true, UID: "self", ST: "test"}, nil
}
func (a *feedTestAPI) FollowedTopics(context.Context, int) ([]weibo.Topic, error) {
	return a.topics, nil
}

func TestRestrictedCommentsUseCandidatesAndResumeFeed(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		batch, maxPages                       int
		first, second                         []string
		wantRequests, wantComments            int
		wantAttempts                          []string
		secondTopic, incomplete, fetchFailure bool
	}{
		{name: "same batch", batch: 3, maxPages: 2, first: []string{"1", "2", "3"}, wantRequests: 1, wantComments: 1, wantAttempts: []string{"1", "2"}},
		{name: "buffered remainder", batch: 1, maxPages: 1, first: []string{"1", "2", "3"}, wantRequests: 1, wantComments: 1, wantAttempts: []string{"1", "2"}},
		{name: "next page deduplicated", batch: 1, maxPages: 2, first: []string{"1"}, second: []string{"1", "2"}, wantRequests: 2, wantComments: 1, wantAttempts: []string{"1", "2"}},
		{name: "page limit continues next topic", batch: 1, maxPages: 1, first: []string{"1"}, second: []string{"2"}, secondTopic: true, incomplete: true, wantRequests: 2, wantComments: 1, wantAttempts: []string{"1", "other"}},
		{name: "exhausted", batch: 1, maxPages: 3, first: []string{"1"}, incomplete: true, wantRequests: 1, wantAttempts: []string{"1"}},
		{name: "refill failure stops", batch: 1, maxPages: 3, first: []string{"1"}, second: []string{"2"}, secondTopic: true, fetchFailure: true, incomplete: true, wantRequests: 2, wantAttempts: []string{"1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			var attempts []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch req.URL.Path {
				case "/api/container/getIndex":
					requests++
					ids, next := tc.first, ""
					if tc.second != nil {
						next = "page2"
					}
					if req.URL.Query().Get("since_id") != "" {
						if tc.fetchFailure {
							http.Error(w, "failed", 503)
							return
						}
						ids, next = tc.second, ""
					}
					if req.URL.Query().Get("containerid") == "other-topic" {
						ids, next = []string{"other"}, ""
					}
					cards := []any{}
					// Self-authored posts must not consume a candidate slot.
					cards = append(cards, map[string]any{"mblog": map[string]any{"mid": "self-post", "user": map[string]any{"id": "self"}}})
					for _, id := range ids {
						cards = append(cards, map[string]any{"mblog": map[string]any{"mid": id, "user": map[string]any{"id": "author"}}})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"ok": 1, "data": map[string]any{"cards": cards, "cardlistInfo": map[string]any{"since_id": next}}})
				case "/api/comments/create":
					_ = req.ParseForm()
					id := req.Form.Get("mid")
					attempts = append(attempts, id)
					if id == "1" {
						fmt.Fprint(w, `{"ok":0,"msg":"由于对方的设置，你不能评论哦！"}`)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"ok": 1, "data": map[string]any{"id": "c" + id}})
				case "/comments/destroy":
					fmt.Fprint(w, `{"ok":1}`)
				default:
					t.Errorf("unexpected request %s", req.URL.Path)
					http.Error(w, "unexpected", 500)
				}
			}))
			defer server.Close()
			api := &feedTestAPI{Client: weibo.NewClient(server.URL, server.Client()), topics: []weibo.Topic{{ID: "topic", Name: "示例", Signed: true}}}
			if tc.secondTopic {
				api.topics = append(api.topics, weibo.Topic{ID: "other-topic", Name: "其他", Signed: true})
			}
			cfg := testConfig()
			cfg.CommentLimit, cfg.RepostLimit, cfg.MaxPostsPerTopic, cfg.MaxFeedPages = 1, 0, tc.batch, tc.maxPages
			cfg.ActionDelayMin, cfg.ActionDelayMax = 1, 1
			state := app.NewState()
			path := filepath.Join(t.TempDir(), "state.json")
			var output bytes.Buffer
			sleeps := 0
			r := Runner{API: api, Config: cfg, State: &state, Options: Options{
				Clock:  func() time.Time { return time.Date(2026, 9, 11, 10, 0, 0, 0, time.Local) },
				Output: &output, Save: func(s app.State) error { return app.SaveState(path, s) },
				Sleep: func(context.Context, time.Duration) error { sleeps++; return nil },
			}}
			summary, err := r.Run(context.Background())
			if (err != nil) != tc.incomplete || requests != tc.wantRequests || summary.Comments != tc.wantComments || summary.Deleted != tc.wantComments || !reflect.DeepEqual(attempts, tc.wantAttempts) {
				t.Fatalf("summary=%+v err=%v requests=%d attempts=%v\n%s", summary, err, requests, attempts, output.String())
			}
			disk, err := app.LoadState(path)
			if err != nil || len(disk.PendingDeletes) != 0 || sleeps == 0 {
				t.Fatalf("disk=%+v err=%v sleeps=%d", disk, err, sleeps)
			}
			for _, id := range disk.Topic("2026-09-11", "topic").CommentedPostIDs {
				if id == "1" {
					t.Fatal("refused post recorded as completed")
				}
			}
			if !strings.Contains(output.String(), "评论已跳过") {
				t.Fatal(output.String())
			}
		})
	}
}

func TestRestrictionWithSaveFailureStillStops(t *testing.T) {
	api := &uncertainAPI{createErr: &weibo.RejectedError{Err: errors.New("permission"), CommentRestricted: true}}
	api.followed = [][]weibo.Topic{{{ID: "topic", Signed: true}}}
	state := app.NewState()
	state.UID = "self"
	saves := 0
	r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{Save: func(app.State) error {
		saves++
		if saves == 2 {
			return errors.New("disk full")
		}
		return nil
	}}}
	_, err := r.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "disk full") || api.commentIndex != 1 {
		t.Fatalf("err=%v calls=%d", err, api.commentIndex)
	}
}

type permissionAPI struct {
	fakeAPI
	refuseAll bool
}

func (a *permissionAPI) Comment(ctx context.Context, mid, content, st string) (string, error) {
	if a.refuseAll || mid == "1" {
		a.commentIndex++
		a.actions = append(a.actions, "comment:"+mid)
		return "", &weibo.RejectedError{Err: errors.New("permission"), CommentRestricted: true}
	}
	return a.fakeAPI.Comment(ctx, mid, content, st)
}

func TestFallbackPreservesRepostCandidatesAndDistinctPosts(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(fmt.Sprint(all), func(t *testing.T) {
			api := &permissionAPI{refuseAll: all}
			api.posts = []weibo.Post{{MID: "1"}, {MID: "2"}, {MID: "3"}, {MID: "3"}, {MID: "4"}}
			api.followed = [][]weibo.Topic{{{ID: "topic", Signed: true}}}
			state := app.NewState()
			r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{Save: func(app.State) error { return nil }}}
			summary, err := r.Run(context.Background())
			wantComments := 2
			if all {
				wantComments = 0
			}
			if (err != nil) != all || summary.Comments != wantComments || summary.Reposts != 1 || summary.Deleted != wantComments+1 {
				t.Fatalf("summary=%+v err=%v actions=%v", summary, err, api.actions)
			}
			seen := map[string]bool{}
			for _, action := range api.actions {
				kind, id, _ := strings.Cut(action, ":")
				if kind != "comment" && kind != "repost" {
					continue
				}
				if seen[id] {
					t.Fatalf("post attempted twice: %v", api.actions)
				}
				seen[id] = true
			}
			if !strings.Contains(strings.Join(api.actions, ","), "repost:3") {
				t.Fatal(api.actions)
			}
		})
	}
}
