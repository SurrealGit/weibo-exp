// Package install manages an explicitly recorded copy of the executable.
// It never recursively removes a user-selected installation or data directory.
package install

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"

	"github.com/SurrealGit/weibo-exp/internal/storage"
)

const ReceiptName = "installation.json"
const SidecarName = ".weibo-exp-install.json"

type Record struct {
	Version     int               `json:"version"`
	Executable  string            `json:"executable"`
	DataDir     string            `json:"data_dir"`
	SHA256      string            `json:"sha256"`
	CreatedDirs []string          `json:"created_dirs,omitempty"`
	Path        *PathRegistration `json:"path,omitempty"`
}

func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Applications", "weibo-exp"), nil
	case "linux":
		return filepath.Join(home, ".local", "share", "weibo-exp"), nil
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			return "", errors.New("未设置 LOCALAPPDATA，无法确定用户安装目录")
		}
		return filepath.Join(local, "Programs", "weibo-exp"), nil
	default:
		return "", errors.New("当前系统不支持安装")
	}
}

func EntryName() string {
	if runtime.GOOS == "windows" {
		return "weibo-exp.exe"
	}
	return "weibo-exp"
}

// CleanPath rejects symlink components rather than following them while later
// deleting files. Missing leaf paths are allowed for a new installation.
func CleanPath(path string) (string, error) {
	if path == "" || strings.ContainsAny(path, "\x00\r\n") {
		return "", errors.New("路径不能为空或包含换行/空字符")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for p := abs; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("为避免操作链接目标，不接受符号链接路径：%s", p)
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	return abs, nil
}

// CanonicalDir resolves an existing prefix (e.g. macOS /var -> /private/var)
// once at installation time. Recorded paths are then checked without following links.
func CanonicalDir(path string) (string, error) {
	if path == "" || strings.ContainsAny(path, "\x00\r\n") {
		return "", errors.New("路径不能为空或包含换行/空字符")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var tail []string
	p := abs
	for {
		if _, err := os.Lstat(p); err == nil {
			base, err := filepath.EvalSymlinks(p)
			if err != nil {
				return "", err
			}
			for i := len(tail) - 1; i >= 0; i-- {
				base = filepath.Join(base, tail[i])
			}
			return CleanPath(base)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		tail = append(tail, filepath.Base(p))
		if p == filepath.Dir(p) {
			return "", errors.New("找不到有效路径父目录")
		}
		p = filepath.Dir(p)
	}
}

func Read(dataDir string) (Record, error) {
	var r Record
	canonical, err := CanonicalDir(dataDir)
	if err != nil {
		return r, err
	}
	if _, err := CleanPath(filepath.Join(canonical, ReceiptName)); err != nil {
		return r, err
	}
	if err := storage.ReadJSON(filepath.Join(canonical, ReceiptName), &r); err != nil {
		return r, err
	}
	if err := r.Validate(); err != nil {
		return r, err
	}
	if canonical != r.DataDir {
		return r, errors.New("安装记录与当前数据目录不匹配")
	}
	return r, nil
}

func (r Record) Sidecar() string { return filepath.Join(filepath.Dir(r.Executable), SidecarName) }

func (r Record) Validate() error {
	if (r.Version != 1 && r.Version != 2) || filepath.Base(r.Executable) != EntryName() || len(r.SHA256) != 64 {
		return errors.New("安装记录无效，未进行文件清理")
	}
	for _, p := range append([]string{r.Executable, r.DataDir}, r.CreatedDirs...) {
		clean, err := CleanPath(p)
		if err != nil {
			return err
		}
		if clean != p || p == filepath.Dir(p) {
			return errors.New("安装记录包含无效路径")
		}
	}
	for _, d := range r.CreatedDirs {
		rel, err := filepath.Rel(d, filepath.Dir(r.Executable))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("安装记录的目录归属无效")
		}
	}
	return validatePathRegistration(r)
}

func fileHash(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("不是普通文件：%s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (r Record) CheckOwned() error {
	if err := r.Validate(); err != nil {
		return err
	}
	if _, err := CleanPath(r.Sidecar()); err != nil {
		return err
	}
	var side Record
	if err := storage.ReadJSON(r.Sidecar(), &side); err != nil {
		return fmt.Errorf("无法确认程序归属：%w", err)
	}
	if side.Executable != r.Executable || side.DataDir != r.DataDir || side.SHA256 != r.SHA256 || !reflect.DeepEqual(side.Path, r.Path) {
		return errors.New("两份安装记录不一致；未覆盖或删除程序")
	}
	hash, err := fileHash(r.Executable)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && hash != r.SHA256 {
		return errors.New("已安装程序与安装记录不符；请检查是否被手动替换，恢复记录对应的原程序后再操作；未删除未知文件")
	}
	return nil
}

// DataDirForExecutable makes custom data directories work when the installed
// executable is invoked without --data-dir. Explicit flags/environment win.
func DataDirForExecutable(executable string) (string, error) {
	var r Record
	if _, err := CleanPath(filepath.Join(filepath.Dir(executable), SidecarName)); err != nil {
		return "", err
	}
	if err := storage.ReadJSON(filepath.Join(filepath.Dir(executable), SidecarName), &r); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	if err := r.Validate(); err != nil {
		return "", err
	}
	if r.Executable != executable {
		return "", nil
	}
	return r.DataDir, nil
}

// Copy installs only the managed executable and its receipts.
func Copy(source, directory, dataDir string) (Record, error) {
	return Install(source, directory, dataDir, false)
}

// Install records PATH ownership before updating the user's environment.
func Install(source, directory, dataDir string, managePath bool) (Record, error) {
	dir, err := CanonicalDir(directory)
	if err != nil {
		return Record{}, err
	}
	dataDir, err = CanonicalDir(dataDir)
	if err != nil {
		return Record{}, err
	}
	target := filepath.Join(dir, EntryName())
	if dir == filepath.Dir(dir) {
		return Record{}, errors.New("不能安装到文件系统根目录")
	}
	hash, err := fileHash(source)
	if err != nil {
		return Record{}, err
	}
	r := Record{Version: 2, Executable: target, DataDir: dataDir, SHA256: hash}
	old, readErr := Read(dataDir)
	if readErr == nil {
		if old.Executable != target {
			return r, errors.New("已有其他安装位置；请先使用 uninstall --keep-config 卸载旧位置再迁移")
		}
		if err := old.CheckOwned(); err != nil {
			return r, err
		}
		r.CreatedDirs = old.CreatedDirs
		r.Path = old.Path
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return r, readErr
	}
	if _, err := os.Lstat(r.Sidecar()); err == nil && readErr != nil {
		return r, errors.New("目标目录存在其他安装记录，拒绝覆盖")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return r, err
	}
	if existing, err := fileHash(target); err == nil {
		if readErr != nil && existing != hash {
			return r, errors.New("目标程序不是已管理安装，拒绝覆盖同名文件")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return r, err
	}
	if readErr != nil {
		// Track only directories actually introduced by this installation,
		// including intermediate components of a new custom path.
		for p := dir; ; p = filepath.Dir(p) {
			if _, err := os.Stat(p); err == nil {
				break
			} else if !errors.Is(err, os.ErrNotExist) {
				return r, err
			}
			r.CreatedDirs = append(r.CreatedDirs, p)
		}
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return r, err
	}
	var pathChanges *pathPlan
	if managePath {
		pathChanges, err = preparePath(source, target, r.Path)
		if err != nil {
			return r, fmt.Errorf("配置 PATH：%w；可使用 install --no-path 跳过 PATH 设置", err)
		}
		r.Path = pathChanges.registration
	}
	// Keep old bytes/receipts so a failed copy or receipt write can be restored.
	paths := []string{target, r.Sidecar(), filepath.Join(dataDir, ReceiptName)}
	type backup struct {
		data   []byte
		exists bool
		mode   os.FileMode
	}
	previous := make([]backup, len(paths))
	for i, path := range paths {
		if _, err := CleanPath(path); err != nil {
			return r, err
		}
		b, err := os.ReadFile(path)
		if err == nil {
			info, err := os.Stat(path)
			if err != nil {
				return r, err
			}
			previous[i] = backup{b, true, info.Mode().Perm()}
		} else if !errors.Is(err, os.ErrNotExist) {
			return r, err
		}
	}
	rollback := func(cause error) error {
		errs := []error{cause}
		for i := len(paths) - 1; i >= 0; i-- {
			if previous[i].exists {
				errs = append(errs, storage.WriteFileAtomic(paths[i], previous[i].data, previous[i].mode))
			} else if err := os.Remove(paths[i]); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
		RemoveEmpty(r.CreatedDirs)
		return errors.Join(errs...)
	}
	if err := storage.WriteFileAtomic(target, data, 0755); err != nil {
		return r, err
	}
	if err := storage.WriteJSON(r.Sidecar(), r, 0600); err != nil {
		return r, rollback(err)
	}
	if err := storage.WriteJSON(paths[2], r, 0600); err != nil {
		return r, rollback(err)
	}
	if pathChanges != nil {
		if err := pathChanges.apply(); err != nil {
			// If PATH rollback failed, keep its ownership receipts for uninstall.
			if restoreErr := pathChanges.restore(); restoreErr != nil {
				return r, errors.Join(err, fmt.Errorf("PATH 回滚未完成，安装记录已保留，请重试安装或卸载：%w", restoreErr))
			}
			return r, rollback(err)
		}
	}
	return r, nil
}

// RemoveFiles only removes ordinary files, never directories or symlink targets.
func RemoveFiles(paths []string) error {
	for _, path := range paths {
		if _, err := CleanPath(path); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("拒绝删除非普通文件：%s", path)
		}
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("删除 %s: %w", path, err)
		}
	}
	return nil
}

// RemoveEmpty leaves shared directories and all unrecognized files intact.
func RemoveEmpty(dirs []string) []string {
	var remaining []string
	for _, dir := range CleanupDirs(dirs) {
		home, _ := os.UserHomeDir()
		if dir == filepath.Dir(dir) || dir == home {
			remaining = append(remaining, dir)
			continue
		}
		if _, err := CleanPath(dir); err != nil {
			remaining = append(remaining, dir)
			continue
		}
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			remaining = append(remaining, dir)
			continue
		}
		if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			remaining = append(remaining, dir)
		}
	}
	return remaining
}

// Children must be removed before parents, including nested custom locations.
func CleanupDirs(dirs []string) []string {
	unique := make(map[string]bool)
	var result []string
	for _, dir := range dirs {
		if !unique[dir] {
			unique[dir] = true
			result = append(result, dir)
		}
	}
	sort.Slice(result, func(i, j int) bool { return len(result[i]) > len(result[j]) })
	return result
}
