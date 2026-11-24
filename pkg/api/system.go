// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"softswitch/core/pkg/sysinfo"
)

// The system page of the panel's tools: what the machine is doing and
// whether each part of the switch answers. Only the account that runs the
// system sees it.

// Service is one part of the system and how to ask whether it is alive: an
// address that answers an HTTP GET with a status below 500.
type Service struct{ Name, URL string }

type serviceState struct {
	Name string `json:"name"`
	Up   bool   `json:"up"`
	Ms   int64  `json:"ms"`             // how long the answer took
	Note string `json:"note,omitempty"` // why it counts as down
}

// systemState is the machine and whether each service answers.
type systemState struct {
	Machine  sysinfo.State  `json:"machine"`
	Services []serviceState `json:"services"`
}

type systemView struct {
	sys      *sysinfo.Collector
	services []Service

	mu      sync.Mutex
	checked time.Time
	states  []serviceState
}

// SetSystem turns the system page on: sys samples the machine, services
// are the parts to look at besides the database, the engine and ClickHouse,
// which the API knows by itself.
func (s *Server) SetSystem(sys *sysinfo.Collector, services []Service) {
	s.system = &systemView{sys: sys, services: services}
}

// ParseServices reads a list such as "SIP=http://10.0.0.1:8888/metrics,
// Media=http://10.0.0.2:2225/metrics".
func ParseServices(list string) []Service {
	var out []Service
	for _, item := range strings.Split(list, ",") {
		if name, url, ok := strings.Cut(strings.TrimSpace(item), "="); ok && name != "" && url != "" {
			out = append(out, Service{Name: strings.TrimSpace(name), URL: strings.TrimSpace(url)})
		}
	}
	return out
}

// probe asks every part at once; an answer is kept for a few seconds so
// that a page that refreshes often does not multiply the questions.
func (v *systemView) probe(ctx context.Context, s *Server) []serviceState {
	v.mu.Lock()
	defer v.mu.Unlock()
	if time.Since(v.checked) < 4*time.Second {
		return v.states
	}
	type check struct {
		name string
		ask  func(context.Context) error
	}
	checks := []check{{"PostgreSQL", func(ctx context.Context) error { return s.db.Ping(ctx) }}}
	if s.engine != "" {
		checks = append(checks, check{"route-engine", func(ctx context.Context) error { _, err := s.engineDo(ctx, "GET", "/v1/stats", nil); return err }})
	}
	if s.analytics != nil {
		checks = append(checks, check{"ClickHouse", func(ctx context.Context) error { _, err := s.analytics.Query(ctx, "select 1", nil); return err }})
	}
	for _, svc := range v.services {
		checks = append(checks, check{svc.Name, func(ctx context.Context) error {
			req, err := http.NewRequestWithContext(ctx, "GET", svc.URL, nil)
			if err != nil {
				return err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			resp.Body.Close()
			if resp.StatusCode >= 500 {
				return Errf(resp.StatusCode, "HTTP %d", resp.StatusCode)
			}
			return nil
		}})
	}
	states := make([]serviceState, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			start := time.Now()
			err := c.ask(ctx)
			states[i] = serviceState{Name: c.name, Up: err == nil, Ms: time.Since(start).Milliseconds()}
			if err != nil {
				// Addresses and credentials stay out of what the panel shows.
				states[i].Note = "cevap vermiyor"
				if ctx.Err() != nil {
					states[i].Note = "zaman aşımı"
				}
			}
		}()
	}
	wg.Wait()
	v.states, v.checked = states, time.Now()
	return states
}

func (s *Server) systemState(w http.ResponseWriter, r *http.Request, c Caller) error {
	ctx := r.Context()
	root, err := s.RootCaller(ctx)
	if err != nil {
		return err
	}
	if c.ResellerID != root.ResellerID {
		return Errf(403, "only the account that runs the system may see it")
	}
	if s.system == nil {
		return Errf(404, "the system page is not set up")
	}
	// The probes get a context of their own: a caller that gives up early
	// must not leave a half-asked answer in the cache.
	WriteJSON(w, 200, systemState{Machine: s.system.sys.State(), Services: s.system.probe(context.WithoutCancel(ctx), s)})
	return nil
}
