package schedule

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/SurrealGit/weibo-exp/internal/install"
	"github.com/SurrealGit/weibo-exp/internal/storage"
)

var launcherPayload = embeddedLauncher

const launcherName = "weibo-exp-launcher.exe"

func launcherPath(dir string) string {
	// Rendering is also tested on Unix with Windows paths.
	if strings.Contains(dir, `\`) {
		return strings.TrimRight(dir, `\/`) + `\` + launcherName
	}
	return filepath.Join(dir, launcherName)
}

type launcherSnapshot struct {
	path                  string
	binary, receipt       []byte
	hasBinary, hasReceipt bool
}

// Only manage files whose receipt matches their content. This is an ownership
// check, not a security signature. Never remove unrelated files in the data dir.
func readLauncher(dir string) (launcherSnapshot, error) {
	s := launcherSnapshot{path: launcherPath(dir)}
	clean, err := install.CleanPath(s.path)
	if err != nil {
		return s, err
	}
	s.path = clean
	for _, path := range []string{s.path, s.path + ".sha256"} {
		if _, err := install.CleanPath(path); err != nil {
			return s, err
		}
	}
	s.binary, s.hasBinary, err = readOptional(s.path)
	if err != nil {
		return s, err
	}
	s.receipt, s.hasReceipt, err = readOptional(s.path + ".sha256")
	if err != nil {
		return s, err
	}
	if !s.hasBinary && !s.hasReceipt {
		return s, nil
	}
	hash, err := hex.DecodeString(strings.TrimSpace(string(s.receipt)))
	if !s.hasReceipt || err != nil || len(hash) != sha256.Size {
		return s, fmt.Errorf("后台启动器缺少有效安装记录，未修改：%s", s.path)
	}
	actual := sha256.Sum256(s.binary)
	if s.hasBinary && !bytes.Equal(actual[:], hash) {
		return s, fmt.Errorf("后台启动器与安装记录不符，未修改：%s", s.path)
	}
	return s, nil
}

func (s launcherSnapshot) restore() error {
	// Do not rewrite an unchanged executable that may still be in use.
	restore := func(path string, data []byte, existed bool) error {
		current, exists, err := readOptional(path)
		if err == nil && exists == existed && bytes.Equal(current, data) {
			return nil
		}
		return restoreFile(path, data, existed)
	}
	return errors.Join(restore(s.path, s.binary, s.hasBinary), restore(s.path+".sha256", s.receipt, s.hasReceipt))
}

func (s launcherSnapshot) remove() error {
	if err := restoreFile(s.path, nil, false); err != nil {
		return err
	}
	if err := restoreFile(s.path+".sha256", nil, false); err != nil {
		return errors.Join(err, s.restore())
	}
	return nil
}

func writeLauncher(s launcherSnapshot) error {
	payload, err := launcherPayload()
	if err != nil || len(payload) == 0 {
		return errors.New("此 Windows 程序未包含后台启动器；请使用完整发行版，源码构建请先执行 go generate ./internal/schedule")
	}
	if s.hasBinary && bytes.Equal(s.binary, payload) {
		return nil
	}
	if err := storage.WriteFileAtomic(s.path, payload, 0700); err != nil {
		return err
	}
	if err := storage.WriteFileAtomic(s.path+".sha256", []byte(fmt.Sprintf("%x\n", sha256.Sum256(payload))), 0600); err != nil {
		return errors.Join(err, s.restore())
	}
	return nil
}
