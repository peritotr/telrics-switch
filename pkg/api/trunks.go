// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// DefaultRealm is the SIP realm passwords are bound to when none is set.
const DefaultRealm = "softswitch"

// SetRealm sets the SIP realm trunk passwords are bound to. The SIP core
// must challenge callers with the same realm, and changing it invalidates
// every trunk password already issued.
func (s *Server) SetRealm(realm string) {
	if realm != "" {
		s.realm = realm
	}
}

// passwordAlphabet leaves out the characters people mix up when typing.
const passwordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// NewTrunkPassword gives the trunk a new random password and returns it;
// only its digest hash is stored, so this is the one time it can be shown.
// The trunk must be one that signs in with a user name.
func (s *Server) NewTrunkPassword(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, trunkID int64) (username, password string, err error) {
	if err := q.QueryRow(ctx, `select username from client_trunks where id = $1 and auth_type = 'digest'`, trunkID).Scan(&username); err != nil {
		if err == pgx.ErrNoRows {
			return "", "", Errf(409, "this trunk is recognised by its address and has no password")
		}
		return "", "", err
	}
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	for i, b := range raw {
		raw[i] = passwordAlphabet[int(b)%len(passwordAlphabet)]
	}
	password = string(raw)
	// The hash SIP digest authentication is defined on (RFC 2617); MD5 is
	// what the protocol asks for here, not a choice.
	sum := md5.Sum([]byte(username + ":" + s.realm + ":" + password))
	var id int64
	err = q.QueryRow(ctx, `update client_trunks set password_ha1 = $2 where id = $1 returning id`, trunkID, hex.EncodeToString(sum[:])).Scan(&id)
	return username, password, err
}

func (s *Server) registerTrunks() {
	s.Handle("POST /v1/client-trunks/{id}/password", true, func(w http.ResponseWriter, r *http.Request, c Caller) error {
		id, err := PathID(r)
		if err != nil {
			return err
		}
		ctx := r.Context()
		if err := s.Resource("client-trunks").Allowed(ctx, s.db, c, id, true); err != nil {
			return err
		}
		var username, password string
		err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
			var name string
			if username, password, err = s.NewTrunkPassword(ctx, tx, id); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `select name from client_trunks where id = $1`, id).Scan(&name); err != nil {
				return err
			}
			// That a password was issued, never the password.
			return s.Audit(ctx, tx, r, c, AuditEntry{Resource: "client-trunks", RowID: id, Action: "password_issued", Label: name,
				Detail: map[string]any{"username": username}})
		})
		if err != nil {
			return err
		}
		WriteJSON(w, 200, trunkPassword{Username: username, Password: password, Realm: s.realm})
		return nil
	}, Summary("Give a trunk that signs in a new random password; returned once."), TakesNothing(), Returns(200, trunkPassword{}))
}

// trunkPassword is a trunk's new sign-in, shown once.
type trunkPassword struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Realm    string `json:"realm"`
}
