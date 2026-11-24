// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Callers of the engine (the SIP core, the management API, an operator's
// scripts) prove themselves with a shared secret, sent with every request
// as "Authorization: Bearer <token>". Two paths stay open because what
// reads them cannot be given a secret as easily and they give none away:
// /metrics (counters, for Prometheus) and /healthz.

// CheckToken reports whether token can be used as the shared secret: the
// characters of a bearer token (RFC 6750), which every caller can put in a
// header and in its configuration unquoted. An empty token is fine and
// means "no authentication".
func CheckToken(token string) error {
	for _, c := range token {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.ContainsRune("-._~+/=", c):
		default:
			return errors.New("the engine token may only contain letters, digits and - . _ ~ + / =")
		}
	}
	return nil
}

// sameBytes compares two digests in constant time; a variable so that a
// test can see it being used.
var sameBytes = subtle.ConstantTimeCompare

// refusedLogged is when a refused request was last written to the log
// (unix seconds): a caller with the wrong token asks many times a second.
var refusedLogged atomic.Int64

// guard lets a request through to next when it carries the token. With no
// token set, everything passes (see Engine.Token).
func (e *Engine) guard(next http.Handler) http.Handler {
	if e.Token == "" {
		return next
	}
	// Digests are compared rather than the values: equal length whatever
	// was sent, so the time taken tells nothing about the token.
	want := sha256.Sum256([]byte(e.Token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		sent, ok := bearer(r.Header.Get("Authorization"))
		got := sha256.Sum256([]byte(sent))
		if sameBytes(got[:], want[:]) != 1 || !ok {
			mUnauthorized.Inc()
			now := time.Now().Unix()
			if last := refusedLogged.Load(); now-last >= 60 && refusedLogged.CompareAndSwap(last, now) {
				// What was sent is never logged: it may be the token with a typo.
				log.Printf("engine: refused %s %s from %s: no or wrong token (logged once a minute; softswitch_engine_unauthorized_total counts them)", r.Method, r.URL.Path, r.RemoteAddr)
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="route-engine"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearer returns the token of an "Authorization: Bearer <token>" header.
func bearer(h string) (string, bool) {
	const scheme = "bearer "
	if len(h) < len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return "", false
	}
	t := strings.TrimSpace(h[len(scheme):])
	return t, t != ""
}
