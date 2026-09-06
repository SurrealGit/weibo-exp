package schedule

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdOfflineWithoutManagedFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Executable: filepath.Join(dir, "weibo-exp"), DataDir: dir, SystemdService: filepath.Join(dir, "weibo-exp.service"), SystemdTimer: filepath.Join(dir, "weibo-exp.timer"), Hour: 10}
	fakePlatform(t, "linux", func(_ string, args ...string) ([]byte, error) {
		if len(args) > 1 && args[1] == "show-environment" {
			return []byte("Failed to connect to bus"), errors.New("exit 1")
		}
		t.Fatalf("unexpected offline mutation: %v", args)
		return nil, nil
	})
	status, err := Inspect(cfg)
	if err != nil || status.Registered || status.FileExists {
		t.Fatalf("absent task: %+v %v", status, err)
	}
	if err := Uninstall(cfg); err != nil {
		t.Fatal(err)
	}
	if err := Install(cfg); err == nil {
		t.Fatal("installation must require user manager")
	}
	if err := os.WriteFile(cfg.SystemdService, []byte("existing service"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(cfg); err == nil {
		t.Fatal("existing units must not be treated as absent")
	}
	if err := Uninstall(cfg); err == nil {
		t.Fatal("existing units removed while manager offline")
	}
	if _, err := os.Stat(cfg.SystemdService); err != nil {
		t.Fatal("existing file lost")
	}
}

func TestSystemdUninstallStopsServiceBeforeRemovingFiles(t *testing.T) {
	for _, failure := range []string{"", "stop", "clean"} {
		t.Run("failure="+failure, func(t *testing.T) {
			dir := t.TempDir()
			cfg := Config{SystemdService: filepath.Join(dir, "weibo-exp.service"), SystemdTimer: filepath.Join(dir, "weibo-exp.timer")}
			for _, p := range []string{cfg.SystemdService, cfg.SystemdTimer} {
				if err := os.WriteFile(p, []byte("unit"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var calls []string
			fakePlatform(t, "linux", func(_ string, args ...string) ([]byte, error) {
				calls = append(calls, strings.Join(args, " "))
				switch args[1] {
				case "is-enabled":
					return []byte("enabled"), nil
				case "is-active":
					return []byte("active"), nil
				case "stop":
					for _, p := range []string{cfg.SystemdService, cfg.SystemdTimer} {
						if _, err := os.Stat(p); err != nil {
							t.Fatal("files removed before service stopped")
						}
					}
					if failure == "stop" {
						return []byte("stop failed"), errors.New("exit 1")
					}
				case "clean":
					if failure == "clean" {
						return []byte("clean failed"), errors.New("exit 1")
					}
				case "show":
					return []byte("loaded"), nil
				}
				return nil, nil
			})
			err := Uninstall(cfg)
			if (err != nil) != (failure != "") {
				t.Fatalf("uninstall: %v", err)
			}
			text := strings.Join(calls, "\n")
			if strings.Index(text, "disable --now") > strings.Index(text, "stop weibo-exp.service") || !strings.Contains(text, "stop weibo-exp.service") {
				t.Fatalf("wrong stop order: %s", text)
			}
			if failure != "stop" && !strings.Contains(text, "clean --what=state weibo-exp.timer") {
				t.Fatal("persistent timer state not cleaned")
			}
			for _, p := range []string{cfg.SystemdService, cfg.SystemdTimer} {
				_, err := os.Stat(p)
				if failure != "" && err != nil || failure == "" && !os.IsNotExist(err) {
					t.Fatalf("unexpected file state: %s %v", p, err)
				}
			}
		})
	}
}
