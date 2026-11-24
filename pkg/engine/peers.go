// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"softswitch/core/pkg/routing"
)

// PeerCheck makes the engine look at the certificate of every vendor that
// is reached over TLS: it connects the way the SIP core would and checks
// that the certificate is signed by a trusted authority and made out to the
// vendor's name. A connection whose certificate fails is given no calls
// until a later look finds it in order. The SIP core's own check stops at
// the signature; the name is checked here.
type PeerCheck struct {
	Roots    *x509.CertPool // nil = the system's
	Interval time.Duration  // between rounds; 0 = one minute
	Timeout  time.Duration  // per connection; 0 = three seconds
	// SIPAddr, if set, is the SIP core's own TLS address: its certificate's
	// expiry is read from there and reported, nothing more.
	SIPAddr string
}

// PeerCheckFromEnv builds the check from the settings every edition's
// engine reads: PEER_VERIFY=off leaves it out (nil), TLS_CA_LIST names the
// file of trusted authorities (the SIP core's list; empty = the system's),
// PEER_CHECK_SEC the time between rounds, SIP_TLS_ADDR the SIP core's own
// TLS address.
func PeerCheckFromEnv() (*PeerCheck, error) {
	if os.Getenv("PEER_VERIFY") == "off" {
		log.Print("PEER_VERIFY=off: the names in vendors' certificates are not checked")
		return nil, nil
	}
	p := &PeerCheck{SIPAddr: os.Getenv("SIP_TLS_ADDR")}
	if file := os.Getenv("TLS_CA_LIST"); file != "" {
		pem, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("TLS_CA_LIST: %w", err)
		}
		p.Roots = x509.NewCertPool()
		if !p.Roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("TLS_CA_LIST: %s holds no certificate", file)
		}
	}
	if v := os.Getenv("PEER_CHECK_SEC"); v != "" {
		sec, err := strconv.Atoi(v)
		if err != nil || sec < 1 {
			return nil, errors.New("PEER_CHECK_SEC must be a positive number of seconds")
		}
		p.Interval = time.Duration(sec) * time.Second
	}
	return p, nil
}

// peerState is what the last round made of the vendors' certificates.
type peerState struct {
	bad     map[int64]string // connection id -> why it gets no calls
	checked int              // connections looked at
}

// peerName is the name a connection's certificate must be made out to.
func peerName(c routing.Connection) string {
	if c.TLSName != "" {
		return c.TLSName
	}
	return c.Host
}

// probe connects to addr and reports whether the certificate presented is
// in order for name. certErr tells a bad certificate from a peer that
// could not be reached at all.
func (p *PeerCheck) probe(ctx context.Context, addr, name string, verify bool) (cert *x509.Certificate, certErr bool, err error) {
	timeout := p.Timeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: timeout}, Config: &tls.Config{
		RootCAs: p.Roots, ServerName: name, MinVersion: tls.VersionTLS12, InsecureSkipVerify: !verify,
	}}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		var ve *tls.CertificateVerificationError
		return nil, errors.As(err, &ve), err
	}
	defer conn.Close()
	if certs := conn.(*tls.Conn).ConnectionState().PeerCertificates; len(certs) > 0 {
		cert = certs[0]
	}
	return cert, false, nil
}

// round looks at every enabled TLS connection of snap. A peer that cannot
// be reached keeps the verdict it had: the SIP core finds out about those
// by itself, call by call.
func (p *PeerCheck) round(ctx context.Context, snap *routing.Snapshot, prev *peerState) *peerState {
	next := &peerState{bad: map[int64]string{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 16)
	for _, c := range snap.Connections {
		if !c.Enabled || c.Transport != "tls" {
			continue
		}
		next.checked++
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots; wg.Done() }()
			_, certErr, err := p.probe(ctx, net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), peerName(c), true)
			was, wasBad := "", false
			if prev != nil {
				was, wasBad = prev.bad[c.ID]
			}
			mu.Lock()
			defer mu.Unlock()
			switch {
			case certErr:
				next.bad[c.ID] = err.Error()
				if !wasBad {
					log.Printf("engine: vendor connection %d (%s): certificate refused, no calls will be sent: %v", c.ID, c.Host, err)
				}
			case err != nil && wasBad:
				next.bad[c.ID] = was
			case err == nil && wasBad:
				log.Printf("engine: vendor connection %d (%s): certificate in order again", c.ID, c.Host)
			}
		}()
	}
	wg.Wait()
	return next
}

// RunPeerCheck looks at the vendors' certificates until ctx ends: at once,
// then every interval and whenever the configuration changed.
func (e *Engine) RunPeerCheck(ctx context.Context, p *PeerCheck) {
	interval := p.Interval
	if interval == 0 {
		interval = time.Minute
	}
	var seen *routing.Snapshot
	last := time.Time{}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if snap := e.snap.Load(); snap != nil && (snap != seen && peersChanged(seen, snap) || time.Since(last) >= interval) {
			e.peers.Store(p.round(ctx, snap, e.peers.Load()))
			if p.SIPAddr != "" {
				host, _, _ := net.SplitHostPort(p.SIPAddr)
				if cert, _, err := p.probe(ctx, p.SIPAddr, host, false); err == nil && cert != nil {
					e.sipCertEnd.Store(cert.NotAfter.Unix())
				} else {
					e.sipCertEnd.Store(0)
				}
			}
			seen, last = snap, time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// peersChanged reports whether b has a TLS connection that a did not have
// in the same form: a new or edited one is looked at without waiting.
func peersChanged(a, b *routing.Snapshot) bool {
	if a == nil {
		return true
	}
	for id, c := range b.Connections {
		if c.Transport != "tls" || !c.Enabled {
			continue
		}
		if old, ok := a.Connections[id]; !ok || old.Host != c.Host || old.Port != c.Port || old.TLSName != c.TLSName || old.Transport != c.Transport || !old.Enabled {
			return true
		}
	}
	return false
}

// trusted takes the routes over connections whose certificate was refused
// out of a decision. With none left the call has no route.
func (e *Engine) trusted(d routing.Decision) routing.Decision {
	st := e.peers.Load()
	if st == nil || len(st.bad) == 0 || !d.Allowed {
		return d
	}
	kept := d.Routes[:0:0]
	for _, r := range d.Routes {
		if _, bad := st.bad[r.ConnectionID]; bad {
			mPeerSkipped.Inc()
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
