// SPDX-License-Identifier: AGPL-3.0-only

// Package engine is the call-control service the SIP core talks to. It
// decides every call from an in-memory snapshot and keeps per-call state in
// Redis; nothing on the call path queries PostgreSQL.
//
// The wire interface (version 1) is plain HTTP and JSON and knows nothing
// about the SIP server behind it: the SIP core asks to authorize a call, then
// reports what happened to it.
package engine

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"softswitch/core/pkg/metrics"
	"strings"
	"sync/atomic"
	"time"

	"softswitch/core/pkg/numplan"
	"softswitch/core/pkg/routing"
)

// Version is the wire interface version this engine speaks.
const Version = 1

// Loader builds a fresh routing snapshot from the configuration store.
type Loader func(context.Context) (*routing.Snapshot, error)

// Sink receives finished call legs. It must not lose a record it accepted.
type Sink interface {
	Publish(CDR)
}

// CDR is one call leg as the rater expects it.
type CDR struct {
	CallID       string     `json:"call_id"`
	Leg          int        `json:"leg"`
	ClientID     int64      `json:"client_id"`
	TrunkID      *int64     `json:"trunk_id"`
	ConnectionID *int64     `json:"connection_id"`
	Src          string     `json:"src"`
	SrcIP        string     `json:"src_ip"`
	Dst          string     `json:"dst"`
	StartAt      time.Time  `json:"start_at"`
	AnswerAt     *time.Time `json:"answer_at"`
	EndAt        time.Time  `json:"end_at"`
	SIPCode      int        `json:"sip_code"`
	// PDDMs is the time the far end took to report progress on this
	// attempt, in milliseconds; 0 when it never did.
	PDDMs int64 `json:"pdd_ms"`
	// DstTrunkID is the client's trunk the attempt was delivered to
	// (routing.ToTrunk); nil on a leg to a vendor. Left out of the JSON when
	// nil, so that a rater older than the field still takes vendor legs.
	DstTrunkID *int64 `json:"dst_trunk_id,omitempty"`
}

// CallInfo is what an Adjust hook is told about a call besides the decision.
type CallInfo struct {
	CallID, Src, SrcIP, Dst string
	// Asserted is the number the caller's side asserts (the user part of
	// P-Asserted-Identity); "" when there is none.
	Asserted string
	Now      time.Time
}

// Leg is one attempt of a call as it ends, or a call the engine refused
// (Record.ConnectionID is then nil). PDD is the time the far end took to
// report progress (0 = it never did), Talk the answered time in whole
// seconds, SellPerMin the client's price for the destination (0 for a
// refused call). Final says no further attempt follows.
//
// NotSent says the attempt never left the switch: the SIP core could not
// set it up (its media relay took no offer, ADR-0039). Record.ConnectionID
// still names the route the engine chose, but that vendor was never asked,
// so the leg says nothing about its quality.
type Leg struct {
	Record     CDR
	PDD        time.Duration
	Talk       int
	SellPerMin int64
	Final      bool
	NotSent    bool
}

// Hangup ends an established call on the SIP core. It is how the engine
// stops a call whose funds have run out.
type Hangup func(ctx context.Context, callID string) error

// DefaultReserve is the talk time, in seconds, paid for in one step.
const DefaultReserve = 60

const (
	// extendLead is how long before the reserved time runs out the engine
	// reserves the next step.
	extendLead = 10 * time.Second
	// cutLead is how long before that moment a call with no more funds is
	// ended, so that it is never billed past what was held for it.
	cutLead = time.Second
	// sweepEvery is how often due calls are looked at.
	sweepEvery = 250 * time.Millisecond
)

type Engine struct {
	snap   atomic.Pointer[routing.Snapshot]
	loaded atomic.Int64 // unix seconds of the last successful load
	// loadTook is how long the last successful load took, in nanoseconds.
	loadTook atomic.Int64
	synced   atomic.Int64 // store clock (ms) when that load started
	load     Loader
	calls    Calls
	sink     Sink
	now      func() time.Time

	// Reserve is the reservation step in seconds; Hangup may be nil, and
	// then only the SIP core's own timer ends a call.
	Reserve int
	Hangup  Hangup
	// MaxCalls, if set, gives the most calls the whole system may carry at
	// once (0 = no limit). It is asked for every call, so it may change
	// while the engine runs.
	MaxCalls func() int
	// TranscodeMax is the most calls whose audio the media relay may
	// translate at once (0 = none). A route that needs it is passed over
	// while that many are in progress, as one whose vendor is at its call
	// limit is. Translation costs the relay's processor, which a single
	// node shares with every other call; this is that node's ceiling.
	TranscodeMax int
	// Adjust, if set, sees every call the engine is about to let through
	// and returns the decision to act on: it may put the routes in another
	// order, leave some out, or refuse the call (Allowed false, with a SIP
	// code and reason). It runs on the call path, for every call: memory
	// only, no waiting. Quality-based routing and traffic screening hook in
	// here; the core itself has no such rules.
	Adjust func(CallInfo, routing.Decision) routing.Decision
	// Incoming, if set, is asked about a call whose source matches no trunk
	// (it would be refused as "unknown source") and may take it on behalf
	// of a client: the call is then decided as that client's (see
	// routing.Inbound), and Adjust sees it as any other. It is how numbers
	// a client owns are reached from a carrier; the core itself takes no
	// such calls. It runs on the call path: it decides from the snapshot it
	// is handed and what it holds in memory, with no I/O and no waiting, and
	// must not change the snapshot. It is not asked about a caller that
	// signed in, nor about an address the operator's firewall denies.
	// Taking a call does not vouch for its address (source_known stays
	// false): the SIP core still treats it as a stranger unless it is a
	// vendor connection's host or the firewall allows it.
	Incoming func(CallInfo, *routing.Snapshot) (routing.Inbound, bool)
	// OnLeg, if set, hears of every attempt as it ends and of every call
	// refused: what an extension measures vendors' quality and clients'
	// traffic from. It is called on the event path and must not block. An
	// event may be delivered more than once, and so may a leg.
	OnLeg func(Leg)

	// Registry keeps where trunks have registered from; Reg bounds how
	// long a registration lasts (zero: DefaultRegLimits). Without a
	// registry no trunk can register and none can be called.
	Registry Registry
	Reg      RegLimits
	regSrc   regSources

	// Unban, if set, makes the SIP core forget a ban; bans are those it
	// reported.
	Unban Unban
	bans  bans

	// Token is the secret every caller must send ("Authorization: Bearer
	// <token>"); a request without it is answered 401, except /metrics and
	// /healthz. Empty: anyone who reaches the listener is served. Set it
	// before Handler is called.
	Token string

	peers      atomic.Pointer[peerState]      // vendors' certificates, when checked
	health     atomic.Pointer[healthState]    // vendors asked whether they are there
	vendorRegs atomic.Pointer[vendorRegState] // the SIP core's registrations with vendors
	prober     *Prober                        // sends those questions; nil = defaults
	sipCertEnd atomic.Int64                   // unix seconds; 0 = not known

	live      live
	authorize latency
	rejected  atomic.Uint64
	extended  atomic.Uint64
	cut       atomic.Uint64
}

func New(load Loader, calls Calls, sink Sink) *Engine {
	return &Engine{load: load, calls: calls, sink: sink, now: time.Now, Reserve: DefaultReserve, TranscodeMax: DefaultTranscodeMax}
}

// Reload replaces the snapshot. Calls in flight keep the one they started with.
func (e *Engine) Reload(ctx context.Context) error {
	// Holds settled before this moment are in the balances about to be read.
	started, clockErr := e.calls.Clock(ctx)
	begun := time.Now()
	s, err := e.load(ctx)
	if err != nil {
		return err
	}
	e.loadTook.Store(int64(time.Since(begun)))
	// The new balances go in first: for a moment a settled charge may count
	// twice, never not at all.
	e.snap.Store(s)
	if clockErr == nil {
		e.synced.Store(started.UnixMilli())
		if err := e.calls.Purge(ctx, started.UnixMilli()); err != nil {
			log.Printf("engine: dropping settled holds: %v", err)
		}
	}
	e.loaded.Store(e.now().Unix())
	e.syncRegSources(ctx)
	return nil
}

// RunSweep extends or ends answered calls as their reserved time runs out,
// until ctx ends.
func (e *Engine) RunSweep(ctx context.Context) {
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for {
				n, err := e.Sweep(ctx)
				if err != nil {
					mSweepErrors.Inc()
					log.Printf("engine: sweep: %v", err)
				}
				if err != nil || n < sweepBatch {
					break
				}
			}
			for {
				n, err := e.SweepExpired(ctx)
				if err != nil {
					mSweepErrors.Inc()
					log.Printf("engine: expired calls: %v", err)
				}
				if err != nil || n < sweepBatch {
					break
				}
			}
		}
	}
}

// SweepExpired writes the records of calls that have outlived the longest
// they could last with no end reported, and reports how many there were.
func (e *Engine) SweepExpired(ctx context.Context) (int, error) {
	now := e.now()
	ids, err := e.calls.Expired(ctx, now, sweepBatch)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := e.expire(ctx, id); err != nil {
			return len(ids), err // the lease brings the rest back
		}
	}
	return len(ids), nil
}

// expire finishes a call whose end the SIP core never reported: it
// restarted and lost the call, or the engine could not be reached when the
// call ended. The call's state has expired (its allowed duration and the
// setup slack have passed), so the call is certainly over, but when it
// ended is not known. Its record holds what is known and charges nothing
// that is not (ADR-0029): an answered attempt is recorded as answered
// (200) and ending at its answer, with no talk time; one never answered
// fails with 408 at the moment its state expired. The money held for it is
// let go.
func (e *Engine) expire(ctx context.Context, id string) error {
	c, err := e.calls.Get(ctx, id)
	if err != nil {
		return err
	}
	if c == nil {
		return e.calls.Forget(ctx, id) // finished meanwhile, or gone
	}
	rec := c.cdr(id, time.UnixMilli(c.StartMs).Add(time.Duration(c.TTL)*time.Second))
	if rec.AnswerAt != nil {
		rec.SIPCode, rec.EndAt = 200, *rec.AnswerAt
	} else {
		rec.SIPCode = 408
	}
	var pdd time.Duration
	if c.RingMs != 0 {
		rec.PDDMs = max(c.RingMs-c.AttemptMs, 0)
		pdd = time.Duration(rec.PDDMs) * time.Millisecond
	}
	// As for an ended call: the hold goes before the record, and the record
	// before the state, so that a repeat after a crash finds the call again.
	if err := e.calls.Release(ctx, id, c, 0, 0); err != nil {
		return err
	}
	e.sink.Publish(rec)
	mExpired.Inc()
	log.Printf("engine: call %s expired with no end reported; recorded as %d", id, rec.SIPCode)
	if e.OnLeg != nil {
		e.OnLeg(Leg{Record: rec, PDD: pdd, SellPerMin: c.Rate.PricePerMin, Final: true})
	}
	return e.calls.Finish(ctx, id, c)
}

const sweepBatch = 500

// Sweep deals with the calls that are due and reports how many there were.
func (e *Engine) Sweep(ctx context.Context) (int, error) {
	now := e.now()
	ids, err := e.calls.Due(ctx, now, sweepBatch)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := e.sweep(ctx, id, now); err != nil {
			return len(ids), err // the lease brings the rest back
		}
	}
	return len(ids), nil
}

func (e *Engine) sweep(ctx context.Context, id string, now time.Time) error {
	c, err := e.calls.Get(ctx, id)
	if err != nil {
		return err
	}
	if c == nil || c.AnswerMs == 0 {
		return e.calls.Defer(ctx, id, time.Time{}) // a stale entry
	}
	if c.Final {
		end := c.reservedUntil().Add(-cutLead)
		if now.Before(end) {
			return e.calls.Defer(ctx, id, end)
		}
		if e.Hangup == nil {
			return e.calls.Defer(ctx, id, now.Add(time.Hour))
		}
		e.cut.Add(1)
		mCut.Inc()
		if err := e.Hangup(ctx, id); err != nil {
			mHangupErrors.Inc()
			log.Printf("engine: hangup %s: %v", id, err)
		}
		// Look again until the SIP core reports the call ended.
		return e.calls.Defer(ctx, id, now.Add(2*time.Second))
	}

	f, bf := e.fundsOf(c)
	next := func(sec int, lead time.Duration) time.Time {
		return time.UnixMilli(c.AnswerMs + int64(sec)*1000).Add(-lead)
	}
	if want := min(c.Reserved+e.Reserve, routing.MaxCallSeconds); want > c.Reserved {
		res, free, bundle, err := e.extend(ctx, id, c, want, false, f, bf, next(want, extendLead))
		if err != nil {
			return err
		}
		switch res {
		case extDone:
			e.extended.Add(1)
			mExtended.Inc()
			return nil
		case extShort:
			// Not a whole step: take what the bundle and the remaining
			// funds pay for.
			if sec := c.Rate.MaxSecondsAfter(c.held()+free.Money, bundle, routing.MaxCallSeconds); sec > c.Reserved && sec < want {
				if res, _, _, err := e.extend(ctx, id, c, sec, true, f, bf, next(sec, cutLead)); err != nil || res != extShort {
					return err
				}
			}
		default:
			return nil // gone, or another engine has just dealt with it
		}
	}
	_, _, err = e.calls.Extend(ctx, id, c, c.Reserved, c.BundleSec, true, f, bf, next(c.Reserved, cutLead))
	return err
}

// fundsOf is what the snapshot says the call's client may spend and what
// it saw left of the call's bundle for the call's period.
func (e *Engine) fundsOf(c *Call) (f, bf Funds) {
	snap := e.snap.Load()
	if snap == nil {
		return
	}
	cl, ok := snap.Clients[c.ClientID]
	if !ok || !cl.Active {
		return
	}
	f = Funds{Total: cl.Balance + cl.CreditLimit, SyncedMs: e.synced.Load()}
	if c.BundleID != 0 {
		for _, b := range snap.Bundles[c.ClientID] {
			if b.ID == c.BundleID {
				bf = Funds{Total: b.Left(c.BundlePeriod), SyncedMs: f.SyncedMs}
			}
		}
	}
	return
}

// bundleTries is how often a hold is tried again because other calls took
// seconds of the bundle meanwhile, before the call goes on without more.
const bundleTries = 3

// extend raises the call's reserved time to sec, taking from its bundle
// what is free there and from the balance the rest. Besides the outcome
// and what was free it reports the bundle seconds the last attempt counted
// on for the call.
func (e *Engine) extend(ctx context.Context, id string, c *Call, sec int, final bool, f, bf Funds, next time.Time) (int, Free, int64, error) {
	// The snapshot's count is a first guess; the store knows what other
	// calls hold.
	bundle := c.bundleFor(sec, bf.Total)
	for try := 0; ; try++ {
		res, free, err := e.calls.Extend(ctx, id, c, sec, bundle, final, f, bf, next)
		if err != nil || res != extBundleShort {
			return res, free, bundle, err
		}
		if bundle = c.bundleFor(sec, free.Bundle); try >= bundleTries-1 {
			bundle = c.BundleSec
		}
	}
}

// Run reloads the snapshot whenever changes signals and at least every
// interval, until ctx ends. changes may be nil.
func (e *Engine) Run(ctx context.Context, interval time.Duration, changes <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-changes:
			// Changes tend to come in bursts; load once for all of them.
			time.Sleep(changeSettle)
			select {
			case <-changes:
			default:
			}
			t.Reset(interval)
		}
		if err := e.Reload(ctx); err != nil {
			mReloadErrors.Inc()
			log.Printf("engine: reload: %v", err)
		}
	}
}

// changeSettle is how long the engine waits after a change for more.
const changeSettle = 50 * time.Millisecond

type authorizeRequest struct {
	V      int    `json:"v"`
	CallID string `json:"call_id"`
	SrcIP  string `json:"src_ip"`
	Src    string `json:"src"`
	Dst    string `json:"dst"`
	// AuthUser is the user name the SIP core has verified the caller's
	// password for; empty when the caller is known by its address only.
	AuthUser string `json:"auth_user"`
	// Transport is how the request reached the SIP core ("udp", "tcp",
	// "tls"); TLSName the common name of the caller's verified certificate.
	Transport string `json:"transport"`
	TLSName   string `json:"tls_name"`
	// Asserted is the number the caller's side asserts for the caller (the
	// user part of P-Asserted-Identity, RFC 3325); absent when there is
	// none. Privacy is the caller's Privacy header as it arrived (RFC
	// 3323); "id" in it asks that the asserted identity go no further.
	Asserted string `json:"asserted,omitempty"`
	Privacy  string `json:"privacy,omitempty"`
}

// privateID reports whether a Privacy header value asks for the caller's
// identity to be withheld: "id" among its values (RFC 3325 §9.3), in any
// case, however the values are separated.
func privateID(privacy string) bool {
	for _, v := range strings.FieldsFunc(privacy, func(r rune) bool { return r == ';' || r == ',' || r == ' ' || r == '\t' }) {
		if strings.EqualFold(v, "id") {
			return true
		}
	}
	return false
}

// wireRoute tells the SIP core where to send a call and how. NextHop, when
// given, is the address the request really goes to (URI stays what the
// request is addressed to): a trunk reached at the address its registration
// came from. Media, when given, is what to offer on that side whatever the
// transport: "rtp" for plain audio. Without it the rule of the interface
// holds: encrypted media exactly when the signalling is. The rest are the
// connection's own settings; each is absent when the connection has none.
type wireRoute struct {
	URI     string `json:"uri"`
	NextHop string `json:"next_hop,omitempty"`
	Media   string `json:"media,omitempty"`
	// Auth is what to sign in with when the far end asks (401/407).
	Auth *wireAuth `json:"auth,omitempty"`
	// Caller is the caller's number to show on this route; it goes before
	// the answer's own caller.
	Caller string `json:"caller,omitempty"`
	// Identity says what becomes of the asserted identity of the caller
	// (P-Asserted-Identity): "remove", or "caller" to set it to the
	// caller's number as this route shows it. Absent: passed on. A caller
	// that asked for privacy (Privacy: id) gets "remove" on every route,
	// whatever the connection's setting.
	Identity string `json:"identity,omitempty"`
	// RingTimeout is how long to wait for an answer, in seconds, before
	// the attempt counts as failed (408).
	RingTimeout int `json:"ring_timeout_sec,omitempty"`
	// MaxDuration is the longest the call may last on this route; the
	// smaller of it and the call's own max_duration_sec holds.
	MaxDuration int `json:"max_duration_sec,omitempty"`
	// Codecs are the audio codecs to offer, in order; others are left out.
	Codecs []string `json:"codecs,omitempty"`
	// Transcode, when given, says the caller and the far end share no
	// codec: the offer carries Codecs even though the caller offers none
	// of them, and the media relay translates. It lists the codecs of the
	// caller's side, for the offers that come back from the far end within
	// the call.
	Transcode []string `json:"transcode,omitempty"`
}

type wireAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// wire is the route as the SIP core is told it, with the settings of its
// connection and of the caller's trunk as the snapshot has them now.
func (e *Engine) wire(c *Call, r callRoute) *wireRoute {
	w := &wireRoute{URI: r.URI, NextHop: r.NextHop}
	defer func() {
		if c.Private {
			w.Identity = "remove"
		}
	}()
	snap := e.snap.Load()
	var trunkCodecs []string
	if snap != nil {
		if t, ok := snap.Trunk(c.TrunkID); ok {
			trunkCodecs = t.Codecs
		}
	}
	if r.TrunkID != 0 {
		// A registered trunk gets encrypted audio (SRTP) when it registered
		// over TLS and the request goes back through that connection, and
		// plain audio otherwise, even where its Contact names TLS: media
		// keys travel in the signalling and must not cross it in the clear
		// (ADR-0015). Saying nothing lets the interface's rule hold
		// (encrypted exactly when the signalling is). Its own codecs go
		// first, in its order, as far as the caller's trunk allows them;
		// deliver has left out a trunk with none in common.
		if hopTransport(r.NextHop) != "tls" {
			w.Media = "rtp"
		}
		w.Codecs = trunkCodecs
		if snap != nil {
			if dst, ok := snap.Trunk(r.TrunkID); ok {
				w.wireCodecs(dst.Codecs, trunkCodecs, r.Transcode)
			}
		}
		return w
	}
	w.Codecs = trunkCodecs
	if snap == nil {
		return w
	}
	conn, ok := snap.Connections[r.ConnectionID]
	if !ok {
		return w
	}
	if conn.AuthUser != "" && conn.AuthPassword != "" {
		w.Auth = &wireAuth{Username: conn.AuthUser, Password: conn.AuthPassword}
	}
	shown := c.Caller
	if shown == "" {
		shown = c.Src
	}
	if own := conn.CallerFor(shown); own != shown {
		w.Caller = own
	}
	if conn.PAIMode == "remove" || conn.PAIMode == "caller" {
		w.Identity = conn.PAIMode
	}
	w.RingTimeout = conn.RingTimeout
	if conn.MaxDuration > 0 && (c.MaxSec == 0 || conn.MaxDuration < c.MaxSec) {
		w.MaxDuration = conn.MaxDuration
	}
	w.wireCodecs(conn.Codecs, trunkCodecs, r.Transcode)
	return w
}

// wireCodecs sets what to offer the far end (its list far as the caller's
// trunk's list allows it) and, on a route admitted as transcoded when the
// two still share nothing, the far end's whole list with the caller's to
// translate from. A route the configuration has since given a codec in
// common is carried as it is, without translation.
func (w *wireRoute) wireCodecs(far, caller []string, transcode bool) {
	list, ok := routing.CommonCodecs(far, caller)
	w.Codecs = list
	if !ok && transcode {
		w.Codecs, w.Transcode = far, caller
	}
}

type authorizeResponse struct {
	V       int  `json:"v"`
	Allowed bool `json:"allowed"`
	// SourceKnown tells the SIP core that the address belongs to a client,
	// whatever became of this call, so it can spare that address its
	// protection against floods from strangers.
	SourceKnown bool `json:"source_known"`
	// SourceBlocked tells the SIP core that the operator's rules shut this
	// address out: it is to be ignored from now on, not merely refused.
	SourceBlocked bool `json:"source_blocked,omitempty"`
	// Trace tells the SIP core that the call is a client's, allowed or
	// refused, so its signalling may be kept for the client's trace
	// (ADR-0046). A call from nobody the engine recognised (a stranger, a
	// blocked address) never has it: what such sources send must not be
	// able to fill the store. Older engines leave it out, and the SIP core
	// then traces only the calls it sends on, as before.
	Trace       bool       `json:"trace,omitempty"`
	SIPCode     int        `json:"sip_code"`
	Reason      string     `json:"reason"`
	MaxDuration int        `json:"max_duration_sec,omitempty"`
	Route       *wireRoute `json:"route,omitempty"`
	// Caller is the number to show vendors as the caller, when it is not
	// the one that arrived.
	Caller string `json:"caller,omitempty"`
}

type eventRequest struct {
	V       int    `json:"v"`
	CallID  string `json:"call_id"`
	Event   string `json:"event"` // failed, answered, ended
	SIPCode int    `json:"sip_code"`
	// Cause is optional (ADR-0039). CauseMedia on an "ended" event before
	// any answer says the attempt was never sent: the SIP core's media
	// relay took no offer. Any other value is ignored, so that a newer SIP
	// core may say more than this engine knows.
	Cause string `json:"cause,omitempty"`
}

// CauseMedia is the cause of an attempt the SIP core could not send because
// its media relay took no offer (ADR-0030, ADR-0039).
const CauseMedia = "media"

type eventResponse struct {
	V     int        `json:"v"`
	Route *wireRoute `json:"route,omitempty"`
}

func (e *Engine) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/authorize", e.handleAuthorize)
	mux.HandleFunc("POST /v1/events", e.handleEvent)
	mux.HandleFunc("POST /v1/source", e.handleSource)
	mux.HandleFunc("POST /v1/ban", e.handleBan)
	mux.HandleFunc("GET /v1/bans", e.handleBans)
	mux.HandleFunc("POST /v1/unban", e.handleUnban)
	mux.HandleFunc("POST /v1/credentials", e.handleCredentials)
	mux.HandleFunc("POST /v1/register", e.handleRegister)
	mux.HandleFunc("GET /v1/registrations", e.handleRegistrations)
	mux.HandleFunc("POST /v1/unregister", e.handleUnregister)
	mux.HandleFunc("POST /v1/reload", func(w http.ResponseWriter, r *http.Request) {
		if err := e.Reload(r.Context()); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /v1/stats", e.handleStats)
	mux.HandleFunc("GET /v1/peers", e.handlePeers)
	mux.HandleFunc("GET /v1/vendor-registrations", e.handleVendorRegistrations)
	mux.HandleFunc("POST /v1/probe", e.handleProbe)
	mux.Handle("GET /metrics", metrics.Handler())
	mux.HandleFunc("GET /v1/live", e.handleLive)
	mux.HandleFunc("GET /v1/calls", e.handleCalls)
	mux.HandleFunc("POST /v1/hangup", e.handleHangup)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if e.snap.Load() == nil {
			http.Error(w, "no snapshot", 503)
			return
		}
		w.Write([]byte("ok\n"))
	})
	return e.guard(mux)
}

func (e *Engine) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var in authorizeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil || in.CallID == "" {
		http.Error(w, "invalid request", 400)
		return
	}
	if in.V != Version {
		http.Error(w, "unsupported interface version", 400)
		return
	}
	out := e.Authorize(r.Context(), in)
	writeJSON(w, out)
	took := time.Since(start)
	e.authorize.observe(took)
	mDecision.Observe(took)
	if out.Allowed {
		mAllowed.Inc()
	} else {
		e.rejected.Add(1)
		mRefused.Inc()
	}
}

// Authorize decides a new call and, when it may proceed, admits it against
// the client and vendor limits and stores its state.
func (e *Engine) Authorize(ctx context.Context, in authorizeRequest) authorizeResponse {
	in.SrcIP = canonIP(in.SrcIP)
	snap := e.snap.Load()
	if snap == nil {
		return authorizeResponse{V: Version, SIPCode: 503, Reason: "not ready"}
	}
	now := e.now()
	if blocked, _ := snap.Firewall.Verdict(in.SrcIP); blocked {
		// No record: a blocked address must not be able to fill the books.
		mBlocked.Inc()
		return authorizeResponse{V: Version, SIPCode: 403, Reason: "source blocked", SourceBlocked: true}
	}
	src := routing.Source{AuthUser: in.AuthUser, IP: in.SrcIP, Transport: strings.ToLower(in.Transport), TLSName: in.TLSName,
		Caller: in.Src, Asserted: in.Asserted, Pick: routing.PickFor(in.CallID), At: now}
	d := snap.AuthorizeFrom(src, in.Dst)
	if d.ClientID == 0 && e.Incoming != nil && in.AuthUser == "" {
		if take, ok := e.Incoming(CallInfo{CallID: in.CallID, Src: in.Src, SrcIP: in.SrcIP, Dst: in.Dst, Asserted: in.Asserted, Now: now}, snap); ok {
			d = snap.AuthorizeFor(take, src, in.Dst)
		}
	}
	d = e.reachable(e.trusted(d))
	if e.Adjust != nil && d.Allowed {
		d = e.Adjust(CallInfo{CallID: in.CallID, Src: in.Src, SrcIP: in.SrcIP, Dst: d.Number, Asserted: in.Asserted, Now: now}, d)
		switch {
		case d.Allowed && len(d.Routes) == 0:
			d.Allowed, d.SIPCode, d.Reason = false, 503, "no route"
		case !d.Allowed && d.SIPCode < 300:
			d.SIPCode, d.Reason = 403, "not allowed"
		}
	}
	// A route to a client's trunk becomes a route to where that trunk is
	// registered; only an extension makes such routes (Adjust, Incoming).
	if d.Allowed && (e.Adjust != nil || d.Inbound) {
		from, _ := snap.Trunk(d.TrunkID)
		routes, noCodec, err := e.deliver(ctx, snap, d.Routes, from, now)
		switch {
		case err != nil:
			mRegErrors.Inc()
			log.Printf("engine: registrations for %s: %v", in.CallID, err)
			d.Allowed, d.SIPCode, d.Reason = false, 503, "state store unavailable"
		case len(routes) == 0 && noCodec:
			// As for a vendor with no codec in common: no route.
			d.Allowed, d.SIPCode, d.Reason = false, 503, "no route"
		case len(routes) == 0:
			d.Allowed, d.SIPCode, d.Reason = false, 480, "not registered"
		default:
			d.Routes = routes
		}
	}
	if d.Allowed {
		f := Funds{Total: d.Funds, SyncedMs: e.synced.Load()}
		bf := Funds{Total: d.BundleFree, SyncedMs: f.SyncedMs}
		call := newCall(in, d, now, min(d.MaxDuration, e.Reserve))
		if e.MaxCalls != nil {
			call.SystemMax = e.MaxCalls()
		}
		call.TranscodeMax = e.TranscodeMax
		// The bundle pays first, by the snapshot's count of it; the store
		// knows what other calls hold of it and of the balance.
		call.BundleSec = call.bundleFor(call.Reserved, d.BundleFree)
		idx, free, err := e.calls.Admit(ctx, in.CallID, call, d, f, bf, now)
		for try, shrunk := 0, false; err == nil && (idx == limitBundle || idx == limitFunds); try++ {
			if idx == limitBundle {
				// Other calls hold part of the bundle: take what is left.
				call.BundleSec = 0
				if try < bundleTries {
					call.BundleSec = call.bundleFor(call.Reserved, free.Bundle)
				}
			} else {
				// Other calls hold part of the balance: settle for the
				// time the bundle and the rest pay for.
				sec := d.Sell.MaxSecondsAfter(free.Money, call.BundleSec, e.Reserve)
				if shrunk || sec <= 0 || sec >= call.Reserved {
					break
				}
				shrunk = true
				call.Reserved, call.BundleSec = sec, min(call.BundleSec, int64(d.Sell.BilledSeconds(sec)))
			}
			idx, free, err = e.calls.Admit(ctx, in.CallID, call, d, f, bf, now)
		}
		mTranscodeFull.Add(int(free.TranscodeFull))
		switch {
		case err != nil:
			// Without the counters the limits cannot be enforced; refuse
			// rather than let traffic through unaccounted.
			log.Printf("engine: admit %s: %v", in.CallID, err)
			d.Allowed, d.SIPCode, d.Reason = false, 503, "state store unavailable"
		case idx == limitCalls:
			d.Allowed, d.SIPCode, d.Reason = false, 503, "call limit reached"
		case idx == limitCPS:
			d.Allowed, d.SIPCode, d.Reason = false, 503, "cps limit reached"
		case idx == limitVendors:
			d.Allowed, d.SIPCode, d.Reason = false, 503, "all routes busy"
		case idx == limitFunds:
			d.Allowed, d.SIPCode, d.Reason = false, 402, "insufficient balance"
		case idx == limitSystem:
			d.Allowed, d.SIPCode, d.Reason = false, 503, "system capacity reached"
		case idx == limitTrunk:
			d.Allowed, d.SIPCode, d.Reason = false, 503, "trunk call limit reached"
		case idx == limitTrunkPS:
			d.Allowed, d.SIPCode, d.Reason = false, 503, "trunk cps limit reached"
		default:
			if call.Routes[idx].Transcode {
				mTranscoded.Inc()
			}
			e.live.arrived(now, d.ClientID, call.Country, call.Prefix)
			// The longest the call could last on what is free right now;
			// the engine ends it earlier if the funds go elsewhere.
			maxDur := max(call.Reserved, min(d.MaxDuration, d.Sell.MaxSecondsAfter(free.Money, free.Bundle, routing.MaxCallSeconds)))
			return authorizeResponse{V: Version, Allowed: true, SourceKnown: in.AuthUser == "" && !d.Inbound, Trace: true, SIPCode: 200, Reason: "ok",
				MaxDuration: maxDur, Route: e.wire(call, call.Routes[idx]), Caller: d.Caller}
		}
	}
	e.live.arrived(now, d.ClientID, numplan.Country(d.Number), d.SellPrefix)
	e.live.finished(now, d.ClientID, numplan.Country(d.Number), 0, outcome{code: d.SIPCode})
	// A refused call from a known client is recorded as a leg of its own.
	if d.ClientID != 0 {
		rec := CDR{CallID: in.CallID, Leg: 1, ClientID: d.ClientID, TrunkID: trunkRef(d.TrunkID),
			Src: in.Src, SrcIP: in.SrcIP, Dst: d.Number, StartAt: now, EndAt: now, SIPCode: d.SIPCode}
		e.sink.Publish(rec)
		if e.OnLeg != nil {
			e.OnLeg(Leg{Record: rec, Final: true})
		}
	}
	// An address is vouched for only when it is what identified the client:
	// a caller that signed in must sign in again from wherever it calls next,
	// and a call an extension took says nothing of the address it came from.
	// A refusal is traced exactly when it is recorded: the record is what
	// lets the client's users find the call and see its trace (mayTrace).
	return authorizeResponse{V: Version, SourceKnown: d.ClientID != 0 && in.AuthUser == "" && !d.Inbound, Trace: d.ClientID != 0,
		SIPCode: d.SIPCode, Reason: d.Reason}
}

// trunkRef is a record's trunk: none for a call that came through no trunk
// (routing.Inbound).
func trunkRef(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// handleCredentials gives the SIP core what it needs to check a caller's
// password. It hands out password hashes: like the rest of this interface
// it must be reachable from the SIP core only.
func (e *Engine) handleCredentials(w http.ResponseWriter, r *http.Request) {
	var in struct {
		V        int    `json:"v"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil || in.V != Version {
		http.Error(w, "invalid request", 400)
		return
	}
	out := map[string]any{"v": Version, "found": false}
	if snap := e.snap.Load(); snap != nil {
		if ha1, ok := snap.Credentials(in.Username); ok {
			out["found"], out["ha1"] = true, ha1
		}
	}
	writeJSON(w, out)
}

// handleSource says whether an address belongs to a client or a vendor.
// The SIP core asks before it answers anything but a call from an address
// it has not seen: strangers get no answer to their probes.
func (e *Engine) handleSource(w http.ResponseWriter, r *http.Request) {
	var in struct {
		V     int    `json:"v"`
		SrcIP string `json:"src_ip"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil || in.V != Version {
		http.Error(w, "invalid request", 400)
		return
	}
	in.SrcIP = canonIP(in.SrcIP)
	snap := e.snap.Load()
	if snap == nil {
		writeJSON(w, map[string]any{"v": Version, "known": false})
		return
	}
	// The operator's rules go first: a blocked address is not known,
	// whatever else it is; an allowed one is, whoever it is.
	blocked, allowed := snap.Firewall.Verdict(in.SrcIP)
	if blocked {
		mBlocked.Inc()
	}
	// An address a trunk is registered from is not "known": its calls are
	// still asked to sign in. But it is not a stranger either, and its
	// probes may be answered. From memory: no look at the store here.
	writeJSON(w, map[string]any{"v": Version, "known": !blocked && (allowed || snap.KnownSource(in.SrcIP)), "blocked": blocked,
		"registered": !blocked && e.regSrc.has(in.SrcIP, e.now())})
}

func (e *Engine) handleEvent(w http.ResponseWriter, r *http.Request) {
	var in eventRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil || in.CallID == "" {
		http.Error(w, "invalid request", 400)
		return
	}
	if in.V != Version {
		http.Error(w, "unsupported interface version", 400)
		return
	}
	if c := mEvents[in.Event]; c != nil {
		c.Inc()
	}
	out, err := e.Event(r.Context(), in)
	if err != nil {
		mEventErrors.Inc()
		log.Printf("engine: event %s %s: %v", in.Event, in.CallID, err)
		http.Error(w, "state store unavailable", 503)
		return
	}
	writeJSON(w, out)
}

// finalCodes end the call when a vendor answers with them: the far end
// itself refused, so another vendor would only reach the same answer.
var finalCodes = map[int]bool{404: true, 480: true, 484: true, 486: true, 487: true}

// Event applies what the SIP core reports about a call. Every event may be
// delivered more than once.
func (e *Engine) Event(ctx context.Context, in eventRequest) (eventResponse, error) {
	out := eventResponse{V: Version}
	now := e.now()
	if in.Event == "answered" {
		return out, e.calls.Answer(ctx, in.CallID, now, extendLead)
	}
	if in.Event == "ringing" {
		return out, e.calls.Ring(ctx, in.CallID, now)
	}
	if in.Event != "failed" && in.Event != "ended" {
		return out, nil
	}
	c, err := e.calls.Get(ctx, in.CallID)
	if err != nil || c == nil {
		return out, err // unknown or already finished
	}
	rec := c.cdr(in.CallID, now)
	switch {
	case c.AnswerMs != 0:
		rec.SIPCode = 200
	case in.SIPCode >= 300:
		rec.SIPCode = in.SIPCode
	default:
		rec.SIPCode = 487 // ended before any final answer
	}
	// The record is published before the state goes: if the engine dies in
	// between, the event is retried and the record is deduplicated by
	// (call_id, leg).
	leg := outcome{code: rec.SIPCode}
	if c.RingMs != 0 {
		leg.pddMs = c.RingMs - c.AttemptMs
		rec.PDDMs = max(leg.pddMs, 0)
	}
	if rec.AnswerAt != nil {
		leg.duration = int((rec.EndAt.Sub(*rec.AnswerAt) + time.Second - 1) / time.Second)
	}
	// An attempt the SIP core could not send is the switch's failure, not
	// the vendor's: it keeps its record (the route that was chosen) but is
	// not counted against the connection (ADR-0039).
	notSent := in.Event == "ended" && in.Cause == CauseMedia && c.AnswerMs == 0
	// A leg towards a client's trunk has no vendor connection.
	var conn int64
	if rec.ConnectionID != nil && !notSent {
		conn = *rec.ConnectionID
		e.live.attempted(now, conn, leg)
	}
	whole := leg
	if c.RingMs != 0 {
		whole.pddMs = c.RingMs - c.StartMs
	}
	heard := Leg{Record: rec, PDD: time.Duration(leg.pddMs) * time.Millisecond, Talk: leg.duration, SellPerMin: c.Rate.PricePerMin, Final: true, NotSent: notSent}
	// Whether a failure moves on to the next route is the standing rule
	// unless the connection that failed has one of its own.
	moves := !finalCodes[in.SIPCode] && in.SIPCode < 600
	if snap := e.snap.Load(); snap != nil && conn != 0 {
		if cn, ok := snap.Connections[conn]; ok {
			moves = cn.MovesOn(in.SIPCode, moves)
		}
	}
	if in.Event == "failed" && c.AnswerMs == 0 && moves {
		e.sink.Publish(rec)
		c.TranscodeMax = e.TranscodeMax
		idx, full, err := e.calls.Next(ctx, in.CallID, c, now)
		mTranscodeFull.Add(int(full))
		if err != nil {
			return out, err
		}
		if idx >= 0 && c.Routes[idx].Transcode {
			mTranscoded.Inc()
		}
		heard.Final = idx < 0
		if e.OnLeg != nil {
			e.OnLeg(heard)
		}
		if idx >= 0 {
			out.Route = e.wire(c, c.Routes[idx])
			return out, nil
		}
		if err := e.calls.Release(ctx, in.CallID, c, 0, 0); err != nil {
			return out, err
		}
		e.live.finished(now, c.ClientID, c.Country, conn, whole)
		return out, e.calls.Finish(ctx, in.CallID, c)
	}
	// The hold becomes the charge before the rater can hear of the call, so
	// that its settlement always finds the hold in place.
	var charge int64
	var bundleSec int
	if rec.AnswerAt != nil {
		_, bundleSec, charge = c.Rate.AmountAfter(leg.duration, c.BundleSec)
	}
	if err := e.calls.Release(ctx, in.CallID, c, charge, int64(bundleSec)); err != nil {
		return out, err
	}
	e.sink.Publish(rec)
	if e.OnLeg != nil {
		e.OnLeg(heard)
	}
	e.live.finished(now, c.ClientID, c.Country, conn, whole)
	return out, e.calls.Finish(ctx, in.CallID, c)
}

func (e *Engine) handleStats(w http.ResponseWriter, r *http.Request) {
	n, p50, p99, max := e.authorize.summary()
	// Vendor connections that get no calls because of their certificate,
	// with the reason; absent when certificates are not checked.
	var peers map[string]any
	if st := e.peers.Load(); st != nil {
		peers = map[string]any{"checked": st.checked, "refused": st.bad}
	}
	writeJSON(w, map[string]any{
		"peers":              peers,
		"snapshot_loaded_at": time.Unix(e.loaded.Load(), 0).UTC(),
		"authorize": map[string]any{
			"count": n, "rejected": e.rejected.Load(),
			"p50_us": p50.Microseconds(), "p99_us": p99.Microseconds(), "max_us": max.Microseconds(),
		},
		"reservations": map[string]any{"extended": e.extended.Load(), "cut": e.cut.Load()},
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// latency is a lock-free histogram in 100 µs steps up to 100 ms.
type latency struct {
	buckets [1001]atomic.Uint64
	max     atomic.Int64
}

func (l *latency) observe(d time.Duration) {
	i := int(d / (100 * time.Microsecond))
	if i >= len(l.buckets) {
		i = len(l.buckets) - 1
	}
	l.buckets[i].Add(1)
	for {
		m := l.max.Load()
		if int64(d) <= m || l.max.CompareAndSwap(m, int64(d)) {
			return
		}
	}
}

func (l *latency) summary() (n uint64, p50, p99, max time.Duration) {
	var counts [len(l.buckets)]uint64
	for i := range l.buckets {
		counts[i] = l.buckets[i].Load()
		n += counts[i]
	}
	at := func(q float64) time.Duration {
		target, seen := uint64(float64(n)*q), uint64(0)
		for i, c := range counts {
			if seen += c; seen > target {
				return time.Duration(i+1) * 100 * time.Microsecond
			}
		}
		return 0
	}
	return n, at(0.50), at(0.99), time.Duration(l.max.Load())
}
