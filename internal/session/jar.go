package session

import (
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/storage"
)

type StoredCookie struct {
	HostOnly bool   `json:"host_only,omitempty"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Expires  int64  `json:"expires,omitempty"`
	Secure   bool   `json:"secure,omitempty"`
	HTTPOnly bool   `json:"http_only,omitempty"`
}

type File struct {
	Version   int            `json:"version"`
	UpdatedAt time.Time      `json:"updated_at"`
	Cookies   []StoredCookie `json:"cookies"`
}

type Jar struct {
	mu      sync.Mutex
	inner   http.CookieJar
	records map[string]StoredCookie
}

func New(cookies []StoredCookie) (*Jar, error) {
	inner, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	jar := &Jar{inner: inner, records: make(map[string]StoredCookie)}
	for _, stored := range cookies {
		if stored.Name == "" || stored.Domain == "" {
			continue
		}
		domain := strings.TrimPrefix(stored.Domain, ".")
		scheme := "http"
		if stored.Secure {
			scheme = "https"
		}
		u := &url.URL{Scheme: scheme, Host: domain, Path: "/"}
		cookie := &http.Cookie{
			Name: stored.Name, Value: stored.Value, Domain: stored.Domain,
			Path: stored.Path, Secure: stored.Secure, HttpOnly: stored.HTTPOnly,
		}
		if stored.HostOnly {
			cookie.Domain = ""
		}
		if stored.Expires > 0 {
			cookie.Expires = time.Unix(stored.Expires, 0)
			if cookie.Expires.Before(time.Now()) {
				continue
			}
		}
		inner.SetCookies(u, []*http.Cookie{cookie})
		jar.records[key(stored)] = stored
	}
	return jar, nil
}

func Empty() (*Jar, error) { return New(nil) }

func (j *Jar) Cookies(u *url.URL) []*http.Cookie {
	return j.inner.Cookies(u)
}

func (j *Jar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if u.Scheme != "http" && u.Scheme != "https" {
		return
	}
	j.inner.SetCookies(u, cookies)
	for _, cookie := range cookies {
		if cookie.Valid() != nil {
			continue
		}
		domain := strings.ToLower(strings.TrimPrefix(cookie.Domain, "."))
		host := strings.ToLower(u.Hostname())
		if domain != "" && domain != host && !strings.HasSuffix(host, "."+domain) {
			continue
		}
		domain = cookie.Domain
		if domain == "" {
			domain = u.Hostname()
		}
		path := cookie.Path
		if path == "" || path[0] != '/' {
			path = "/"
			if index := strings.LastIndex(u.Path, "/"); index > 0 {
				path = u.Path[:index]
			}
		}
		stored := StoredCookie{
			Name: cookie.Name, Value: cookie.Value, Domain: strings.ToLower(strings.TrimPrefix(domain, ".")), Path: path, HostOnly: cookie.Domain == "",
			Secure: cookie.Secure, HTTPOnly: cookie.HttpOnly,
		}
		if !cookie.Expires.IsZero() {
			stored.Expires = cookie.Expires.Unix()
		}
		if cookie.MaxAge > 0 {
			stored.Expires = time.Now().Add(time.Duration(cookie.MaxAge) * time.Second).Unix()
		}
		cookieKey := key(stored)
		if cookie.MaxAge < 0 || (stored.Expires > 0 && stored.Expires <= time.Now().Unix()) {
			delete(j.records, cookieKey)
			continue
		}
		j.records[cookieKey] = stored
	}
}

func (j *Jar) Snapshot() []StoredCookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	result := make([]StoredCookie, 0, len(j.records))
	for _, cookie := range j.records {
		result = append(result, cookie)
	}
	return result
}

func (j *Jar) Has(name string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, cookie := range j.records {
		if cookie.Name == name && cookie.Value != "" && (cookie.Expires == 0 || cookie.Expires > time.Now().Unix()) {
			return true
		}
	}
	return false
}

func Load(path string) (*Jar, error) {
	var file File
	if err := storage.ReadJSON(path, &file); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Empty()
		}
		return nil, err
	}
	return New(file.Cookies)
}

func Save(path string, jar *Jar) error {
	return storage.WriteJSON(path, File{
		Version: 2, UpdatedAt: time.Now(), Cookies: jar.Snapshot(),
	}, 0o600)
}

func key(cookie StoredCookie) string {
	return strings.ToLower(strings.TrimPrefix(cookie.Domain, ".")) + "\x00" + cookie.Path + "\x00" + cookie.Name
}
