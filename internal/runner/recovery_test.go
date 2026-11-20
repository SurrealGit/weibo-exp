package runner

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

type retryAPI struct {
	fakeAPI
	failures   []error
	deletedIDs []string
}

func (a *retryAPI) DeleteComment(_ context.Context, id, _ string) error {
	a.deletedIDs = append(a.deletedIDs, id)
	if len(a.failures) == 0 {
		return nil
	}
	err := a.failures[0]
	a.failures = a.failures[1:]
	return err
}

func TestDeleteRetriesOnlyTransientFailures(t *testing.T) {
	temporary := &weibo.HTTPFailure{Status: 503, Diagnostic: "temporary"}
	for _, tc := range []struct {
		name      string
		failures  []error
		calls     int
		wantError bool
		delay     time.Duration
	}{
		{"recovers", []error{temporary, io.ErrUnexpectedEOF}, 3, false, 6 * time.Second},
		{"exhausted", []error{temporary, temporary, temporary}, 3, true, 6 * time.Second},
		{"permission", []error{&weibo.HTTPFailure{Status: 403}}, 1, true, 0},
		{"login", []error{weibo.ErrNotLoggedIn}, 1, true, 0},
		{"absent unverified", []error{errors.New("内容不存在")}, 1, true, 0},
		{"rate limit long", []error{&weibo.HTTPFailure{Status: 429, RetryAfter: time.Minute}}, 1, true, 0},
		{"rate limit short", []error{&weibo.HTTPFailure{Status: 429, RetryAfter: 5 * time.Second}}, 2, false, 5 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &retryAPI{failures: append([]error(nil), tc.failures...)}
			var waited time.Duration
			r := Runner{API: a, Options: Options{Output: io.Discard, Sleep: func(_ context.Context, d time.Duration) error { waited += d; return nil }}}
			err := r.deleteContent(context.Background(), "comment", "known-id", "st")
			if (err != nil) != tc.wantError || len(a.deletedIDs) != tc.calls || waited != tc.delay {
				t.Fatalf("err=%v ids=%v wait=%v", err, a.deletedIDs, waited)
			}
			for _, id := range a.deletedIDs {
				if id != "known-id" {
					t.Fatal("changed deletion target")
				}
			}
		})
	}
}

func TestDeletionRetryCancellationRetainsPending(t *testing.T) {
	a := &retryAPI{failures: []error{io.ErrUnexpectedEOF}}
	state := app.NewState()
	state.AddPendingDelete(app.PendingDelete{Kind: "comment", ContentID: "known"})
	r := Runner{API: a, Config: testConfig(), State: &state, Options: Options{Output: io.Discard, Save: func(app.State) error { return nil }, Sleep: func(context.Context, time.Duration) error { return context.Canceled }}}
	_, err := r.Run(context.Background())
	if !errors.Is(err, context.Canceled) || len(state.PendingDeletes) != 1 || len(a.deletedIDs) != 1 || a.commentIndex != 0 {
		t.Fatalf("%v %+v", err, state)
	}
}

func TestMixedPendingCleansKnownIDsBeforeBlocking(t *testing.T) {
	a := &fakeAPI{}
	state := app.NewState()
	state.PendingDeletes = []app.PendingDelete{{Kind: "repost", SourcePostID: "unknown"}, {Kind: "comment", ContentID: "known-c"}, {Kind: "repost", ContentID: "known-r"}}
	var saved app.State
	r := Runner{API: a, Config: testConfig(), State: &state, Options: Options{Save: func(s app.State) error { saved = s; return nil }}}
	summary, err := r.Run(context.Background())
	if !errors.Is(err, ErrManualPending) || summary.Deleted != 2 || len(saved.PendingDeletes) != 1 || saved.PendingDeletes[0].SourcePostID != "unknown" {
		t.Fatalf("%+v %v %+v", summary, err, saved)
	}
	if !reflect.DeepEqual(a.actions, []string{"delete-comment:known-c", "delete-repost:known-r"}) {
		t.Fatal(a.actions)
	}
}

func TestRetrySuccessDoesNotRepeatCreation(t *testing.T) {
	a := &retryAPI{failures: []error{io.ErrUnexpectedEOF}}
	state := app.NewState()
	cfg := testConfig()
	cfg.CommentLimit = 1
	cfg.RepostLimit = 0
	r := Runner{API: a, Config: cfg, State: &state, Options: Options{Save: func(app.State) error { return nil }, Sleep: func(context.Context, time.Duration) error { return nil }}}
	summary, err := r.Run(context.Background())
	if err != nil || summary.Comments != 1 || summary.Deleted != 1 || a.commentIndex != 1 || len(state.PendingDeletes) != 0 || len(a.deletedIDs) != 2 {
		t.Fatalf("%+v %v %+v", summary, err, a)
	}
}
