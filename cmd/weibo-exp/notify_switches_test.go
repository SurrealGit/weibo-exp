package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/mailnotify"
	"github.com/SurrealGit/weibo-exp/internal/storage"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

func TestNotificationTypeSwitches(t *testing.T) {
	for _, initialErrors := range []bool{false, true} {
		for _, initialSuccess := range []bool{false, true} {
			for _, command := range []string{"enable", "disable"} {
				for _, selection := range [][]string{nil, {"--errors"}, {"--success"}, {"--errors", "--success"}} {
					t.Run(fmt.Sprintf("%v/%v/%s/%v", initialErrors, initialSuccess, command, selection), func(t *testing.T) {
						paths, cfg := mailFixture(t)
						cfg.ErrorsEnabled, cfg.SuccessEnabled = initialErrors, initialSuccess
						path := filepath.Join(paths.DataDir, mailnotify.ConfigFile)
						if err := storage.WriteJSON(path, cfg, 0600); err != nil {
							t.Fatal(err)
						}
						mockMail(t, func(context.Context, mailnotify.Config, string, string) error {
							t.Fatal("switch sent mail")
							return nil
						})
						var output bytes.Buffer
						if err := notifyCommand(context.Background(), paths, append([]string{command}, selection...), &output, io.Discard); err != nil {
							t.Fatal(err)
						}
						want := cfg
						if len(selection) != 1 || selection[0] == "--errors" {
							want.ErrorsEnabled = command == "enable"
						}
						if len(selection) != 1 || selection[0] == "--success" {
							want.SuccessEnabled = command == "enable"
						}
						got, err := loadMailConfig(path)
						if err != nil || got != want {
							t.Fatalf("unexpected switch state: errors=%v success=%v err=%v", got.ErrorsEnabled, got.SuccessEnabled, err)
						}
						var expected bytes.Buffer
						printNotificationSwitches(&expected, want)
						if output.String() != expected.String() {
							t.Fatal(output.String())
						}
						output.Reset()
						if err := notifyCommand(context.Background(), paths, []string{"status"}, &output, io.Discard); err != nil || !strings.Contains(output.String(), expected.String()) {
							t.Fatalf("status does not show both switches: %v\n%s", err, &output)
						}
						status := "disabled"
						if want.ErrorsEnabled || want.SuccessEnabled {
							status = "enabled"
						}
						if mailNotificationStatus(paths) != status {
							t.Fatal("incorrect summary status")
						}
					})
				}
			}
		}
	}
}

func TestNotificationSelectionAndDedup(t *testing.T) {
	for _, enabled := range [][]string{nil, {"--errors"}, {"--success"}, {"--errors", "--success"}} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			paths, _ := mailFixture(t)
			ctx, now := context.Background(), time.Now()
			counts := map[string]int{}
			mockMail(t, func(_ context.Context, _ mailnotify.Config, subject, _ string) error { counts[subject]++; return nil })
			call := func(args ...string) {
				t.Helper()
				if err := notifyCommand(ctx, paths, args, io.Discard, io.Discard); err != nil {
					t.Fatal(err)
				}
			}
			call("disable")
			if len(enabled) > 0 {
				call(append([]string{"enable"}, enabled...)...)
			}
			check := func() {
				notifyScheduledFailure(ctx, paths, weibo.ErrNotLoggedIn, io.Discard, now)
				notifyScheduledSuccess(ctx, paths, 2, io.Discard, io.Discard, now)
			}
			check()
			want := len(enabled)
			if len(counts) != want {
				t.Fatalf("sent %d categories, want %d", len(counts), want)
			}
			// Enabling the other category may send its first mail, but must not
			// resend an already-notified category due to a changed config hash.
			call("enable")
			check()
			for _, flag := range []string{"--errors", "--success"} {
				call("disable", flag)
				check()
				call("enable", flag)
				check()
			}
			if len(counts) != 2 {
				t.Fatal(counts)
			}
			for _, count := range counts {
				if count != 1 {
					t.Fatalf("switch reset dedup: %v", counts)
				}
			}
		})
	}
}

func TestLegacyMailConfigAndDedupUpgrade(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			paths, _ := mailFixture(t)
			path := filepath.Join(paths.DataDir, mailnotify.ConfigFile)
			// Exact v0.8.0 field order and values, independent of the new encoder.
			legacy := []byte(fmt.Sprintf(`{"enabled":%t,"host":"smtp.example.com","port":465,"security":"tls","username":"sender@example.com","password":"secret-password","from":"sender@example.com","to":"recipient@example.com"}`, enabled))
			if err := os.WriteFile(path, legacy, 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadMailConfig(path)
			if err != nil || cfg.ErrorsEnabled != enabled || cfg.SuccessEnabled != enabled {
				t.Fatalf("legacy switches not preserved: %v", err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(after, legacy) {
				t.Fatal("read rewrote legacy config")
			}
			now := time.Now()
			state := notificationState{ConfigHash: fmt.Sprintf("%x", sha256.Sum256(legacy)), Sent: map[string]string{"login": app.Today(now), "success": app.Today(now)}}
			if err := storage.WriteJSON(filepath.Join(paths.DataDir, mailnotify.StateFile), state, 0600); err != nil {
				t.Fatal(err)
			}
			mockMail(t, func(context.Context, mailnotify.Config, string, string) error {
				t.Fatal("upgrade resent today's mail")
				return nil
			})
			for _, args := range [][]string{{"disable", "--success"}, {"enable"}} {
				if err := notifyCommand(context.Background(), paths, args, io.Discard, io.Discard); err != nil {
					t.Fatal(err)
				}
			}
			after, _ = os.ReadFile(path)
			if bytes.Contains(after, []byte(`"enabled"`)) || !bytes.Contains(after, []byte(`"errors_enabled"`)) || !bytes.Contains(after, []byte(`"success_enabled"`)) {
				t.Fatal("configuration not migrated to independent switches")
			}
			notifyScheduledFailure(context.Background(), paths, weibo.ErrNotLoggedIn, io.Discard, now)
			notifyScheduledSuccess(context.Background(), paths, 2, io.Discard, io.Discard, now)
		})
	}
}

func TestNotifyInvalidSelectionDoesNotModifyConfig(t *testing.T) {
	paths, _ := mailFixture(t)
	path := filepath.Join(paths.DataDir, mailnotify.ConfigFile)
	before, _ := os.ReadFile(path)
	for _, args := range [][]string{{"enable", "--errors=false"}, {"disable", "--success=false", "--errors"}, {"enable", "--error"}, {"status", "--errors"}, {"test", "--success"}} {
		if err := notifyCommand(context.Background(), paths, args, io.Discard, io.Discard); err == nil {
			t.Fatalf("invalid selection accepted: %v", args)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatal("invalid selection changed configuration")
		}
	}
}

func TestNotifyImportPlainSMTPConfig(t *testing.T) {
	paths, _ := mailFixture(t)
	path := filepath.Join(paths.DataDir, mailnotify.ConfigFile)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "smtp.json")
	data := []byte(`{"host":"smtp.example.com","port":465,"security":"ssl","username":"sender@example.com","password":"secret-password","from":"sender@example.com","to":"recipient@example.com"}`)
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	mockMail(t, func(context.Context, mailnotify.Config, string, string) error {
		t.Fatal("import sent mail")
		return nil
	})
	if err := notifyCommand(context.Background(), paths, []string{"configure", "--file", source}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadMailConfig(path)
	if err != nil || cfg.ErrorsEnabled || cfg.SuccessEnabled || cfg.Host != "smtp.example.com" {
		t.Fatalf("plain import failed or enabled notifications: %v", err)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(after, data) {
		t.Fatal("import modified the user's source file")
	}
}

func TestNotificationSwitchesLeaveTaskResultUnchanged(t *testing.T) {
	paths, _ := mailFixture(t)
	if err := notifyCommand(context.Background(), paths, []string{"disable", "--errors"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	mockMail(t, func(context.Context, mailnotify.Config, string, string) error {
		t.Fatal("disabled error notification sent")
		return nil
	})
	// Invalid business configuration fails before any Weibo requests are made.
	if err := os.WriteFile(paths.Config, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := scheduledCommand(context.Background(), paths, "https://example.invalid"); err == nil {
		t.Fatal("disabled notifications swallowed the task failure")
	}
	if _, err := os.Stat(filepath.Join(paths.DataDir, mailnotify.StateFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("disabled notification wrote dedup state")
	}
}
