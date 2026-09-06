package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

type midnightRefreshAPI struct {
	fakeAPI
	now *time.Time
}

func (f *midnightRefreshAPI) FollowedTopics(ctx context.Context, pages int) ([]weibo.Topic, error) {
	if f.followedCall == 1 {
		*f.now = f.now.Add(2 * time.Second)
	}
	return f.fakeAPI.FollowedTopics(ctx, pages)
}

func TestMidnightDuringCheckinRefreshStopsBeforeWrite(t *testing.T) {
	now := time.Date(2026, 9, 5, 23, 59, 59, 0, time.Local)
	topic := weibo.Topic{ID: "topic", CanCheckin: true, CheckinURL: "/checkin"}
	api := &midnightRefreshAPI{fakeAPI: fakeAPI{followed: [][]weibo.Topic{{topic}}}, now: &now}
	state := app.NewState()
	r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{
		Clock: func() time.Time { return now }, Save: func(app.State) error { return nil },
	}}
	summary, err := r.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "日期已改变") || len(api.actions) != 0 || summary.Checkins != 0 {
		t.Fatalf("跨日刷新后仍执行写操作：%+v %v %v", summary, err, api.actions)
	}
}

type quotaAPI struct {
	fakeAPI
	pages, posts int
	excluded     []string
}

func (f *quotaAPI) TopicPosts(_ context.Context, _, _ string, pages, posts int, excluded []string) ([]weibo.Post, error) {
	f.pages, f.posts = pages, posts
	f.excluded = append([]string(nil), excluded...)
	result := make([]weibo.Post, posts)
	for i := range result {
		result[i].MID = strconv.Itoa(i + 1)
	}
	return result, nil
}

func TestRunnerExcludesCompletedPostsDuringFetch(t *testing.T) {
	now := time.Date(2026, 9, 11, 10, 0, 0, 0, time.Local)
	api := &quotaAPI{fakeAPI: fakeAPI{followed: [][]weibo.Topic{{{ID: "topic", Signed: true}}}}}
	state := app.NewState()
	state.UID = "self"
	state.Mark("2026-09-11", "topic", "comment", "1")
	state.Mark("2026-09-11", "topic", "repost", "2")
	r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{DryRun: true, Clock: func() time.Time { return now }}}
	_, err := r.Run(context.Background())
	if err != nil || strings.Join(api.excluded, ",") != "1,2" {
		t.Fatalf("completed history not passed to pagination: %v %v", api.excluded, err)
	}
}

func TestHighQuotaRaisesCandidateLimitAndUsesConfiguredPages(t *testing.T) {
	api := &quotaAPI{fakeAPI: fakeAPI{followed: [][]weibo.Topic{{{ID: "topic", Signed: true}}}}}
	cfg := testConfig()
	cfg.CommentLimit, cfg.RepostLimit, cfg.MaxFeedPages = 100, 100, 30
	state := app.NewState()
	r := Runner{API: api, Config: cfg, State: &state, Options: Options{DryRun: true}}
	summary, err := r.Run(context.Background())
	if err != nil || api.pages != 30 || api.posts != 200 || summary.Comments != 100 || summary.Reposts != 100 {
		t.Fatalf("配额未传递到读取计划：%+v %v %+v", summary, err, api)
	}
}

func TestInsufficientCandidatesExplainsReadLimits(t *testing.T) {
	api := &fakeAPI{posts: []weibo.Post{{MID: "one"}}, followed: [][]weibo.Topic{{{ID: "topic", Signed: true}}}}
	state := app.NewState()
	var output bytes.Buffer
	r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{DryRun: true, Output: &output}}
	_, err := r.Run(context.Background())
	if err == nil || !strings.Contains(output.String(), "最多 8 页、80 条候选") || !strings.Contains(output.String(), "--max-feed-pages") {
		t.Fatalf("未说明读取边界：%v\n%s", err, output.String())
	}
}

func TestMissingIDRemainsBlockedAfterReload(t *testing.T) {
	api := &fakeAPI{emptyCommentID: true, followed: [][]weibo.Topic{{{ID: "topic", Signed: true}}}}
	state := app.NewState()
	cfg := testConfig()
	cfg.CommentLimit = 1
	cfg.RepostLimit = 0
	path := filepath.Join(t.TempDir(), "state.json")
	r := Runner{API: api, Config: cfg, State: &state, Options: Options{Save: func(s app.State) error { return app.SaveState(path, s) }}}
	if _, err := r.Run(context.Background()); err == nil {
		t.Fatal("missing ID must fail")
	}
	reloaded, err := app.LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	r.State = &reloaded
	if _, err := r.Run(context.Background()); err == nil {
		t.Fatal("manual review must remain blocked")
	}
	if api.commentIndex != 1 || len(reloaded.PendingDeletes) != 1 {
		t.Fatal("repeated interaction or lost pending")
	}
}

func TestPendingCleanupBeforeDailyStart(t *testing.T) {
	api := &fakeAPI{}
	state := app.NewState()
	state.AddPendingDelete(app.PendingDelete{Kind: "comment", ContentID: "old"})
	r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{IfDue: true, Clock: func() time.Time { return time.Date(2026, 9, 6, 2, 0, 0, 0, time.Local) }, Save: func(app.State) error { return nil }}}
	summary, err := r.Run(context.Background())
	if err != nil || summary.Deleted != 1 || api.commentIndex != 0 || api.followedCall != 0 {
		t.Fatalf("%+v %v %+v", summary, err, api)
	}
}

type cleanupThenFailureAPI struct{ fakeAPI }

func (f *cleanupThenFailureAPI) FollowedTopics(context.Context, int) ([]weibo.Topic, error) {
	return nil, errors.New("feed unavailable")
}
func TestCleanupSummarySurvivesFeedFailure(t *testing.T) {
	api := &cleanupThenFailureAPI{}
	state := app.NewState()
	state.AddPendingDelete(app.PendingDelete{Kind: "repost", ContentID: "old"})
	r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{Save: func(app.State) error { return nil }}}
	summary, err := r.Run(context.Background())
	if err == nil || summary.Deleted != 1 || len(state.PendingDeletes) != 0 {
		t.Fatalf("%+v %v", summary, err)
	}
}

func TestAccountMismatchDoesNotDelete(t *testing.T) {
	state := app.NewState()
	state.UID = "other"
	state.AddPendingDelete(app.PendingDelete{Kind: "comment", ContentID: "old"})
	api := &fakeAPI{}
	r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{Save: func(app.State) error { return nil }}}
	if _, err := r.Run(context.Background()); err == nil || len(api.actions) != 0 {
		t.Fatal("account mismatch must stop all writes")
	}
}

func TestCrossMidnightCleansCurrentInteractionThenStops(t *testing.T) {
	now := time.Date(2026, 9, 5, 23, 59, 59, 0, time.Local)
	api := &fakeAPI{followed: [][]weibo.Topic{{{ID: "topic", Signed: true}}}}
	state := app.NewState()
	r := Runner{API: api, Config: app.DefaultConfig(), State: &state, Options: Options{
		Clock: func() time.Time { return now }, Save: func(app.State) error { return nil },
		Sleep: func(context.Context, time.Duration) error { now = now.Add(time.Second * 2); return nil },
	}}
	_, err := r.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "日期已改变") || api.commentIndex != 1 || len(state.PendingDeletes) != 0 {
		t.Fatalf("%v %+v", err, api.actions)
	}
}

func TestWriteRequiresPersistence(t *testing.T) {
	state := app.NewState()
	api := &fakeAPI{}
	r := Runner{API: api, Config: testConfig(), State: &state}
	if _, err := r.Run(context.Background()); err == nil || len(api.actions) != 0 {
		t.Fatal("missing save must stop writes")
	}
}

func TestLiveLockCannotBeReclaimedByAge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.lock")
	release, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if second, err := AcquireLock(path); err == nil {
		second()
		t.Fatal("active lock reclaimed")
	}
}

func TestLockReleasedAfterProcessExit(t *testing.T) {
	if path := os.Getenv("WEIBO_TEST_LOCK_CHILD"); path != "" {
		release, err := AcquireLock(path)
		if err != nil {
			os.Exit(2)
		}
		_ = release
		os.Exit(0) // No deferred release, simulates abrupt process termination.
	}
	path := filepath.Join(t.TempDir(), "run.lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockReleasedAfterProcessExit$")
	cmd.Env = append(os.Environ(), "WEIBO_TEST_LOCK_CHILD="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	release, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
