package runner

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

type uncertainAPI struct {
	fakeAPI
	createErr error
}

func (f *uncertainAPI) Comment(ctx context.Context, mid, content, st string) (string, error) {
	f.commentIndex++
	return "", f.createErr
}
func (f *uncertainAPI) Repost(ctx context.Context, mid, st string) (string, error) {
	f.repostIndex++
	return "", f.createErr
}

func TestCreationIntentBlocksAmbiguousRetry(t *testing.T) {
	for _, kind := range []string{"comment", "repost"} {
		for _, rejected := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/uncertain", true: "/rejected"}[rejected], func(t *testing.T) {
				api := &uncertainAPI{createErr: io.ErrUnexpectedEOF}
				if rejected {
					api.createErr = &weibo.RejectedError{Err: errors.New("explicit refusal")}
				}
				api.followed = [][]weibo.Topic{{{ID: "topic", Signed: true}}}
				state := app.NewState()
				cfg := testConfig()
				cfg.CommentLimit = 1
				cfg.RepostLimit = 0
				if kind == "repost" {
					cfg.CommentLimit = 0
					cfg.RepostLimit = 1
				}
				path := filepath.Join(t.TempDir(), "state.json")
				r := Runner{API: api, Config: cfg, State: &state, Options: Options{Save: func(s app.State) error { return app.SaveState(path, s) }}}
				summary, err := r.Run(context.Background())
				if err == nil || summary.Comments+summary.Reposts != 0 {
					t.Fatalf("unexpected success %+v %v", summary, err)
				}
				disk, err := app.LoadState(path)
				if err != nil {
					t.Fatal(err)
				}
				if len(disk.History) != 0 {
					t.Fatal("unknown result counted as success")
				}
				want := 1
				if rejected {
					want = 0
				}
				if len(disk.PendingDeletes) != want {
					t.Fatalf("pending %+v", disk.PendingDeletes)
				}
				r.State = &disk
				_, _ = r.Run(context.Background())
				calls := 1
				if rejected {
					calls = 2
				}
				if api.commentIndex+api.repostIndex != calls {
					t.Fatal("incorrect retry policy")
				}
			})
		}
	}
}

func TestIntentSaveFailureNeverSendsRequest(t *testing.T) {
	api := &fakeAPI{followed: [][]weibo.Topic{{{ID: "topic", Signed: true}}}}
	state := app.NewState()
	state.UID = "self"
	r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{Save: func(app.State) error { return errors.New("disk full") }}}
	if _, err := r.Run(context.Background()); err == nil {
		t.Fatal("expected persistence failure")
	}
	if api.commentIndex+api.repostIndex != 0 {
		t.Fatal("sent before intent persisted")
	}
}
