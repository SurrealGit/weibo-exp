package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDefaultInstallationDirectoryName(t *testing.T) {
	dir, err := DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "weibo-exp" {
		t.Fatalf("inconsistent installation directory: %s", dir)
	}
}

func TestRemoveEmptyNestedDirectories(t *testing.T) {
	root := testRoot(t)
	parent := filepath.Join(root, "data")
	child := filepath.Join(parent, "program")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	if remaining := RemoveEmpty([]string{parent, child, child}); len(remaining) != 0 {
		t.Fatalf("%v", remaining)
	}
	if _, err := os.Stat(parent); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%v", err)
	}
}

func TestDefaultDirectoryIsUserScoped(t *testing.T) {
	dir, err := DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(dir) || dir == filepath.Dir(dir) {
		t.Fatalf("%s", dir)
	}
}

func TestCustomIntermediateDirectoryOwnership(t *testing.T) {
	root := testRoot(t)
	source := filepath.Join(root, "download")
	if err := os.WriteFile(source, []byte("program"), 0700); err != nil {
		t.Fatal(err)
	}
	r, err := Copy(source, filepath.Join(root, "new", "nested", "program"), filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.CreatedDirs) != 3 {
		t.Fatalf("%v", r.CreatedDirs)
	}
	if err := r.CheckOwned(); err != nil {
		t.Fatal(err)
	}
	if err := RemoveFiles([]string{r.Executable, r.Sidecar()}); err != nil {
		t.Fatal(err)
	}
	if remaining := RemoveEmpty(r.CreatedDirs); len(remaining) != 0 {
		t.Fatalf("%v", remaining)
	}
	if _, err := os.Stat(filepath.Join(root, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("intermediate directory remains")
	}
}

func TestInstallUpgradeAndDataDiscovery(t *testing.T) {
	root := testRoot(t)
	source := filepath.Join(root, "download")
	dir, data := filepath.Join(root, "custom location"), filepath.Join(root, "data")
	if err := os.WriteFile(source, []byte("version one"), 0700); err != nil {
		t.Fatal(err)
	}
	r, err := Copy(source, dir, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CheckOwned(); err != nil {
		t.Fatal(err)
	}
	if resolved, err := DataDirForExecutable(r.Executable); err != nil || resolved != data {
		t.Fatalf("%s %v", resolved, err)
	}
	if err := os.WriteFile(filepath.Join(data, "config.json"), []byte("user config"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("version two"), 0700); err != nil {
		t.Fatal(err)
	}
	r, err = Copy(source, dir, data)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(r.Executable)
	if err != nil || string(content) != "version two" {
		t.Fatalf("%s %v", content, err)
	}
	content, _ = os.ReadFile(filepath.Join(data, "config.json"))
	if string(content) != "user config" {
		t.Fatal("upgrade changed user data")
	}
	if len(r.CreatedDirs) != 1 {
		t.Fatal("upgrade lost directory ownership")
	}
}

func TestInstallRejectsUnownedCollisionAndSymlink(t *testing.T) {
	root := testRoot(t)
	source := filepath.Join(root, "download")
	if err := os.WriteFile(source, []byte("program"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, EntryName()), []byte("unrelated"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Copy(source, root, filepath.Join(root, "data")); err == nil {
		t.Fatal("overwrote unowned binary")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(source, link); err == nil {
		if err := RemoveFiles([]string{link}); err == nil {
			t.Fatal("removed symlink")
		}
	}
}

func TestDeletionPreflightAndEmptyDirectoryPolicy(t *testing.T) {
	root := testRoot(t)
	file := filepath.Join(root, "owned")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveFiles([]string{file, root}); err == nil {
		t.Fatal("accepted directory deletion")
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("partial deletion before preflight")
	}
	if remaining := RemoveEmpty([]string{root}); len(remaining) != 1 {
		t.Fatal("removed nonempty directory")
	}
}

func TestTamperedExecutableCannotBeUninstalled(t *testing.T) {
	root := testRoot(t)
	source := filepath.Join(root, "download")
	os.WriteFile(source, []byte("original"), 0700)
	r, err := Copy(source, filepath.Join(root, "bin"), filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(r.Executable, []byte("replacement"), 0700)
	if err := r.CheckOwned(); err == nil {
		t.Fatal("accepted unknown replacement")
	}
}

func TestWindowsCleanupIsLiteralAndNonRecursive(t *testing.T) {
	root := testRoot(t)
	source := filepath.Join(root, "download")
	os.WriteFile(source, []byte("program"), 0700)
	r, err := Copy(source, filepath.Join(root, "quotes ' $ & 目录"), filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	script, err := WindowsCleanupScript(r, []string{r.Executable, r.Sidecar()}, r.CreatedDirs, 12345)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, "-Recurse") || strings.Contains(script, "quotes '") || !strings.Contains(script, "Wait-Process -Id 12345") || !strings.Contains(script, "Get-FileHash -LiteralPath") {
		t.Fatalf("unsafe cleanup script: %s", script)
	}
	if !strings.Contains(script, "$guard.WaitOne(0)") ||
		strings.Index(script, "$guard.WaitOne(0)") > strings.Index(script, "Remove-Item") ||
		!strings.Contains(script, "$guard.ReleaseMutex()") {
		t.Fatal("cleanup is not protected by the native mutex")
	}
	if !strings.Contains(script, "$ProgressPreference = 'SilentlyContinue'") || !strings.Contains(script, "Write-Error") || !strings.Contains(script, "exit 1") {
		t.Fatal("cleanup must suppress progress but retain errors")
	}
}
