package session

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestPersistCookieScopePathAndMaxAge(t *testing.T) {
	origin, _ := url.Parse("https://example.test/dir/page")
	sub, _ := url.Parse("https://sub.example.test/dir/page")
	root, _ := url.Parse("https://example.test/")
	jar, _ := Empty()
	jar.SetCookies(origin, []*http.Cookie{{Name: "session", Value: "fake", Secure: true, MaxAge: 60}})
	loaded, err := New(jar.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Cookies(sub)) != 0 || len(loaded.Cookies(root)) != 0 || len(loaded.Cookies(origin)) != 1 {
		t.Fatal("cookie scope changed")
	}
	stored := jar.Snapshot()[0]
	if !stored.HostOnly || stored.Path != "/dir" || stored.Expires < time.Now().Unix()+55 {
		t.Fatalf("%+v", stored)
	}
}
func TestRejectedCookieNotResurrected(t *testing.T) {
	origin, _ := url.Parse("https://example.test/")
	jar, _ := Empty()
	jar.SetCookies(origin, []*http.Cookie{{Name: "x", Value: "x", Domain: "unrelated.test"}})
	if len(jar.Snapshot()) != 0 {
		t.Fatal("invalid domain persisted")
	}
}
func TestMaxAgeTakesPrecedenceOverExpires(t *testing.T) {
	u, _ := url.Parse("https://example.test/")
	jar, _ := Empty()
	jar.SetCookies(u, []*http.Cookie{{Name: "x", Value: "x", MaxAge: 60, Expires: time.Unix(1, 0)}})
	if len(jar.Snapshot()) != 1 || len(jar.Cookies(u)) != 1 {
		t.Fatal("MaxAge ignored")
	}
}
