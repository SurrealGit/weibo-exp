package runner

import (
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

func chooseTemplate(postID, dateAndTopic string, templates []string) string {
	if len(templates) == 0 {
		return ""
	}
	var hash uint32 = 2166136261
	for _, r := range dateAndTopic + ":" + postID {
		hash ^= uint32(r)
		hash *= 16777619
	}
	return templates[int(hash%uint32(len(templates)))]
}

func selectPosts(posts []weibo.Post, completed []string, limit int) []weibo.Post {
	if limit <= 0 {
		return nil
	}
	done := make(map[string]bool, len(completed))
	for _, id := range completed {
		done[id] = true
	}
	result := make([]weibo.Post, 0, limit)
	for _, post := range posts {
		if post.MID == "" || post.IsSelf || done[post.MID] {
			continue
		}
		result = append(result, post)
		if len(result) == limit {
			break
		}
	}
	return result
}
