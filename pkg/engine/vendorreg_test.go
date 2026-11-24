// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"softswitch/core/pkg/routing"
	"softswitch/core/pkg/sipctl"
)

// vendorRegSnapshot has four connections: two that register (one at an IPv6
// address, one under a domain of its own), one turned off and one without
// a sign-in name.
func vendorRegSnapshot() *routing.Snapshot {
	snap := testSnapshot()
	set := func(id int64, f func(*routing.Connection)) {
		c := snap.Connections[id]
		c.Enabled, c.Transport, c.Port = true, "udp", 5060
		f(&c)
		snap.Connections[id] = c
	}
	set(1, func(c *routing.Connection) {
		c.Host, c.AuthUser, c.AuthPassword, c.Register, c.RegisterExpires = "198.51.100.7", "acme", "s3cret:pass", true, 3600
	})
	set(2, func(c *routing.Connection) {
		c.Host, c.Transport, c.Port, c.AuthUser, c.AuthPassword = "2001:db8::5", "tls", 5061, "acme2", "x"
		c.Register, c.RegisterExpires, c.RegisterDomain = true, 600, "voip.example"
	})
	set(3, func(c *routing.Connection) {
		c.Host, c.AuthUser, c.Register, c.Enabled = "198.51.100.8", "off", true, false
	})
	return snap
}

func TestVendorRegistrationsList(t *testing.T) {
	snap := vendorRegSnapshot()
	c := snap.Connections[3]
	c.Enabled, c.AuthUser = true, "" // no name: nothing to register as
	snap.Connections[3] = c
	got := vendorRegistrations(snap)
	if len(got) != 2 {
		t.Fatalf("registrations: %+v", got)
	}
	want := []VendorRegistration{
		{ID: 1, Registrar: "sip:198.51.100.7:5060;transport=udp", AOR: "sip:acme@198.51.100.7", Transport: "udp", Username: "acme", Password: "s3cret:pass", Expires: 3600},
		{ID: 2, Registrar: "sip:[2001:db8::5]:5061;transport=tls", AOR: "sip:acme2@voip.example", Transport: "tls", Username: "acme2", Password: "x", Expires: 600},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("registration %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	// A second connection that would register the same name with the same
	// registrar is the same registration.
	dup := snap.Connections[1]
	dup.ID = 9
	snap.Connections[9] = dup
	if got := vendorRegistrations(snap); len(got) != 2 || got[0].ID != 1 {
		t.Errorf("a duplicate is one registration, the older connection's: %+v", got)
	}
}

func TestVendorRegistrationsHandler(t *testing.T) {
	e := New(nil, nil, &memSink{})
	e.snap.Store(vendorRegSnapshot())
	ask := func(query string) map[string]json.RawMessage {
		rec := httptest.NewRecorder()
		e.handleVendorRegistrations(rec, httptest.NewRequest("GET", "/v1/vendor-registrations"+query, nil))
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("answer %d %v", rec.Code, rec.Header())
		}
		var out map[string]json.RawMessage
		json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	first := ask("")
	var version string
	json.Unmarshal(first["version"], &version)
	if len(version) != 32 || !strings.Contains(string(first["registrations"]), `"password":"s3cret:pass"`) {
		t.Fatalf("first answer: %s", first)
	}
	// The version is not a plain hash of the list.
	if strings.Contains(version, "s3cret") {
		t.Fatal("version shows the password")
	}
	// Asked with the version it holds, the SIP core is told nothing new and
	// is sent no password.
	same := ask("?version=" + version)
	if string(same["unchanged"]) != "true" || same["registrations"] != nil {
		t.Errorf("unchanged list: %s", same)
	}
	snap := vendorRegSnapshot()
	c := snap.Connections[1]
	c.AuthPassword = "changed"
	snap.Connections[1] = c
	e.snap.Store(snap)
	changed := ask("?version=" + version)
	if changed["unchanged"] != nil || !strings.Contains(string(changed["registrations"]), `"password":"changed"`) {
		t.Errorf("after a change of password: %s", changed)
	}
	// Nothing to register is an empty list, not null.
	e.snap.Store(testSnapshot())
	if got := string(ask("")["registrations"]); got != "[]" {
		t.Errorf("no registrations: %s", got)
	}
}

func TestVendorRegVerdicts(t *testing.T) {
	e := New(nil, nil, &memSink{})
	snap := vendorRegSnapshot()
	aor1, reg1 := regTarget(snap.Connections[1])
	aor2, reg2 := regTarget(snap.Connections[2])
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	round := func(at time.Duration, err error, reported ...sipctl.Registration) *vendorRegState {
		st := e.vendorRegVerdicts(e.vendorRegs.Load(), snap, reported, err, t0.Add(at))
		e.vendorRegs.Store(st)
		return st
	}
	// Not yet in the SIP core's table, then on its way: pending. The
	// connection turned off has no verdict.
	st := round(0, nil, sipctl.Registration{AOR: aor1, Registrar: reg1, State: sipctl.RegPending})
	if len(st.regs) != 2 || st.regs[1].State != VendorRegPending || st.regs[2].State != VendorRegPending || st.regs[2].Reason != "pending" {
		t.Fatalf("first round: %+v", st.regs)
	}
	st = round(time.Second, nil,
		sipctl.Registration{AOR: aor1, Registrar: reg1, State: sipctl.RegRegistered, Expires: 120},
		sipctl.Registration{AOR: aor2, Registrar: reg2, State: sipctl.RegRejected})
	if r := st.regs[1]; r.State != VendorRegRegistered || r.Expires != 120 || !r.Since.Equal(t0.Add(time.Second)) {
		t.Errorf("registered: %+v", r)
	}
	if r := st.regs[2]; r.State != VendorRegFailing || r.Reason != sipctl.RegRejected {
		t.Errorf("wrong password: %+v", r)
	}
	if st.count(VendorRegFailing) != 1 || st.count(VendorRegRegistered) != 1 {
		t.Errorf("counts: %+v", st.regs)
	}
	// A refresh, and a new try after the failure, keep the verdicts and
	// the times they began.
	st = round(2*time.Second, nil,
		sipctl.Registration{AOR: aor1, Registrar: reg1, State: sipctl.RegPending},
		sipctl.Registration{AOR: aor2, Registrar: reg2, State: sipctl.RegPending})
	if r := st.regs[1]; r.State != VendorRegRegistered || r.Expires != 120 || !r.Since.Equal(t0.Add(time.Second)) {
		t.Errorf("during a refresh: %+v", r)
	}
	if r := st.regs[2]; r.State != VendorRegFailing || r.Reason != sipctl.RegRejected {
		t.Errorf("during a retry: %+v", r)
	}
	// The SIP core cannot be asked: nothing is known.
	st = round(3*time.Second, errors.New("no answer"))
	if r := st.regs[1]; r.State != VendorRegUnknown || r.Reason != "sip_core" {
		t.Errorf("SIP core not asked: %+v", r)
	}
}

func TestRunVendorRegsAsksOnlyWhenThereIsSomething(t *testing.T) {
	e := New(nil, nil, &memSink{})
	e.snap.Store(testSnapshot())
	asked := 0
	cfg := &VendorRegConfig{List: func(context.Context) ([]sipctl.Registration, error) { asked++; return nil, nil }}
	e.vendorRegRound(context.Background(), cfg, e.snap.Load())
	if asked != 0 || len(e.vendorRegs.Load().regs) != 0 {
		t.Errorf("nothing to register, yet the SIP core was asked %d times", asked)
	}
	e.vendorRegRound(context.Background(), cfg, vendorRegSnapshot())
	if asked != 1 || e.vendorRegs.Load().regs[1].State != VendorRegPending {
		t.Errorf("asked %d times: %+v", asked, e.vendorRegs.Load().regs)
	}
}
