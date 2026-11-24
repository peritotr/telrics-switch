// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"softswitch/core/pkg/routing"
)

// sipPeer answers every datagram it gets on a local UDP port with the
// given status line, or with nothing when the line is empty. skip is how
// many requests go unanswered first.
func sipPeer(t *testing.T, status string, skip int) (port int, seen func() []string) {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	var mu sync.Mutex
	var got []string
	seen = func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			mu.Lock()
			got = append(got, string(buf[:n]))
			mu.Unlock()
			if skip > 0 {
				skip--
				continue
			}
			if status != "" {
				conn.WriteTo([]byte("SIP/2.0 "+status+"\r\nContent-Length: 0\r\n\r\n"), from)
			}
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr).Port, seen
}

func TestProbe(t *testing.T) {
	p := &Prober{Timeout: 1200 * time.Millisecond}
	ctx := context.Background()
	port, seen := sipPeer(t, "200 OK", 0)
	res := p.Probe(ctx, ProbeTarget{Host: "127.0.0.1", Port: port, Transport: "udp"})
	if res.Outcome != ProbeAnswered || res.Code != 200 || res.Reason != "OK" || !res.Up() {
		t.Errorf("answering peer: %+v", res)
	}
	if req := seen()[0]; !strings.HasPrefix(req, fmt.Sprintf("OPTIONS sip:127.0.0.1:%d;transport=udp SIP/2.0\r\n", port)) ||
		!strings.Contains(req, "\r\nCSeq: 1 OPTIONS\r\n") || !strings.Contains(req, "\r\nVia: SIP/2.0/UDP 127.0.0.1:") || !strings.HasSuffix(req, "Content-Length: 0\r\n\r\n") {
		t.Errorf("request sent:\n%s", req)
	}
	// Any answer shows somebody is there, except the one that says "out of service".
	port, _ = sipPeer(t, "404 Not Found", 0)
	if res := p.Probe(ctx, ProbeTarget{Host: "127.0.0.1", Port: port, Transport: "udp"}); res.Code != 404 || !res.Up() {
		t.Errorf("peer answering 404: %+v", res)
	}
	port, _ = sipPeer(t, "503 Service Unavailable", 0)
	if res := p.Probe(ctx, ProbeTarget{Host: "127.0.0.1", Port: port, Transport: "udp"}); res.Outcome != ProbeAnswered || res.Up() {
		t.Errorf("peer answering 503: %+v", res)
	}
	// A lost first request is sent again.
	port, seen = sipPeer(t, "200 OK", 1)
	if res := p.Probe(ctx, ProbeTarget{Host: "127.0.0.1", Port: port, Transport: "udp"}); !res.Up() || len(seen()) != 2 {
		t.Errorf("peer that missed the first request: %+v after %d requests", res, len(seen()))
	}
	port, _ = sipPeer(t, "", 0)
	if res := p.Probe(ctx, ProbeTarget{Host: "127.0.0.1", Port: port, Transport: "udp"}); res.Outcome != ProbeNoAnswer || res.Up() || res.Ms < 1000 {
		t.Errorf("silent peer: %+v", res)
	}
	// Nothing listens: over TCP that is a refusal.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := l.Addr().(*net.TCPAddr).Port
	l.Close()
	if res := p.Probe(ctx, ProbeTarget{Host: "127.0.0.1", Port: closed, Transport: "tcp"}); res.Outcome != ProbeRefused {
		t.Errorf("closed TCP port: %+v", res)
	}
	// A TCP peer that answers.
	l, _ = net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 4096)
			c.Read(buf)
			c.Write([]byte("SIP/2.0 200 OK\r\nContent-Length: 0\r\n\r\n"))
			c.Close()
		}
	}()
	if res := p.Probe(ctx, ProbeTarget{Host: "127.0.0.1", Port: l.Addr().(*net.TCPAddr).Port, Transport: "tcp"}); !res.Up() {
		t.Errorf("answering TCP peer: %+v", res)
	}
	if res := p.Probe(ctx, ProbeTarget{Host: "127.0.0.1", Port: 5060, Transport: "sctp"}); res.Outcome != ProbeFailed {
		t.Errorf("unknown transport: %+v", res)
	}
}

// A connection that is asked and does not answer twice in a row gets no
// calls; one answer brings it back. Connections that are not asked are
// never judged.
func TestHealthTakesConnectionsOut(t *testing.T) {
	up, _ := sipPeer(t, "200 OK", 0)
	quiet, _ := sipPeer(t, "", 0)
	snap := testSnapshot()
	set := func(id int64, port, interval int) {
		c := snap.Connections[id]
		c.Host, c.Port, c.OptionsInterval = "127.0.0.1", port, interval
		snap.Connections[id] = c
	}
	set(1, quiet, 1)
	set(2, up, 1)
	set(3, quiet, 0) // silent too, but nobody asks it
	e := New(nil, nil, &memSink{})
	e.snap.Store(snap)
	p := &Prober{Timeout: 300 * time.Millisecond}
	slots := make(chan struct{}, 4)
	round := func() {
		// Every connection is due: forget when each was to be asked next.
		if st := e.health.Load(); st != nil {
			for id, h := range st.peers {
				h.next = time.Time{}
				st.peers[id] = h
			}
		}
		e.healthRound(context.Background(), p, snap, slots)
	}
	round()
	st := e.health.Load()
	if len(st.peers) != 2 || st.peers[1].State != "unknown" || st.peers[2].State != "up" || len(st.down) != 0 {
		t.Fatalf("after one round: %+v", st.peers)
	}
	d := snap.Authorize("10.0.0.1", "905321112233")
	if got := e.reachable(d); len(got.Routes) != 3 {
		t.Fatalf("one missed answer took a route out: %+v", got.Routes)
	}
	round()
	st = e.health.Load()
	if st.peers[1].State != "down" || !st.down[1] || st.down[2] || st.peers[1].Last.Outcome != ProbeNoAnswer {
		t.Fatalf("after two rounds: %+v", st.peers)
	}
	got := e.reachable(d)
	if len(got.Routes) != 2 || got.Routes[0].ConnectionID != 2 || got.Routes[1].ConnectionID != 3 {
		t.Fatalf("routes with connection 1 down: %+v", got.Routes)
	}
	// The same connection at an address that answers is judged afresh.
	set(1, up, 1)
	round()
	if st = e.health.Load(); st.peers[1].State != "up" || len(st.down) != 0 {
		t.Fatalf("after the vendor answered: %+v", st.peers)
	}
	// With every route down the call has none.
	set(1, quiet, 1)
	set(2, quiet, 1)
	set(3, quiet, 1)
	round()
	round()
	round()
	if got := e.reachable(d); got.Allowed || got.SIPCode != 503 || got.Reason != "no route" {
		t.Fatalf("every route down: %+v", got)
	}
}

// What the SIP core is told about a route: the connection's own settings,
// each absent when the connection has none.
func TestWireRouteCarriesTheConnectionsSettings(t *testing.T) {
	snap := testSnapshot()
	snap.Trunks[0].Codecs = []string{"PCMU", "PCMA"}
	c := snap.Connections[1]
	c.AuthUser, c.AuthPassword = "acme", "s3cret"
	c.CLIStrip, c.CLIAdd, c.PAIMode = 2, "0", "caller"
	c.RingTimeout, c.MaxDuration = 20, 600
	c.Codecs = []string{"PCMA", "G729"}
	snap.Connections[1] = c
	e := New(nil, nil, &memSink{})
	e.snap.Store(snap)
	call := &Call{TrunkID: 1, Src: "902125550000", MaxSec: 3000}
	js := func(r callRoute) string {
		b, _ := json.Marshal(e.wire(call, r))
		return string(b)
	}
	want := `{"uri":"sip:x@a","auth":{"username":"acme","password":"s3cret"},"caller":"02125550000","identity":"caller","ring_timeout_sec":20,"max_duration_sec":600,"codecs":["PCMA"]}`
	if got := js(callRoute{ConnectionID: 1, URI: "sip:x@a"}); got != want {
		t.Errorf("route with settings:\n got %s\nwant %s", got, want)
	}
	// A connection without settings adds only what the trunk asks for.
	if got := js(callRoute{ConnectionID: 2, URI: "sip:x@b"}); got != `{"uri":"sip:x@b","codecs":["PCMU","PCMA"]}` {
		t.Errorf("route without settings: %s", got)
	}
	// The connection's rule works on the caller the rules about callers left.
	call.Caller, call.MaxSec = "905550001122", 300
	if got := js(callRoute{ConnectionID: 1, URI: "sip:x@a"}); !strings.Contains(got, `"caller":"05550001122"`) || strings.Contains(got, "max_duration_sec") {
		t.Errorf("after a caller rule, with a shorter call: %s", got)
	}
	// A route to a registered trunk has no connection.
	if got := js(callRoute{TrunkID: 9, URI: "sip:pbx@10.0.0.5", NextHop: "sip:203.0.113.9:40123"}); got != `{"uri":"sip:pbx@10.0.0.5","next_hop":"sip:203.0.113.9:40123","media":"rtp","codecs":["PCMU","PCMA"]}` {
		t.Errorf("route to a trunk: %s", got)
	}
}

var _ = routing.MaxCallSeconds

// A vendor at an IPv6 address is asked with the address in brackets in the
// request line, To and the switch's own Via and Contact (RFC 5118, T-28).
func TestProbeIPv6(t *testing.T) {
	conn, err := net.ListenPacket("udp", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback:", err)
	}
	defer conn.Close()
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		got <- string(buf[:n])
		conn.WriteTo([]byte("SIP/2.0 200 OK\r\nContent-Length: 0\r\n\r\n"), from)
	}()
	port := conn.LocalAddr().(*net.UDPAddr).Port
	p := &Prober{Timeout: time.Second}
	if res := p.Probe(context.Background(), ProbeTarget{Host: "::1", Port: port, Transport: "udp"}); !res.Up() {
		t.Fatalf("IPv6 peer: %+v", res)
	}
	req := <-got
	for _, want := range []string{
		fmt.Sprintf("OPTIONS sip:[::1]:%d;transport=udp SIP/2.0\r\n", port),
		fmt.Sprintf("\r\nTo: <sip:[::1]:%d>\r\n", port),
		"\r\nVia: SIP/2.0/UDP [::1]:",
		"\r\nContact: <sip:ping@[::1]:",
	} {
		if !strings.Contains(req, want) {
			t.Errorf("request lacks %q:\n%s", want, req)
		}
	}
}
