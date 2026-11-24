// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// The engine's part in the switch's firewall. The operator's rules come
// with the snapshot and are answered to the SIP core with every question
// about a source. The SIP core in turn reports the addresses it shut out by
// itself for flooding, so that an operator can see them and let one go.

// Ban is an address the SIP core is ignoring for a while.
type Ban struct {
	IP     string    `json:"ip"`
	Reason string    `json:"reason"`
	Since  time.Time `json:"since"`
	Until  time.Time `json:"until"`
	// Count is how many times in a row the address has been shut out.
	Count int `json:"count"`
}

// Unban makes the SIP core forget that it shut an address out.
type Unban func(ctx context.Context, ip string) error

type bans struct {
	mu sync.Mutex
	by map[string]Ban
}

// maxBans bounds what is remembered: a flood from forged addresses must not
// fill the engine's memory.
const maxBans = 10_000

func (b *bans) add(ip, reason string, now time.Time, d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.by == nil {
		b.by = map[string]Ban{}
	}
	ban, known := b.by[ip]
	if !known || !ban.Until.After(now.Add(-time.Minute)) {
		if !known && len(b.by) >= maxBans {
			b.sweep(now)
			if len(b.by) >= maxBans {
				return
			}
		}
		ban = Ban{IP: ip, Since: now.UTC()}
	}
	ban.Reason, ban.Until, ban.Count = reason, now.Add(d).UTC(), ban.Count+1
	b.by[ip] = ban
}

func (b *bans) sweep(now time.Time) {
	for ip, ban := range b.by {
		if !ban.Until.After(now) {
			delete(b.by, ip)
		}
	}
}

func (b *bans) remove(ip string) {
	b.mu.Lock()
	delete(b.by, ip)
	b.mu.Unlock()
}

// list returns the bans in force, the longest-lasting first.
func (b *bans) list(now time.Time) []Ban {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sweep(now)
	out := make([]Ban, 0, len(b.by))
	for _, ban := range b.by {
		out = append(out, ban)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Until.Equal(out[j].Until) {
			return out[i].Until.After(out[j].Until)
		}
		return out[i].IP < out[j].IP
	})
	return out
}

// handleBan takes the SIP core's report that it shut an address out.
func (e *Engine) handleBan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		V       int    `json:"v"`
		SrcIP   string `json:"src_ip"`
		Reason  string `json:"reason"`
		Seconds int    `json:"seconds"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil || in.V != Version {
		http.Error(w, "invalid request", 400)
		return
	}
	in.SrcIP = canonIP(in.SrcIP)
	if _, err := netip.ParseAddr(in.SrcIP); err != nil || in.Seconds < 1 || in.Seconds > 86400 {
		http.Error(w, "invalid request", 400)
		return
	}
	if len(in.Reason) > 40 {
		in.Reason = in.Reason[:40]
	}
	e.bans.add(in.SrcIP, in.Reason, e.now(), time.Duration(in.Seconds)*time.Second)
	mBans.Inc()
	w.WriteHeader(204)
}

func (e *Engine) handleBans(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"v": Version, "bans": e.bans.list(e.now())})
}

// handleUnban lets an address go before its time is up.
func (e *Engine) handleUnban(w http.ResponseWriter, r *http.Request) {
	var in struct {
		V     int    `json:"v"`
		SrcIP string `json:"src_ip"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil || in.V != Version {
		http.Error(w, "invalid request", 400)
		return
	}
	in.SrcIP = canonIP(in.SrcIP)
	if _, err := netip.ParseAddr(in.SrcIP); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if e.Unban == nil {
		http.Error(w, "the SIP core cannot be reached to lift a ban", 501)
		return
	}
	if err := e.Unban(r.Context(), in.SrcIP); err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	e.bans.remove(in.SrcIP)
	w.WriteHeader(204)
}
