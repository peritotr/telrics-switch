// SPDX-License-Identifier: AGPL-3.0-only

// Package mail sends plain-text e-mail over SMTP. It knows nothing about
// what is sent or why; queuing and retrying belong to the caller.
package mail

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

type Message struct {
	To      string
	Subject string
	Body    string // plain text, UTF-8
}

// Sender delivers one message or says why it could not.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

type smtpSender struct {
	host, addr string
	implicit   bool // TLS from the first byte (smtps)
	auth       smtp.Auth
	from       *mail.Address
}

// NewSMTP takes the server as a URL — smtp://user:password@host:587 (TLS is
// started when the server offers it) or smtps://user:password@host:465 —
// and the sender address, such as "Example <no-reply@example.com>".
func NewSMTP(server, from string) (Sender, error) {
	u, err := url.Parse(server)
	if err != nil || u.Hostname() == "" || (u.Scheme != "smtp" && u.Scheme != "smtps") {
		return nil, fmt.Errorf("mail: server must be smtp://… or smtps://…")
	}
	addr, err := mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("mail: invalid sender address: %w", err)
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"smtp": "587", "smtps": "465"}[u.Scheme]
	}
	s := &smtpSender{host: u.Hostname(), addr: net.JoinHostPort(u.Hostname(), port), implicit: u.Scheme == "smtps", from: addr}
	if u.User != nil {
		pw, _ := u.User.Password()
		s.auth = smtp.PlainAuth("", u.User.Username(), pw, s.host)
	}
	return s, nil
}

func (s *smtpSender) Send(ctx context.Context, m Message) error {
	to, err := mail.ParseAddress(m.To)
	if err != nil || strings.ContainsAny(m.Subject, "\r\n") {
		return fmt.Errorf("mail: invalid recipient or subject")
	}
	d := net.Dialer{Timeout: 15 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Minute))
	if s.implicit {
		conn = tls.Client(conn, &tls.Config{ServerName: s.host})
	}
	c, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok && !s.implicit {
		if err := c.StartTLS(&tls.Config{ServerName: s.host}); err != nil {
			return err
		}
	}
	if s.auth != nil {
		// net/smtp refuses to send a password over a connection that is not
		// encrypted, except to localhost.
		if err := c.Auth(s.auth); err != nil {
			return err
		}
	}
	if err := c.Mail(s.from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(to.Address); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	id := make([]byte, 12)
	rand.Read(id)
	domain := s.from.Address[strings.LastIndex(s.from.Address, "@")+1:]
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\n", s.from.String(), to.String(), mime.QEncoding.Encode("utf-8", m.Subject))
	fmt.Fprintf(&b, "Date: %s\r\nMessage-ID: <%s@%s>\r\n", time.Now().Format(time.RFC1123Z), hex.EncodeToString(id), domain)
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	enc := base64.StdEncoding.EncodeToString([]byte(m.Body))
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	if _, err := w.Write([]byte(b.String())); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
