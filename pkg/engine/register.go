// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"softswitch/core/pkg/metrics"
	"softswitch/core/pkg/routing"
)

// Registration. A trunk that signs in with a user name may tell the switch
// where it is right now (SIP REGISTER), so that calls can be delivered to
// it and so that it can be reached behind address translation (NAT). The
// SIP core checks the password and passes the request on; whether it is
// taken, for how long, and what a trunk's registrations are is decided and
// kept here. The SIP core holds no registrations of its own.

// Binding is one place a trunk can be reached at right now.
type Binding struct {
	TrunkID int64 `json:"trunk_id"`
	// Contact is the address the trunk named for itself. Behind address
	// translation it is an address only the trunk's own network knows.
	Contact string `json:"contact"`
	// SrcIP, SrcPort and Transport are where the registration really came
	// from and how.
	SrcIP     string `json:"src_ip"`
	SrcPort   int    `json:"src_port"`
	Transport string `json:"transport"`
	// NAT says requests for the trunk go to where the registration came
	// from, not to Contact: the trunk is behind address translation, or
	// connected over TCP or TLS and is reached through that connection.
	NAT       bool      `json:"nat"`
	Since     time.Time `json:"since"`
	Until     time.Time `json:"until"`
	UserAgent string    `json:"user_agent,omitempty"`
	CallID    string    `json:"call_id,omitempty"`
	CSeq      int64     `json:"cseq,omitempty"`
}

// NextHop is where a request for the binding is sent when that is not
// where Contact points; "" when Contact itself is the place.
func (b Binding) NextHop() string {
	if !b.NAT {
		return ""
	}
	host := b.SrcIP
	if a, err := netip.ParseAddr(host); err == nil && a.Is6() {
		host = "[" + host + "]"
	}
	return "sip:" + host + ":" + strconv.Itoa(b.SrcPort) + ";transport=" + b.Transport
}

// hopTransport is the transport a next hop (as NextHop writes it) is
// reached over, in lower case; "" when there is no next hop.
func hopTransport(hop string) string {
	_, t, ok := strings.Cut(hop, ";transport=")
	if !ok {
		return ""
	}
	t, _, _ = strings.Cut(t, ";")
	return strings.ToLower(t)
}

// Registry keeps the bindings. Every operation drops what has expired.
type Registry interface {
	// Put stores a binding, or renews the one with the same Contact, and
	// keeps at most max bindings of the trunk: the ones renewed longest ago
	// make way. It returns the trunk's bindings in force.
	Put(ctx context.Context, b Binding, max int, now time.Time) ([]Binding, error)
	// Remove drops the trunk's binding with that Contact, or all of them
	// when contact is "", and returns what is left.
	Remove(ctx context.Context, trunk int64, contact string, now time.Time) ([]Binding, error)
	// Of returns the trunk's bindings, the most recently renewed first.
	Of(ctx context.Context, trunk int64, now time.Time) ([]Binding, error)
	// All returns every binding in force.
	All(ctx context.Context, now time.Time) ([]Binding, error)
}

// RegLimits bounds how long a registration lasts, in seconds.
type RegLimits struct {
	// Min and Max bound what a trunk may ask for: less than Min is refused
	// (423, with the least it may ask), more than Max is cut down to it.
	Min, Max int
	// NAT is the longest a registration lasts when the trunk is reached
	// through the address the request came from (behind address
	// translation, or over TCP or TLS). The trunk must register again
	// before then, and each registration keeps the path to it open; the
	// switch sends no keepalives of its own.
	NAT int
}

// DefaultRegLimits is what applies when nothing is set.
var DefaultRegLimits = RegLimits{Min: 60, Max: 3600, NAT: 60}

// RegLimitsFromEnv reads REG_MIN_EXPIRES, REG_MAX_EXPIRES and
// REG_NAT_EXPIRES (seconds), each optional.
func RegLimitsFromEnv() (RegLimits, error) {
	l := DefaultRegLimits
	for name, into := range map[string]*int{"REG_MIN_EXPIRES": &l.Min, "REG_MAX_EXPIRES": &l.Max, "REG_NAT_EXPIRES": &l.NAT} {
		if v := os.Getenv(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 86400 {
				return l, fmt.Errorf("%s must be a number of seconds between 1 and 86400", name)
			}
			*into = n
		}
	}
	if l.Min > l.Max {
		return l, fmt.Errorf("REG_MIN_EXPIRES (%d) is more than REG_MAX_EXPIRES (%d)", l.Min, l.Max)
	}
	return l, nil
}

// maxContactsPerRequest bounds what one REGISTER may name.
const maxContactsPerRequest = 10

var (
	mRegStored  = metrics.NewCounter("softswitch_engine_register_total", "Registrations asked about, by outcome.", "result=stored")
	mRegRemoved = metrics.NewCounter("softswitch_engine_register_total", "Registrations asked about, by outcome.", "result=removed")
	mRegQueried = metrics.NewCounter("softswitch_engine_register_total", "Registrations asked about, by outcome.", "result=queried")
	mRegRefused = metrics.NewCounter("softswitch_engine_register_total", "Registrations asked about, by outcome.", "result=refused")
	mRegErrors  = metrics.NewCounter("softswitch_engine_register_errors_total", "Registrations that could not be stored or read (state store unavailable).")
	mUndeliver  = metrics.NewCounter("softswitch_engine_trunk_unreachable_total", "Routes to a client's trunk left out of a call because the trunk was not registered.")
	// A trunk whose codecs the caller's trunk has none of is no route; the
	// trunk's settings, not a fault, so it gets no alert of its own.
	mUndeliverCodec = metrics.NewCounter("softswitch_engine_trunk_codec_mismatch_total", "Routes to a client's trunk left out of a call because it shares no codec with the caller's trunk.")
)

type registerRequest struct {
	V int `json:"v"`
	// AuthUser is the user name the SIP core has verified the password of.
	AuthUser  string `json:"auth_user"`
	SrcIP     string `json:"src_ip"`
	SrcPort   int    `json:"src_port"`
	Transport string `json:"transport"`
	TLSName   string `json:"tls_name"`
	// NAT says the request shows signs of address translation: the
	// addresses it names for itself are not where it came from.
	NAT bool `json:"nat"`
	// Contacts is the request's Contact header, every value of it joined
	// with commas, as it arrived; Expires its Expires header, absent when
	// the request had none.
	Contacts  string `json:"contacts"`
	Expires   *int   `json:"expires"`
	CallID    string `json:"call_id"`
	CSeq      int64  `json:"cseq"`
	UserAgent string `json:"user_agent"`
}

type registerResponse struct {
	V       int  `json:"v"`
	Allowed bool `json:"allowed"`
	// SourceBlocked: as in the answer to authorize.
	SourceBlocked bool   `json:"source_blocked,omitempty"`
	SIPCode       int    `json:"sip_code"`
	Reason        string `json:"reason"`
	// Contacts is the Contact header for the answer: every binding of the
	// trunk in force, each with the seconds it has left. Empty: none.
	Contacts string `json:"contacts,omitempty"`
	// MinExpires goes with 423: the least the trunk may ask for.
	MinExpires int `json:"min_expires,omitempty"`
}

// contact is one value of a Contact header.
type contact struct {
	uri     string
	expires int // -1 = not said
	star    bool
}

// parseContacts reads the values of a Contact header: `"Name" <uri>;params`,
// `<uri>;params`, `uri;params` or `*`, separated by commas.
func parseContacts(h string) ([]contact, error) {
	var out []contact
	for _, part := range splitContacts(h) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if part == "*" {
			out = append(out, contact{star: true, expires: -1})
			continue
		}
		var uri, params string
		if open := bracket(part); open >= 0 {
			end := strings.IndexByte(part[open:], '>')
			if end < 0 {
				return nil, fmt.Errorf("unterminated address")
			}
			uri, params = part[open+1:open+end], part[open+end+1:]
		} else if semi := strings.IndexByte(part, ';'); semi >= 0 {
			// Without brackets the parameters belong to the header.
			uri, params = part[:semi], part[semi:]
		} else {
			uri = part
		}
		uri = strings.TrimSpace(uri)
		if !validContactURI(uri) {
			return nil, fmt.Errorf("not a SIP address")
		}
		c := contact{uri: uri, expires: -1}
		for _, p := range strings.Split(params, ";") {
			k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
			if strings.EqualFold(strings.TrimSpace(k), "expires") {
				n, err := strconv.Atoi(strings.TrimSpace(v))
				if err != nil || n < 0 {
					return nil, fmt.Errorf("bad expires")
				}
				c.expires = n
			}
		}
		out = append(out, c)
		if len(out) > maxContactsPerRequest {
			return nil, fmt.Errorf("too many contacts")
		}
	}
	return out, nil
}

// bracket finds the "<" that opens the address of a value, past a quoted
// display name; -1 when the address stands bare.
func bracket(part string) int {
	quoted := false
	for i := 0; i < len(part); i++ {
		switch c := part[i]; {
		case quoted && c == '\\':
			i++
		case c == '"':
			quoted = !quoted
		case c == '<' && !quoted:
			return i
		}
	}
	return -1
}

// splitContacts cuts at the commas that are not inside quotes or brackets.
func splitContacts(h string) []string {
	var out []string
	start, quoted, angle := 0, false, false
	for i := 0; i < len(h); i++ {
		switch c := h[i]; {
		case quoted:
			if c == '\\' {
				i++
			} else if c == '"' {
				quoted = false
			}
		case c == '"' && !angle:
			quoted = true
		case c == '<':
			angle = true
		case c == '>':
			angle = false
		case c == ',' && !angle:
			out = append(out, h[start:i])
			start = i + 1
		}
	}
	return append(out, h[start:])
}

// validContactURI takes a sip: or sips: address with a host, of a length
// and of characters that are safe to store and to send back in a header.
func validContactURI(uri string) bool {
	if len(uri) < 5 || len(uri) > 255 {
		return false
	}
	rest := ""
	switch low := strings.ToLower(uri); {
	case strings.HasPrefix(low, "sip:"):
		rest = uri[4:]
	case strings.HasPrefix(low, "sips:"):
		rest = uri[5:]
	default:
		return false
	}
	for i := 0; i < len(uri); i++ {
		if c := uri[i]; c <= ' ' || c >= 0x7f || c == '<' || c == '>' || c == '"' || c == ',' || c == '\\' {
			return false
		}
	}
	if at := strings.LastIndexByte(rest, '@'); at >= 0 {
		rest = rest[at+1:]
	}
	host, _, _ := strings.Cut(rest, ";")
	host, _, _ = strings.Cut(host, "?")
	return host != "" && host[0] != ':'
}

// contactHeader is the Contact header of an answer to REGISTER.
func contactHeader(list []Binding, now time.Time) string {
	parts := make([]string, 0, len(list))
	for _, b := range list {
		left := int((b.Until.Sub(now) + time.Second - 1) / time.Second)
		parts = append(parts, "<"+b.Contact+">;expires="+strconv.Itoa(max(left, 1)))
	}
	return strings.Join(parts, ", ")
}

func (e *Engine) regLimits() RegLimits {
	if e.Reg.Max == 0 {
		return DefaultRegLimits
	}
	return e.Reg
}

// Register decides a registration the SIP core has checked the password
// of, and stores or removes the trunk's bindings accordingly.
func (e *Engine) Register(ctx context.Context, in registerRequest) registerResponse {
	refuse := func(code int, reason string) registerResponse {
		mRegRefused.Inc()
		return registerResponse{V: Version, SIPCode: code, Reason: reason}
	}
	in.SrcIP = canonIP(in.SrcIP)
	snap := e.snap.Load()
	if snap == nil || e.Registry == nil {
		return refuse(503, "not ready")
	}
	if blocked, _ := snap.Firewall.Verdict(in.SrcIP); blocked {
		mBlocked.Inc()
		out := refuse(403, "source blocked")
		out.SourceBlocked = true
		return out
	}
	trunk, ok := snap.Registrant(in.AuthUser)
	if !ok {
		return refuse(403, "unknown user")
	}
	transport := strings.ToLower(in.Transport)
	if reason, ok := trunk.Admits(transport, in.TLSName); !ok {
		return refuse(403, reason)
	}
	if client, ok := snap.Clients[trunk.ClientID]; !ok || !client.Active {
		return refuse(403, "account inactive")
	}
	if _, err := netip.ParseAddr(in.SrcIP); err != nil || in.SrcPort < 1 || in.SrcPort > 65535 ||
		(transport != "udp" && transport != "tcp" && transport != "tls") {
		return refuse(400, "bad request")
	}
	contacts, err := parseContacts(in.Contacts)
	if err != nil {
		return refuse(400, "bad contact: "+err.Error())
	}
	now := e.now()
	lim := e.regLimits()
	// A trunk reached through the address its request came from must come
	// back often enough to keep that path open.
	through := in.NAT || transport != "udp"
	least := lim.Min
	if through {
		least = min(least, lim.NAT)
	}
	header := -1
	if in.Expires != nil {
		header = *in.Expires
	}
	var puts []Binding
	var drops []string
	all := false
	for _, c := range contacts {
		exp := c.expires
		if exp < 0 {
			exp = header
		}
		if c.star {
			// "*" means every binding, and only ever to remove them.
			if header != 0 || len(contacts) != 1 {
				return refuse(400, "bad contact: * goes alone, with Expires: 0")
			}
			all = true
			continue
		}
		switch {
		case exp == 0:
			drops = append(drops, c.uri)
			continue
		case exp < 0:
			exp = lim.Max
		case exp < least:
			out := refuse(423, "interval too brief")
			out.MinExpires = least
			return out
		}
		exp = min(exp, lim.Max)
		if through {
			exp = min(exp, lim.NAT)
		}
		puts = append(puts, Binding{TrunkID: trunk.ID, Contact: c.uri, SrcIP: in.SrcIP, SrcPort: in.SrcPort,
			Transport: transport, NAT: through, Since: now.UTC(), Until: now.Add(time.Duration(exp) * time.Second).UTC(),
			UserAgent: clip(in.UserAgent, 120), CallID: clip(in.CallID, 255), CSeq: in.CSeq})
	}
	limit := max(trunk.MaxContacts, 1)
	if len(puts) > limit {
		return refuse(403, "too many contacts")
	}

	fail := func(err error) registerResponse {
		mRegErrors.Inc()
		log.Printf("engine: register %s: %v", in.AuthUser, err)
		return registerResponse{V: Version, SIPCode: 503, Reason: "state store unavailable"}
	}
	var list []Binding
	switch {
	case all:
		list, err = e.Registry.Remove(ctx, trunk.ID, "", now)
	case len(puts) == 0 && len(drops) == 0:
		list, err = e.Registry.Of(ctx, trunk.ID, now)
	}
	if err != nil {
		return fail(err)
	}
	for _, uri := range drops {
		if list, err = e.Registry.Remove(ctx, trunk.ID, uri, now); err != nil {
			return fail(err)
		}
	}
	for _, b := range puts {
		if list, err = e.Registry.Put(ctx, b, limit, now); err != nil {
			return fail(err)
		}
		e.regSrc.add(b.SrcIP, b.Until)
	}
	switch {
	case len(puts) > 0:
		mRegStored.Inc()
	case all || len(drops) > 0:
		mRegRemoved.Inc()
	default:
		mRegQueried.Inc()
	}
	return registerResponse{V: Version, Allowed: true, SIPCode: 200, Reason: "ok", Contacts: contactHeader(list, now)}
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (e *Engine) handleRegister(w http.ResponseWriter, r *http.Request) {
	var in registerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil || in.V != Version || in.AuthUser == "" {
		http.Error(w, "invalid request", 400)
		return
	}
	writeJSON(w, e.Register(r.Context(), in))
}

// handleRegistrations lists bindings: of the trunks named (?trunk_id=,
// which may be given more than once) or of all.
func (e *Engine) handleRegistrations(w http.ResponseWriter, r *http.Request) {
	if e.Registry == nil {
		http.Error(w, "no registry", 503)
		return
	}
	var list []Binding
	var err error
	if ids := r.URL.Query()["trunk_id"]; len(ids) > 0 {
		for _, v := range ids {
			id, perr := strconv.ParseInt(v, 10, 64)
			if perr != nil {
				http.Error(w, "invalid trunk_id", 400)
				return
			}
			var of []Binding
			if of, err = e.Registry.Of(r.Context(), id, e.now()); err != nil {
				break
			}
			list = append(list, of...)
		}
	} else {
		list, err = e.Registry.All(r.Context(), e.now())
	}
	if err != nil {
		mRegErrors.Inc()
		http.Error(w, "state store unavailable", 503)
		return
	}
	if list == nil {
		list = []Binding{}
	}
	writeJSON(w, map[string]any{"v": Version, "registrations": list})
}

// handleUnregister drops a trunk's binding, or all of them: an operator's
// order. The trunk is free to register again.
func (e *Engine) handleUnregister(w http.ResponseWriter, r *http.Request) {
	var in struct {
		V       int    `json:"v"`
		TrunkID int64  `json:"trunk_id"`
		Contact string `json:"contact"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil || in.V != Version || in.TrunkID <= 0 {
		http.Error(w, "invalid request", 400)
		return
	}
	if e.Registry == nil {
		http.Error(w, "no registry", 503)
		return
	}
	left, err := e.Registry.Remove(r.Context(), in.TrunkID, in.Contact, e.now())
	if err != nil {
		mRegErrors.Inc()
		http.Error(w, "state store unavailable", 503)
		return
	}
	if left == nil {
		left = []Binding{}
	}
	writeJSON(w, map[string]any{"v": Version, "registrations": left})
}

// regSources is what the engine remembers, in memory, of the addresses
// registrations came from: enough to tell the SIP core that an address is
// a registered trunk's without a look at the store. It is filled as
// registrations are stored here and replaced from the store with every
// load of the configuration, which also brings in what other engines
// stored and drops what an operator removed.
type regSources struct {
	mu sync.RWMutex
	by map[string]int64 // address -> unix ms its last registration ends
}

func (r *regSources) add(ip string, until time.Time) {
	r.mu.Lock()
	if r.by == nil {
		r.by = map[string]int64{}
	}
	r.by[ip] = max(r.by[ip], until.UnixMilli())
	r.mu.Unlock()
}

func (r *regSources) has(ip string, now time.Time) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.by[ip] > now.UnixMilli()
}

func (r *regSources) replace(list []Binding) {
	by := make(map[string]int64, len(list))
	for _, b := range list {
		by[b.SrcIP] = max(by[b.SrcIP], b.Until.UnixMilli())
	}
	r.mu.Lock()
	r.by = by
	r.mu.Unlock()
}

// syncRegSources reads the registered addresses from the store.
func (e *Engine) syncRegSources(ctx context.Context) {
	if e.Registry == nil {
		return
	}
	list, err := e.Registry.All(ctx, e.now())
	if err != nil {
		mRegErrors.Inc()
		log.Printf("engine: reading registrations: %v", err)
		return
	}
	e.regSrc.replace(list)
}

// deliver turns the routes of a decision that lead to a client's trunk
// into routes to where that trunk is registered right now: one per
// binding, the most recently renewed first. A trunk that is not registered,
// or is no longer one that may register, leaves no route; so does one with
// no codec in common with the caller's trunk (from) unless one of the two
// lets the media relay translate (the route is then transcoded), and it is
// not looked up in the store. Each route carries the trunk's own limits
// and those of the trunk's client, which the admission checks in the same
// script as the rest: the call counts among the trunk's calls (cc:t:<trunk>,
// shared with the calls the trunk places) and among its client's
// (cc:c:<client>), ADR-0042. noCodec says a trunk was
// left out for its codecs. Decisions without such routes, which is all the
// core itself makes, pass untouched and without a look at the store.
func (e *Engine) deliver(ctx context.Context, snap *routing.Snapshot, routes []routing.Route, from routing.Trunk, now time.Time) (out []routing.Route, noCodec bool, err error) {
	toTrunk := false
	for _, r := range routes {
		toTrunk = toTrunk || r.TrunkID != 0
	}
	if !toTrunk {
		return routes, false, nil
	}
	out = make([]routing.Route, 0, len(routes))
	for _, r := range routes {
		if r.TrunkID == 0 {
			out = append(out, r)
			continue
		}
		var list []Binding
		t, ok := snap.Trunk(r.TrunkID)
		transcode := false
		if ok {
			var carried bool
			if transcode, carried = routing.Transcodes(t.Codecs, from.Codecs, t.Transcode || from.Transcode); !carried {
				mUndeliverCodec.Inc()
				noCodec = true
				continue
			}
		}
		if ok && t.Username != "" && e.Registry != nil {
			if cl, ok := snap.Clients[t.ClientID]; ok && cl.Active {
				if list, err = e.Registry.Of(ctx, r.TrunkID, now); err != nil {
					return nil, false, err
				}
			}
		}
		if len(list) == 0 {
			mUndeliver.Inc()
		}
		dst := snap.Clients[t.ClientID]
		for _, b := range list {
			to := r
			to.ConnectionID, to.VendorID = 0, 0
			to.URI, to.NextHop = b.Contact, b.NextHop()
			to.MaxCalls, to.MaxCPS = t.MaxCalls, t.MaxCPS
			to.DstClientID, to.ClientMaxCalls, to.ClientMaxCPS = t.ClientID, dst.MaxCalls, dst.MaxCPS
			to.Transcode = transcode
			out = append(out, to)
		}
	}
	return out, noCodec, nil
}
