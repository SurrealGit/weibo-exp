package runner

import (
	"testing"

	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

func TestSelectionAndTemplateAreStable(t *testing.T) {
	posts := []weibo.Post{{MID: "1"}, {MID: "2", IsSelf: true}, {MID: "3"}, {MID: "4"}}
	selected := selectPosts(posts, []string{"1"}, 2)
	if len(selected) != 2 || selected[0].MID != "3" || selected[1].MID != "4" {
		t.Fatalf("帖子选择错误: %#v", selected)
	}
	a := chooseTemplate("3", "2026-09-05:topic", []string{"打卡", "tt", "[哇]"})
	b := chooseTemplate("3", "2026-09-05:topic", []string{"打卡", "tt", "[哇]"})
	if a == "" || a != b {
		t.Fatalf("评论模板选择不稳定: %q %q", a, b)
	}
}
