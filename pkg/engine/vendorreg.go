// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"softswitch/core/pkg/routing"
	"softswitch/core/pkg/sipctl"
)

// Registering with vendors (T-14, ADR-0035). A vendor that takes calls only
// from a switch registered with it is registered with by the SIP core, from
// the switch's own address, so that the vendor sends its calls where the
// switch listens. The engine says which registrations there are to be
// (GET /v1/vendor-registrations, from the connections in the snapshot) and
// reads back how they stand (VendorRegConfig.List); it sends no REGISTER of
// its own. Nothing here is on the call path.

// VendorRegistration is one registration the SIP core is to hold. Password
// is the connection's sign-in password in the clear: the answer that
// carries it goes only to holders of the engine's token, and is never
// logged.
type VendorRegistration struct {
	ID        int64  `json:"id"`
	Registrar string `json:"registrar"` // sip:host:port;transport=…
	AOR       string `json:"aor"`       // sip:user@domain
	Transport string `json:"transport"` // udp, tcp or tls
	Username  string `json:"username"`
	Password  string `json:"password"`
	Expires   int    `json:"expires"` // seconds asked for
}

// uriHost writes a host for a SIP URI: an IPv6 address goes in brackets.
func uriHost(h string) string {
	if strings.Contains(h, ":") && !strings.HasPrefix(h, "[") {
		return "[" + h + "]"
	}
	return h
}

// regTarget is the address of record and registrar of a connection's
// registration, as the SIP core is given them and reports them back.
func regTarget(c routing.Connection) (aor, registrar string) {
	domain := c.RegisterDomain
	if domain == "" {
		domain = c.Host
	}
	aor = "sip:" + c.AuthUser + "@" + uriHost(domain)
	registrar = "sip:" + net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) + ";transport=" + c.Transport
	return aor, registrar
}

// registers reports whether a connection is to be registered: turned on,
// in use, and with a name to register as.
func registers(c routing.Connection) bool {
	return c.Register && c.Enabled && c.AuthUser != ""
}

// vendorRegistrations is what the SIP core is to register, by connection
// id. Two connections that would register the same address of record with
// the same registrar are one registration, the older connection's.
func vendorRegistrations(snap *routing.Snapshot) []VendorRegistration {
	var out []VendorRegistration
	for id, c := range snap.Connections {
		if !registers(c) {
			continue
		}
		aor, registrar := regTarget(c)
		out = append(out, VendorRegistration{ID: id, Registrar: registrar, AOR: aor, Transport: c.Transport,
			Username: c.AuthUser, Password: c.AuthPassword, Expires: c.RegisterExpires})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	seen := map[string]bool{}
	kept := out[:0]
	for _, r := range out {
		if key := r.AOR + "\x00" + r.Registrar; !seen[key] {
			seen[key] = true
			kept = append(kept, r)
		}
	}
	return kept
}

// regSalt keeps the version of the list from being a plain hash of what it
// holds, passwords included.
var regSalt = func() []byte {
	b := make([]byte, 16)
	rand.Read(b)
	return b
}()

// regVersion names a list: the SIP core asks with the version it holds and
// is told when nothing has changed, so passwords cross the wire only when
// the list does.
func regVersion(list []VendorRegistration) string {
	h := sha256.New()
	h.Write(regSalt)
	json.NewEncoder(h).Encode(list)
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// handleVendorRegistrations gives the SIP core the registrations it is to
// hold. ?version= the one it holds: when that is still current, the answer
// says so and carries nothing else.
func (e *Engine) handleVendorRegistrations(w http.ResponseWriter, r *http.Request) {
	snap := e.snap.Load()
	if snap == nil {
		http.Error(w, "not loaded", 503)
		return
	}
	list := vendorRegistrations(snap)
	if list == nil {
		list = []VendorRegistration{}
	}
	version := regVersion(list)
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("version") == version {
		writeJSON(w, map[string]any{"v": Version, "version": version, "unchanged": true})
		return
	}
	writeJSON(w, map[string]any{"v": Version, "version": version, "registrations": list})
}

// Registration verdicts, as GET /v1/peers gives them.
const (
	VendorRegPending    = "pending"    // not yet registered, nothing has gone wrong
	VendorRegRegistered = "registered" // the vendor took it
	VendorRegFailing    = "failing"    // the last attempt failed (Reason says how)
	VendorRegUnknown    = "unknown"    // the SIP core could not be asked
)

// VendorRegState is what the engine knows of one connection's registration.
type VendorRegState struct {
	State string `json:"state"`
	// Reason is the SIP core's word for the state: pending, registered,
	// no_answer, credentials, refused, error, or sip_core when it could not
	// be asked.
	Reason    string    `json:"reason"`
	Since     time.Time `json:"since"` // when State began
	CheckedAt time.Time `json:"checked_at"`
	// Expires is the time the vendor granted (asked for, until it answers).
	Expires int `json:"expires,omitempty"`
}

// vendorRegState is every registration's verdict; replaced, never changed.
type vendorRegState struct {
	regs map[int64]VendorRegState
}

func (s *vendorRegState) count(state string) int {
	n := 0
	for _, r := range s.regs {
		if r.State == state {
			n++
		}
	}
	return n
}

// VendorRegConfig is how the engine reads the SIP core's registrations.
type VendorRegConfig struct {
	// List asks the SIP core how its registrations stand.
	List func(ctx context.Context) ([]sipctl.Registration, error)
	// Interval is the time between questions; 0 = ten seconds.
	Interval time.Duration
}

// VendorRegConfigFromEnv reads REG_STATUS_SEC, the time between questions.
func VendorRegConfigFromEnv(list func(ctx context.Context) ([]sipctl.Registration, error)) (*VendorRegConfig, error) {
	c := &VendorRegConfig{List: list}
	if v := os.Getenv("REG_STATUS_SEC"); v != "" {
		sec, err := strconv.Atoi(v)
		if err != nil || sec < 1 {
			return nil, errors.New("REG_STATUS_SEC must be a positive number of seconds")
		}
		c.Interval = time.Duration(sec) * time.Second
	}
	return c, nil
}

// RunVendorRegs asks the SIP core how the registrations stand until ctx
// ends.
func (e *Engine) RunVendorRegs(ctx context.Context, cfg *VendorRegConfig) {
	every := cfg.Interval
	if every == 0 {
		every = 10 * time.Second
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		if snap := e.snap.Load(); snap != nil {
			e.vendorRegRound(ctx, cfg, snap)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// vendorRegRound reads the SIP core's registrations and stores verdicts for
// the connections that are to be registered.
func (e *Engine) vendorRegRound(ctx context.Context, cfg *VendorRegConfig, snap *routing.Snapshot) {
	var reported []sipctl.Registration
	var askErr error
	wanted := false
	for _, c := range snap.Connections {
		if registers(c) {
			wanted = true
			break
		}
	}
	if wanted {
		ask, cancel := context.WithTimeout(ctx, 3*time.Second)
		reported, askErr = cfg.List(ask)
		cancel()
		if ctx.Err() != nil {
			return
		}
	}
	prev := e.vendorRegs.Load()
	next := e.vendorRegVerdicts(prev, snap, reported, askErr, time.Now())
	if askErr != nil && (prev == nil || prev.count(VendorRegUnknown) == 0) {
		log.Printf("engine: asking the SIP core how vendor registrations stand: %v", askErr)
	}
	e.vendorRegs.Store(next)
}

// vendorRegVerdicts turns what the SIP core reported into verdicts. A
// registration on its way (a refresh, or a new try after a failure) keeps
// the verdict it had, so that a working registration does not show as
// pending at every refresh, nor a failing one at every retry.
func (e *Engine) vendorRegVerdicts(prev *vendorRegState, snap *routing.Snapshot, reported []sipctl.Registration, askErr error, now time.Time) *vendorRegState {
	byTarget := make(map[string]sipctl.Registration, len(reported))
	for _, r := range reported {
		byTarget[r.AOR+"\x00"+r.Registrar] = r
	}
	next := &vendorRegState{regs: map[int64]VendorRegState{}}
	for id, c := range snap.Connections {
		if !registers(c) {
			continue
		}
		var was VendorRegState
		if prev != nil {
			was = prev.regs[id]
		}
		v := VendorRegState{CheckedAt: now, Expires: c.RegisterExpires}
		aor, registrar := regTarget(c)
		r, found := byTarget[aor+"\x00"+registrar]
		switch {
		case askErr != nil:
			v.State, v.Reason = VendorRegUnknown, "sip_core"
		case !found:
			// Not yet in the SIP core's table: it reads the list every few
			// seconds.
			v.State, v.Reason = VendorRegPending, sipctl.RegPending
		case r.State == sipctl.RegRegistered:
			v.State, v.Reason, v.Expires = VendorRegRegistered, r.State, r.Expires
		case r.State == sipctl.RegPending:
			v.State, v.Reason = VendorRegPending, r.State
			if was.State == VendorRegRegistered || was.State == VendorRegFailing {
				v.State, v.Reason, v.Expires = was.State, was.Reason, was.Expires
			}
		default:
			v.State, v.Reason = VendorRegFailing, r.State
		}
		v.Since = was.Since
		if v.State != was.State || was.Since.IsZero() {
			v.Since = now
			switch {
			case v.State == VendorRegFailing:
				log.Printf("engine: vendor connection %d (%s): registration failed (%s)", id, c.Host, v.Reason)
			case v.State == VendorRegRegistered:
				log.Printf("engine: vendor connection %d (%s): registered for %d s", id, c.Host, v.Expires)
			}
		}
		next.regs[id] = v
	}
	return next
}
