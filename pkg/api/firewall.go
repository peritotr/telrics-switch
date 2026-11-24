// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"net/http"
	"net/netip"
)

// The firewall page of the panel's tools. The rules are an ordinary
// resource ("firewall-rules"); what the SIP core has shut out by itself is
// known to the engine and read from there. Both belong to the account that
// runs the system: only its rules are applied, and only it sees the bans.

func (s *Server) onlyRoot(ctx context.Context, c Caller) error {
	root, err := s.RootCaller(ctx)
	if err != nil {
		return err
	}
	if c.ResellerID != root.ResellerID {
		return Errf(403, "only the account that runs the system may do this")
	}
	return nil
}

// firewallBans lists the addresses the SIP core is ignoring for flooding.
func (s *Server) firewallBans(w http.ResponseWriter, r *http.Request, c Caller) error {
	if err := s.onlyRoot(r.Context(), c); err != nil {
		return err
	}
	raw, err := s.engineDo(r.Context(), "GET", "/v1/bans", nil)
	if err != nil {
		return err
	}
	WriteRaw(w, 200, string(raw))
	return nil
}

// firewallUnban lets an address go before its time is up.
func (s *Server) firewallUnban(w http.ResponseWriter, r *http.Request, c Caller) error {
	if err := s.onlyRoot(r.Context(), c); err != nil {
		return err
	}
	var in unbanRequest
	if err := ReadJSON(r, &in); err != nil {
		return err
	}
	if _, err := netip.ParseAddr(in.IP); err != nil {
		return Errf(422, "ip must be an address")
	}
	if _, err := s.engineDo(r.Context(), "POST", "/v1/unban", map[string]any{"v": 1, "src_ip": in.IP}); err != nil {
		return err
	}
	// As with ending a call: the engine has acted, the record follows.
	if err := s.Audit(r.Context(), s.db, r, c, AuditEntry{Resource: "firewall", Action: "unban", Label: in.IP}); err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}
