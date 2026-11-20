package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/runner"
	"github.com/SurrealGit/weibo-exp/internal/session"
	"github.com/SurrealGit/weibo-exp/internal/storage"
)

func TestRenewalRetainsCandidateAcrossFailures(t *testing.T) {
	for _, phase := range []string{"verify", "redirect", "save"} {
		t.Run(phase, func(t *testing.T) {
			s := newRenewServer(t, map[string]string{"sub-new": "test"})
			s.mu.Lock()
			s.verifyFailure = phase == "verify"
			s.homeFailure = phase == "redirect"
			s.mu.Unlock()
			now := time.Now()
			jar := renewJar(t, s, "old", "alc-1", now.Add(-25*time.Hour))
			path := filepath.Join(t.TempDir(), "session.json")
			if err := session.Save(path, jar); err != nil {
				t.Fatal(err)
			}
			failSave := phase == "save"
			r := renewal{Now: now, Output: io.Discard, PendingPath: renewalPath(path), Save: func(j *session.Jar) error {
				if failSave {
					return errors.New("disk unavailable")
				}
				return session.Save(path, j)
			}}
			if got, err := ensureSession(context.Background(), renewClient(s), jar, app.State{UID: "test"}, r); err == nil || got != nil {
				t.Fatalf("must stop: %v", err)
			}
			info, err := os.Stat(r.PendingPath)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("candidate absent/insecure: %v", err)
			}
			saved, err := session.Load(path)
			if err != nil || jarCookie(saved, s.URL, "SUB") != "old" {
				t.Fatal("overwrote original session")
			}
			requests := s.passportRequests()
			s.mu.Lock()
			s.verifyFailure = false
			s.homeFailure = false
			s.mu.Unlock()
			failSave = false
			// Recover using only the on-disk original and checkpoint, as on restart.
			got, err := ensureSession(context.Background(), renewClient(s), saved, app.State{UID: "test"}, r)
			if err != nil || jarCookie(got, s.URL, "ALC") != "alc-2" {
				t.Fatalf("recovery failed: %v", err)
			}
			if s.passportRequests() != requests {
				t.Fatal("reused consumed credential")
			}
			if _, err := os.Stat(r.PendingPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("checkpoint not removed")
			}
		})
	}
}

func TestRenewalFailureDoesNotReachBusinessTasks(t *testing.T) {
	s := newRenewServer(t, map[string]string{"sub-new": "test"})
	paths := scheduledRenewFixture(t, s, "old", "alc-1", time.Time{})
	s.mu.Lock()
	s.verifyFailure = true
	s.mu.Unlock()
	var called atomic.Bool
	s.setOnTasks(func() { called.Store(true) })
	cfg, _ := app.LoadConfig(paths.Config)
	_, err := executeRun(context.Background(), paths, cfg, renewClient(s), runner.Options{Clock: time.Now, Output: io.Discard})
	if err == nil || called.Load() {
		t.Fatalf("ran tasks after failed renewal: %v", err)
	}
	// An unwritable checkpoint must stop before subsequent redirects/tasks too.
	if err := os.Remove(renewalPath(paths.Session)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(renewalPath(paths.Session), 0700); err != nil {
		t.Fatal(err)
	}
	_, err = executeRun(context.Background(), paths, cfg, renewClient(s), runner.Options{Clock: time.Now, Output: io.Discard})
	if err == nil || called.Load() {
		t.Fatal("ignored checkpoint read error")
	}
}

func TestRenewalCandidateAccountAndMalformedProtection(t *testing.T) {
	s := newRenewServer(t, map[string]string{"sub-new": "other"})
	jar := renewJar(t, s, "old", "alc-1", time.Time{})
	path := filepath.Join(t.TempDir(), "pending.json")
	r := renewal{Now: time.Now(), Output: io.Discard, PendingPath: path, Save: func(*session.Jar) error { t.Fatal("promoted other account"); return nil }}
	_, err := ensureSession(context.Background(), renewClient(s), jar, app.State{UID: "test"}, r)
	if err == nil {
		t.Fatal("accepted other account")
	}
	requests := s.passportRequests()
	_, err = ensureSession(context.Background(), renewClient(s), jar, app.State{UID: "test"}, r)
	if err == nil || s.passportRequests() != requests {
		t.Fatal("did not protect resumed identity")
	}
	if err := storage.WriteJSON(path, pendingRenewal{Version: 9}, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = ensureSession(context.Background(), renewClient(s), jar, app.State{UID: "test"}, r)
	if err == nil || !strings.Contains(err.Error(), "格式无效") {
		t.Fatalf("%v", err)
	}
}
