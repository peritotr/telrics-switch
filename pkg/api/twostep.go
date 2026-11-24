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

	"softswitch/core/pkg/totp"
)

// Two-step sign-in for panel users: a time-based code (TOTP, RFC 6238) from
// an authenticator app after the password, with single-use recovery codes
// for a lost device. API keys are for programs and are not affected.

const (
	challengePrefix = "tsc_" // tells a sign-in challenge from a session token
	challengeLife   = 5 * time.Minute
	recoveryCodes   = 10
)

// TwoStepIssuer is the name an authenticator app shows beside the account.
var TwoStepIssuer = "Telrics Switch"

// twoStepNow is the clock the codes are checked against; tests move it.
var twoStepNow = time.Now

// SetSecretKey sets the key that seals two-step secrets in the database:
// 32 bytes as 64 hexadecimal digits. Without it they are stored
// unencrypted. Secrets sealed with a key cannot be read without it.
func (s *Server) SetSecretKey(hexKey string) error {
	sealer, err := totp.NewSealer(hexKey)
	if err == nil {
		s.sealer = sealer
	}
	return err
}

// The user's id is bound to the sealed text, so one user's row cannot be
// copied onto another's.
func sealBound(userID int64) string { return fmt.Sprintf("user-totp:%d", userID) }

func recoveryHash(userID int64, code string) []byte {
	return HashKey(fmt.Sprintf("user-recovery:%d:%s", userID, code))
}

// useTOTP accepts a code once: the time step it belongs to must be later
// than the last one accepted for the user.
func (s *Server) useTOTP(ctx context.Context, tx pgx.Tx, userID int64, code string) (bool, error) {
	var stored []byte
	if err := tx.QueryRow(ctx, `select totp_secret from users where id = $1 and totp_on for update`, userID).Scan(&stored); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	key, err := s.sealer.Open(sealBound(userID), stored)
	if err != nil {
		return false, err
	}
	step := totp.Step(key, code, twoStepNow())
	if step == 0 {
		return false, nil
	}
	tag, err := tx.Exec(ctx, `update users set totp_last_step = $2 where id = $1 and totp_last_step < $2`, userID, step)
	return tag.RowsAffected() == 1, err
}

// useSecondStep accepts a code from the app, or a recovery code, once;
// recovery tells which it was.
func (s *Server) useSecondStep(ctx context.Context, tx pgx.Tx, userID int64, code string) (ok, recovery bool, err error) {
	code = totp.PlainCode(code)
	if totp.IsDigits(code, totp.Digits) {
		ok, err = s.useTOTP(ctx, tx, userID, code)
		return ok, false, err
	}
	if code == "" || len(code) > 64 {
		return false, false, nil
	}
	tag, err := tx.Exec(ctx, `update user_recovery_codes set used_at = now() where user_id = $1 and code_hash = $2 and used_at is null`,
		userID, recoveryHash(userID, code))
	return tag.RowsAffected() == 1, true, err
}

// newRecoveryCodes replaces the user's recovery codes and returns the new
// ones; only their hashes are kept.
func newRecoveryCodes(ctx context.Context, tx pgx.Tx, userID int64) ([]string, error) {
	if _, err := tx.Exec(ctx, `delete from user_recovery_codes where user_id = $1`, userID); err != nil {
		return nil, err
	}
	codes := make([]string, 0, recoveryCodes)
	for len(codes) < recoveryCodes {
		plain, shown, err := totp.NewRecoveryCode()
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `insert into user_recovery_codes (user_id, code_hash) values ($1, $2)`, userID, recoveryHash(userID, plain)); err != nil {
			return nil, err
		}
		codes = append(codes, shown)
	}
	return codes, nil
}

// twoStepOff removes everything two-step sign-in keeps for a user and
// tells whether it was on.
func twoStepOff(ctx context.Context, tx pgx.Tx, userID int64) (was bool, email string, err error) {
	err = tx.QueryRow(ctx, `
		with was as (select totp_on, email from users where id = $1 for update),
		     u as (update users set totp_on = false, totp_secret = null, totp_pending = null, totp_last_step = 0 where id = $1),
		     ch as (delete from user_login_challenges where user_id = $1),
		     rc as (delete from user_recovery_codes where user_id = $1)
		select totp_on, email from was`, userID).Scan(&was, &email)
	return
}

// ---- signing in: the second step ----

// loginChallenge is the answer to a correct password when two-step sign-in
// is on: the challenge to send back with a code.
type loginChallenge struct {
	TwoStep   bool      `json:"two_step"` // always true
	Challenge string    `json:"challenge"`
	ExpiresAt time.Time `json:"expires_at"`
}

type loginVerifyRequest struct {
	Challenge string `json:"challenge" openapi:"required"`
	Code      string `json:"code" openapi:"required"`
}

type twoStepState struct {
	Enabled           bool  `json:"enabled"`
	RecoveryCodesLeft int64 `json:"recovery_codes_left"`
}

// twoStepSecret is the secret to put into an authenticator app, as text
// and as the address a QR code carries.
type twoStepSecret struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
}

type twoStepCode struct {
	Code string `json:"code" openapi:"required"`
}

// recoveryCodeList is shown once; only their hashes are kept.
type recoveryCodeList struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

type twoStepDisableRequest struct {
	Password string `json:"password" openapi:"required"`
	Code     string `json:"code" openapi:"required"`
}

func (s *Server) loginChallenge(ctx context.Context, userID int64) (*loginChallenge, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	challenge := challengePrefix + hex.EncodeToString(raw)
	expires := time.Now().Add(challengeLife)
	if _, err := s.db.Exec(ctx, `
		with old as (delete from user_login_challenges where expires_at < now())
		insert into user_login_challenges (token_hash, user_id, expires_at) values ($1, $2, $3)`,
		HashKey(challenge), userID, expires); err != nil {
		return nil, err
	}
	return &loginChallenge{TwoStep: true, Challenge: challenge, ExpiresAt: expires.UTC()}, nil
}

var errExpiredChallenge = Errf(410, "this sign-in has expired; start again with the password")

func (s *Server) loginVerify(w http.ResponseWriter, r *http.Request) {
	var in loginVerifyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil || !strings.HasPrefix(in.Challenge, challengePrefix) || in.Code == "" {
		writeErr(w, Errf(400, "challenge and code are required"))
		return
	}
	ctx := r.Context()
	hash := HashKey(in.Challenge)
	var u Caller
	var locked *time.Time
	err := s.db.QueryRow(ctx, `
		select u.id, u.reseller_id, u.locked_until from user_login_challenges ch join users u on u.id = ch.user_id
		join resellers r on r.id = u.reseller_id
		where ch.token_hash = $1 and ch.expires_at > now() and u.status = 'active' and u.totp_on and r.status = 'active'`,
		hash).Scan(&u.UserID, &u.ResellerID, &locked)
	if errors.Is(err, pgx.ErrNoRows) {
		err = errExpiredChallenge
	}
	if err == nil && locked != nil && locked.After(time.Now()) {
		err = Errf(429, "too many failed attempts; try again later")
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	accepted, recovery := false, false
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		ok, rec, err := s.useSecondStep(ctx, tx, u.UserID, in.Code)
		if err != nil || !ok {
			return err
		}
		// The challenge is spent with the code that answers it.
		tag, err := tx.Exec(ctx, `delete from user_login_challenges where token_hash = $1`, hash)
		if err == nil && tag.RowsAffected() != 1 {
			return errExpiredChallenge
		}
		accepted, recovery = err == nil, rec
		return err
	})
	if err == nil && !accepted {
		// A wrong code counts like a wrong password.
		if err = s.failedLogin(ctx, r, u, "code"); err == nil {
			err = Errf(401, "code is wrong or expired")
		}
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	method := "app"
	if recovery {
		method = "recovery"
	}
	s.startSession(w, r, u, map[string]any{"two_step": true, "method": method})
}

// ---- the signed-in user's own set-up ----

func onlyUser(c Caller) error {
	if c.UserID == 0 {
		return Errf(400, "only a signed-in user has two-step sign-in; API keys do not")
	}
	return nil
}

func (s *Server) twoStepStatus(w http.ResponseWriter, r *http.Request, c Caller) error {
	if err := onlyUser(c); err != nil {
		return err
	}
	var out twoStepState
	err := s.db.QueryRow(r.Context(), `
		select u.totp_on, (select count(*) from user_recovery_codes b where b.user_id = u.id and b.used_at is null)
		from users u where u.id = $1`, c.UserID).Scan(&out.Enabled, &out.RecoveryCodesLeft)
	if err != nil {
		return err
	}
	WriteJSON(w, 200, out)
	return nil
}

// twoStepSetup hands out a new secret. Nothing is on until a code made
// from it comes back; asking again replaces the secret that was waiting.
func (s *Server) twoStepSetup(w http.ResponseWriter, r *http.Request, c Caller) error {
	if err := onlyUser(c); err != nil {
		return err
	}
	secret, err := totp.NewSecret()
	if err != nil {
		return err
	}
	sealed, err := s.sealer.Seal(sealBound(c.UserID), secret)
	if err != nil {
		return err
	}
	var email string
	err = s.db.QueryRow(r.Context(), `update users set totp_pending = $2 where id = $1 and not totp_on returning email`, c.UserID, sealed).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return Errf(409, "two-step sign-in is already on")
	}
	if err != nil {
		return err
	}
	WriteJSON(w, 200, twoStepSecret{Secret: totp.Base32.EncodeToString(secret), URI: totp.URI(TwoStepIssuer, email, secret)})
	return nil
}

func (s *Server) twoStepConfirm(w http.ResponseWriter, r *http.Request, c Caller) error {
	if err := onlyUser(c); err != nil {
		return err
	}
	var in twoStepCode
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil {
		return Errf(400, "invalid request")
	}
	ctx := r.Context()
	var codes []string
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var pending []byte
		var on bool
		var email string
		if err := tx.QueryRow(ctx, `select totp_pending, totp_on, email from users where id = $1 for update`, c.UserID).Scan(&pending, &on, &email); err != nil {
			return err
		}
		if on {
			return Errf(409, "two-step sign-in is already on")
		}
		if pending == nil {
			return Errf(409, "start the set-up first")
		}
		key, err := s.sealer.Open(sealBound(c.UserID), pending)
		if err != nil {
			return err
		}
		step := totp.Step(key, totp.PlainCode(in.Code), twoStepNow())
		if step == 0 {
			return Errf(422, "code is wrong or expired")
		}
		// The confirming code is spent too: it does not sign anyone in afterwards.
		if _, err := tx.Exec(ctx, `update users set totp_on = true, totp_secret = totp_pending, totp_pending = null, totp_last_step = $2
			where id = $1`, c.UserID, step); err != nil {
			return err
		}
		if codes, err = newRecoveryCodes(ctx, tx, c.UserID); err != nil {
			return err
		}
		return s.Audit(ctx, tx, r, c, AuditEntry{Resource: "users", RowID: c.UserID, Action: "two_step_on", Label: email})
	})
	if err != nil {
		return err
	}
	WriteJSON(w, 200, recoveryCodeList{RecoveryCodes: codes})
	return nil
}

// wrongSecondStep counts a wrong code given by a signed-in user towards
// the lock, as at sign-in, and refuses.
func (s *Server) wrongSecondStep(ctx context.Context, r *http.Request, c Caller) error {
	if err := s.failedLogin(ctx, r, c, "code"); err != nil {
		return err
	}
	return Errf(403, "code is wrong or expired")
}

// twoStepReady refuses a signed-in user's request that needs a code when
// two-step sign-in is off or the account is locked.
func (s *Server) twoStepReady(ctx context.Context, c Caller) (passwordHash string, err error) {
	var on bool
	var locked *time.Time
	if err := s.db.QueryRow(ctx, `select totp_on, locked_until, password_hash from users where id = $1`, c.UserID).Scan(&on, &locked, &passwordHash); err != nil {
		return "", err
	}
	if !on {
		return "", Errf(409, "two-step sign-in is off")
	}
	if locked != nil && locked.After(time.Now()) {
		return "", Errf(429, "too many failed attempts; try again later")
	}
	return passwordHash, nil
}

// twoStepRecoveryCodes replaces the recovery codes; it takes a code from
// the app, not a recovery code.
func (s *Server) twoStepRecoveryCodes(w http.ResponseWriter, r *http.Request, c Caller) error {
	if err := onlyUser(c); err != nil {
		return err
	}
	var in twoStepCode
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil {
		return Errf(400, "invalid request")
	}
	ctx := r.Context()
	if _, err := s.twoStepReady(ctx, c); err != nil {
		return err
	}
	var codes []string
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		code := totp.PlainCode(in.Code)
		if !totp.IsDigits(code, totp.Digits) {
			return nil
		}
		ok, err := s.useTOTP(ctx, tx, c.UserID, code)
		if err != nil || !ok {
			return err
		}
		if codes, err = newRecoveryCodes(ctx, tx, c.UserID); err != nil {
			return err
		}
		return s.Audit(ctx, tx, r, c, AuditEntry{Resource: "users", RowID: c.UserID, Action: "recovery_codes_renewed"})
	})
	if err != nil {
		return err
	}
	if codes == nil {
		return s.wrongSecondStep(ctx, r, c)
	}
	WriteJSON(w, 200, recoveryCodeList{RecoveryCodes: codes})
	return nil
}

// twoStepDisable turns two-step sign-in off for the signed-in user, who
// proves it with the password and a code (or a recovery code).
func (s *Server) twoStepDisable(w http.ResponseWriter, r *http.Request, c Caller) error {
	if err := onlyUser(c); err != nil {
		return err
	}
	var in twoStepDisableRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil {
		return Errf(400, "invalid request")
	}
	ctx := r.Context()
	hash, err := s.twoStepReady(ctx, c)
	if err != nil {
		return err
	}
	if !CheckPassword(hash, in.Password) {
		if err := s.failedLogin(ctx, r, c, "password"); err != nil {
			return err
		}
		return Errf(403, "current password is wrong")
	}
	done := false
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		ok, _, err := s.useSecondStep(ctx, tx, c.UserID, in.Code)
		if err != nil || !ok {
			return err
		}
		_, email, err := twoStepOff(ctx, tx, c.UserID)
		if err != nil {
			return err
		}
		done = true
		return s.Audit(ctx, tx, r, c, AuditEntry{Resource: "users", RowID: c.UserID, Action: "two_step_off", Label: email})
	})
	if err != nil {
		return err
	}
	if !done {
		return s.wrongSecondStep(ctx, r, c)
	}
	w.WriteHeader(204)
	return nil
}

// twoStepReset lets an administrator remove another user's two-step set-up
// (a lost phone and no recovery codes). The user signs in with the password
// alone afterwards and may set it up again. Nobody resets their own this
// way: that takes the password and a code.
func (s *Server) twoStepReset(w http.ResponseWriter, r *http.Request, c Caller) error {
	id, err := PathID(r)
	if err != nil {
		return err
	}
	if id == c.UserID {
		return Errf(422, "turn your own two-step sign-in off with your password and a code")
	}
	ctx := r.Context()
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := s.userSnapshot(ctx, tx, c, id); err != nil {
			return err
		}
		was, email, err := twoStepOff(ctx, tx, id)
		if err != nil {
			return err
		}
		return s.Audit(ctx, tx, r, c, AuditEntry{Resource: "users", RowID: id, Action: "two_step_reset", Label: email, Detail: map[string]any{"was_on": was}})
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

// ---- an account that requires two-step sign-in (T-23, ADR-0045) ----

// twoStepSetupRoutes are all a session may reach while its user must still
// set up two-step sign-in: who is signed in, the set-up itself, signing
// out. Everything else, an extension's routes included, is refused with
// errTwoStepRequired. API keys are never held back: they are for programs.
var twoStepSetupRoutes = map[string]bool{
	"GET /v1/session":              true,
	"GET /v1/me":                   true,
	"GET /v1/me/two-step":          true,
	"POST /v1/me/two-step/setup":   true,
	"POST /v1/me/two-step/confirm": true,
	"POST /v1/logout":              true,
}

var errTwoStepRequired = errCoded(403, "two_step_required",
	"this account requires two-step sign-in; set it up before anything else")

// accountTwoStep is whether the account requires two-step sign-in.
type accountTwoStep struct {
	Required bool `json:"required"`
	// UsersWithout counts the account's active users who have not set it
	// up: while it is required, they reach only the set-up.
	UsersWithout int64 `json:"users_without"`
}

type accountTwoStepChange struct {
	Required *bool `json:"required" openapi:"required"`
}

func (s *Server) readAccountTwoStep(ctx context.Context, q Querier, account int64) (out accountTwoStep, err error) {
	err = q.QueryRow(ctx, `
		select r.require_two_step, (select count(*) from users u where u.reseller_id = r.id and u.status = 'active' and not u.totp_on)
		from resellers r where r.id = $1`, account).Scan(&out.Required, &out.UsersWithout)
	return
}

func (s *Server) accountTwoStep(w http.ResponseWriter, r *http.Request, c Caller) error {
	out, err := s.readAccountTwoStep(r.Context(), s.db, c.ResellerID)
	if err != nil {
		return err
	}
	WriteJSON(w, 200, out)
	return nil
}

// setAccountTwoStep turns the requirement on or off for the caller's own
// account. A person who turns it on must have their own second step on:
// they show it works before asking it of everyone, and are never held back
// by their own switch. An API key may turn it on as it is; nobody can be
// locked out by it either way, since the set-up stays open to everyone it
// holds back. Sessions already open are held back from their next request.
func (s *Server) setAccountTwoStep(w http.ResponseWriter, r *http.Request, c Caller) error {
	var in accountTwoStepChange
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil || in.Required == nil {
		return Errf(400, "required (true or false) is needed")
	}
	ctx := r.Context()
	if *in.Required && c.UserID != 0 {
		var on bool
		if err := s.db.QueryRow(ctx, `select totp_on from users where id = $1`, c.UserID).Scan(&on); err != nil {
			return err
		}
		if !on {
			return errCoded(409, "own_two_step_off", "turn your own two-step sign-in on before requiring it of everyone")
		}
	}
	var out accountTwoStep
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var was bool
		var name string
		if err := tx.QueryRow(ctx, `select require_two_step, name from resellers where id = $1 for update`, c.ResellerID).Scan(&was, &name); err != nil {
			return err
		}
		if was != *in.Required {
			if _, err := tx.Exec(ctx, `update resellers set require_two_step = $2 where id = $1`, c.ResellerID, *in.Required); err != nil {
				return err
			}
			action := "require_two_step_off"
			if *in.Required {
				action = "require_two_step_on"
			}
			old, _ := json.Marshal(was)
			now, _ := json.Marshal(*in.Required)
			if err := s.Audit(ctx, tx, r, c, AuditEntry{Resource: "account", RowID: c.ResellerID, Action: action, Label: name,
				Changes: map[string]AuditChange{"require_two_step": {Old: old, New: now}}}); err != nil {
				return err
			}
		} else {
			AuditNothing(r) // already so: nothing changed
		}
		var err error
		out, err = s.readAccountTwoStep(ctx, tx, c.ResellerID)
		return err
	})
	if err != nil {
		return err
	}
	WriteJSON(w, 200, out)
	return nil
}
