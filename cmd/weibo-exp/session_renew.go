package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/session"
	"github.com/SurrealGit/weibo-exp/internal/storage"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

const sessionRenewInterval = 24 * time.Hour

type renewal struct {
	Now         time.Time
	Output      io.Writer
	Save        func(*session.Jar) error
	PendingPath string
}

// Candidates cannot be used by tasks before UID verification and durable
// promotion. Retain the expected identity across process restarts.
type pendingRenewal struct {
	Version   int                    `json:"version"`
	UID       string                 `json:"uid"`
	CreatedAt time.Time              `json:"created_at"`
	Cookies   []session.StoredCookie `json:"cookies"`
}

func renewalPath(sessionPath string) string { return sessionPath + ".renewal.json" }

func removeRenewal(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (r renewal) promote(ctx context.Context, client *weibo.Client, state app.State, pending pendingRenewal) (*session.Jar, error) {
	if pending.Version != 1 || pending.CreatedAt.IsZero() || len(pending.Cookies) == 0 {
		return nil, errors.New("待验证登录凭据格式无效，请重新执行 login")
	}
	candidate, err := session.New(pending.Cookies)
	if err != nil {
		return nil, err
	}
	if err := verifyRenewed(ctx, client.WithJar(candidate), state, weibo.LoginStatus{UID: pending.UID}, pending.UID == ""); err != nil {
		return nil, fmt.Errorf("新登录凭据已暂存，但校验未通过；下次运行将重新校验，也可执行 login 重新扫码: %w", err)
	}
	candidate.SetRenewedAt(pending.CreatedAt)
	if err := r.Save(candidate); err != nil {
		return nil, fmt.Errorf("保存续期会话失败，已停止任务；待验证凭据保留，下次运行重试: %w", err)
	}
	if err := removeRenewal(r.PendingPath); err != nil {
		return nil, fmt.Errorf("会话已保存，但清理续期暂存文件失败: %w", err)
	}
	fmt.Fprintln(r.Output, "登录状态：已自动续期。")
	return candidate, nil
}

func ensureSession(ctx context.Context, client *weibo.Client, jar *session.Jar, state app.State, r renewal) (*session.Jar, error) {
	if client.PassportURL == "" {
		return jar, nil
	}
	var pending pendingRenewal
	if r.PendingPath == "" {
		return nil, errors.New("未配置续期暂存路径")
	}
	if err := storage.ReadJSON(r.PendingPath, &pending); err == nil {
		fmt.Fprintln(r.Output, "登录状态：正在恢复上次暂存的登录凭据。")
		return r.promote(ctx, client, state, pending)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("读取续期暂存文件失败: %w", err)
	}
	current, err := client.WithJar(jar).LoginStatus(ctx)
	expired := errors.Is(err, weibo.ErrNotLoggedIn)
	if err != nil && !expired {
		return jar, nil
	}
	if !expired {
		if err := state.CheckAccount(current.UID); err != nil {
			return nil, err
		}
		if r.Now.Sub(jar.RenewedAt()) < sessionRenewInterval {
			return jar, nil
		}
	}
	uid := state.UID
	if !expired {
		uid = current.UID
	}
	jar.SetRenewedAt(r.Now)
	pending = pendingRenewal{Version: 1, UID: uid, CreatedAt: r.Now}
	retained := false
	var checkpointErr error
	checkpoint := func(candidate *session.Jar) error {
		if !newLoginCredentials(jar, candidate) {
			return nil
		}
		pending.Cookies = candidate.Snapshot()
		if err := storage.WriteJSON(r.PendingPath, pending, 0o600); err != nil {
			checkpointErr = fmt.Errorf("暂存续期凭据失败，已停止任务；请检查目录写入权限，必要时重新扫码: %w", err)
			return checkpointErr
		}
		retained = true
		return nil
	}
	candidate, err := weibo.RenewSession(ctx, jar, weibo.RenewOptions{
		PassportURL: client.PassportURL, ReturnURL: client.BaseURL + "/", Timeout: 30 * time.Second, Checkpoint: checkpoint,
	})
	if checkpointErr != nil {
		return nil, checkpointErr
	}
	if err != nil {
		if retained {
			return nil, fmt.Errorf("续期链路中断，新凭据已暂存；下次运行将先校验恢复: %w", err)
		}
		if expired {
			fmt.Fprintf(r.Output, "登录状态：已失效，自动续期未成功（%v）。\n", err)
		} else {
			fmt.Fprintf(r.Output, "提示：登录自动续期未成功（%v），当前登录仍有效。\n", err)
		}
		return jar, nil
	}
	// Never promote an uncheckpointed candidate, even if no cookie changed.
	pending.Cookies = candidate.Snapshot()
	if err := storage.WriteJSON(r.PendingPath, pending, 0o600); err != nil {
		return nil, fmt.Errorf("暂存续期凭据失败，尚未执行任务: %w", err)
	}
	return r.promote(ctx, client, state, pending)
}

func newLoginCredentials(base, candidate *session.Jar) bool {
	old := make(map[string]string)
	for _, c := range base.Snapshot() {
		old[c.Domain+"\x00"+c.Path+"\x00"+c.Name] = c.Value
	}
	for _, c := range candidate.Snapshot() {
		if (c.Name == "ALC" || c.Name == "SUB") && c.Value != "" && old[c.Domain+"\x00"+c.Path+"\x00"+c.Name] != c.Value {
			return true
		}
	}
	return false
}

func verifyRenewed(ctx context.Context, client *weibo.Client, state app.State, before weibo.LoginStatus, expired bool) error {
	status, err := client.LoginStatus(ctx)
	if err != nil {
		return err
	}
	if err := state.CheckAccount(status.UID); err != nil {
		return err
	}
	if !expired && status.UID != before.UID {
		return errors.New("续期后的账户与当前账户不一致")
	}
	return nil
}
