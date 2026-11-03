package session

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestJarRoundTrip(t *testing.T) {
	jar, err := Empty()
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("https://m.weibo.cn/")
	jar.SetCookies(u, []*http.Cookie{{Name: "SUB", Value: "secret", Domain: ".weibo.cn", Path: "/", Secure: true, HttpOnly: true}})
	path := filepath.Join(t.TempDir(), "session.json")
	if err := Save(path, jar); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("会话文件权限为 %o，期望 600", info.Mode().Perm())
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Has("SUB") || len(loaded.Cookies(u)) != 1 {
		t.Fatalf("会话 Cookie 未正确恢复: %#v", loaded.Snapshot())
	}
	if !loaded.RenewedAt().IsZero() {
		t.Fatalf("未设置续期时间时应读出零值，实际为 %v", loaded.RenewedAt())
	}
}

func TestJarRenewedAtRoundTrip(t *testing.T) {
	jar, err := Empty()
	if err != nil {
		t.Fatal(err)
	}
	renewedAt := time.Unix(1793625600, 0)
	jar.SetRenewedAt(renewedAt)
	path := filepath.Join(t.TempDir(), "session.json")
	if err := Save(path, jar); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.RenewedAt().Equal(renewedAt) {
		t.Fatalf("续期时间为 %v，期望 %v", loaded.RenewedAt(), renewedAt)
	}
}

func TestLoadLegacyFileWithoutRenewedAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	legacy := `{"version":2,"updated_at":"2026-10-22T15:00:00+08:00","cookies":[{"name":"SUB","value":"secret","domain":"weibo.cn","path":"/","secure":true}]}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Has("SUB") || !loaded.RenewedAt().IsZero() {
		t.Fatalf("旧会话文件读取结果不正确: renewedAt=%v cookies=%#v", loaded.RenewedAt(), loaded.Snapshot())
	}
}
