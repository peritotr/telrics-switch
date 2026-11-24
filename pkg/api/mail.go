// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"softswitch/core/pkg/mail"
	"softswitch/core/pkg/rating"
)

const (
	mailAttempts = 8
	resetLife    = 30 * time.Minute
)

// Execer is satisfied by both a pool and a transaction.
type Execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// SetMail switches e-mail on: messages queued from now on are sent through
// sender. links is the address people reach the panel at, for the links in
// messages, such as https://panel.example.com.
func (s *Server) SetMail(sender mail.Sender, links string) {
	s.mailer, s.panelURL = sender, strings.TrimRight(links, "/")
}

// MailEnabled reports whether messages are being sent.
func (s *Server) MailEnabled() bool { return s.mailer != nil }

// QueueMail stores a message to be sent. Pass the transaction that causes
// the message, so that it is sent if and only if that transaction commits.
// Without a mail server, or without a recipient, it does nothing.
func (s *Server) QueueMail(ctx context.Context, q Execer, to, subject, body string) error {
	to = strings.TrimSpace(to)
	if s.mailer == nil || to == "" {
		return nil
	}
	_, err := q.Exec(ctx, `insert into mail_outbox (to_addr, subject, body) values ($1, $2, $3)`, to, subject, body)
	return err
}

// RunMail sends what is queued until ctx ends.
func (s *Server) RunMail(ctx context.Context, every time.Duration) {
	if s.mailer == nil {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		for {
			n, err := s.SendQueuedMail(ctx)
			if err != nil && ctx.Err() == nil {
				log.Printf("api: mail: %v", err)
			}
			if err != nil || n == 0 {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SendQueuedMail tries one batch of due messages and reports how many it
// took. A message that fails is tried again later, up to mailAttempts
// times, each wait twice the last.
func (s *Server) SendQueuedMail(ctx context.Context) (n int, err error) {
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			select id, to_addr, subject, body, attempts from mail_outbox
			where sent_at is null and next_try_at <= now() and attempts < $1
			order by id limit 20 for update skip locked`, mailAttempts)
		if err != nil {
			return err
		}
		type item struct {
			id       int64
			m        mail.Message
			attempts int
		}
		var due []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.id, &it.m.To, &it.m.Subject, &it.m.Body, &it.attempts); err != nil {
				return err
			}
			due = append(due, it)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		n = len(due)
		for _, it := range due {
			if err := s.mailer.Send(ctx, it.m); err != nil {
				wait := time.Minute << it.attempts
				if _, err := tx.Exec(ctx, `update mail_outbox set attempts = attempts + 1, last_error = $2, next_try_at = now() + $3::interval where id = $1`,
					it.id, err.Error(), fmt.Sprintf("%d seconds", int(wait.Seconds()))); err != nil {
					return err
				}
				continue
			}
			if _, err := tx.Exec(ctx, `update mail_outbox set attempts = attempts + 1, last_error = null, sent_at = now() where id = $1`, it.id); err != nil {
				return err
			}
		}
		return nil
	})
	return n, err
}

// Amount writes micro-units the way people read money here: 1.234,56.
func Amount(micro int64) string {
	sign := ""
	if micro < 0 {
		sign, micro = "-", -micro
	}
	cents := (micro + rating.Micro/200) / (rating.Micro / 100)
	whole := fmt.Sprint(cents / 100)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "." + whole[i:]
	}
	return fmt.Sprintf("%s%s,%02d", sign, whole, cents%100)
}

// ---- forgotten passwords ----

func (s *Server) registerMail() {
	s.HandlePublic("POST /v1/password/forgot", s.forgotPassword,
		Summary("Ask for a password reset e-mail; the answer is the same whether or not the address has a user."),
		Takes(forgotRequest{}), ReturnsNothing())
	s.HandlePublic("POST /v1/password/reset", s.resetPassword, Summary("Set a new password with a reset token."),
		Takes(resetRequest{}), ReturnsNothing())
}

type forgotRequest struct {
	Email string `json:"email"`
}

type resetRequest struct {
	Token    string `json:"token" openapi:"required"`
	Password string `json:"password" openapi:"required"`
}

// NewResetToken returns a token for a link and the hash to store for it.
func NewResetToken() (token string, hash []byte) {
	raw := make([]byte, 32)
	rand.Read(raw)
	token = hex.EncodeToString(raw)
	return token, HashKey(token)
}

// forgotPassword always answers the same, whether or not the address
// belongs to a user, so that it cannot be used to find out who has one.
func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var in forgotRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil {
		writeErr(w, Errf(400, "invalid request"))
		return
	}
	if s.mailer == nil {
		writeErr(w, Errf(503, "e-mail is not set up; ask an administrator to reset the password"))
		return
	}
	ctx := r.Context()
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var id int64
		var name, email string
		err := tx.QueryRow(ctx, `select id, name, email from users where lower(email) = lower($1) and status = 'active'`,
			strings.TrimSpace(in.Email)).Scan(&id, &name, &email)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		// One link at a time, and not more often than once a minute.
		var recent bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from password_resets where user_id = $1 and expires_at > now() + $2::interval)`,
			id, fmt.Sprintf("%d seconds", int((resetLife-time.Minute).Seconds()))).Scan(&recent); err != nil || recent {
			return err
		}
		token, hash := NewResetToken()
		if _, err := tx.Exec(ctx, `
			with old as (delete from password_resets where user_id = $1)
			insert into password_resets (token_hash, user_id, expires_at) values ($2, $1, now() + $3::interval)`,
			id, hash, fmt.Sprintf("%d seconds", int(resetLife.Seconds()))); err != nil {
			return err
		}
		var account int64
		if err := tx.QueryRow(ctx, `select reseller_id from users where id = $1`, id).Scan(&account); err != nil {
			return err
		}
		if err := s.Audit(ctx, tx, r, Caller{ResellerID: account, UserID: id}, AuditEntry{Resource: "users", RowID: id, Action: "password_reset_requested", Label: email}); err != nil {
			return err
		}
		return s.QueueMail(ctx, tx, email, "Parola sıfırlama",
			fmt.Sprintf("Merhaba %s,\n\nParolanızı sıfırlamak için aşağıdaki bağlantıyı açın. Bağlantı 30 dakika geçerlidir ve bir kez kullanılabilir.\n\n%s/reset?token=%s\n\nBu isteği siz yapmadıysanız bu e-postayı yok sayabilirsiniz; parolanız değişmez.\n",
				name, s.panelURL, token))
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var in resetRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil {
		writeErr(w, Errf(400, "invalid request"))
		return
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		writeErr(w, err)
		return
	}
	ctx := r.Context()
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var id int64
		err := tx.QueryRow(ctx, `update password_resets set used_at = now()
			where token_hash = $1 and used_at is null and expires_at > now() returning user_id`, HashKey(in.Token)).Scan(&id)
		if err == pgx.ErrNoRows {
			return Errf(410, "this link is no longer valid; ask for a new one")
		}
		if err != nil {
			return err
		}
		// The new password ends every session and lifts a lock.
		var email string
		var account int64
		if err := tx.QueryRow(ctx, `
			with pw as (update users set password_hash = $2, failed_logins = 0, locked_until = null where id = $1 returning email, reseller_id),
			     ended as (delete from user_sessions where user_id = $1)
			select email, reseller_id from pw`, id, hash).Scan(&email, &account); err != nil {
			return err
		}
		return s.Audit(ctx, tx, r, Caller{ResellerID: account, UserID: id}, AuditEntry{Resource: "users", RowID: id, Action: "password_reset", Label: email})
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(204)
}
