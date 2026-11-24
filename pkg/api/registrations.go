// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strconv"
)

// Where trunks are registered right now. A trunk that signs in with a user
// name may register (SIP REGISTER); the engine keeps the registrations and
// this reads them from there, for the trunks the caller may see, and lets
// an operator remove one.

// Registration is one place a trunk can be reached at, as the engine
// reports it.
type Registration struct {
	TrunkID   int64  `json:"trunk_id"`
	Contact   string `json:"contact"`
	SrcIP     string `json:"src_ip"`
	SrcPort   int    `json:"src_port"`
	Transport string `json:"transport"`
	NAT       bool   `json:"nat"`
	Since     string `json:"since"`
	Until     string `json:"until"`
	UserAgent string `json:"user_agent,omitempty"`
}

// registrationsOf asks the engine for the registrations of the trunks.
func (s *Server) registrationsOf(r *http.Request, trunks []int64) ([]Registration, error) {
	out := []Registration{}
	if len(trunks) == 0 {
		return out, nil
	}
	q := url.Values{}
	for _, id := range trunks {
		q.Add("trunk_id", strconv.FormatInt(id, 10))
	}
	raw, err := s.engineDo(r.Context(), "GET", "/v1/registrations?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var page struct {
		Registrations []Registration `json:"registrations"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, Errf(502, "unreadable answer from route-engine")
	}
	return append(out, page.Registrations...), nil
}

func (s *Server) registerRegistrations() {
	// Every registration of one client's trunks: what the client's page shows.
	s.Handle("GET /v1/registrations", false, s.clientRegistrations,
		Summary("Where a client's trunks are registered right now."),
		Query("client_id", int64(0), true, "The client."), Returns(200, registrationList{}))
	s.Handle("GET /v1/client-trunks/{id}/registrations", false, s.trunkRegistrations,
		Summary("Where the trunk is registered right now: contact, the address it came from, since when and until when."),
		Returns(200, registrationList{}))
	s.Handle("DELETE /v1/client-trunks/{id}/registrations", true, s.unregisterTrunk,
		Summary("Remove the trunk's registrations, or the one with the given contact. The trunk may register again."),
		Query("contact", "", false, "Only the registration with this contact."),
		Returns(200, registrationList{}))
}

// registrationList is a trunk's or client's registrations; after a removal,
// those left.
type registrationList struct {
	Registrations []Registration `json:"registrations"`
}

func (s *Server) clientRegistrations(w http.ResponseWriter, r *http.Request, c Caller) error {
	client, err := strconv.ParseInt(r.URL.Query().Get("client_id"), 10, 64)
	if err != nil {
		return Errf(400, "client_id is required")
	}
	ctx := r.Context()
	if err := s.Resource("clients").Allowed(ctx, s.db, c, client, false); err != nil {
		return err
	}
	var trunks []int64
	if err := s.db.QueryRow(ctx, `select coalesce(array_agg(id order by id), '{}') from client_trunks
		where client_id = $1 and auth_type = 'digest'`, client).Scan(&trunks); err != nil {
		return err
	}
	list, err := s.registrationsOf(r, trunks)
	if err != nil {
		return err
	}
	WriteJSON(w, 200, registrationList{list})
	return nil
}

func (s *Server) trunkRegistrations(w http.ResponseWriter, r *http.Request, c Caller) error {
	id, err := PathID(r)
	if err != nil {
		return err
	}
	if err := s.Resource("client-trunks").Allowed(r.Context(), s.db, c, id, false); err != nil {
		return err
	}
	list, err := s.registrationsOf(r, []int64{id})
	if err != nil {
		return err
	}
	WriteJSON(w, 200, registrationList{list})
	return nil
}

// unregisterTrunk removes the trunk's registration with the given contact,
// or all of them. The trunk is not shut out: it may register again at once.
func (s *Server) unregisterTrunk(w http.ResponseWriter, r *http.Request, c Caller) error {
	id, err := PathID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.Resource("client-trunks").Allowed(ctx, s.db, c, id, true); err != nil {
		return err
	}
	contact := r.URL.Query().Get("contact")
	before, err := s.registrationsOf(r, []int64{id})
	if err != nil {
		return err
	}
	removed := []map[string]any{}
	for _, b := range before {
		if contact == "" || b.Contact == contact {
			removed = append(removed, map[string]any{"contact": b.Contact, "from": net.JoinHostPort(b.SrcIP, strconv.Itoa(b.SrcPort)), "transport": b.Transport})
		}
	}
	if len(removed) == 0 {
		return Errf(404, "the trunk has no such registration")
	}
	raw, err := s.engineDo(ctx, "POST", "/v1/unregister", map[string]any{"v": 1, "trunk_id": id, "contact": contact})
	if err != nil {
		return err
	}
	var left struct {
		Registrations []Registration `json:"registrations"`
	}
	json.Unmarshal(raw, &left)
	if left.Registrations == nil {
		left.Registrations = []Registration{}
	}
	// As with ending a call: the engine has acted, the record follows.
	var name string
	if err := s.db.QueryRow(ctx, `select name from client_trunks where id = $1`, id).Scan(&name); err != nil {
		return err
	}
	if err := s.Audit(ctx, s.db, r, c, AuditEntry{Resource: "client-trunks", RowID: id, Action: "unregister", Label: name,
		Detail: map[string]any{"removed": removed}}); err != nil {
		return err
	}
	WriteJSON(w, 200, registrationList{left.Registrations})
	return nil
}
