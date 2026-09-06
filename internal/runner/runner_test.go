package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

type fakeAPI struct {
	actions        []string
	deleteErr      error
	commentIndex   int
	repostIndex    int
	followed       [][]weibo.Topic
	followedCall   int
	posts          []weibo.Post
	emptyCommentID bool
}

func (f *fakeAPI) LoginStatus(context.Context) (weibo.LoginStatus, error) {
	return weibo.LoginStatus{Login: true, ST: "token", UID: "self"}, nil
}
func (f *fakeAPI) FollowedTopics(context.Context, int) ([]weibo.Topic, error) {
	if len(f.followed) > 0 {
		index := min(f.followedCall, len(f.followed)-1)
		f.followedCall++
		return f.followed[index], nil
	}
	return []weibo.Topic{{ID: "topic", Name: "测试", CanCheckin: true, CheckinURL: "/checkin"}}, nil
}
func (f *fakeAPI) TopicPosts(context.Context, string, string, int, int, []string) ([]weibo.Post, error) {
	if f.posts != nil {
		return f.posts, nil
	}
	return []weibo.Post{{MID: "1"}, {MID: "2"}, {MID: "3"}, {MID: "4"}, {MID: "5"}, {MID: "6"}}, nil
}
func (f *fakeAPI) Checkin(_ context.Context, target string) error {
	f.actions = append(f.actions, "checkin:"+target)
	return nil
}

func TestRunRefreshesCheckinURLBeforeWrite(t *testing.T) {
	stale := weibo.Topic{ID: "topic", Name: "测试", CanCheckin: true, CheckinURL: "/checkin?sign=stale"}
	fresh := weibo.Topic{ID: "topic", Name: "测试", CanCheckin: true, CheckinURL: "/checkin?sign=fresh"}
	api := &fakeAPI{followed: [][]weibo.Topic{{stale}, {fresh}}}
	state := app.NewState()
	r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{Save: func(app.State) error { return nil },
		Clock: func() time.Time { return time.Date(2026, 9, 5, 10, 0, 0, 0, time.Local) },
	}}
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(api.actions) == 0 || api.actions[0] != "checkin:/checkin?sign=fresh" {
		t.Fatalf("签到未使用刷新后的 URL：%#v", api.actions)
	}
}

func TestEmptyPostsAreSkippedAndReported(t *testing.T) {
	api := &fakeAPI{posts: []weibo.Post{}}
	state := app.NewState()
	var output bytes.Buffer
	r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{Save: func(app.State) error { return nil },
		DryRun: true, Clock: func() time.Time { return time.Date(2026, 9, 5, 10, 0, 0, 0, time.Local) }, Output: &output,
	}}
	summary, err := r.Run(context.Background())
	if err == nil || summary.SkippedTopics != 1 {
		t.Fatalf("空帖子响应未标记为未完成：summary=%#v err=%v", summary, err)
	}
	if !strings.Contains(output.String(), "0 条可用") || !strings.Contains(output.String(), "--max-feed-pages") {
		t.Fatalf("空帖子输出不明确：\n%s", output.String())
	}
}
func (f *fakeAPI) Comment(_ context.Context, mid, content, st string) (string, error) {
	f.commentIndex++
	f.actions = append(f.actions, "comment:"+mid)
	if f.emptyCommentID {
		return "", nil
	}
	return "c" + mid, nil
}
func (f *fakeAPI) DeleteComment(_ context.Context, id, st string) error {
	f.actions = append(f.actions, "delete-comment:"+id)
	return f.deleteErr
}
func (f *fakeAPI) Repost(_ context.Context, mid, st string) (string, error) {
	f.repostIndex++
	f.actions = append(f.actions, "repost:"+mid)
	return "r" + mid, nil
}
func (f *fakeAPI) DeleteRepost(_ context.Context, id, st string) error {
	f.actions = append(f.actions, "delete-repost:"+id)
	return f.deleteErr
}

func testConfig() app.Config {
	cfg := app.DefaultConfig()
	cfg.CommentLimit = 2
	cfg.RepostLimit = 1
	cfg.VisibleDelayMin, cfg.VisibleDelayMax = 0, 0
	cfg.ActionDelayMin, cfg.ActionDelayMax = 0, 0
	cfg.TopicDelayMin, cfg.TopicDelayMax = 0, 0
	return cfg
}

func TestRunCreatesDeletesAndRecordsHistory(t *testing.T) {
	api := &fakeAPI{}
	state := app.NewState()
	var saved int
	var output bytes.Buffer
	r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{
		Clock: func() time.Time { return time.Date(2026, 9, 5, 10, 0, 0, 0, time.Local) }, Output: &output,
		Save: func(app.State) error { saved++; return nil },
	}}
	summary, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Checkins != 1 || summary.Comments != 2 || summary.Reposts != 1 || summary.Deleted != 3 {
		t.Fatalf("汇总错误: %#v", summary)
	}
	history := state.Topic("2026-09-05", "topic")
	if len(history.CommentedPostIDs) != 2 || len(history.RepostedPostIDs) != 1 {
		t.Fatalf("历史错误: %#v", state)
	}
	if len(state.PendingDeletes) != 0 {
		t.Fatalf("删除成功后仍存在待删除记录: %#v", state.PendingDeletes)
	}
	if saved != 11 {
		t.Fatalf("保存次数为 %d，期望 11（包含三次创建前记录）", saved)
	}
	text := output.String()
	for _, expected := range []string{
		"签到状态：未签到；正在刷新签到凭证",
		"签到结果：成功",
		"评论 [1/2]：已创建",
		"评论 [1/2]：已删除",
		"转发 [1/1]：已创建",
		"转发 [1/1]：已删除",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("执行输出缺少 %q：\n%s", expected, text)
		}
	}
}

func TestDeleteFailureStopsQueue(t *testing.T) {
	api := &fakeAPI{deleteErr: errors.New("delete failed")}
	state := app.NewState()
	r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{Save: func(app.State) error { return nil },
		Clock: func() time.Time { return time.Date(2026, 9, 5, 10, 0, 0, 0, time.Local) },
	}}
	_, err := r.Run(context.Background())
	if err == nil {
		t.Fatal("删除失败时未返回错误")
	}
	if api.commentIndex != 1 || api.repostIndex != 0 {
		t.Fatalf("删除失败后仍继续了队列: comments=%d reposts=%d", api.commentIndex, api.repostIndex)
	}
	if len(state.Topic("2026-09-05", "topic").CommentedPostIDs) != 1 {
		t.Fatal("创建成功的评论未立即记账")
	}
	if len(state.PendingDeletes) != 1 || state.PendingDeletes[0].ContentID != "c1" {
		t.Fatalf("删除失败未保留待删除记录：%#v", state.PendingDeletes)
	}
}

func TestPendingDeleteIsRetriedBeforeNewWrites(t *testing.T) {
	api := &fakeAPI{}
	state := app.NewState()
	state.AddPendingDelete(app.PendingDelete{Kind: "comment", ContentID: "old", TopicID: "topic"})
	r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{Save: func(app.State) error { return nil },
		Clock: func() time.Time { return time.Date(2026, 9, 5, 10, 0, 0, 0, time.Local) },
	}}
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(api.actions) < 2 || api.actions[0] != "delete-comment:old" {
		t.Fatalf("未优先恢复删除：%#v", api.actions)
	}
	if len(state.PendingDeletes) != 0 {
		t.Fatalf("恢复后仍有待删除记录：%#v", state.PendingDeletes)
	}
}

func TestPendingDeleteFailurePreventsNewInteraction(t *testing.T) {
	api := &fakeAPI{deleteErr: errors.New("still public")}
	state := app.NewState()
	state.AddPendingDelete(app.PendingDelete{Kind: "repost", ContentID: "old", TopicID: "topic"})
	r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{Save: func(app.State) error { return nil }, Clock: time.Now}}
	if _, err := r.Run(context.Background()); err == nil {
		t.Fatal("待删除恢复失败未停止")
	}
	if api.commentIndex != 0 || api.repostIndex != 0 {
		t.Fatalf("清理失败后仍创建互动：%#v", api.actions)
	}
}

func TestMissingCreatedIDRecordsProgressAndRequiresManualCheck(t *testing.T) {
	api := &fakeAPI{emptyCommentID: true}
	state := app.NewState()
	r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{Save: func(app.State) error { return nil },
		Clock: func() time.Time { return time.Date(2026, 9, 5, 10, 0, 0, 0, time.Local) },
	}}
	_, err := r.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "人工检查") {
		t.Fatalf("缺少 ID 的错误不明确：%v", err)
	}
	if len(state.Topic("2026-09-05", "topic").CommentedPostIDs) != 1 || len(state.PendingDeletes) != 1 {
		t.Fatalf("缺少 ID 时状态错误：%#v", state)
	}
}

func TestUnknownCheckinStateMakesRunIncomplete(t *testing.T) {
	api := &fakeAPI{followed: [][]weibo.Topic{{{ID: "topic", Name: "测试", ButtonName: "去看看"}}}}
	state := app.NewState()
	r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{Save: func(app.State) error { return nil }, DryRun: true, Clock: time.Now}}
	if _, err := r.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "无法确认签到") {
		t.Fatalf("签到状态未知未标记未完成：%v", err)
	}
}

func TestDryRunHasNoSideEffects(t *testing.T) {
	api := &fakeAPI{}
	state := app.NewState()
	var output bytes.Buffer
	r := Runner{API: api, Config: testConfig(), State: &state, Options: Options{Save: func(app.State) error { return nil },
		DryRun: true, Clock: func() time.Time { return time.Date(2026, 9, 5, 10, 0, 0, 0, time.Local) }, Output: &output,
	}}
	summary, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(api.actions) != 0 || summary.Checkins != 1 || summary.Comments != 2 || summary.Reposts != 1 {
		t.Fatalf("只读计划产生了副作用或汇总错误: %#v %#v", api.actions, summary)
	}
	text := output.String()
	for _, expected := range []string{
		"只读预演：本次不会向微博发送签到、评论、转发或删除请求。",
		"签到状态：未签到；正式运行时将执行签到",
		"候选帖子：6 条；剩余任务目标：评论 2 条，转发 1 条；已分配不同帖子 3 条",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("预演输出缺少 %q：\n%s", expected, text)
		}
	}
}

func TestAcquireLockRejectsActiveAndRecoversStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.lock")
	release, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLock(path); err == nil {
		t.Fatal("活跃锁未阻止并发")
	}
	release()
	if err := os.WriteFile(path, []byte("stale 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-16 * time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	release, err = AcquireLock(path)
	if err != nil {
		t.Fatalf("过期锁未自动恢复：%v", err)
	}
	release()
}
