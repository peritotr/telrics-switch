// SPDX-License-Identifier: AGPL-3.0-only

package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"os"
	"strings"
	"time"
)

// ResendURL is where Resend takes messages; tests point it elsewhere.
var ResendURL = "https://api.resend.com/emails"

type resendSender struct {
	key  string
	from *mail.Address
	http *http.Client
}

// NewResend sends through Resend's HTTP API with the given API key. The
// sender address must belong to a domain verified in that Resend account.
func NewResend(apiKey, from string) (Sender, error) {
	addr, err := mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("mail: invalid sender address: %w", err)
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("mail: the Resend API key is empty")
	}
	return &resendSender{key: strings.TrimSpace(apiKey), from: addr, http: &http.Client{Timeout: 30 * time.Second}}, nil
}

func (s *resendSender) Send(ctx context.Context, m Message) error {
	to, err := mail.ParseAddress(m.To)
	if err != nil || strings.ContainsAny(m.Subject, "\r\n") {
		return fmt.Errorf("mail: invalid recipient or subject")
	}
	body, _ := json.Marshal(map[string]any{
		"from": s.from.String(), "to": []string{to.Address}, "subject": m.Subject, "text": m.Body,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", ResendURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("resend: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

// FromEnv builds the sender the environment asks for: RESEND_API_KEY for
// Resend, or SMTP_URL for an SMTP server, with MAIL_FROM as the sender
// address. It returns nil when neither is set: e-mail is then off.
func FromEnv() (Sender, error) {
	key, server, from := os.Getenv("RESEND_API_KEY"), os.Getenv("SMTP_URL"), os.Getenv("MAIL_FROM")
	switch {
	case key == "" && server == "":
		return nil, nil
	case key != "" && server != "":
		return nil, fmt.Errorf("mail: set RESEND_API_KEY or SMTP_URL, not both")
	case from == "":
		return nil, fmt.Errorf("mail: MAIL_FROM is required")
	case key != "":
		return NewResend(key, from)
	}
	return NewSMTP(server, from)
}
