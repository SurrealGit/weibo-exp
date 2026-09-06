package install

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/SurrealGit/weibo-exp/internal/storage"
)

type PathRegistration struct {
	Directory           string        `json:"directory"`
	Profiles            []PathProfile `json:"profiles,omitempty"`
	WindowsAdded        bool          `json:"windows_added,omitempty"`
	WindowsValueExisted bool          `json:"windows_value_existed,omitempty"`
}

type PathProfile struct {
	File        string   `json:"file"`
	Block       string   `json:"block"`
	Created     bool     `json:"created,omitempty"`
	CreatedDirs []string `json:"created_dirs,omitempty"`
}

type userPathValue struct {
	Exists bool
	Value  string
	Type   uint32
}

var pathPlatform = runtime.GOOS
var findPathCommand = exec.LookPath
var readUserPath = nativeReadUserPath
var writeUserPath = nativeWriteUserPath
var windowsEnvReference = regexp.MustCompile(`%[^%]+%`)

type profileChange struct {
	profile       PathProfile
	before, after []byte
	existed       bool
	mode          os.FileMode
	applied       bool
}

type pathPlan struct {
	registration                  *PathRegistration
	profiles                      []profileChange
	before, after                 userPathValue
	windowsChange, windowsApplied bool
}

func validatePathRegistration(r Record) error {
	p := r.Path
	if p == nil {
		return nil
	}
	if p.Directory != filepath.Dir(r.Executable) {
		return errors.New("PATH 记录与安装目录不一致")
	}
	for _, profile := range p.Profiles {
		clean, err := CleanPath(profile.File)
		if err != nil {
			return err
		}
		if clean != profile.File || !strings.HasPrefix(profile.Block, "\n# >>> weibo-exp PATH ") || !strings.HasSuffix(profile.Block, "# <<< weibo-exp PATH <<<\n") {
			return errors.New("PATH 配置记录无效")
		}
		for _, dir := range profile.CreatedDirs {
			if dir == filepath.Dir(dir) {
				return errors.New("PATH 目录记录无效")
			}
			if clean, err := CleanPath(dir); err != nil || clean != dir {
				return errors.New("PATH 目录记录无效")
			}
			rel, err := filepath.Rel(dir, filepath.Dir(profile.File))
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return errors.New("PATH 目录归属无效")
			}
		}
	}
	return nil
}

func preparePath(source, target string, old *PathRegistration) (*pathPlan, error) {
	dir := filepath.Dir(target)
	delimiter := ":"
	if pathPlatform == "windows" {
		delimiter = ";"
	}
	if strings.Contains(dir, delimiter) {
		return nil, fmt.Errorf("安装目录包含 PATH 分隔符 %q", delimiter)
	}
	if found, err := findPathCommand(EntryName()); err == nil || errors.Is(err, exec.ErrDot) {
		absolute, resolveErr := filepath.Abs(found)
		if resolveErr != nil {
			return nil, resolveErr
		}
		resolved, resolveErr := filepath.EvalSymlinks(absolute)
		if resolveErr != nil {
			return nil, resolveErr
		}
		// Windows searches the current directory even before PATH.
		sameTarget, sameSource := resolved == target, resolved == source
		if pathPlatform == "windows" {
			sameTarget, sameSource = windowsPathEqual(resolved, target), windowsPathEqual(resolved, source)
		}
		fromCurrentDir := pathPlatform == "windows" && errors.Is(err, exec.ErrDot) && sameSource
		if !sameTarget && !fromCurrentDir {
			return nil, fmt.Errorf("已有同名命令：%s，请先处理该命令以免调用错误版本", found)
		}
	}
	p := &pathPlan{registration: &PathRegistration{Directory: dir}}
	if old != nil {
		copy := *old
		copy.Profiles = append([]PathProfile(nil), old.Profiles...)
		p.registration = &copy
	}
	if pathPlatform == "windows" {
		before, err := readUserPath()
		if err != nil {
			return nil, err
		}
		if !windowsPathContains(before.Value, dir) {
			p.before, p.after = before, before
			p.after.Exists = true
			if !before.Exists {
				p.after.Type = 2
			}
			p.after.Value = before.Value
			if p.after.Value != "" {
				p.after.Value += ";"
			}
			p.after.Value += dir
			p.windowsChange = true
			if !p.registration.WindowsAdded {
				p.registration.WindowsValueExisted = before.Exists
			}
			p.registration.WindowsAdded = true
		}
		return p, nil
	}
	files, fish, err := shellProfiles()
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		parent, err := CanonicalDir(filepath.Dir(file))
		if err != nil {
			return nil, err
		}
		file = filepath.Join(parent, filepath.Base(file))
		change, err := readProfile(file)
		if err != nil {
			return nil, err
		}
		profile := PathProfile{File: file, Block: shellPathBlock(dir, fish), Created: !change.existed}
		index := -1
		for i, previous := range p.registration.Profiles {
			if previous.File == file {
				profile, index = previous, i
				break
			}
		}
		if index < 0 {
			for d := parent; ; d = filepath.Dir(d) {
				if _, err := os.Stat(d); err == nil {
					break
				} else if !errors.Is(err, os.ErrNotExist) {
					return nil, err
				}
				profile.CreatedDirs = append(profile.CreatedDirs, d)
			}
			p.registration.Profiles = append(p.registration.Profiles, profile)
		}
		if bytes.Contains(change.before, []byte(profile.Block)) {
			continue
		}
		if bytes.Contains(change.before, []byte(blockMarker(profile.Block))) {
			return nil, fmt.Errorf("%s 中本工具的 PATH 段已被修改，请先检查该段", file)
		}
		change.profile = profile
		change.after = append(append([]byte(nil), change.before...), profile.Block...)
		p.profiles = append(p.profiles, change)
	}
	return p, nil
}

func shellProfiles() ([]string, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, false, err
	}
	shell := filepath.Base(os.Getenv("SHELL"))
	if shell == "." || shell == "" {
		if pathPlatform == "darwin" {
			shell = "zsh"
		} else {
			shell = "bash"
		}
	}
	switch shell {
	case "zsh":
		dir := os.Getenv("ZDOTDIR")
		if dir == "" {
			dir = home
		}
		return []string{filepath.Join(dir, ".zshrc")}, false, nil
	case "bash":
		login := filepath.Join(home, ".profile")
		for _, name := range []string{".bash_profile", ".bash_login", ".profile"} {
			file := filepath.Join(home, name)
			if _, err := os.Lstat(file); err == nil {
				login = file
				break
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, false, err
			}
		}
		return []string{filepath.Join(home, ".bashrc"), login}, false, nil
	case "fish":
		dir := os.Getenv("XDG_CONFIG_HOME")
		if dir == "" {
			dir = filepath.Join(home, ".config")
		}
		return []string{filepath.Join(dir, "fish", "conf.d", "weibo-exp.fish")}, true, nil
	default:
		return nil, false, fmt.Errorf("暂不支持自动配置 %s，请手动将安装目录加入 PATH", shell)
	}
}

func shellPathBlock(dir string, fish bool) string {
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(dir)))[:12]
	quoted := "'" + strings.ReplaceAll(dir, "'", "'\"'\"'") + "'"
	body := "case \":$PATH:\" in\n  *:" + quoted + ":*) ;;\n  *) export PATH=" + quoted + ":\"$PATH\" ;;\nesac\n"
	if fish {
		quoted = "'" + strings.ReplaceAll(strings.ReplaceAll(dir, "\\", "\\\\"), "'", "\\'") + "'"
		body = "if not contains -- " + quoted + " $PATH\n  set -gx PATH " + quoted + " $PATH\nend\n"
	}
	return "\n# >>> weibo-exp PATH " + id + " >>>\n" + body + "# <<< weibo-exp PATH <<<\n"
}

func blockMarker(block string) string { return strings.Split(strings.TrimPrefix(block, "\n"), "\n")[0] }

func readProfile(file string) (profileChange, error) {
	c := profileChange{profile: PathProfile{File: file}, mode: 0644}
	if _, err := CleanPath(file); err != nil {
		return c, err
	}
	info, err := os.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() {
		return c, fmt.Errorf("shell 配置不是普通文件：%s", file)
	}
	c.before, err = os.ReadFile(file)
	c.existed, c.mode = true, info.Mode().Perm()
	return c, err
}

func (p *pathPlan) apply() error {
	for i := range p.profiles {
		c := &p.profiles[i]
		current, err := readProfile(c.profile.File)
		if err != nil {
			return err
		}
		if current.existed != c.existed || !bytes.Equal(current.before, c.before) {
			return fmt.Errorf("%s 已变化，请重试安装", c.profile.File)
		}
		// Track directory creation even if writing the file subsequently fails.
		c.applied = true
		if err := storage.WriteFileAtomic(c.profile.File, c.after, c.mode); err != nil {
			return err
		}
	}
	if p.windowsChange {
		current, err := readUserPath()
		if err != nil {
			return err
		}
		if current != p.before {
			return errors.New("用户 PATH 已变化，请重试安装")
		}
		p.windowsApplied = true
		return writeUserPath(p.after)
	}
	return nil
}

func (p *pathPlan) restore() error {
	var errs []error
	for i := len(p.profiles) - 1; i >= 0; i-- {
		c := p.profiles[i]
		if !c.applied {
			continue
		}
		current, err := readProfile(c.profile.File)
		if err == nil && !bytes.Equal(current.before, c.before) {
			if !bytes.Equal(current.before, c.after) {
				err = fmt.Errorf("%s 已被其他程序修改，未回滚", c.profile.File)
			} else if c.existed {
				err = storage.WriteFileAtomic(c.profile.File, c.before, c.mode)
			} else {
				err = os.Remove(c.profile.File)
			}
		}
		errs = append(errs, err)
		RemoveEmpty(c.profile.CreatedDirs)
	}
	if p.windowsApplied {
		current, err := readUserPath()
		if err == nil && current != p.before {
			if current != p.after {
				err = errors.New("用户 PATH 已被其他程序修改，未回滚")
			} else {
				err = writeUserPath(p.before)
			}
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// RemovePath removes only the exact blocks/entry recorded by this installation.
// Missing blocks are already cleaned; edited blocks require user attention.
func RemovePath(r Record) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if r.Path == nil {
		return nil
	}
	var changes []profileChange
	for _, profile := range r.Path.Profiles {
		c, err := readProfile(profile.File)
		if err != nil {
			return err
		}
		if !bytes.Contains(c.before, []byte(profile.Block)) {
			if bytes.Contains(c.before, []byte(blockMarker(profile.Block))) {
				return fmt.Errorf("%s 中本工具的 PATH 段已被修改，请手动移除该段后重试卸载", profile.File)
			}
			continue
		}
		c.profile = profile
		c.after = bytes.Replace(c.before, []byte(profile.Block), nil, 1)
		changes = append(changes, c)
	}
	for _, c := range changes {
		current, err := readProfile(c.profile.File)
		if err != nil {
			return err
		}
		if !bytes.Equal(current.before, c.before) {
			return fmt.Errorf("%s 已变化，请重试卸载", c.profile.File)
		}
		if c.profile.Created && len(c.after) == 0 {
			err = os.Remove(c.profile.File)
		} else {
			err = storage.WriteFileAtomic(c.profile.File, c.after, c.mode)
		}
		if err != nil {
			return err
		}
	}
	for _, profile := range r.Path.Profiles {
		RemoveEmpty(profile.CreatedDirs)
	}
	if r.Path.WindowsAdded {
		current, err := readUserPath()
		if err != nil {
			return err
		}
		entries := strings.Split(current.Value, ";")
		for i, entry := range entries {
			if windowsPathEqual(entry, r.Path.Directory) {
				entries = append(entries[:i], entries[i+1:]...)
				current.Value = strings.Join(entries, ";")
				if current.Value == "" && !r.Path.WindowsValueExisted {
					current.Exists = false
				}
				return writeUserPath(current)
			}
		}
	}
	return nil
}

func windowsPathContains(value, dir string) bool {
	for _, entry := range strings.Split(value, ";") {
		if windowsPathEqual(entry, dir) {
			return true
		}
	}
	return false
}

func windowsPathEqual(a, b string) bool {
	normalize := func(s string) string {
		s = strings.Trim(strings.TrimSpace(s), "\"")
		// Expand variables for comparison only; preserve the original registry text.
		s = windowsEnvReference.ReplaceAllStringFunc(s, func(reference string) string {
			if value, ok := os.LookupEnv(strings.Trim(reference, "%")); ok {
				return value
			}
			return reference
		})
		return strings.ToLower(path.Clean(strings.ReplaceAll(s, "\\", "/")))
	}
	return normalize(a) == normalize(b)
}
