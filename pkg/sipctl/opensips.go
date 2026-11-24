// SPDX-License-Identifier: AGPL-3.0-only

// Package sipctl carries the engine's commands to the SIP core. The engine
// only knows "end this call"; how that is said to a given SIP server lives
// here, and is spoken over the network only.
package sipctl

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// mi sends one command to OpenSIPS's management interface over its
// datagram socket and reports the error it answers with, if any. A missing
// object (404) is not an error: what was to go is gone.
func mi(ctx context.Context, addr, method string, params map[string]string) error {
	_, err := miResult(ctx, addr, method, params)
	return err
}

// miResult is mi that also gives back what the command answered (null for
// a missing object).
func miResult(ctx context.Context, addr, method string, params map[string]string) (json.RawMessage, error) {
	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		body["params"] = params
	}
	req, _ := json.Marshal(body)
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := conn.Write(req); err != nil {
		return nil, err
	}
	buf := make([]byte, 65535)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		return nil, fmt.Errorf("opensips: unreadable reply: %w", err)
	}
	if resp.Error != nil && resp.Error.Code != 404 {
		return nil, fmt.Errorf("opensips: %d %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

// OpenSIPS returns a function that ends a call through the management
// interface of the OpenSIPS at addr (JSON-RPC over UDP, module mi_datagram).
// A call OpenSIPS no longer knows counts as ended.
func OpenSIPS(addr string) func(ctx context.Context, callID string) error {
	return func(ctx context.Context, callID string) error {
		return mi(ctx, addr, "dlg_end_dlg", map[string]string{"dialog_id": callID})
	}
}

// OpenSIPSUnban returns a function that makes OpenSIPS forget it shut an
// address out: the script keeps its bans in the local cache as "ban:<ip>".
func OpenSIPSUnban(addr string) func(ctx context.Context, ip string) error {
	return func(ctx context.Context, ip string) error {
		// The flood detector remembers the address a while longer than the
		// script's ban lasts and would shut it out again at once; it is
		// told to forget it too. It may not know the address: no matter.
		mi(ctx, addr, "pike_rm", map[string]string{"IP": ip})
		// The script keys its ban by the address as OpenSIPS writes it,
		// which for IPv6 is not the usual form: both are removed.
		for _, form := range banForms(ip) {
			if err := mi(ctx, addr, "cache_remove", map[string]string{"system": "local", "attr": "ban:" + form}); err != nil {
				return err
			}
		}
		return nil
	}
}

// banForms is the address as given and, for IPv6, as OpenSIPS writes it
// ($si): every group in capitals, without leading zeros and without "::"
// ("fd30::21" is "FD30:0:0:0:0:0:0:21").
func banForms(ip string) []string {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is6() || a.Is4In6() {
		return []string{ip}
	}
	b := a.As16()
	groups := make([]string, 8)
	for i := range groups {
		groups[i] = strings.ToUpper(strconv.FormatUint(uint64(b[2*i])<<8|uint64(b[2*i+1]), 16))
	}
	if full := strings.Join(groups, ":"); full != ip {
		return []string{ip, full}
	}
	return []string{ip}
}

// Registration is what the SIP core says of one of its registrations with
// a vendor (ADR-0035): the address of record and registrar it was given,
// and how the registration stands.
type Registration struct {
	AOR, Registrar string
	// State is one of the Reg… values.
	State string
	// Expires is the time the vendor granted, in seconds (the time asked for
	// until it has answered).
	Expires int
}

// How a registration stands, whatever the SIP server.
const (
	RegPending    = "pending"     // not yet sent, or waiting for the answer
	RegRegistered = "registered"  // the vendor took it
	RegNoAnswer   = "no_answer"   // the vendor did not answer
	RegRejected   = "credentials" // the vendor did not accept the password
	RegRefused    = "refused"     // the vendor refused with another error
	RegError      = "error"       // it could not be sent, or the answer read
)

// opensipsRegStates names uac_registrant's states (reg_list) the way the
// engine knows them.
var opensipsRegStates = map[string]string{
	"NOT_REGISTERED_STATE":            RegPending,
	"REGISTERING_STATE":               RegPending,
	"AUTHENTICATING_STATE":            RegPending,
	"REGISTERED_STATE":                RegRegistered,
	"REGISTER_TIMEOUT_STATE":          RegNoAnswer,
	"INTERNAL_ERROR_STATE":            RegError,
	"WRONG_CREDENTIALS_STATE":         RegRejected,
	"REGISTRAR_ERROR_STATE":           RegRefused,
	"UNREGISTERING_STATE":             RegPending,
	"AUTHENTICATING_UNREGISTER_STATE": RegPending,
}

// parseRegList reads the answer to uac_registrant's "reg_list".
func parseRegList(raw json.RawMessage) ([]Registration, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var res struct {
		Records []struct {
			AOR       string `json:"AOR"`
			Registrar string `json:"registrar"`
			State     string `json:"state"`
			Expires   int    `json:"expires"`
		} `json:"Records"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("opensips: unreadable registrant list: %w", err)
	}
	out := make([]Registration, 0, len(res.Records))
	for _, r := range res.Records {
		state, ok := opensipsRegStates[strings.TrimSpace(r.State)]
		if !ok {
			state = RegError
		}
		out = append(out, Registration{AOR: r.AOR, Registrar: r.Registrar, State: state, Expires: r.Expires})
	}
	return out, nil
}

// OpenSIPSRegistrations returns a function that asks the OpenSIPS at addr
// how its registrations with vendors stand (module uac_registrant). The
// registrations themselves reach it from the engine through a table of
// its own (deploy/images/opensips/registrants.sh).
func OpenSIPSRegistrations(addr string) func(ctx context.Context) ([]Registration, error) {
	return func(ctx context.Context) ([]Registration, error) {
		raw, err := miResult(ctx, addr, "reg_list", nil)
		if err != nil {
			return nil, err
		}
		return parseRegList(raw)
	}
}
