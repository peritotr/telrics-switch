// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

// liveWindow is how many seconds of traffic the live view covers.
const liveWindow = 60

// tally counts calls (or, for a vendor connection, attempts) that came in
// and how those that finished went.
type tally struct {
	Attempts   uint32 `json:"attempts"`
	Finished   uint32 `json:"finished"`
	Answered   uint32 `json:"answered"`
	NetFailed  uint32 `json:"failed_network"` // ended by the network, not by the far end
	Busy503    uint32 `json:"code_503"`
	DurationS  uint64 `json:"duration_sec"` // of answered calls
	PDDMs      uint64 `json:"pdd_ms"`       // until the first progress report
	PDDSamples uint32 `json:"pdd_samples"`
}

func (t *tally) add(o *tally) {
	t.Attempts += o.Attempts
	t.Finished += o.Finished
	t.Answered += o.Answered
	t.NetFailed += o.NetFailed
	t.Busy503 += o.Busy503
	t.DurationS += o.DurationS
	t.PDDMs += o.PDDMs
	t.PDDSamples += o.PDDSamples
}

// outcome is how a call or an attempt ended.
type outcome struct {
	code     int
	duration int   // seconds talked
	pddMs    int64 // 0 = no progress was reported
}

func (t *tally) finish(o outcome) {
	t.Finished++
	switch {
	case o.code == 200:
		t.Answered++
		t.DurationS += uint64(o.duration)
	case !finalCodes[o.code] && o.code < 600 && o.code != 402 && o.code != 403:
		t.NetFailed++
	}
	if o.code == 503 {
		t.Busy503++
	}
	if o.pddMs > 0 {
		t.PDDMs += uint64(o.pddMs)
		t.PDDSamples++
	}
}

type countryTally struct {
	tally
	prefixes map[string]uint32 // calls by selling prefix
	conns    map[int64]uint32  // finished calls by vendor connection
}

type liveSlot struct {
	sec       int64
	calls     int64 // calls in progress, sampled once a second
	total     tally
	clients   map[int64]*tally
	conns     map[int64]*tally
	countries map[string]*countryTally
	codes     map[int]uint32 // final answers of attempts
}

// live keeps the last minute of traffic, one slot per second. It lives in
// the engine's memory: it starts empty after a restart, and several engines
// each see their own share.
type live struct {
	mu    sync.Mutex
	slots [liveWindow]liveSlot
}

func (l *live) slot(now time.Time) *liveSlot {
	sec := now.Unix()
	s := &l.slots[sec%liveWindow]
	if s.sec != sec {
		*s = liveSlot{sec: sec, clients: map[int64]*tally{}, conns: map[int64]*tally{},
			countries: map[string]*countryTally{}, codes: map[int]uint32{}}
	}
	return s
}

func (s *liveSlot) client(id int64) *tally {
	t := s.clients[id]
	if t == nil {
		t = &tally{}
		s.clients[id] = t
	}
	return t
}

func (s *liveSlot) conn(id int64) *tally {
	t := s.conns[id]
	if t == nil {
		t = &tally{}
		s.conns[id] = t
	}
	return t
}

func (s *liveSlot) country(iso string) *countryTally {
	t := s.countries[iso]
	if t == nil {
		t = &countryTally{prefixes: map[string]uint32{}, conns: map[int64]uint32{}}
		s.countries[iso] = t
	}
	return t
}

// arrived counts a new call; client 0 is an unknown source.
func (l *live) arrived(now time.Time, client int64, country, prefix string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.slot(now)
	s.total.Attempts++
	if client != 0 {
		s.client(client).Attempts++
		c := s.country(country)
		c.Attempts++
		c.prefixes[prefix]++
	}
}

// attempted counts the end of one attempt over a vendor connection.
func (l *live) attempted(now time.Time, conn int64, o outcome) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.slot(now)
	t := s.conn(conn)
	t.Attempts++
	t.finish(o)
	s.codes[o.code]++
}

// finished counts the end of a call. conn is 0 when no vendor was tried.
func (l *live) finished(now time.Time, client int64, country string, conn int64, o outcome) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.slot(now)
	s.total.finish(o)
	if client == 0 {
		return
	}
	s.client(client).finish(o)
	c := s.country(country)
	c.finish(o)
	if conn != 0 {
		c.conns[conn]++
	} else {
		s.codes[o.code]++ // refused before any attempt
	}
}

func (l *live) sample(now time.Time, calls int64) {
	l.mu.Lock()
	l.slot(now).calls = calls
	l.mu.Unlock()
}

type liveSecond struct {
	T     int64 `json:"t"`
	Calls int64 `json:"calls"`
	tally
}

type liveClient struct {
	ClientID int64 `json:"client_id"`
	Calls    int64 `json:"calls"`
	MaxCalls int   `json:"max_calls"`
	MaxCPS   int   `json:"max_cps"`
	tally
}

type liveConn struct {
	ConnectionID int64 `json:"connection_id"`
	Calls        int64 `json:"calls"`
	MaxCalls     int   `json:"max_calls"`
	tally
}

type liveShare struct {
	Key   string `json:"key"`
	Count uint32 `json:"count"`
}

type liveCountry struct {
	Country string `json:"country"` // ISO alpha-2; "" = unknown
	Calls   int64  `json:"calls"`
	tally
	Prefixes    []liveShare `json:"prefixes"`
	Connections []liveShare `json:"connections"`
}

// LiveView is the answer to GET /v1/live. The management API passes it on
// and describes it with a type of its own; its tests compare the two.
type LiveView = liveView

type liveView struct {
	V           int           `json:"v"`
	At          time.Time     `json:"at"`
	WindowSec   int           `json:"window_sec"`
	Calls       int64         `json:"calls"`
	Total       tally         `json:"total"`
	Seconds     []liveSecond  `json:"seconds"` // oldest first; the current second is left out
	Clients     []liveClient  `json:"clients"`
	Connections []liveConn    `json:"connections"`
	Countries   []liveCountry `json:"countries"`
	SIPCodes    []liveShare   `json:"sip_codes"`
}

func shares[K comparable](m map[K]uint32, name func(K) string) []liveShare {
	out := make([]liveShare, 0, len(m))
	for k, n := range m {
		out = append(out, liveShare{name(k), n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// view sums the window that ended with the last full second.
func (e *Engine) view(ctx context.Context) (*liveView, error) {
	now := e.now()
	v := &liveView{V: Version, At: now.UTC(), WindowSec: liveWindow}
	clients, conns := map[int64]*tally{}, map[int64]*tally{}
	countries := map[string]*countryTally{}
	codes := map[int]uint32{}

	l := &e.live
	l.mu.Lock()
	for sec := now.Unix() - liveWindow; sec < now.Unix(); sec++ {
		s := &l.slots[sec%liveWindow]
		row := liveSecond{T: sec}
		if s.sec == sec {
			row.Calls, row.tally = s.calls, s.total
			v.Total.add(&s.total)
			for id, t := range s.clients {
				if clients[id] == nil {
					clients[id] = &tally{}
				}
				clients[id].add(t)
			}
			for id, t := range s.conns {
				if conns[id] == nil {
					conns[id] = &tally{}
				}
				conns[id].add(t)
			}
			for iso, t := range s.countries {
				c := countries[iso]
				if c == nil {
					c = &countryTally{prefixes: map[string]uint32{}, conns: map[int64]uint32{}}
					countries[iso] = c
				}
				c.add(&t.tally)
				for k, n := range t.prefixes {
					c.prefixes[k] += n
				}
				for k, n := range t.conns {
					c.conns[k] += n
				}
			}
			for code, n := range s.codes {
				codes[code] += n
			}
		}
		v.Seconds = append(v.Seconds, row)
	}
	l.mu.Unlock()

	// Calls in progress come from the state store, so they are right even
	// just after a restart. Every client and connection with a limit is
	// listed, and whatever else had traffic.
	snap := e.snap.Load()
	if snap != nil {
		for id, c := range snap.Clients {
			if (c.MaxCalls > 0 || c.MaxCPS > 0) && clients[id] == nil {
				clients[id] = &tally{}
			}
		}
		for id, c := range snap.Connections {
			if c.MaxCalls > 0 && conns[id] == nil {
				conns[id] = &tally{}
			}
		}
	}
	keys := []string{allCalls}
	for id, t := range clients {
		lc := liveClient{ClientID: id, tally: *t}
		if snap != nil {
			lc.MaxCalls, lc.MaxCPS = snap.Clients[id].MaxCalls, snap.Clients[id].MaxCPS
		}
		v.Clients = append(v.Clients, lc)
	}
	sort.Slice(v.Clients, func(i, j int) bool { return v.Clients[i].ClientID < v.Clients[j].ClientID })
	for id, t := range conns {
		lc := liveConn{ConnectionID: id, tally: *t}
		if snap != nil {
			lc.MaxCalls = snap.Connections[id].MaxCalls
		}
		v.Connections = append(v.Connections, lc)
	}
	sort.Slice(v.Connections, func(i, j int) bool { return v.Connections[i].ConnectionID < v.Connections[j].ConnectionID })
	for iso, t := range countries {
		v.Countries = append(v.Countries, liveCountry{Country: iso, tally: t.tally,
			Prefixes:    shares(t.prefixes, func(k string) string { return k }),
			Connections: shares(t.conns, func(k int64) string { return strconv.FormatInt(k, 10) })})
	}
	sort.Slice(v.Countries, func(i, j int) bool { return v.Countries[i].Country < v.Countries[j].Country })
	for i := range v.Clients {
		keys = append(keys, clientCalls(v.Clients[i].ClientID))
	}
	for i := range v.Connections {
		keys = append(keys, connCalls(v.Connections[i].ConnectionID))
	}
	for i := range v.Countries {
		keys = append(keys, countryCalls(v.Countries[i].Country))
	}
	counts, err := e.calls.Count(ctx, now, keys)
	if err != nil {
		return nil, err
	}
	v.Calls, counts = counts[0], counts[1:]
	for i := range v.Clients {
		v.Clients[i].Calls = counts[i]
	}
	counts = counts[len(v.Clients):]
	for i := range v.Connections {
		v.Connections[i].Calls = counts[i]
	}
	counts = counts[len(v.Connections):]
	for i := range v.Countries {
		v.Countries[i].Calls = counts[i]
	}
	v.SIPCodes = shares(codes, strconv.Itoa)
	return v, nil
}

// RunLive samples the number of calls in progress once a second.
func (e *Engine) RunLive(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := e.now()
			if n, err := e.calls.Count(ctx, now, []string{allCalls}); err == nil {
				e.live.sample(now, n[0])
			}
		}
	}
}

func (e *Engine) handleLive(w http.ResponseWriter, r *http.Request) {
	v, err := e.view(r.Context())
	if err != nil {
		http.Error(w, "state store unavailable", 503)
		return
	}
	writeJSON(w, v)
}

func (e *Engine) handleCalls(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	client, _ := strconv.ParseInt(q.Get("client_id"), 10, 64)
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 500 {
		limit = 100
	}
	calls, total, err := e.calls.List(r.Context(), e.now(), client, max(offset, 0), limit)
	if err != nil {
		http.Error(w, "state store unavailable", 503)
		return
	}
	writeJSON(w, map[string]any{"v": Version, "total": total, "calls": calls})
}

// handleHangup ends a call on request, as an operator may from the panel.
func (e *Engine) handleHangup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		V      int    `json:"v"`
		CallID string `json:"call_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil || in.CallID == "" || in.V != Version {
		http.Error(w, "invalid request", 400)
		return
	}
	c, err := e.calls.Get(r.Context(), in.CallID)
	switch {
	case err != nil:
		http.Error(w, "state store unavailable", 503)
	case c == nil:
		http.Error(w, "no such call", 404)
	case c.AnswerMs == 0:
		http.Error(w, "call is not answered yet", 409)
	case e.Hangup == nil:
		http.Error(w, "this engine cannot end calls", 501)
	default:
		if err := e.Hangup(r.Context(), in.CallID); err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		w.WriteHeader(204)
	}
}
