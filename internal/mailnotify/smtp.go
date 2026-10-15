// Package mailnotify sends small, plain-text notifications using encrypted SMTP.
package mailnotify

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

const ConfigFile = "smtp.json"
const StateFile = "notification-state.json"

type Config struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Security string `json:"security"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	To       string `json:"to"`
}

// SecurityMode resolves user-facing aliases without changing stored settings.
// The legacy "tls" value continues to mean implicit TLS, independent of port.
func (c Config) SecurityMode() string {
	mode := strings.ToLower(strings.TrimSpace(c.Security))
	switch mode {
	case "ssl", "ssl/tls", "tls":
		return "ssl"
	default:
		return mode
	}
}

func (c Config) Validate() error {
	if c.Host == "" || strings.ContainsAny(c.Host, " /\\\r\n\t") || strings.Contains(c.Host, "://") {
		return errors.New("SMTP host 必须是服务器主机名或 IP，不包含协议和端口")
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("SMTP port 必须在 1–65535 之间")
	}
	if mode := c.SecurityMode(); mode != "ssl" && mode != "starttls" {
		return errors.New("SMTP security 请填写 ssl（连接时加密，通常为 465 端口）或 starttls（连接后升级加密，通常为 587 端口）；兼容 tls、ssl/tls，不区分大小写")
	}
	if c.Username == "" || c.Password == "" || strings.ContainsAny(c.Username+c.Password, "\r\n\x00") {
		return errors.New("SMTP username 和 password 不能为空或包含换行、空字符")
	}
	for _, address := range []string{c.From, c.To} {
		parsed, err := mail.ParseAddress(address)
		if err != nil || strings.ContainsAny(address, "\r\n") || parsed.Address != address {
			return errors.New("SMTP from 和 to 必须各填写一个邮箱地址，不带显示名称")
		}
	}
	return nil
}

// SMTP server replies can contain credentials or echoed commands. Return only
// fixed stage descriptions; preserve cancellation without exposing raw replies.
func stageError(ctx context.Context, stage string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("邮件发送已取消或超时: %w", err)
	}
	return fmt.Errorf("邮件发送失败：%s；请检查 SMTP 配置、网络和邮箱服务设置", stage)
}

func Send(ctx context.Context, cfg Config, subject, body string) error {
	return send(ctx, cfg, subject, body, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.Host})
}

// tlsConfig is separate only to allow tests to trust a local SMTP certificate.
func send(ctx context.Context, cfg Config, subject, body string, tlsConfig *tls.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if strings.ContainsAny(subject, "\r\n") {
		return errors.New("邮件主题不能包含换行")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		return stageError(ctx, "连接服务器失败")
	}
	defer conn.Close()
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return stageError(ctx, "设置连接超时失败")
	}
	if cfg.SecurityMode() == "ssl" {
		secured := tls.Client(conn, tlsConfig)
		if err := secured.HandshakeContext(ctx); err != nil {
			return stageError(ctx, "SSL/TLS 连接或证书校验失败，请核对端口与加密方式")
		}
		conn = secured
	}
	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return stageError(ctx, "读取服务器欢迎信息失败")
	}
	// The raw connection is always closed above, including after STARTTLS.
	if cfg.SecurityMode() == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return stageError(ctx, "服务器未提供 STARTTLS，已停止发送")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return stageError(ctx, "STARTTLS 连接或证书校验失败")
		}
	}
	_, mechanisms := client.Extension("AUTH")
	var auth smtp.Auth
	oauth := false
	for _, mechanism := range strings.Fields(strings.ToUpper(mechanisms)) {
		if mechanism == "XOAUTH2" || mechanism == "OAUTHBEARER" {
			oauth = true
		}
		if mechanism == "PLAIN" {
			auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
			break
		}
		if mechanism == "LOGIN" {
			auth = &loginAuth{username: cfg.Username, password: cfg.Password}
		}
	}
	if auth == nil {
		if oauth {
			return stageError(ctx, "服务器提供 OAuth 认证但未提供 AUTH PLAIN / LOGIN；当前暂不支持 OAuth，请使用支持授权码或应用专用密码的 SMTP 服务")
		}
		return stageError(ctx, "服务器未提供 AUTH PLAIN 或 LOGIN")
	}
	if err := client.Auth(auth); err != nil {
		return stageError(ctx, "身份认证失败")
	}
	if err := client.Mail(cfg.From); err != nil {
		return stageError(ctx, "发件人被服务器拒绝")
	}
	if err := client.Rcpt(cfg.To); err != nil {
		return stageError(ctx, "收件人被服务器拒绝")
	}
	w, err := client.Data()
	if err != nil {
		return stageError(ctx, "服务器未接受邮件正文")
	}
	if _, err := fmt.Fprint(w, message(cfg, subject, body)); err != nil {
		return stageError(ctx, "传输邮件失败")
	}
	if err := w.Close(); err != nil {
		return stageError(ctx, "未确认服务器接收邮件")
	}
	// DATA's successful final reply is the acknowledgement. A failed QUIT
	// must not turn an accepted notification into an immediate duplicate.
	return nil
}

func message(cfg Config, subject, body string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n",
		cfg.From, cfg.To, mime.QEncoding.Encode("UTF-8", subject), time.Now().Format(time.RFC1123Z))
	w := quotedprintable.NewWriter(&out)
	_, _ = w.Write([]byte(body))
	_ = w.Close()
	return out.String()
}

type loginAuth struct {
	username, password string
	step               int
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("LOGIN requires TLS")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	a.step++
	switch a.step {
	case 1:
		return []byte(a.username), nil
	case 2:
		return []byte(a.password), nil
	default:
		return nil, errors.New("unexpected LOGIN challenge")
	}
}
