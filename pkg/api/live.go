// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SetEngine sets the address of the route-engine the live views read from,
// such as http://127.0.0.1:58090. Without it they answer 503.
func (s *Server) SetEngine(addr string) { s.engine = strings.TrimRight(addr, "/") }

// SetEngineToken sets the secret the route-engine expects from its callers
// (its ENGINE_TOKEN); it is sent with every request and shown nowhere.
func (s *Server) SetEngineToken(token string) { s.engineToken = token }

var engineClient = &http.Client{Timeout: 5 * time.Second}

func (s *Server) registerLive() {
	s.Handle("GET /v1/live", false, s.live,
		Summary("The last minute of traffic, from the engine, with the names of the clients and connections in it."),
		Returns(200, liveAnswer{}))
	s.Handle("GET /v1/live/calls", false, s.liveCalls, Summary("Calls in progress, from the engine."),
		Query("client_id", int64(0), false, "Only this client's calls."),
		Query("offset", 0, false, "Calls to skip."), Query("limit", 0, false, "Page size (1-500, 100 when left out)."),
		Returns(200, liveCallsAnswer{}))
	s.Handle("POST /v1/live/calls/hangup", true, s.liveHangup, Summary("End a call in progress."),
		Takes(hangupRequest{}), ReturnsNothing())
}

// connectionName is what the live views show next to a connection's id.
type connectionName struct {
	Name      string `json:"name"`
	VendorID  int64  `json:"vendor_id"`
	Vendor    string `json:"vendor"`
	Host      string `json:"host"`
	Transport string `json:"transport"`
}

// liveAnswer is the engine's view with names: clients by id, connections
// by id.
type liveAnswer struct {
	Live        liveTraffic               `json:"live"`
	Clients     map[string]string         `json:"clients"`
	Connections map[string]connectionName `json:"connections"`
}

type liveCallsAnswer struct {
	Total       int64                     `json:"total"`
	Calls       []liveCall                `json:"calls"`
	Clients     map[string]string         `json:"clients"`
	Connections map[string]connectionName `json:"connections"`
}

type hangupRequest struct {
	CallID string `json:"call_id" openapi:"required"`
}

// engineDo calls the route-engine and returns its answer; an error answer
// is passed on with its own status.
func (s *Server) engineDo(ctx context.Context, method, path string, body any) ([]byte, error) {
	if s.engine == "" {
		return nil, Errf(503, "route-engine address is not configured")
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.engine+path, rd)
	if err != nil {
		return nil, err
	}
	if s.engineToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.engineToken)
	}
	resp, err := engineClient.Do(req)
	if err != nil {
		// The client's error is not passed on: it names the address.
		return nil, Errf(503, "route-engine is not reachable")
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, Errf(503, "route-engine is not reachable")
	}
	if resp.StatusCode == 401 {
		// Not passed on as 401: the panel would take it for its own session
		// having ended. The setting is named, never its value.
		return nil, Errf(503, "route-engine refused the API: ENGINE_TOKEN is missing or differs from the engine's")
	}
	if resp.StatusCode >= 300 {
		return nil, Errf(resp.StatusCode, "%s", strings.TrimSpace(string(out)))
	}
	return out, nil
}

// seesAll reports whether the caller may see every client. Live traffic is
// not split by account, so only such a caller gets the whole picture.
func (s *Server) seesAll(ctx context.Context, c Caller) error {
	var all bool
	err := s.db.QueryRow(ctx, fmt.Sprintf(
		`select not exists (select 1 from clients t where not (t.reseller_id in %s))`, s.scope), c.ResellerID).Scan(&all)
	if err == nil && !all {
		err = Errf(403, "live traffic is available only to an account that sees every client")
	}
	return err
}

// names looks up what the panel shows next to the ids the engine reports.
func (s *Server) names(ctx context.Context, clients, conns []int64) (json.RawMessage, json.RawMessage, error) {
	var cl, cn string
	err := s.db.QueryRow(ctx, `
		select (select coalesce(jsonb_object_agg(id, name), '{}') from clients where id = any($1))::text,
		       (select coalesce(jsonb_object_agg(vc.id, jsonb_build_object('name', vc.name, 'vendor_id', v.id,
		                 'vendor', v.name, 'host', vc.host, 'transport', vc.transport)), '{}')
		        from vendor_connections vc join vendors v on v.id = vc.vendor_id where vc.id = any($2))::text`,
		clients, conns).Scan(&cl, &cn)
	return json.RawMessage(cl), json.RawMessage(cn), err
}

func (s *Server) live(w http.ResponseWriter, r *http.Request, c Caller) error {
	if err := s.seesAll(r.Context(), c); err != nil {
		return err
	}
	raw, err := s.engineDo(r.Context(), "GET", "/v1/live", nil)
	if err != nil {
		return err
	}
	var ids struct {
		Clients []struct {
			ClientID int64 `json:"client_id"`
		} `json:"clients"`
		Connections []struct {
			ConnectionID int64 `json:"connection_id"`
		} `json:"connections"`
		Countries []struct {
			Connections []struct {
				Key string `json:"key"`
			} `json:"connections"`
		} `json:"countries"`
	}
	if err := json.Unmarshal(raw, &ids); err != nil {
		return Errf(502, "unreadable answer from route-engine")
	}
	var clients, conns []int64
	for _, x := range ids.Clients {
		clients = append(clients, x.ClientID)
	}
	for _, x := range ids.Connections {
		conns = append(conns, x.ConnectionID)
	}
	for _, country := range ids.Countries {
		for _, x := range country.Connections {
			if id, err := strconv.ParseInt(x.Key, 10, 64); err == nil {
				conns = append(conns, id)
			}
		}
	}
	cl, cn, err := s.names(r.Context(), clients, conns)
	if err != nil {
		return err
	}
	WriteJSON(w, 200, map[string]json.RawMessage{"live": raw, "clients": cl, "connections": cn})
	return nil
}

func (s *Server) liveCalls(w http.ResponseWriter, r *http.Request, c Caller) error {
	if err := s.seesAll(r.Context(), c); err != nil {
		return err
	}
	q := url.Values{}
	for _, k := range []string{"client_id", "offset", "limit"} {
		if v := r.URL.Query().Get(k); v != "" {
			if _, err := strconv.ParseInt(v, 10, 64); err != nil {
				return Errf(400, "%s must be a number", k)
			}
			q.Set(k, v)
		}
	}
	raw, err := s.engineDo(r.Context(), "GET", "/v1/calls?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	var page struct {
		Total int64             `json:"total"`
		Calls []json.RawMessage `json:"calls"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return Errf(502, "unreadable answer from route-engine")
	}
	var clients, conns []int64
	for _, call := range page.Calls {
		var x struct {
			ClientID     int64 `json:"client_id"`
			ConnectionID int64 `json:"connection_id"`
		}
		json.Unmarshal(call, &x)
		clients, conns = append(clients, x.ClientID), append(conns, x.ConnectionID)
	}
	cl, cn, err := s.names(r.Context(), clients, conns)
	if err != nil {
		return err
	}
	if page.Calls == nil {
		page.Calls = []json.RawMessage{}
	}
	WriteJSON(w, 200, map[string]any{"total": page.Total, "calls": page.Calls, "clients": cl, "connections": cn})
	return nil
}

func (s *Server) liveHangup(w http.ResponseWriter, r *http.Request, c Caller) error {
	if err := s.seesAll(r.Context(), c); err != nil {
		return err
	}
	var in hangupRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil || in.CallID == "" {
		return Errf(400, "call_id is required")
	}
	if _, err := s.engineDo(r.Context(), "POST", "/v1/hangup", map[string]any{"v": 1, "call_id": in.CallID}); err != nil {
		return err
	}
	// The call is over whatever happens next, so the record cannot share a
	// transaction with it: it is written once the engine has answered.
	if err := s.Audit(r.Context(), s.db, r, c, AuditEntry{Resource: "live-calls", Action: "hangup", Label: in.CallID}); err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}
