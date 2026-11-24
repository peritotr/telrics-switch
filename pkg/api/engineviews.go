// SPDX-License-Identifier: AGPL-3.0-only

package api

import "time"

// What the route-engine answers where the API passes its answer on, for the
// API's description. The API does not import the engine (it would bring the
// engine's dependencies and metrics into this process), so these types say
// the same as the engine's own; engineviews_test.go compares the two, so a
// field the engine adds fails the tests until it is described here too.

// liveTally counts calls (attempts, for a vendor connection) and how those
// that finished went.
type liveTally struct {
	Attempts   int64 `json:"attempts"`
	Finished   int64 `json:"finished"`
	Answered   int64 `json:"answered"`
	NetFailed  int64 `json:"failed_network"` // ended by the network, not by the far end
	Busy503    int64 `json:"code_503"`
	DurationS  int64 `json:"duration_sec"` // of answered calls
	PDDMs      int64 `json:"pdd_ms"`       // until the first progress report
	PDDSamples int64 `json:"pdd_samples"`
}

type liveSecond struct {
	T     int64 `json:"t"` // Unix seconds
	Calls int64 `json:"calls"`
	liveTally
}

type liveClient struct {
	ClientID int64 `json:"client_id"`
	Calls    int64 `json:"calls"`
	MaxCalls int64 `json:"max_calls"`
	MaxCPS   int64 `json:"max_cps"`
	liveTally
}

type liveConnection struct {
	ConnectionID int64 `json:"connection_id"`
	Calls        int64 `json:"calls"`
	MaxCalls     int64 `json:"max_calls"`
	liveTally
}

type liveShare struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

type liveCountry struct {
	Country string `json:"country"` // ISO alpha-2; "" = unknown
	Calls   int64  `json:"calls"`
	liveTally
	Prefixes    []liveShare `json:"prefixes"`
	Connections []liveShare `json:"connections"`
}

// liveTraffic is the engine's last minute of traffic (GET /v1/live).
type liveTraffic struct {
	V           int64            `json:"v"`
	At          time.Time        `json:"at"`
	WindowSec   int64            `json:"window_sec"`
	Calls       int64            `json:"calls"`
	Total       liveTally        `json:"total"`
	Seconds     []liveSecond     `json:"seconds"` // oldest first; the current second is left out
	Clients     []liveClient     `json:"clients"`
	Connections []liveConnection `json:"connections"`
	Countries   []liveCountry    `json:"countries"`
	SIPCodes    []liveShare      `json:"sip_codes"`
}

// liveCall is a call in progress.
type liveCall struct {
	CallID       string     `json:"call_id"`
	ClientID     int64      `json:"client_id"`
	ConnectionID int64      `json:"connection_id"`
	Src          string     `json:"src"`
	SrcIP        string     `json:"src_ip"`
	Dst          string     `json:"dst"`
	Country      string     `json:"country"`
	Leg          int64      `json:"leg"`
	StartAt      time.Time  `json:"start_at"`
	RingAt       *time.Time `json:"ring_at"`
	AnswerAt     *time.Time `json:"answer_at"`
}

// firewallBan is an address the SIP core is ignoring for a while.
type firewallBan struct {
	IP     string    `json:"ip"`
	Reason string    `json:"reason"`
	Since  time.Time `json:"since"`
	Until  time.Time `json:"until"`
	Count  int64     `json:"count"` // how many times in a row it has been shut out
}

type firewallBans struct {
	V    int64         `json:"v"`
	Bans []firewallBan `json:"bans"`
}

// probeResult is what one OPTIONS request to a vendor came to. Outcome is
// answered, no_answer, unreachable, certificate or failed.
type probeResult struct {
	Outcome string `json:"outcome"`
	Code    int64  `json:"sip_code,omitempty"`
	Reason  string `json:"sip_reason,omitempty"`
	Ms      int64  `json:"ms"`
	Detail  string `json:"detail,omitempty"`
}

type probeAnswer struct {
	V int64 `json:"v"`
	probeResult
}

// peerHealth is what the engine knows of a connection it asks: State is
// up, down, or unknown before the first verdict.
type peerHealth struct {
	State     string      `json:"state"`
	Since     time.Time   `json:"since"`
	CheckedAt time.Time   `json:"checked_at"`
	Last      probeResult `json:"last"`
}

// vendorRegistration is how the switch's registration with a vendor
// stands: State is pending, registered, failing or unknown.
type vendorRegistration struct {
	State     string    `json:"state"`
	Reason    string    `json:"reason"`
	Since     time.Time `json:"since"`
	CheckedAt time.Time `json:"checked_at"`
	Expires   int64     `json:"expires,omitempty"`
}
