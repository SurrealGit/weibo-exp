package mailnotify

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http/httptest"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"
)

type serverOptions struct {
	security, auth, fail string
}

func smtpFixture(t *testing.T, options serverOptions) (Config, *tls.Config, <-chan string) {
	t.Helper()
	https := httptest.NewTLSServer(nil)
	cert := https.TLS.Certificates[0]
	pool := x509.NewCertPool()
	pool.AddCert(https.Certificate())
	https.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverTLS := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	if options.security == "tls" {
		listener = tls.NewListener(listener, serverTLS)
	}
	done := make(chan struct{})
	messages := make(chan string, 1)
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		if options.fail == "hang" {
			_, _ = io.Copy(io.Discard, conn)
			return
		}
		wire := textproto.NewConn(conn)
		_ = wire.PrintfLine("220 localhost")
		secured := options.security == "tls"
		for {
			line, err := wire.ReadLine()
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				_ = wire.PrintfLine("250-localhost")
				if !secured && options.fail != "no-starttls" {
					_ = wire.PrintfLine("250-STARTTLS")
				}
				_ = wire.PrintfLine("250 AUTH %s", options.auth)
			case line == "STARTTLS":
				_ = wire.PrintfLine("220 upgrade")
				conn = tls.Server(conn, serverTLS)
				wire = textproto.NewConn(conn)
				secured = true
			case strings.HasPrefix(line, "AUTH"):
				if !secured {
					t.Error("credentials sent without TLS")
					return
				}
				if options.fail == "auth" {
					_ = wire.PrintfLine("535 secret-password token=private-server-response")
					continue
				}
				if options.auth == "LOGIN" {
					_ = wire.PrintfLine("334 VXNlcm5hbWU6")
					username, _ := wire.ReadLine()
					_ = wire.PrintfLine("334 UGFzc3dvcmQ6")
					password, _ := wire.ReadLine()
					if username != base64.StdEncoding.EncodeToString([]byte("sender@example.com")) || password != base64.StdEncoding.EncodeToString([]byte("secret-password")) {
						t.Error("invalid LOGIN exchange")
					}
				} else {
					if line != "AUTH PLAIN "+base64.StdEncoding.EncodeToString([]byte("\x00sender@example.com\x00secret-password")) {
						t.Error("invalid PLAIN exchange")
					}
				}
				_ = wire.PrintfLine("235 authenticated")
			case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
				_ = wire.PrintfLine("250 accepted")
			case line == "DATA":
				_ = wire.PrintfLine("354 content")
				data, err := wire.ReadDotBytes()
				if err != nil {
					return
				}
				messages <- string(data)
				if options.fail == "data" {
					_ = wire.PrintfLine("550 secret-password token=private-server-response")
				} else {
					_ = wire.PrintfLine("250 queued")
				}
			case line == "QUIT":
				return
			default:
				_ = wire.PrintfLine("500 unknown")
			}
		}
	}()
	t.Cleanup(func() { listener.Close(); <-done })
	_, portString, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portString)
	cfg := Config{Enabled: true, Host: "127.0.0.1", Port: port, Security: options.security, Username: "sender@example.com", Password: "secret-password", From: "sender@example.com", To: "receiver@example.com"}
	return cfg, &tls.Config{RootCAs: pool, ServerName: cfg.Host, MinVersion: tls.VersionTLS12}, messages
}

func TestEncryptedSMTP(t *testing.T) {
	for _, tc := range []struct{ input, wire string }{
		{"ssl", "tls"}, {" SSL ", "tls"}, {"ssl/tls", "tls"}, {" SSL/TLS ", "tls"},
		{"tls", "tls"}, {" TLS ", "tls"}, {"starttls", "starttls"}, {" STARTTLS ", "starttls"},
	} {
		for _, auth := range []string{"PLAIN", "LOGIN", "XOAUTH2 PLAIN"} {
			t.Run(tc.input+auth, func(t *testing.T) {
				cfg, tlsConfig, messages := smtpFixture(t, serverOptions{security: tc.wire, auth: auth})
				cfg.Security = tc.input
				if err := send(context.Background(), cfg, "微博超话：登录失效", "请重新登录。\nweibo-exp login\n", tlsConfig); err != nil {
					t.Fatal(err)
				}
				msg, err := mail.ReadMessage(strings.NewReader(<-messages))
				if err != nil {
					t.Fatal(err)
				}
				subject, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
				body, _ := io.ReadAll(quotedprintable.NewReader(msg.Body))
				if subject != "微博超话：登录失效" || !strings.Contains(string(body), "请重新登录。") || strings.Contains(string(body), cfg.Password) {
					t.Fatal("message encoding or privacy failure")
				}
			})
		}
	}
}

func TestSMTPFailuresArePrivate(t *testing.T) {
	for _, failure := range []string{"auth", "data", "no-starttls", "certificate", "unsupported-auth", "oauthbearer", "no-auth"} {
		t.Run(failure, func(t *testing.T) {
			auth := "PLAIN"
			if failure == "unsupported-auth" {
				auth = "XOAUTH2"
			}
			if failure == "oauthbearer" {
				auth = "OAUTHBEARER"
			}
			if failure == "no-auth" {
				auth = ""
			}
			cfg, tlsConfig, _ := smtpFixture(t, serverOptions{security: "starttls", auth: auth, fail: failure})
			if failure == "certificate" {
				tlsConfig.RootCAs = x509.NewCertPool()
			}
			err := send(context.Background(), cfg, "测试", "测试", tlsConfig)
			if err == nil || strings.Contains(err.Error(), "secret-password") || strings.Contains(err.Error(), "private-server-response") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
			if (failure == "unsupported-auth" || failure == "oauthbearer") && !strings.Contains(err.Error(), "当前暂不支持 OAuth") {
				t.Fatalf("missing OAuth guidance: %v", err)
			}
			if failure == "no-auth" && strings.Contains(err.Error(), "OAuth") {
				t.Fatalf("misidentified authentication: %v", err)
			}
		})
	}
}

func TestSMTPTimeoutAndCancellation(t *testing.T) {
	for _, cancelEarly := range []bool{false, true} {
		cfg, tlsConfig, _ := smtpFixture(t, serverOptions{security: "starttls", fail: "hang"})
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		if cancelEarly {
			time.AfterFunc(10*time.Millisecond, cancel)
		}
		start := time.Now()
		err := send(ctx, cfg, "测试", "测试", tlsConfig)
		cancel()
		if err == nil || time.Since(start) > time.Second {
			t.Fatalf("timeout not enforced: %v", err)
		}
		if cancelEarly && !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation missing: %v", err)
		}
	}
}

func TestSMTPValidation(t *testing.T) {
	base := Config{Host: "smtp.example.com", Port: 465, Security: "tls", Username: "user", Password: "secret", From: "sender@example.com", To: "recipient@example.com"}
	for _, change := range []func(*Config){
		func(c *Config) { c.To += "\r\nBcc: evil@example.com" },
		func(c *Config) { c.From += "\nBad" },
		func(c *Config) { c.To += ",second@example.com" },
		func(c *Config) { c.Security = "none" },
		func(c *Config) { c.Security = "" },
		func(c *Config) { c.Security = "ssl\x00" },
		func(c *Config) { c.Port = 0 },
		func(c *Config) { c.Password = "" },
		func(c *Config) { c.Host = "https://smtp.example.com" },
	} {
		cfg := base
		change(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatal("accepted invalid configuration")
		}
	}
	if err := Send(context.Background(), base, "subject\r\nBcc: evil@example.com", "body"); err == nil {
		t.Fatal("accepted header injection")
	}
	if _, _, err := (&loginAuth{}).Start(&smtp.ServerInfo{}); err == nil {
		t.Fatal("LOGIN allowed plaintext")
	}
}

func TestSecurityModeDoesNotInferFromPort(t *testing.T) {
	for _, port := range []int{25, 465, 587, 1465} {
		for _, security := range []string{"ssl", " SSL/TLS ", " TLS "} {
			cfg := Config{Port: port, Security: security}
			if cfg.SecurityMode() != "ssl" || cfg.Security != security {
				t.Fatal("alias changed meaning or stored value")
			}
		}
		if (Config{Port: port, Security: " STARTTLS "}).SecurityMode() != "starttls" {
			t.Fatal("STARTTLS changed meaning")
		}
	}
}
