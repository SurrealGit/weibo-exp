package session

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
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
}
