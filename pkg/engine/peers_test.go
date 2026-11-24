// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"softswitch/core/pkg/routing"
)

func TestPeerCheck(t *testing.T) {
	// A TLS server whose certificate is made out to example.com and 127.0.0.1.
	srv := httptest.NewTLSServer(nil)
	defer srv.Close()
	host, portText, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	// A port nobody listens on.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	_, deadText, _ := net.SplitHostPort(l.Addr().String())
	dead, _ := strconv.Atoi(deadText)
	l.Close()

	conn := func(id int64, name string, port int) routing.Connection {
		return routing.Connection{ID: id, Host: host, Port: port, Transport: "tls", TLSName: name, Enabled: true}
	}
	snap := &routing.Snapshot{Connections: map[int64]routing.Connection{
		1: conn(1, "", port),            // by address: the certificate names it
		2: conn(2, "example.com", port), // by the name asked for
		3: conn(3, "other.test", port),  // made out to somebody else
		4: conn(4, "example.com", dead), // cannot be reached: no verdict
		5: {ID: 5, Host: host, Port: port, Transport: "udp", Enabled: true},
		6: {ID: 6, Host: host, Port: port, Transport: "tls", TLSName: "other.test"}, // switched off
	}}
	p := &PeerCheck{Roots: roots}
	st := p.round(context.Background(), snap, nil)
	if st.checked != 4 || len(st.bad) != 1 || st.bad[3] == "" {
		t.Fatalf("first round: %+v", st)
	}
	// An authority nobody trusts: every reachable certificate is refused,
	// and the unreachable one still has no verdict.
	st = (&PeerCheck{Roots: x509.NewCertPool()}).round(context.Background(), snap, nil)
	if len(st.bad) != 3 || st.bad[4] != "" {
		t.Fatalf("untrusted authority: %+v", st.bad)
	}
	// A refused peer that then cannot be reached stays refused; one that
	// shows a good certificate is taken back.
	prev := &peerState{bad: map[int64]string{2: "was refused", 4: "was refused"}}
	st = p.round(context.Background(), snap, prev)
	if st.bad[4] != "was refused" || st.bad[2] != "" || st.bad[3] == "" {
		t.Fatalf("after a refusal: %+v", st.bad)
	}

	// Routes over refused connections are left out; with none left the call
	// has no route.
	e := &Engine{}
	d := routing.Decision{Allowed: true, SIPCode: 200, Routes: []routing.Route{{ConnectionID: 3}, {ConnectionID: 2}}}
	if got := e.trusted(d); len(got.Routes) != 2 {
		t.Errorf("without a check: %+v", got)
	}
	e.peers.Store(st)
	if got := e.trusted(d); !got.Allowed || len(got.Routes) != 1 || got.Routes[0].ConnectionID != 2 {
		t.Errorf("one refused: %+v", got)
	}
	d.Routes = d.Routes[:1]
	if got := e.trusted(d); got.Allowed || got.SIPCode != 503 || got.Reason != "no route" {
		t.Errorf("all refused: %+v", got)
	}

	if !peersChanged(nil, snap) || peersChanged(snap, snap) {
		t.Error("peersChanged on the same configuration")
	}
	edited := &routing.Snapshot{Connections: map[int64]routing.Connection{1: conn(1, "example.com", port)}}
	if !peersChanged(snap, edited) {
		t.Error("an edited name was not noticed")
	}
}

func TestBans(t *testing.T) {
	var b bans
	now := time.Unix(1_800_000_000, 0)
	b.add("203.0.113.7", "flood", now, 2*time.Minute)
	b.add("203.0.113.8", "flood", now, time.Minute)
	// Shut out again while still out, or just after: the same spell.
	b.add("203.0.113.7", "flood", now.Add(2*time.Minute+30*time.Second), 2*time.Minute)
	list := b.list(now.Add(2*time.Minute + 31*time.Second))
	if len(list) != 1 || list[0].IP != "203.0.113.7" || list[0].Count != 2 || !list[0].Since.Equal(now) {
		t.Fatalf("bans: %+v", list)
	}
	// Long after, a new spell starts.
	b.add("203.0.113.7", "flood", now.Add(time.Hour), 2*time.Minute)
	if list := b.list(now.Add(time.Hour)); len(list) != 1 || list[0].Count != 1 || !list[0].Since.Equal(now.Add(time.Hour)) {
		t.Fatalf("a new spell: %+v", list)
	}
	b.remove("203.0.113.7")
	if list := b.list(now.Add(time.Hour)); len(list) != 0 {
		t.Fatalf("after lifting: %+v", list)
	}
	// The list is bounded; spells that are over make room.
	for i := 0; i < maxBans+50; i++ {
		b.add(fmt.Sprintf("10.%d.%d.%d", i>>16, i>>8&255, i&255), "flood", now, time.Minute)
	}
	if n := len(b.list(now)); n != maxBans {
		t.Errorf("bounded list: %d", n)
	}
	b.add("192.0.2.1", "flood", now.Add(2*time.Minute), time.Minute)
	if list := b.list(now.Add(2 * time.Minute)); len(list) != 1 || list[0].IP != "192.0.2.1" {
		t.Errorf("after the old spells ended: %d", len(list))
	}
}
