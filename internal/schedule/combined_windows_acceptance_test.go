//go:build windows

package schedule

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/SurrealGit/weibo-exp/internal/install"
	"github.com/SurrealGit/weibo-exp/internal/session"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

// Explicit desktop-only acceptance. Uses real Windows APIs but synthetic login
// data and a task action which only writes a local event. Never calls Weibo.
func TestNativeWindowsCombinedLifecycle(t *testing.T) {
	if os.Getenv("WEIBO_TEST_NATIVE_WINDOWS_COMBINED") != "1" {
		t.Skip("opt-in desktop test: WEIBO_TEST_NATIVE_WINDOWS_COMBINED=1")
	}
	var sessionID uint32
	ok, _, err := syscall.NewLazyDLL("kernel32.dll").NewProc("ProcessIdToSessionId").Call(uintptr(os.Getpid()), uintptr(unsafe.Pointer(&sessionID)))
	if ok == 0 || sessionID == 0 {
		t.Fatalf("requires interactive desktop session: session=%d error=%v", sessionID, err)
	}
	stage := func(name string) {
		t.Logf("%s stage=%s session=%d", time.Now().Format(time.RFC3339Nano), name, sessionID)
	}
	dir := t.TempDir()
	source := os.Getenv("WEIBO_TEST_WINDOWS_PROBE")
	stage("install-and-path")
	r, err := install.Install(source, filepath.Join(dir, "program"), filepath.Join(dir, "data"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stage("remove-path")
		if err := install.RemovePath(r); err != nil {
			t.Errorf("PATH cleanup failed: %v", err)
		}
	})
	if r.Path == nil || !r.Path.WindowsAdded {
		t.Fatal("temporary installation did not register its own PATH entry")
	}
	if err := r.CheckOwned(); err != nil {
		t.Fatal(err)
	}
	stage("login-and-open-image")
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 64, 64))); err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/sso/signin":
			http.SetCookie(w, &http.Cookie{Name: "X-CSRF-TOKEN", Value: "fixture", Path: "/"})
			fmt.Fprint(w, `{}`)
		case "/sso/v2/qrcode/image":
			fmt.Fprintf(w, `{"retcode":20000000,"data":{"qrid":"fixture","image":%q}}`, server.URL+"/qr.png")
		case "/qr.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(picture.Bytes())
		case "/sso/v2/qrcode/check":
			fmt.Fprintf(w, `{"retcode":20000000,"data":{"url":%q}}`, server.URL+"/cross")
		case "/cross":
			http.SetCookie(w, &http.Cookie{Name: "SUB", Value: "fixture-not-a-real-session", Path: "/"})
			fmt.Fprint(w, `{}`)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()
	jar, err := session.Empty()
	if err != nil {
		t.Fatal(err)
	}
	opened := false
	var openErr error
	err = weibo.QRLogin(context.Background(), jar, weibo.QRLoginOptions{
		PassportURL: server.URL, RedirectURL: server.URL + "/", TempDir: filepath.Join(dir, "qr"),
		PollEvery: time.Second, Timeout: 15 * time.Second,
		OpenImage: func(path string) error {
			opened = true
			openErr = weibo.OpenImage(path)
			stage("image-open-returned")
			// Give the associated app time to read this synthetic PNG.
			time.Sleep(3 * time.Second)
			return openErr
		},
	})
	if err != nil || !opened || openErr != nil || !jar.Has("SUB") {
		t.Fatalf("mock login: err=%v opened=%v openErr=%v", err, opened, openErr)
	}
	if err := session.Save(filepath.Join(r.DataDir, "session.json"), jar); err != nil {
		t.Fatal(err)
	}
	stage("native-schedule")
	// Reuse the native lifecycle test; it rewrites only the task name, not the
	// production XML/import logic, and verifies least-privilege event execution.
	t.Setenv("WEIBO_TEST_NATIVE_WINDOWS", "1")
	t.Setenv("WEIBO_TEST_WINDOWS_PROBE", r.Executable)
	if !t.Run("task", TestNativeWindowsIsolatedLifecycle) {
		t.Fatal("native schedule lifecycle failed")
	}
	stage("complete")
	data, _ := json.Marshal(map[string]any{"session": sessionID, "synthetic_login": true, "native_task": true})
	t.Log(string(data))
}
