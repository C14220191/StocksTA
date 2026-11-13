package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

// Pengiriman email akun (verifikasi, reset kata sandi). Dengan SMTP_HOST terisi, email dikirim lewat
// SMTP (port 465 = TLS langsung, selain itu STARTTLS wajib kalau ada login). Tanpa SMTP_HOST, isi
// email dicetak ke log backend supaya alur tetap bisa dicoba di lokal (salin link dari log).

// Mailer: pengirim email akun.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
	Kind() string // "smtp" | "log"
}

func newMailer() Mailer {
	host := strings.TrimSpace(env("SMTP_HOST", ""))
	if host == "" {
		slog.Warn("SMTP_HOST kosong: email verifikasi/reset tidak dikirim, link dicetak ke log backend")
		return logMailer{}
	}
	user := env("SMTP_USERNAME", "")
	from := env("SMTP_FROM", user)
	addr, err := mail.ParseAddress(from)
	if err != nil {
		slog.Error("SMTP_FROM tidak valid, email dicetak ke log", "from", from, "err", err)
		return logMailer{}
	}
	return &smtpMailer{host: host, port: env("SMTP_PORT", "587"), user: user, pass: env("SMTP_PASSWORD", ""), from: addr}
}

type logMailer struct{}

func (logMailer) Kind() string { return "log" }

func (logMailer) Send(_ context.Context, to, subject, body string) error {
	slog.Warn("EMAIL (SMTP belum diisi, tidak dikirim)", "to", to, "subject", subject, "body", body)
	return nil
}

type smtpMailer struct {
	host, port, user, pass string
	from                   *mail.Address
}

func (m *smtpMailer) Kind() string { return "smtp" }

func (m *smtpMailer) Send(ctx context.Context, to, subject, body string) error {
	msg, err := buildMessage(m.from, to, subject, body, time.Now())
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(m.host, m.port)
	d := &net.Dialer{Timeout: 15 * time.Second}
	tlsCfg := &tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	if m.port == "465" {
		conn, err = (&tls.Dialer{NetDialer: d, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("konek SMTP %s: %w", addr, err)
	}
	deadline := time.Now().Add(45 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, m.host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SMTP greeting: %w", err)
	}
	defer c.Close()
	if m.port != "465" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("STARTTLS: %w", err)
			}
		} else if m.user != "" {
			return errors.New("server SMTP tidak mendukung STARTTLS; login tidak dikirim tanpa enkripsi")
		}
	}
	if m.user != "" {
		if err := c.Auth(smtp.PlainAuth("", m.user, m.pass, m.host)); err != nil {
			return fmt.Errorf("login SMTP: %w", err)
		}
	}
	if err := c.Mail(m.from.Address); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("tulis email: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("kirim email: %w", err)
	}
	return c.Quit()
}

// buildMessage: email teks UTF-8 (quoted-printable). to sudah lolos validasi (tanpa CR/LF).
func buildMessage(from *mail.Address, to, subject, body string, now time.Time) ([]byte, error) {
	if strings.ContainsAny(to, "\r\n") {
		return nil, errors.New("alamat tujuan tidak valid")
	}
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	_, domain, _ := strings.Cut(from.Address, "@")
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from.String())
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), domain)
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n"))); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
