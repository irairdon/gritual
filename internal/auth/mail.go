package auth

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strings"

	"github.com/irairdon/gritual/internal/config"
)

type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

type stdoutMailer struct{}

func (stdoutMailer) Send(_ context.Context, to, subject, body string) error {
	slog.Info("email stdout delivery", "to", to, "subject", subject)
	fmt.Fprintf(os.Stdout, "gritual-mail to=%s subject=%q\n%s\n", to, subject, body)
	return nil
}

type smtpMailer struct {
	host string
	port string
	user string
	pass string
	from string
}

func newMailer(cfg config.Config) Mailer {
	if cfg.SMTPHost == "" {
		return stdoutMailer{}
	}
	return smtpMailer{
		host: cfg.SMTPHost,
		port: cfg.SMTPPort,
		user: cfg.SMTPUser,
		pass: cfg.SMTPPass,
		from: cfg.SMTPFrom,
	}
}

func (m smtpMailer) Send(_ context.Context, to, subject, body string) error {
	fromAddr := m.from
	if parsed, err := mail.ParseAddress(m.from); err == nil {
		fromAddr = parsed.Address
	}
	msg := strings.Builder{}
	fmt.Fprintf(&msg, "From: %s\r\n", m.from)
	fmt.Fprintf(&msg, "To: %s\r\n", to)
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	msg.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		msg.WriteString("\r\n")
	}

	addr := net.JoinHostPort(m.host, m.port)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, m.host)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if m.user != "" {
		if err := c.Auth(smtp.PlainAuth("", m.user, m.pass, m.host)); err != nil {
			return err
		}
	}
	if err := c.Mail(fromAddr); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg.String())); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
