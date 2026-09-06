package runner

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

func TestCreateSaveFailureReportsCompensationResult(t *testing.T) {
	for _, kind := range []string{"comment", "repost"} {
		for _, deleteFails := range []bool{false, true} {
			name := kind + "/delete-success"
			if deleteFails {
				name = kind + "/delete-failure"
			}
			t.Run(name, func(t *testing.T) {
				api := &fakeAPI{followed: [][]weibo.Topic{{{ID: "topic", Signed: true}}}}
				saveErr, deleteErr := errors.New("injected save failure"), errors.New("injected delete failure")
				if deleteFails {
					api.deleteErr = deleteErr
				}
				state := app.NewState()
				state.UID = "self"
				path := filepath.Join(t.TempDir(), "state.json")
				if err := app.SaveState(path, state); err != nil {
					t.Fatal(err)
				}
				cfg := testConfig()
				cfg.CommentLimit, cfg.RepostLimit = 2, 0
				if kind == "repost" {
					cfg.CommentLimit, cfg.RepostLimit = 0, 2
				}
				var output bytes.Buffer
				saves := 0
				r := Runner{API: api, Config: cfg, State: &state, Options: Options{
					Output: &output, Save: func(s app.State) error {
						saves++
						if saves == 1 {
							return app.SaveState(path, s)
						}
						// The ID must be observable before even attempting persistence.
						if !strings.Contains(output.String(), "新内容 "+kind[:1]+"1") {
							t.Fatal("ID logged too late")
						}
						return saveErr
					},
				}}
				summary, err := r.Run(context.Background())
				if !errors.Is(err, saveErr) || errors.Is(err, deleteErr) != deleteFails || !strings.Contains(err.Error(), kind[:1]+"1") || !strings.Contains(err.Error(), "人工检查") {
					t.Fatalf("lost failure context: %+v %v", summary, err)
				}
				if saves != 2 || api.commentIndex+api.repostIndex != 1 || len(api.actions) != 2 {
					t.Fatalf("queue continued: %+v", api.actions)
				}
				wantDeleted, wantPending := 1, 0
				if deleteFails {
					wantDeleted, wantPending = 0, 1
				}
				if summary.Deleted != wantDeleted || len(state.PendingDeletes) != wantPending {
					t.Fatalf("incorrect compensation state: %+v %+v", summary, state)
				}
				disk, err := app.LoadState(path)
				if err != nil || len(disk.History) != 0 || len(disk.PendingDeletes) != 1 || disk.PendingDeletes[0].ContentID != "" {
					t.Fatalf("failed save changed disk: %+v %v", disk, err)
				}
			})
		}
	}
}

func TestDeleteSuccessThenSaveFailureKeepsDiskRecoveryRecord(t *testing.T) {
	for _, kind := range []string{"comment", "repost"} {
		t.Run(kind, func(t *testing.T) {
			api := &fakeAPI{followed: [][]weibo.Topic{{{ID: "topic", Signed: true}}}}
			cfg := testConfig()
			cfg.CommentLimit, cfg.RepostLimit = 2, 0
			if kind == "repost" {
				cfg.CommentLimit, cfg.RepostLimit = 0, 2
			}
			state := app.NewState()
			state.UID = "self"
			path := filepath.Join(t.TempDir(), "state.json")
			saveErr := errors.New("cleanup persistence failed")
			saves := 0
			r := Runner{API: api, Config: cfg, State: &state, Options: Options{Save: func(s app.State) error {
				saves++
				if saves == 3 {
					return saveErr
				}
				return app.SaveState(path, s)
			}}}
			summary, err := r.Run(context.Background())
			if !errors.Is(err, saveErr) || summary.Deleted != 1 || api.commentIndex+api.repostIndex != 1 {
				t.Fatalf("%+v %v", summary, err)
			}
			disk, err := app.LoadState(path)
			if err != nil || len(disk.PendingDeletes) != 1 || disk.PendingDeletes[0].ContentID != kind[:1]+"1" {
				t.Fatalf("lost disk recovery: %+v %v", disk, err)
			}
			progress := disk.Progress(app.Today(time.Now()), "topic", true, cfg)
			if progress.CommentsDone+progress.RepostsDone != 1 {
				t.Fatal("lost successful creation")
			}
			// Unknown repeat-delete errors must block; do not invent Weibo semantics.
			api.deleteErr = errors.New("repeat deletion refused")
			r.State = &disk
			if _, err := r.Run(context.Background()); err == nil || api.commentIndex+api.repostIndex != 1 {
				t.Fatal("created again before recovery")
			}
		})
	}
}

type secondDeleteFailureAPI struct{ fakeAPI }

func (f *secondDeleteFailureAPI) DeleteRepost(ctx context.Context, id, st string) error {
	_ = f.fakeAPI.DeleteRepost(ctx, id, st)
	return errors.New("second deletion failed")
}

func TestPartialRecoveryKeepsSummaryAndRemainingDiskQueue(t *testing.T) {
	for _, saveFails := range []bool{false, true} {
		name := "second-delete-fails"
		if saveFails {
			name = "first-cleanup-save-fails"
		}
		t.Run(name, func(t *testing.T) {
			state := app.NewState()
			state.UID = "self"
			state.AddPendingDelete(app.PendingDelete{Kind: "comment", ContentID: "old-comment"})
			state.AddPendingDelete(app.PendingDelete{Kind: "repost", ContentID: "old-repost"})
			path := filepath.Join(t.TempDir(), "state.json")
			if err := app.SaveState(path, state); err != nil {
				t.Fatal(err)
			}
			api := &secondDeleteFailureAPI{}
			r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{Save: func(s app.State) error {
				if saveFails {
					return errors.New("recovery save failed")
				}
				return app.SaveState(path, s)
			}}}
			summary, err := r.Run(context.Background())
			if err == nil || summary.Deleted != 1 || api.commentIndex+api.repostIndex != 0 || api.followedCall != 0 {
				t.Fatalf("%+v %v", summary, err)
			}
			disk, err := app.LoadState(path)
			want := 1
			if saveFails {
				want = 2
			}
			if err != nil || len(disk.PendingDeletes) != want || disk.PendingDeletes[want-1].ContentID != "old-repost" {
				t.Fatalf("%+v %v", disk, err)
			}
		})
	}
}
