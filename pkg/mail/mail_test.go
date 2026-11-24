// SPDX-License-Identifier: AGPL-3.0-only

package mail

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResend(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&got)
		if got["subject"] == "refuse" {
			http.Error(w, `{"message":"domain not verified"}`, 403)
			return
		}
		w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()
	defer func(old string) { ResendURL = old }(ResendURL)
	ResendURL = srv.URL

	s, err := NewResend("re_test", "Örnek <no-reply@example.test>")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), Message{To: "ada@example.test", Subject: "Düşük bakiye", Body: "Merhaba"}); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer re_test" || got["text"] != "Merhaba" || got["subject"] != "Düşük bakiye" ||
		got["to"].([]any)[0] != "ada@example.test" || !strings.Contains(got["from"].(string), "no-reply@example.test") {
		t.Errorf("request: %s %v", auth, got)
	}
	if err := s.Send(context.Background(), Message{To: "ada@example.test", Subject: "refuse", Body: "x"}); err == nil || !strings.Contains(err.Error(), "domain not verified") {
		t.Errorf("refusal: %v", err)
	}
	if err := s.Send(context.Background(), Message{To: "ada@example.test", Subject: "a\r\nBcc: x@y", Body: "x"}); err == nil {
		t.Error("a subject with a line break must be refused")
	}
}

// A minimal SMTP server: enough to see what the client sends.
func TestSMTP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	data := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		say := func(s string) { conn.Write([]byte(s + "\r\n")) }
		say("220 test")
		var body strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case inData && line == ".\r\n":
				inData = false
				data <- body.String()
				say("250 ok")
			case inData:
				body.WriteString(line)
			case strings.HasPrefix(line, "EHLO"):
				say("250 test")
			case strings.HasPrefix(line, "DATA"):
				inData = true
				say("354 go")
			case strings.HasPrefix(line, "QUIT"):
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	s, err := NewSMTP("smtp://"+ln.Addr().String(), "Örnek <no-reply@example.test>")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), Message{To: "ada@example.test", Subject: "Parola sıfırlama", Body: "Bağlantı: https://x.test/reset?token=1\n"}); err != nil {
		t.Fatal(err)
	}
	msg := <-data
	head, body, _ := strings.Cut(msg, "\r\n\r\n")
	text, _ := base64.StdEncoding.DecodeString(strings.ReplaceAll(body, "\r\n", ""))
	if !strings.Contains(head, "To: <ada@example.test>") || !strings.Contains(head, "Subject: =?utf-8?q?Parola_s=C4=B1f=C4=B1rlama?=") ||
		!strings.Contains(string(text), "reset?token=1") {
		t.Errorf("message:\n%s\n---\n%s", head, text)
	}
}

func TestFromEnv(t *testing.T) {
	for _, k := range []string{"RESEND_API_KEY", "SMTP_URL", "MAIL_FROM"} {
		t.Setenv(k, "")
	}
	if s, err := FromEnv(); s != nil || err != nil {
		t.Errorf("nothing set: %v %v", s, err)
	}
	t.Setenv("SMTP_URL", "smtp://mail.example.test")
	if _, err := FromEnv(); err == nil {
		t.Error("a missing MAIL_FROM must be an error")
	}
	t.Setenv("MAIL_FROM", "no-reply@example.test")
	if s, err := FromEnv(); s == nil || err != nil {
		t.Errorf("smtp: %v %v", s, err)
	}
	t.Setenv("RESEND_API_KEY", "re_x")
	if _, err := FromEnv(); err == nil {
		t.Error("both set must be an error")
	}
}
