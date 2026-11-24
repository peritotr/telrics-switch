// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionPrefix  = "tss_" // tells a session token from an API key
	sessionLife    = 12 * time.Hour
	minPassword    = 10
	maxPassword    = 72 // bcrypt reads no further
	loginAttempts  = 5
	loginLock      = 15 * time.Minute
	passwordRounds = 12
)

// dummyHash is compared against when the e-mail address is unknown, so that
// answering takes as long as for a known one.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("no such user"), passwordRounds)

func (s *Server) registerUsers() {
	s.HandlePublic("POST /v1/login", s.login,
		Summary("Sign in with e-mail and password; returns a session token, or a challenge when two-step sign-in is on."),
		Takes(loginRequest{}), Returns(200, sessionToken{}, loginChallenge{}))
	s.HandlePublic("POST /v1/login/verify", s.loginVerify,
		Summary("Second step of sign-in when two-step sign-in is on; returns a session token."),
		Takes(loginVerifyRequest{}), Returns(200, sessionToken{}))
	s.Handle("GET /v1/me/two-step", false, s.twoStepStatus,
		Summary("Whether the signed-in user has two-step sign-in on, and the recovery codes left."), Returns(200, twoStepState{}))
	s.Handle("POST /v1/me/two-step/setup", false, s.twoStepSetup,
		Summary("Start setting up two-step sign-in: returns the secret and its otpauth:// address."),
		TakesNothing(), Returns(200, twoStepSecret{}))
	s.Handle("POST /v1/me/two-step/confirm", false, s.twoStepConfirm,
		Summary("Turn two-step sign-in on with a code from the app; returns the recovery codes, once."),
		Takes(twoStepCode{}), Returns(200, recoveryCodeList{}))
	s.Handle("POST /v1/me/two-step/recovery-codes", false, s.twoStepRecoveryCodes,
		Summary("Replace the recovery codes, with a code from the app; returned once."),
		Takes(twoStepCode{}), Returns(200, recoveryCodeList{}))
	s.Handle("POST /v1/me/two-step/disable", false, s.twoStepDisable,
		Summary("Turn two-step sign-in off with the password and a code (or a recovery code)."),
		Takes(twoStepDisableRequest{}), ReturnsNothing())
	s.Handle("GET /v1/account/two-step", false, s.accountTwoStep,
		Summary("Whether the account requires two-step sign-in of its panel users, and how many have not set it up."),
		Returns(200, accountTwoStep{}))
	s.Handle("PUT /v1/account/two-step", true, s.setAccountTwoStep,
		Summary("Require two-step sign-in of the account's panel users, or stop requiring it. A user turning it on must have their own on."),
		Takes(accountTwoStepChange{}), Returns(200, accountTwoStep{}))
	s.Handle("POST /v1/users/{id}/two-step/reset", true, s.twoStepReset,
		Summary("Remove another user's two-step set-up (administrators)."), TakesNothing(), ReturnsNothing())
	s.Handle("POST /v1/logout", false, s.logout, Summary("End the session."), TakesNothing(), ReturnsNothing())
	s.Handle("POST /v1/me/password", false, s.changeOwnPassword,
		Summary("Change the signed-in user's password; the user's other sessions end."),
		Takes(passwordChange{}), ReturnsNothing())
	s.Handle("GET /v1/users", false, s.listUsers, Summary("List panel users."), Returns(200, []panelUser{}))
	s.Handle("POST /v1/users", true, s.createUser, Summary("Create a panel user."), Takes(newUser{}), Returns(201, panelUser{}))
	s.Handle("PATCH /v1/users/{id}", true, s.updateUser, Summary("Change a panel user; only the fields given change."),
		Takes(userChange{}), Returns(200, panelUser{}))
	s.Handle("DELETE /v1/users/{id}", true, s.deleteUser, Summary("Remove a panel user."), ReturnsNothing())
}

type loginRequest struct {
	Email    string `json:"email" openapi:"required"`
	Password string `json:"password" openapi:"required"`
}

// sessionToken is a session: the token to send as a bearer token.
type sessionToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	// TwoStepSetupRequired: the account requires two-step sign-in and the
	// user has not set it up; the session reaches only the set-up until then.
	TwoStepSetupRequired bool `json:"two_step_setup_required,omitempty"`
}

type passwordChange struct {
	Current string `json:"current" openapi:"required"`
	New     string `json:"new" openapi:"required"`
}

// panelUser is a user as the API shows one (userJSON writes it).
type panelUser struct {
	ID          int64      `json:"id"`
	ResellerID  int64      `json:"reseller_id"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Role        string     `json:"role"`   // admin or readonly
	Status      string     `json:"status"` // active or disabled
	Locked      bool       `json:"locked"` // locked for a while after failed sign-ins
	TwoStep     bool       `json:"two_step"`
	LastLoginAt *time.Time `json:"last_login_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

type newUser struct {
	Email    string `json:"email" openapi:"required"`
	Name     string `json:"name" openapi:"required"`
	Role     string `json:"role,omitempty"` // readonly when left out
	Password string `json:"password" openapi:"required"`
}

// userChange changes the fields it carries; Unlock lifts a lock.
type userChange struct {
	Email    *string `json:"email,omitempty"`
	Name     *string `json:"name,omitempty"`
	Role     *string `json:"role,omitempty"`
	Status   *string `json:"status,omitempty"`
	Password *string `json:"password,omitempty"`
	Unlock   bool    `json:"unlock,omitempty"`
}

func hashPassword(pw string) (string, error) { return HashPassword(pw, minPassword) }

// HashPassword returns the hash to store for a password of at least min
// characters.
func HashPassword(pw string, min int) (string, error) {
	if len(pw) < min {
		return "", Errf(422, "password must be at least %d characters", min)
	}
	if len(pw) > maxPassword {
		return "", Errf(422, "password must be at most %d bytes", maxPassword)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), passwordRounds)
	return string(h), err
}

// CheckPassword reports whether pw is the password hash was made from. An
// empty hash stands for "no such account" and takes as long to refuse.
func CheckPassword(hash, pw string) bool {
	h := []byte(hash)
	if hash == "" {
		h = dummyHash
	}
	return bcrypt.CompareHashAndPassword(h, []byte(pw)) == nil && hash != ""
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil || in.Email == "" || in.Password == "" {
		writeErr(w, Errf(400, "email and password are required"))
		return
	}
	ctx := r.Context()
	var u Caller
	var hash, status string
	var locked *time.Time
	var twoStep bool
	err := s.db.QueryRow(ctx, `
		select u.id, u.reseller_id, u.password_hash, u.status, u.locked_until, u.totp_on from users u join resellers r on r.id = u.reseller_id
		where lower(u.email) = lower($1) and r.status = 'active'`, strings.TrimSpace(in.Email)).Scan(&u.UserID, &u.ResellerID, &hash, &status, &locked, &twoStep)
	if errors.Is(err, pgx.ErrNoRows) {
		// Not in the audit log: it has no account to belong to, and anyone
		// could fill the log with addresses that do not exist.
		bcrypt.CompareHashAndPassword(dummyHash, []byte(in.Password))
		writeErr(w, Errf(401, "invalid e-mail or password"))
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	if locked != nil && locked.After(time.Now()) {
		writeErr(w, Errf(429, "too many failed attempts; try again later"))
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil || status != "active" {
		reason := "password"
		if status != "active" {
			reason = "disabled"
		}
		if err := s.failedLogin(ctx, r, u, reason); err != nil {
			writeErr(w, err)
			return
		}
		writeErr(w, Errf(401, "invalid e-mail or password"))
		return
	}
	if twoStep {
		// A correct password earns only the right to present a code.
		out, err := s.loginChallenge(ctx, u.UserID)
		if err != nil {
			writeErr(w, err)
			return
		}
		WriteJSON(w, 200, out)
		return
	}
	s.startSession(w, r, u, nil)
}

// failedLogin counts a wrong password or code and records it. The fifth
// failure in a row locks the account for a while.
func (s *Server) failedLogin(ctx context.Context, r *http.Request, u Caller, reason string) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var email string
		var locked bool
		if err := tx.QueryRow(ctx, `
			update users set
			  locked_until = case when failed_logins + 1 >= $2 then now() + $3::interval else locked_until end,
			  failed_logins = case when failed_logins + 1 >= $2 then 0 else failed_logins + 1 end
			where id = $1 returning email, coalesce(locked_until > now(), false)`,
			u.UserID, loginAttempts, fmt.Sprintf("%d seconds", int(loginLock.Seconds()))).Scan(&email, &locked); err != nil {
			return err
		}
		return s.Audit(ctx, tx, r, Caller{ResellerID: u.ResellerID, UserID: u.UserID}, AuditEntry{Resource: "users", RowID: u.UserID,
			Action: "login_failed", Label: email, Detail: map[string]any{"reason": reason, "locked": locked}})
	})
}

// startSession signs the user in and answers with the session token.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u Caller, detail map[string]any) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeErr(w, err)
		return
	}
	ctx := r.Context()
	token := sessionPrefix + hex.EncodeToString(raw)
	expires := time.Now().Add(sessionLife)
	setupOnly := false
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var email string
		if err := tx.QueryRow(ctx, `
			with seen as (update users set failed_logins = 0, locked_until = null, last_login_at = now() where id = $1
			                returning email, totp_on, reseller_id),
			     old as (delete from user_sessions where expires_at < now()),
			     new as (insert into user_sessions (user_id, token_hash, expires_at) values ($1, $2, $3))
			select seen.email, r.require_two_step and not seen.totp_on from seen join resellers r on r.id = seen.reseller_id`,
			u.UserID, HashKey(token), expires).Scan(&email, &setupOnly); err != nil {
			return err
		}
		if setupOnly {
			if detail == nil {
				detail = map[string]any{}
			}
			detail["two_step_setup_required"] = true
		}
		return s.Audit(ctx, tx, r, Caller{ResellerID: u.ResellerID, UserID: u.UserID}, AuditEntry{Resource: "users", RowID: u.UserID,
			Action: "login", Label: email, Detail: detail})
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	WriteJSON(w, 200, sessionToken{Token: token, ExpiresAt: expires.UTC(), TwoStepSetupRequired: setupOnly})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, c Caller) error {
	if token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); strings.HasPrefix(token, sessionPrefix) {
		if _, err := s.db.Exec(r.Context(), `delete from user_sessions where token_hash = $1`, HashKey(token)); err != nil {
			return err
		}
	}
	w.WriteHeader(204)
	return nil
}

func (s *Server) changeOwnPassword(w http.ResponseWriter, r *http.Request, c Caller) error {
	if c.UserID == 0 {
		return Errf(400, "only a signed-in user has a password")
	}
	var in passwordChange
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil {
		return Errf(400, "invalid request")
	}
	ctx := r.Context()
	var hash string
	if err := s.db.QueryRow(ctx, `select password_hash from users where id = $1`, c.UserID).Scan(&hash); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Current)) != nil {
		return Errf(403, "current password is wrong")
	}
	next, err := hashPassword(in.New)
	if err != nil {
		return err
	}
	// Every other session of the user ends; this one goes on.
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var email string
		if err := tx.QueryRow(ctx, `
			with pw as (update users set password_hash = $2 where id = $1 returning email),
			     ended as (delete from user_sessions where user_id = $1 and token_hash <> $3)
			select email from pw`, c.UserID, next, HashKey(token)).Scan(&email); err != nil {
			return err
		}
		return s.Audit(ctx, tx, r, c, AuditEntry{Resource: "users", RowID: c.UserID, Action: "password_changed", Label: email})
	})
	if err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}

const userJSON = `jsonb_build_object('id', u.id, 'reseller_id', u.reseller_id, 'email', u.email, 'name', u.name, 'role', u.role,
	'status', u.status, 'locked', coalesce(u.locked_until > now(), false), 'two_step', u.totp_on, 'last_login_at', u.last_login_at, 'created_at', u.created_at)`

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, c Caller) error {
	var out string
	err := s.db.QueryRow(r.Context(), fmt.Sprintf(
		`select coalesce(jsonb_agg(%s order by lower(u.name), u.id), '[]')::text from users u where u.reseller_id in %s`,
		userJSON, s.scope), c.ResellerID).Scan(&out)
	if err != nil {
		return err
	}
	WriteRaw(w, 200, out)
	return nil
}

func userRole(role string) error {
	if role != "admin" && role != "readonly" {
		return Errf(422, "role must be admin or readonly")
	}
	return nil
}

func duplicateEmail(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return Errf(409, "a user with this e-mail address already exists")
	}
	return err
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request, c Caller) error {
	var in newUser
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil {
		return Errf(400, "invalid request")
	}
	in.Email, in.Name = strings.TrimSpace(in.Email), strings.TrimSpace(in.Name)
	if in.Name == "" || !strings.Contains(in.Email, "@") || strings.ContainsAny(in.Email, " \t") {
		return Errf(422, "name and a valid e-mail address are required")
	}
	if in.Role == "" {
		in.Role = "readonly"
	}
	if err := userRole(in.Role); err != nil {
		return err
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return err
	}
	var out string
	ctx := r.Context()
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var id int64
		if err := tx.QueryRow(ctx, fmt.Sprintf(`
			with u as (insert into users (reseller_id, email, name, role, password_hash) values ($1, $2, $3, $4, $5) returning *)
			select u.id, %s::text from u`, userJSON), c.ResellerID, in.Email, in.Name, in.Role, hash).Scan(&id, &out); err != nil {
			return err
		}
		return s.auditUser(ctx, tx, r, c, id, "create", nil)
	})
	if err != nil {
		return duplicateEmail(err)
	}
	WriteRaw(w, 201, out)
	return nil
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request, c Caller) error {
	id, err := PathID(r)
	if err != nil {
		return err
	}
	var in userChange
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil {
		return Errf(400, "invalid request")
	}
	// Nobody takes their own rights away or locks themselves out by mistake.
	if id == c.UserID && (in.Role != nil || in.Status != nil) {
		return Errf(422, "you cannot change your own role or status")
	}
	if in.Role != nil {
		if err := userRole(*in.Role); err != nil {
			return err
		}
	}
	if in.Status != nil && *in.Status != "active" && *in.Status != "disabled" {
		return Errf(422, "status must be active or disabled")
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" || in.Email != nil && !strings.Contains(*in.Email, "@") {
		return Errf(422, "name and a valid e-mail address are required")
	}
	var hash *string
	if in.Password != nil {
		h, err := hashPassword(*in.Password)
		if err != nil {
			return err
		}
		hash = &h
	}
	var out string
	ctx := r.Context()
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		before, err := s.userSnapshot(ctx, tx, c, id)
		if err != nil {
			return err
		}
		// A new password or a disabled account ends the user's sessions.
		if err := tx.QueryRow(ctx, fmt.Sprintf(`
		with u as (
		  update users u set
		    email = coalesce($3, email), name = coalesce($4, name), role = coalesce($5, role), status = coalesce($6, status),
		    password_hash = coalesce($7, password_hash),
		    locked_until = case when $8 or $7 is not null then null else locked_until end,
		    failed_logins = case when $8 or $7 is not null then 0 else failed_logins end
		  where u.id = $2 and u.reseller_id in %s returning *),
		ended as (delete from user_sessions where user_id = $2 and ($7::text is not null or $6 = 'disabled') and $2 <> $9)
		select %s::text from u`, s.scope, userJSON),
			c.ResellerID, id, in.Email, in.Name, in.Role, in.Status, hash, in.Unlock, c.UserID).Scan(&out); err != nil {
			return err
		}
		return s.auditUser(ctx, tx, r, c, id, "update", before)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Errf(404, "not found")
	}
	if err != nil {
		return duplicateEmail(err)
	}
	WriteRaw(w, 200, out)
	return nil
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, c Caller) error {
	id, err := PathID(r)
	if err != nil {
		return err
	}
	if id == c.UserID {
		return Errf(422, "you cannot delete your own account")
	}
	ctx := r.Context()
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		before, err := s.userSnapshot(ctx, tx, c, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `delete from users where id = $1`, id); err != nil {
			return err
		}
		return s.auditUser(ctx, tx, r, c, id, "delete", before)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Errf(404, "not found")
	}
	if err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}

// userSnapshot reads a user the caller may manage, for the audit log;
// one it may not is "not found".
func (s *Server) userSnapshot(ctx context.Context, tx pgx.Tx, c Caller, id int64) (AuditRow, error) {
	var ok bool
	if err := tx.QueryRow(ctx, fmt.Sprintf(`select exists(select 1 from users u where u.id = $2 and u.reseller_id in %s)`, s.scope),
		c.ResellerID, id).Scan(&ok); err != nil {
		return nil, err
	}
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return AuditSnapshot(ctx, tx, "users", id)
}

// userNoise are the columns that move by themselves as people sign in;
// they would bury what an administrator changed.
var userNoise = []string{"failed_logins", "locked_until", "last_login_at", "totp_last_step"}

// auditUser records a change to a user: what the row is now against what
// it was. The password hash and the two-step secret are masked by their
// names, as everywhere in the log.
func (s *Server) auditUser(ctx context.Context, tx pgx.Tx, r *http.Request, c Caller, id int64, action string, before AuditRow) error {
	var after AuditRow
	if action != "delete" {
		var err error
		if after, err = AuditSnapshot(ctx, tx, "users", id); err != nil {
			return err
		}
	}
	e := AuditEntry{Resource: "users", RowID: id, Action: action, Changes: AuditDiff(before, after)}
	unlocked := action == "update" && string(before["locked_until"]) != "null" && string(after["locked_until"]) == "null"
	for _, field := range userNoise {
		delete(e.Changes, field)
	}
	if unlocked {
		e.Detail = map[string]any{"unlocked": true}
	}
	if e.Label, _ = userEmail(after); e.Label == "" {
		e.Label, _ = userEmail(before)
	}
	return s.Audit(ctx, tx, r, c, e)
}

func userEmail(row AuditRow) (email string, err error) {
	return email, json.Unmarshal(row["email"], &email)
}
