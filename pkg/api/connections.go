// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"softswitch/core/pkg/routing"
)

// A vendor connection's settings are plain columns served by the generic
// operations (crud.go). What needs more is here: the password the switch
// signs in to the vendor with, the test of a connection, and whether the
// engine finds the connections it asks reachable.

// signInBound ties a sealed password to its connection, so that one
// connection's row cannot be copied onto another's.
func signInBound(id int64) string { return "vendor-connection:" + strconv.FormatInt(id, 10) }

var signInWarned atomic.Int64 // unix seconds of the last warning

// openSignIns opens the vendors' passwords in a snapshot with the
// installation's key. A password that cannot be opened (another key, or
// none here) is left out: the vendor's challenge then fails the attempt
// and the call goes on to its next route.
func (s *Server) openSignIns(snap *routing.Snapshot) {
	for id, c := range snap.Connections {
		if len(c.AuthSealed) == 0 || c.AuthUser == "" {
			continue
		}
		plain, err := s.sealer.Open(signInBound(id), c.AuthSealed)
		if err != nil {
			if last := signInWarned.Load(); time.Now().Unix()-last > 300 && signInWarned.CompareAndSwap(last, time.Now().Unix()) {
				log.Printf("vendor connection %d: its sign-in password cannot be read here; set SECRET_KEY to the API's value", id)
			}
			continue
		}
		c.AuthPassword, c.AuthSealed = string(plain), nil
		snap.Connections[id] = c
	}
}

func (s *Server) registerConnections() {
	conns := func() *Resource { return s.Resource("vendor-connections") }

	// The password is written here and nowhere else, sealed; nothing gives
	// it back. The audit record says that it was set, and for which user.
	s.Handle("PUT /v1/vendor-connections/{id}/sign-in", true, func(w http.ResponseWriter, r *http.Request, c Caller) error {
		id, err := PathID(r)
		if err != nil {
			return err
		}
		var in signInRequest
		if err := ReadJSON(r, &in); err != nil {
			return err
		}
		in.Username = strings.TrimSpace(in.Username)
		plain := func(v string, most int) bool {
			return v != "" && len(v) <= most && !strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f })
		}
		if !plain(in.Username, 128) || strings.ContainsAny(in.Username, " \"") {
			return Errf(422, "username must be 1 to 128 characters without spaces or quotes")
		}
		if !plain(in.Password, 128) {
			return Errf(422, "password must be 1 to 128 printable characters")
		}
		ctx := r.Context()
		if err := conns().Allowed(ctx, s.db, c, id, true); err != nil {
			return err
		}
		sealed, err := s.sealer.Seal(signInBound(id), []byte(in.Password))
		if err != nil {
			return err
		}
		err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
			var name string
			if err := tx.QueryRow(ctx, `update vendor_connections set auth_username = $2, auth_secret = $3 where id = $1 returning name`,
				id, in.Username, sealed).Scan(&name); err != nil {
				return err
			}
			return s.Audit(ctx, tx, r, c, AuditEntry{Resource: "vendor-connections", RowID: id, Action: "sign_in_set", Label: name,
				Detail: map[string]any{"username": in.Username}})
		})
		if err != nil {
			return err
		}
		WriteJSON(w, 200, signInState{AuthUsername: in.Username, AuthSet: true})
		return nil
	}, Summary("Set the user name and password the switch signs in to the vendor with. The password is stored sealed and never returned."),
		Takes(signInRequest{}), Returns(200, signInState{}))
	s.Handle("DELETE /v1/vendor-connections/{id}/sign-in", true, func(w http.ResponseWriter, r *http.Request, c Caller) error {
		id, err := PathID(r)
		if err != nil {
			return err
		}
		ctx := r.Context()
		if err := conns().Allowed(ctx, s.db, c, id, true); err != nil {
			return err
		}
		err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
			var name string
			var had bool
			if err := tx.QueryRow(ctx, `
				update vendor_connections t set auth_username = '', auth_secret = null
				from (select auth_set or auth_username <> '' as had from vendor_connections where id = $1 for update) o
				where t.id = $1 returning t.name, o.had`, id).Scan(&name, &had); err != nil {
				return err
			}
			if !had {
				AuditNothing(r)
				return nil
			}
			return s.Audit(ctx, tx, r, c, AuditEntry{Resource: "vendor-connections", RowID: id, Action: "sign_in_cleared", Label: name})
		})
		if err != nil {
			return err
		}
		WriteJSON(w, 200, signInState{})
		return nil
	}, Summary("Remove the connection's sign-in name and password."), Returns(200, signInState{}))

	// One question to the vendor, now, and what came of it. Nothing is
	// changed and no verdict is kept.
	s.Handle("POST /v1/vendor-connections/{id}/test", true, func(w http.ResponseWriter, r *http.Request, c Caller) error {
		id, err := PathID(r)
		if err != nil {
			return err
		}
		ctx := r.Context()
		if err := conns().Allowed(ctx, s.db, c, id, false); err != nil {
			return err
		}
		target := map[string]any{"v": 1}
		var host, transport, tlsName string
		var port int
		if err := s.db.QueryRow(ctx, `select host, port, transport, tls_name from vendor_connections where id = $1`, id).
			Scan(&host, &port, &transport, &tlsName); err != nil {
			return err
		}
		target["host"], target["port"], target["transport"], target["tls_name"] = host, port, transport, tlsName
		raw, err := s.engineDo(ctx, "POST", "/v1/probe", target)
		if err != nil {
			return err
		}
		AuditNothing(r)
		WriteRaw(w, 200, string(raw))
		return nil
	}, Summary("Ask the vendor once, now, whether it is there (SIP OPTIONS from the engine): outcome, SIP code and time taken. Changes nothing."),
		TakesNothing(), Returns(200, probeAnswer{}))

	// Whether the engine finds the connections it asks (those with an
	// interval set) reachable, and how the SIP core's registrations with
	// the vendor stand (ADR-0035), for the connections of one vendor.
	s.Handle("GET /v1/vendor-connections/health", false, func(w http.ResponseWriter, r *http.Request, c Caller) error {
		vendor, err := strconv.ParseInt(r.URL.Query().Get("vendor_id"), 10, 64)
		if err != nil {
			return Errf(400, "vendor_id is required")
		}
		ctx := r.Context()
		var ids []int64
		if err := s.db.QueryRow(ctx, fmt.Sprintf(`select coalesce(array_agg(t.id), '{}') from vendor_connections t
			where t.vendor_id = $2 and %s in %s`, conns().scopeFor("t", false), s.scope), c.ResellerID, vendor).Scan(&ids); err != nil {
			return err
		}
		raw, err := s.engineDo(ctx, "GET", "/v1/peers", nil)
		if err != nil {
			return err
		}
		var all struct {
			Connections   map[string]json.RawMessage `json:"connections"`
			Registrations map[string]json.RawMessage `json:"registrations"`
		}
		if err := json.Unmarshal(raw, &all); err != nil {
			return Errf(502, "unreadable answer from route-engine")
		}
		out, regs := map[string]json.RawMessage{}, map[string]json.RawMessage{}
		for _, id := range ids {
			key := strconv.FormatInt(id, 10)
			if h, ok := all.Connections[key]; ok {
				out[key] = h
			}
			if h, ok := all.Registrations[key]; ok {
				regs[key] = h
			}
		}
		WriteJSON(w, 200, map[string]any{"connections": out, "registrations": regs})
		return nil
	}, Summary("Whether the engine finds a vendor's connections reachable (those with options_interval_sec set), "+
		"and how the switch's registrations with the vendor stand (connections with register set); both by connection id."),
		Query("vendor_id", int64(0), true, "The vendor."), Returns(200, connectionHealth{}))
}

type signInRequest struct {
	Username string `json:"username" openapi:"required"`
	Password string `json:"password" openapi:"required"`
}

// signInState is what a connection's sign-in now is; never the password.
type signInState struct {
	AuthUsername string `json:"auth_username"`
	AuthSet      bool   `json:"auth_set"`
}

// connectionHealth is the engine's verdicts, by connection id.
type connectionHealth struct {
	Connections   map[string]peerHealth         `json:"connections"`
	Registrations map[string]vendorRegistration `json:"registrations"`
}

// LoadSnapshot builds a routing snapshot with the loader in force, for
// services such as route-engine that run outside the API process. The
// vendors' sign-in passwords are opened with the key given to SetSecretKey.
func (s *Server) LoadSnapshot(ctx context.Context) (*routing.Snapshot, error) {
	snap, err := s.snapshot(ctx, s.db)
	if err == nil {
		s.openSignIns(snap)
	}
	return snap, err
}
