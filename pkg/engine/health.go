// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"softswitch/core/pkg/routing"
)

// The engine asks vendors whether they are there with a SIP OPTIONS request
// of its own, over the connection's transport. It speaks just enough SIP
// for that and nothing passes through the SIP core, so the interface with
// it stays as it is. A connection that does not answer is given no calls
// until it answers again: the verdicts are kept in memory and read on the
// call path without a lock, like those about certificates (peers.go).

// Probe outcomes.
const (
	ProbeAnswered    = "answered"    // a SIP answer came back (Code says which)
	ProbeNoAnswer    = "no_answer"   // nothing came back in time
	ProbeUnresolved  = "unresolved"  // the host name is not known
	ProbeRefused     = "refused"     // nothing listens there
	ProbeCertificate = "certificate" // the TLS certificate was not accepted
	ProbeFailed      = "failed"      // anything else (Detail says what)
)

// ProbeTarget is where a probe goes.
type ProbeTarget struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Transport string `json:"transport"` // udp, tcp or tls
	TLSName   string `json:"tls_name"`  // name the certificate must carry; "" = Host
}

// ProbeResult is what one OPTIONS request came to.
type ProbeResult struct {
	Outcome string `json:"outcome"`
	Code    int    `json:"sip_code,omitempty"`
	Reason  string `json:"sip_reason,omitempty"`
	Ms      int64  `json:"ms"`
	Detail  string `json:"detail,omitempty"`
}

// Up reports whether the result shows a vendor that takes calls: any SIP
// answer but 503, which is the far end saying it is out of service.
func (r ProbeResult) Up() bool { return r.Outcome == ProbeAnswered && r.Code != 503 }

// Prober sends the OPTIONS requests.
type Prober struct {
	Roots   *x509.CertPool // for TLS vendors; nil = the system's
	NoNames bool           // do not check certificates (PEER_VERIFY=off)
	Timeout time.Duration  // per probe; 0 = three seconds
}

func token() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// request is the OPTIONS request for target as sent from local.
func (t ProbeTarget) request(local net.Addr) []byte {
	host, port, _ := net.SplitHostPort(local.String())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	proto := strings.ToUpper(t.Transport)
	peer := t.Host
	if strings.Contains(peer, ":") {
		peer = "[" + peer + "]"
	}
	uri := fmt.Sprintf("sip:%s:%d;transport=%s", peer, t.Port, t.Transport)
	return []byte("OPTIONS " + uri + " SIP/2.0\r\n" +
		"Via: SIP/2.0/" + proto + " " + host + ":" + port + ";branch=z9hG4bK" + token() + ";rport\r\n" +
		"Max-Forwards: 70\r\n" +
		"From: <sip:ping@" + host + ">;tag=" + token() + "\r\n" +
		"To: <sip:" + peer + ":" + strconv.Itoa(t.Port) + ">\r\n" +
		"Call-ID: " + token() + token() + "@" + host + "\r\n" +
		"CSeq: 1 OPTIONS\r\n" +
		"Contact: <sip:ping@" + host + ":" + port + ";transport=" + t.Transport + ">\r\n" +
		"User-Agent: softswitch\r\n" +
		"Accept: application/sdp\r\n" +
		"Content-Length: 0\r\n\r\n")
}

// statusLine reads "SIP/2.0 200 OK".
func statusLine(line string) (code int, reason string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(line), "SIP/2.0 ")
	if !found || len(rest) < 3 {
		return 0, "", false
	}
	code, err := strconv.Atoi(rest[:3])
	if err != nil || code < 100 || code > 699 {
		return 0, "", false
	}
	return code, strings.TrimSpace(rest[3:]), true
}

// Probe sends one OPTIONS request to target and waits for an answer.
func (p *Prober) Probe(ctx context.Context, target ProbeTarget) ProbeResult {
	timeout := p.Timeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	res := p.probe(ctx, target, timeout)
	res.Ms = time.Since(start).Milliseconds()
	return res
}

func (p *Prober) probe(ctx context.Context, target ProbeTarget, timeout time.Duration) ProbeResult {
	fail := func(err error) ProbeResult {
		var dns *net.DNSError
		var cert *tls.CertificateVerificationError
		var timedOut interface{ Timeout() bool }
		switch {
		case errors.As(err, &dns) && !dns.IsTimeout:
			return ProbeResult{Outcome: ProbeUnresolved, Detail: "host name not found"}
		case errors.As(err, &cert):
			return ProbeResult{Outcome: ProbeCertificate, Detail: cert.Err.Error()}
		case errors.Is(err, syscall.ECONNREFUSED):
			return ProbeResult{Outcome: ProbeRefused}
		case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timedOut) && timedOut.Timeout():
			return ProbeResult{Outcome: ProbeNoAnswer}
		}
		return ProbeResult{Outcome: ProbeFailed, Detail: err.Error()}
	}
	addr := net.JoinHostPort(target.Host, strconv.Itoa(target.Port))
	deadline, _ := ctx.Deadline()
	dialer := &net.Dialer{}
	answered := func(code int, reason string) ProbeResult {
		return ProbeResult{Outcome: ProbeAnswered, Code: code, Reason: reason}
	}
	switch target.Transport {
	case "udp":
		conn, err := dialer.DialContext(ctx, "udp", addr)
		if err != nil {
			return fail(err)
		}
		defer conn.Close()
		req := target.request(conn.LocalAddr())
		buf := make([]byte, 4096)
		// Sent again after half a second, then a second, as SIP does over
		// UDP: one lost packet must not pass for a dead vendor.
		for wait := 500 * time.Millisecond; ; wait *= 2 {
			if _, err := conn.Write(req); err != nil {
				return fail(err)
			}
			until := time.Now().Add(wait)
			if until.After(deadline) {
				until = deadline
			}
			conn.SetReadDeadline(until)
			for {
				n, err := conn.Read(buf)
				if err != nil {
					if errors.Is(err, syscall.ECONNREFUSED) {
						return fail(err)
					}
					break
				}
				line, _, _ := strings.Cut(string(buf[:n]), "\r\n")
				// A provisional answer is one too: somebody is there.
				if code, reason, ok := statusLine(line); ok {
					return answered(code, reason)
				}
			}
			if !time.Now().Before(deadline) {
				return ProbeResult{Outcome: ProbeNoAnswer}
			}
		}
	case "tcp", "tls":
		var conn net.Conn
		var err error
		if target.Transport == "tls" {
			name := target.TLSName
			if name == "" {
				name = target.Host
			}
			d := tls.Dialer{NetDialer: dialer, Config: &tls.Config{RootCAs: p.Roots, ServerName: name, MinVersion: tls.VersionTLS12, InsecureSkipVerify: p.NoNames}}
			conn, err = d.DialContext(ctx, "tcp", addr)
		} else {
			conn, err = dialer.DialContext(ctx, "tcp", addr)
		}
		if err != nil {
			return fail(err)
		}
		defer conn.Close()
		conn.SetDeadline(deadline)
		if _, err := conn.Write(target.request(conn.LocalAddr())); err != nil {
			return fail(err)
		}
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			return fail(err)
		}
		if code, reason, ok := statusLine(line); ok {
			return answered(code, reason)
		}
		return ProbeResult{Outcome: ProbeFailed, Detail: "the answer is not SIP"}
	}
	return ProbeResult{Outcome: ProbeFailed, Detail: "unknown transport"}
}

// downAfter is how many probes in a row must fail before a connection is
// taken out; one answer brings it back.
const downAfter = 2

// PeerHealth is what the engine knows of one connection it asks.
type PeerHealth struct {
	// State is "up", "down", or "unknown" before the first verdict.
	State     string      `json:"state"`
	Since     time.Time   `json:"since"`      // when State began
	CheckedAt time.Time   `json:"checked_at"` // the last probe
	Last      ProbeResult `json:"last"`
	fails     int
	next      time.Time
	target    ProbeTarget
	interval  int
}

// healthState is every connection that is asked; down is read on the call
// path. Both are replaced, never changed.
type healthState struct {
	peers map[int64]PeerHealth
	down  map[int64]bool
}

func probeTarget(c routing.Connection) ProbeTarget {
	return ProbeTarget{Host: c.Host, Port: c.Port, Transport: c.Transport, TLSName: c.TLSName}
}

// RunHealth asks every enabled connection that has an interval set, each
// at its own pace, until ctx ends. A new or changed connection is asked
// without waiting.
func (e *Engine) RunHealth(ctx context.Context, p *Prober) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	slots := make(chan struct{}, 32)
	for {
		if snap := e.snap.Load(); snap != nil {
			e.healthRound(ctx, p, snap, slots)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// healthRound probes the connections that are due and stores the verdicts.
func (e *Engine) healthRound(ctx context.Context, p *Prober, snap *routing.Snapshot, slots chan struct{}) {
	prev := e.health.Load()
	now := time.Now()
	next := &healthState{peers: map[int64]PeerHealth{}, down: map[int64]bool{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for id, c := range snap.Connections {
		if !c.Enabled || c.OptionsInterval <= 0 {
			continue
		}
		var h PeerHealth
		if prev != nil {
			h = prev.peers[id]
		}
		if target := probeTarget(c); h.State == "" || h.target != target {
			h = PeerHealth{State: "unknown", Since: now, target: target}
		}
		h.interval = c.OptionsInterval
		if now.Before(h.next) {
			// Probes already under way write to the same map.
			mu.Lock()
			next.peers[id] = h
			mu.Unlock()
			continue
		}
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots; wg.Done() }()
			res := p.Probe(ctx, h.target)
			if ctx.Err() != nil {
				return
			}
			at := time.Now()
			was := h.State
			h.Last, h.CheckedAt = res, at
			h.next = at.Add(time.Duration(h.interval) * time.Second)
			switch {
			case res.Up():
				h.fails, h.State = 0, "up"
			case h.fails+1 >= downAfter:
				h.fails, h.State = h.fails+1, "down"
			default:
				// Not yet a verdict: look again soon rather than a whole
				// interval later.
				h.fails++
				h.next = at.Add(min(time.Duration(h.interval)*time.Second, 2*time.Second))
			}
			if h.State != was {
				h.Since = at
				switch h.State {
				case "down":
					log.Printf("engine: vendor connection %d (%s): no answer to OPTIONS (%s), no calls will be sent", id, c.Host, res.Outcome)
				case "up":
					if was == "down" {
						log.Printf("engine: vendor connection %d (%s): answers again", id, c.Host)
					}
				}
			}
			mu.Lock()
			next.peers[id] = h
			mu.Unlock()
		}()
	}
	wg.Wait()
	for id, h := range next.peers {
		if h.State == "down" {
			next.down[id] = true
		}
	}
	e.health.Store(next)
}

// reachable takes the routes over connections that do not answer out of a
// decision. With none left the call has no route.
func (e *Engine) reachable(d routing.Decision) routing.Decision {
	st := e.health.Load()
	if st == nil || len(st.down) == 0 || !d.Allowed {
		return d
	}
	kept := d.Routes[:0:0]
	for _, r := range d.Routes {
		if st.down[r.ConnectionID] {
			mHealthSkipped.Inc()
			continue
		}
		kept = append(kept, r)
	}
	d.Routes = kept
	if len(kept) == 0 {
		d.Allowed, d.SIPCode, d.Reason = false, 503, "no route"
	}
	return d
}

// handlePeers lists what is known of the connections the engine asks, by
// connection id, and how the SIP core's registrations with vendors stand
// (vendorreg.go).
func (e *Engine) handlePeers(w http.ResponseWriter, r *http.Request) {
	out := map[string]PeerHealth{}
	if st := e.health.Load(); st != nil {
		for id, h := range st.peers {
			out[strconv.FormatInt(id, 10)] = h
		}
	}
	regs := map[string]VendorRegState{}
	if st := e.vendorRegs.Load(); st != nil {
		for id, v := range st.regs {
			regs[strconv.FormatInt(id, 10)] = v
		}
	}
	writeJSON(w, map[string]any{"v": Version, "connections": out, "registrations": regs})
}

// handleProbe sends one OPTIONS request now and says what came of it. It
// changes nothing: no verdict is kept.
func (e *Engine) handleProbe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		V int `json:"v"`
		ProbeTarget
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil || in.V != Version {
		http.Error(w, "invalid request", 400)
		return
	}
	in.Transport = strings.ToLower(in.Transport)
	if in.Host == "" || in.Port < 1 || in.Port > 65535 || (in.Transport != "udp" && in.Transport != "tcp" && in.Transport != "tls") {
		http.Error(w, "invalid target", 400)
		return
	}
	p := e.prober
	if p == nil {
		p = &Prober{}
	}
	mProbes.Inc()
	writeJSON(w, struct {
		V int `json:"v"`
		ProbeResult
	}{Version, p.Probe(r.Context(), in.ProbeTarget)})
}
