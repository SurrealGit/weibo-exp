package install

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/SurrealGit/weibo-exp/internal/storage"
)

func pathFixture(t *testing.T, shell string) (root, source, dir, data string) {
	t.Helper()
	root = testRoot(t)
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("ZDOTDIR", "")
	if err := os.Unsetenv("ZDOTDIR"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("SHELL", "/bin/"+shell)
	oldPlatform, oldFind, oldRead, oldWrite := pathPlatform, findPathCommand, readUserPath, writeUserPath
	t.Cleanup(func() {
		pathPlatform, findPathCommand, readUserPath, writeUserPath = oldPlatform, oldFind, oldRead, oldWrite
	})
	pathPlatform = "linux"
	if runtime.GOOS == "windows" {
		pathPlatform = "windows"
	}
	value := userPathValue{}
	readUserPath = func() (userPathValue, error) { return value, nil }
	writeUserPath = func(v userPathValue) error { value = v; return nil }
	findPathCommand = func(string) (string, error) { return "", exec.ErrNotFound }
	source = filepath.Join(root, "download")
	dir, data = filepath.Join(root, "程序 space ' $value"), filepath.Join(root, "data")
	if err := os.WriteFile(source, []byte("program"), 0700); err != nil {
		t.Fatal(err)
	}
	return
}

func shellPathFixture(t *testing.T, shell string) (root, source, dir, data string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell profiles; Windows registry PATH has separate tests")
	}
	return pathFixture(t, shell)
}

func TestPathInstallUpgradeAndExactRemoval(t *testing.T) {
	for _, shell := range []string{"zsh", "bash", "fish"} {
		t.Run(shell, func(t *testing.T) {
			_, source, dir, data := shellPathFixture(t, shell)
			files, _, err := shellProfiles()
			if err != nil {
				t.Fatal(err)
			}
			original := "# user settings (no trailing newline)"
			if err := storage.WriteFileAtomic(files[0], []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			r, err := Install(source, dir, data, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.CheckOwned(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, []byte("upgrade"), 0700); err != nil {
				t.Fatal(err)
			}
			r, err = Install(source, dir, data, true)
			if err != nil {
				t.Fatal(err)
			}
			for _, profile := range r.Path.Profiles {
				b, err := os.ReadFile(profile.File)
				if err != nil || strings.Count(string(b), "# >>> weibo-exp PATH") != 1 {
					t.Fatalf("%q %v", b, err)
				}
			}
			// Later user edits outside the managed block must survive uninstall.
			b, _ := os.ReadFile(files[0])
			if err := os.WriteFile(files[0], append(b, []byte("\n# added later\n")...), 0600); err != nil {
				t.Fatal(err)
			}
			if err := RemovePath(r); err != nil {
				t.Fatal(err)
			}
			if err := RemovePath(r); err != nil {
				t.Fatal(err)
			}
			b, err = os.ReadFile(files[0])
			if err != nil || string(b) != original+"\n# added later\n" {
				t.Fatalf("user settings changed: %q %v", b, err)
			}
			info, _ := os.Stat(files[0])
			if info.Mode().Perm() != 0600 {
				t.Fatal("profile permissions changed")
			}
			for _, file := range files[1:] {
				if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("created profile left behind: %v", err)
				}
			}
		})
	}
}

func TestPathCreatesAndRemovesFishDirectories(t *testing.T) {
	root, source, dir, data := shellPathFixture(t, "fish")
	r, err := Install(source, dir, data, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := RemovePath(r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".config")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("residue: %v", err)
	}
}

func TestPathNoPathPreservesExistingRegistration(t *testing.T) {
	_, source, dir, data := pathFixture(t, "zsh")
	r, err := Install(source, dir, data, false)
	if err != nil || r.Path != nil {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = Install(source, dir, data, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", "/bin/unsupported")
	upgraded, err := Install(source, dir, data, false)
	if err != nil || upgraded.Path == nil {
		t.Fatalf("%+v %v", upgraded, err)
	}
	if err := RemovePath(upgraded); err != nil {
		t.Fatal(err)
	}
}

func TestPathConflictAndUnsafeProfileDoNotInstall(t *testing.T) {
	for _, scenario := range []string{"conflict", "symlink", "unsupported", "delimiter"} {
		t.Run(scenario, func(t *testing.T) {
			root, source, dir, data := shellPathFixture(t, "zsh")
			switch scenario {
			case "conflict":
				findPathCommand = func(string) (string, error) { return source, nil }
			case "symlink":
				if err := os.Symlink(source, filepath.Join(root, ".zshrc")); err != nil {
					t.Skip(err)
				}
			case "unsupported":
				t.Setenv("SHELL", "/bin/unsupported")
			case "delimiter":
				dir += ":invalid"
			}
			if _, err := Install(source, dir, data, true); err == nil || !strings.Contains(err.Error(), "--no-path") {
				t.Fatalf("expected actionable error: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, EntryName())); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("installed despite failed preflight")
			}
		})
	}
}

func TestPathEditedBlockStopsCleanup(t *testing.T) {
	_, source, dir, data := shellPathFixture(t, "zsh")
	r, err := Install(source, dir, data, true)
	if err != nil {
		t.Fatal(err)
	}
	file := r.Path.Profiles[0].File
	b, _ := os.ReadFile(file)
	b = []byte(strings.ReplaceAll(string(b), "export PATH", "export CUSTOM_PATH"))
	if err := os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RemovePath(r); err == nil {
		t.Fatal("removed edited block")
	}
	if _, err := Install(source, dir, data, true); err == nil {
		t.Fatal("overwrote edited block")
	}
	got, _ := os.ReadFile(file)
	if string(got) != string(b) {
		t.Fatal("edited file changed")
	}
}

func TestPathRollbackRestoresProgramAndReceipts(t *testing.T) {
	_, source, dir, data := pathFixture(t, "zsh")
	pathPlatform = "windows"
	old, err := Copy(source, dir, data)
	if err != nil {
		t.Fatal(err)
	}
	before := userPathValue{Exists: true, Value: "existing", Type: 2}
	value := before
	readUserPath = func() (userPathValue, error) { return value, nil }
	writeUserPath = func(v userPathValue) error {
		value = v
		if v != before {
			return errors.New("injected failure after registry write")
		}
		return nil
	}
	if err := os.WriteFile(source, []byte("new version"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(source, dir, data, true); err == nil {
		t.Fatal("expected failure")
	}
	if value != before {
		t.Fatal("PATH not restored")
	}
	restored, err := Read(data)
	if err != nil || restored.SHA256 != old.SHA256 || restored.Path != nil {
		t.Fatalf("%+v %v", restored, err)
	}
	if err := restored.CheckOwned(); err != nil {
		t.Fatal(err)
	}
}

func TestPathProfileConcurrentEditAndRollback(t *testing.T) {
	_, _, dir, _ := shellPathFixture(t, "bash")
	p, err := preparePath("", filepath.Join(dir, EntryName()), nil)
	if err != nil {
		t.Fatal(err)
	}
	file := p.profiles[1].profile.File
	if err := os.WriteFile(file, []byte("concurrent edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.apply(); err == nil {
		t.Fatal("expected conflict")
	}
	if err := p.restore(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.profiles[0].profile.File); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("first profile was not rolled back")
	}
	b, _ := os.ReadFile(file)
	if string(b) != "concurrent edit" {
		t.Fatal("overwrote concurrent edit")
	}
}

func TestWindowsPathOwnership(t *testing.T) {
	for _, original := range []userPathValue{{}, {Exists: true, Type: 1}, {Exists: true, Value: "original;", Type: 2}, {Exists: true, Value: `%USERPROFILE%\bin;C:\Other`, Type: 2}} {
		t.Run(original.Value, func(t *testing.T) {
			_, source, dir, data := pathFixture(t, "zsh")
			pathPlatform = "windows"
			value := original
			readUserPath = func() (userPathValue, error) { return value, nil }
			writeUserPath = func(v userPathValue) error { value = v; return nil }
			r, err := Install(source, dir, data, true)
			if err != nil {
				t.Fatal(err)
			}
			r, err = Install(source, dir, data, true)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(value.Value, dir) != 1 {
				t.Fatalf("duplicate PATH: %+v", value)
			}
			if err := RemovePath(r); err != nil {
				t.Fatal(err)
			}
			if value.Exists != original.Exists || value.Value != original.Value || (value.Exists && value.Type != original.Type) {
				t.Fatalf("want %+v got %+v", original, value)
			}
		})
	}
	t.Run("preexisting-entry", func(t *testing.T) {
		_, source, dir, data := pathFixture(t, "zsh")
		pathPlatform = "windows"
		value := userPathValue{Exists: true, Value: strings.ToUpper(dir), Type: 1}
		readUserPath = func() (userPathValue, error) { return value, nil }
		writeUserPath = func(userPathValue) error { t.Fatal("should preserve preexisting PATH"); return nil }
		r, err := Install(source, dir, data, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := RemovePath(r); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("later-user-entry", func(t *testing.T) {
		_, source, dir, data := pathFixture(t, "zsh")
		pathPlatform = "windows"
		value := userPathValue{Exists: true, Value: "original", Type: 2}
		readUserPath = func() (userPathValue, error) { return value, nil }
		writeUserPath = func(v userPathValue) error { value = v; return nil }
		r, err := Install(source, dir, data, true)
		if err != nil {
			t.Fatal(err)
		}
		value.Value += ";user-added"
		if err := RemovePath(r); err != nil {
			t.Fatal(err)
		}
		if value.Value != "original;user-added" {
			t.Fatalf("%+v", value)
		}
	})
}

func TestWindowsPathComparison(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\Example\AppData\Local`)
	for _, pair := range [][2]string{
		{`%LOCALAPPDATA%\Programs\weibo-exp`, `c:/users/example/appdata/local/Programs/weibo-exp/`},
		{`C:\tools\old\..\weibo-exp`, `c:\tools\weibo-exp`},
		{`"C:\tools\weibo-exp"`, `c:\tools\weibo-exp`},
	} {
		if !windowsPathEqual(pair[0], pair[1]) {
			t.Fatalf("not equivalent: %v", pair)
		}
	}
	if windowsPathEqual(`C:\literal%name`, `C:\literalname`) {
		t.Fatal("lost literal percent")
	}
}

func TestShellPathWorksFromAnotherDirectory(t *testing.T) {
	for _, shell := range []string{"zsh", "bash", "fish"} {
		t.Run(shell, func(t *testing.T) {
			binary, err := exec.LookPath(shell)
			if err != nil {
				t.Skip("shell unavailable")
			}
			root, source, dir, data := shellPathFixture(t, shell)
			if err := os.WriteFile(source, []byte("#!/bin/sh\nprintf 'path-ok'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			r, err := Install(source, dir, data, true)
			if err != nil {
				t.Fatal(err)
			}
			// Interactive shells load the managed files. PATH starts without target.
			for _, mode := range []string{"-ic", "-lic"} {
				cmd := exec.Command(binary, mode, "weibo-exp")
				cmd.Dir = root
				cmd.Env = os.Environ()
				output, err := cmd.CombinedOutput()
				if err != nil || !strings.Contains(string(output), "path-ok") {
					t.Fatalf("%s: %v %s", mode, err, output)
				}
			}
			if err := RemovePath(r); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLegacyReceiptUpgradesWithPath(t *testing.T) {
	_, source, dir, data := pathFixture(t, "zsh")
	r, err := Copy(source, dir, data)
	if err != nil {
		t.Fatal(err)
	}
	r.Version = 1
	if err := storage.WriteJSON(r.Sidecar(), r, 0600); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSON(filepath.Join(data, ReceiptName), r, 0600); err != nil {
		t.Fatal(err)
	}
	r, err = Install(source, dir, data, true)
	if err != nil || r.Version != 2 || r.Path == nil {
		t.Fatalf("%+v %v", r, err)
	}
	if err := r.CheckOwned(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsCurrentDirectoryDownloadIsNotConflict(t *testing.T) {
	_, source, dir, _ := pathFixture(t, "zsh")
	pathPlatform = "windows"
	relative, err := filepath.Rel(mustWorkingDirectory(t), source)
	if err != nil {
		t.Fatal(err)
	}
	findPathCommand = func(string) (string, error) { return relative, exec.ErrDot }
	readUserPath = func() (userPathValue, error) { return userPathValue{}, nil }
	if _, err := preparePath(source, filepath.Join(dir, EntryName()), nil); err != nil {
		t.Fatal(err)
	}
}

func mustWorkingDirectory(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestShellProfileSelection(t *testing.T) {
	root, _, _, _ := pathFixture(t, "bash")
	if err := os.WriteFile(filepath.Join(root, ".bash_profile"), []byte("# existing"), 0600); err != nil {
		t.Fatal(err)
	}
	files, _, err := shellProfiles()
	if err != nil || files[1] != filepath.Join(root, ".bash_profile") {
		t.Fatalf("%v %v", files, err)
	}
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("ZDOTDIR", filepath.Join(root, "zsh"))
	files, _, err = shellProfiles()
	if err != nil || files[0] != filepath.Join(root, "zsh", ".zshrc") {
		t.Fatalf("%v %v", files, err)
	}
	t.Setenv("SHELL", "/bin/fish")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	files, fish, err := shellProfiles()
	if err != nil || !fish || files[0] != filepath.Join(root, "xdg", "fish", "conf.d", "weibo-exp.fish") {
		t.Fatalf("%v %v", files, err)
	}
}
