package runner

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

func TestCreationForbidden100006LeavesNoManualBlock(t *testing.T) {
	for _, kind := range []string{"comment", "repost"} {
		t.Run(kind, func(t *testing.T) {
			endpoint := "/api/statuses/repost"
			label := "转发"
			if kind == "comment" {
				endpoint = "/api/comments/create"
				label = "评论"
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != endpoint {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				calls++
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"error_code":100006}`)
			}))
			defer server.Close()
			state := app.NewState()
			path := filepath.Join(t.TempDir(), "state.json")
			r := Runner{API: weibo.NewClient(server.URL, server.Client()), Config: testConfig(), State: &state, Options: Options{
				Clock: time.Now, Output: io.Discard, Save: func(s app.State) error { return app.SaveState(path, s) },
			}}
			for attempt := 0; attempt < 2; attempt++ {
				summary := Summary{}
				err := r.interact(context.Background(), "topic", weibo.Post{MID: "123"}, kind, app.Today(time.Now()), "token", 1, 1, &summary)
				if err == nil || !strings.Contains(err.Error(), label+"未创建") || strings.Contains(err.Error(), "无法确认") {
					t.Fatalf("unexpected diagnostic: %v", err)
				}
				disk, err := app.LoadState(path)
				if err != nil || len(disk.PendingDeletes) != 0 || len(disk.History) != 0 || summary.Comments != 0 || summary.Reposts != 0 || summary.Deleted != 0 {
					t.Fatalf("pending/success retained: %+v %+v %v", disk, summary, err)
				}
				state = disk
			}
			if calls != 2 {
				t.Fatalf("next attempt blocked or extra requests: %d", calls)
			}
		})
	}
}
