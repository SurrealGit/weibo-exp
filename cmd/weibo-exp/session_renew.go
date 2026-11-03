package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/SurrealGit/weibo-exp/internal/app"
	"github.com/SurrealGit/weibo-exp/internal/session"
	"github.com/SurrealGit/weibo-exp/internal/weibo"
)

// Renewing daily keeps the ~7-day SUB far from expiry while staying close to
// how often a browser restores its login.
const sessionRenewInterval = 24 * time.Hour

type renewal struct {
	Now    time.Time
	Output io.Writer
	Save   func(*session.Jar) error
}

// ensureSession returns the session this run should use: a renewed one when
// renewal succeeds, otherwise the original. The passport ALC is single-use, so
// a renewed session is saved before any task runs.
func ensureSession(ctx context.Context, client *weibo.Client, jar *session.Jar, state app.State, r renewal) *session.Jar {
	if client.PassportURL == "" {
		return jar
	}
	current, err := client.WithJar(jar).LoginStatus(ctx)
	expired := errors.Is(err, weibo.ErrNotLoggedIn)
	if err != nil && !expired {
		return jar // The runner reports network and other errors as before.
	}
	if !expired && r.Now.Sub(jar.RenewedAt()) < sessionRenewInterval {
		return jar
	}
	// Record the attempt so a failing proactive renewal is retried daily, not
	// on every scheduled run.
	jar.SetRenewedAt(r.Now)
	candidate, err := weibo.RenewSession(ctx, jar, weibo.RenewOptions{
		PassportURL: client.PassportURL, ReturnURL: client.BaseURL + "/", Timeout: 30 * time.Second,
	})
	if err == nil {
		err = verifyRenewed(ctx, client.WithJar(candidate), state, current, expired)
	}
	if err != nil {
		if expired {
			fmt.Fprintf(r.Output, "登录状态：已失效，自动续期未成功（%v）。\n", err)
		} else {
			fmt.Fprintf(r.Output, "提示：登录自动续期未成功（%v），当前登录仍有效。\n", err)
		}
		return jar
	}
	candidate.SetRenewedAt(r.Now)
	if err := r.Save(candidate); err != nil {
		fmt.Fprintf(r.Output, "警告：续期后的会话未能立即保存（%v），运行结束时重试。\n", err)
	}
	fmt.Fprintln(r.Output, "登录状态：已自动续期。")
	return candidate
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
