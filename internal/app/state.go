package app

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/storage"
)

const (
	StateVersion         = 4
	HistoryRetentionDays = 7
)

type TopicHistory struct {
	CommentedPostIDs []string `json:"comments,omitempty"`
	RepostedPostIDs  []string `json:"reposts,omitempty"`
}

// PendingDelete is written before an interaction is deleted, so an interrupted
// run can finish cleanup before creating anything new.
type PendingDelete struct {
	Kind         string    `json:"kind"`
	ContentID    string    `json:"id"`
	TopicID      string    `json:"topic_id,omitempty"`
	SourcePostID string    `json:"source_post_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type State struct {
	UID            string                             `json:"uid,omitempty"`
	Version        int                                `json:"version"`
	History        map[string]map[string]TopicHistory `json:"history"`
	PendingDeletes []PendingDelete                    `json:"pending_deletes,omitempty"`
}

func NewState() State {
	return State{Version: StateVersion, History: make(map[string]map[string]TopicHistory)}
}

func LoadState(path string) (State, error) {
	state := NewState()
	if err := storage.ReadJSON(path, &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state, nil
		}
		return State{}, err
	}
	if state.History == nil {
		state.History = make(map[string]map[string]TopicHistory)
	}
	state.Version = StateVersion
	return state, nil
}

func SaveState(path string, state State) error {
	state.Version = StateVersion
	history := make(map[string]map[string]TopicHistory, len(state.History))
	for date, topics := range state.History {
		history[date] = topics
	}
	state.History = history
	state.PruneHistory(time.Now(), HistoryRetentionDays)
	return storage.WriteJSON(path, state, 0o600)
}

func (s *State) Topic(date, topicID string) TopicHistory {
	if s.History[date] == nil {
		return TopicHistory{}
	}
	return s.History[date][topicID]
}

func (s *State) Mark(date, topicID, kind, postID string) {
	if s.History == nil {
		s.History = make(map[string]map[string]TopicHistory)
	}
	if s.History[date] == nil {
		s.History[date] = make(map[string]TopicHistory)
	}
	history := s.History[date][topicID]
	if kind == "comment" {
		history.CommentedPostIDs = appendUnique(history.CommentedPostIDs, postID)
	} else if kind == "repost" {
		history.RepostedPostIDs = appendUnique(history.RepostedPostIDs, postID)
	}
	s.History[date][topicID] = history
}

func (s *State) AddPendingDelete(item PendingDelete) {
	for _, existing := range s.PendingDeletes {
		if existing.Kind == item.Kind && existing.ContentID == item.ContentID && (item.ContentID != "" || existing.SourcePostID == item.SourcePostID) {
			return
		}
	}
	s.PendingDeletes = append(s.PendingDeletes, item)
}

func (s *State) RemovePendingDelete(kind, id string) bool {
	for index, item := range s.PendingDeletes {
		if item.Kind == kind && item.ContentID == id {
			s.PendingDeletes = append(s.PendingDeletes[:index], s.PendingDeletes[index+1:]...)
			return true
		}
	}
	return false
}

func (s *State) PendingForTopic(topicID string) int {
	count := 0
	for _, item := range s.PendingDeletes {
		if item.TopicID == topicID {
			count++
		}
	}
	return count
}

// PruneHistory retains today and the preceding days in the caller's timezone.
func (s *State) PruneHistory(now time.Time, retentionDays int) {
	if retentionDays < 1 || len(s.History) == 0 {
		return
	}
	cutoff := now.AddDate(0, 0, -(retentionDays - 1)).Format("2006-01-02")
	for date := range s.History {
		if date < cutoff {
			delete(s.History, date)
		}
	}
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func (s *State) CheckAccount(uid string) error {
	if s.UID != "" && s.UID != uid {
		return fmt.Errorf("任务记录属于另一微博账户；请使用原账户，或通过 --data-dir 指定独立数据目录")
	}
	return nil
}

type TopicProgress struct {
	CommentsDone, RepostsDone           int
	CommentsRemaining, RepostsRemaining int
	Pending                             int
	Complete                            bool
}

func (s *State) Progress(date, topicID string, signed bool, cfg Config) TopicProgress {
	h := s.Topic(date, topicID)
	p := TopicProgress{
		CommentsDone:      min(len(h.CommentedPostIDs), cfg.CommentLimit),
		RepostsDone:       min(len(h.RepostedPostIDs), cfg.RepostLimit),
		CommentsRemaining: max(0, cfg.CommentLimit-len(h.CommentedPostIDs)),
		RepostsRemaining:  max(0, cfg.RepostLimit-len(h.RepostedPostIDs)),
		Pending:           s.PendingForTopic(topicID),
	}
	p.Complete = signed && p.CommentsRemaining == 0 && p.RepostsRemaining == 0 && p.Pending == 0
	return p
}
