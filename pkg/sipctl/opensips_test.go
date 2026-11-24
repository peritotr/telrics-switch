// SPDX-License-Identifier: AGPL-3.0-only

package sipctl

import (
	"context"
	"encoding/json"
	"net"
	"testing"
)

// An answer to reg_list as uac_registrant (OpenSIPS 3.6) gives it.
const regListAnswer = `{"jsonrpc":"2.0","result":{"Records":[
 {"AOR":"sip:acme@198.51.100.7","expires":120,"state":"REGISTERED_STATE","enabled":"yes","last_register_sent":"Mon Nov 23 10:00:00 2026","registration_t_out":"Mon Nov 23 10:01:50 2026","registrar":"sip:198.51.100.7:5060;transport=udp","binding":"sip:acme@192.0.2.1:5060"},
 {"AOR":"sip:other@198.51.100.8","expires":3600,"state":"WRONG_CREDENTIALS_STATE","enabled":"yes","registrar":"sip:198.51.100.8:5060;transport=udp","binding":"sip:other@192.0.2.1:5060"},
 {"AOR":"sip:x@198.51.100.9","expires":3600,"state":"SOMETHING_NEW","registrar":"sip:198.51.100.9:5060;transport=udp"}]},"id":1}`

func TestOpenSIPSRegistrations(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var asked map[string]any
	go func() {
		buf := make([]byte, 4096)
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		json.Unmarshal(buf[:n], &asked)
		conn.WriteTo([]byte(regListAnswer), from)
	}()
	got, err := OpenSIPSRegistrations(conn.LocalAddr().String())(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if asked["method"] != "reg_list" {
		t.Errorf("asked %v", asked)
	}
	want := []Registration{
		{AOR: "sip:acme@198.51.100.7", Registrar: "sip:198.51.100.7:5060;transport=udp", State: RegRegistered, Expires: 120},
		{AOR: "sip:other@198.51.100.8", Registrar: "sip:198.51.100.8:5060;transport=udp", State: RegRejected, Expires: 3600},
		{AOR: "sip:x@198.51.100.9", Registrar: "sip:198.51.100.9:5060;transport=udp", State: RegError, Expires: 3600},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("record %d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseRegListEmpty(t *testing.T) {
	for _, raw := range []string{``, `null`, `{"Records":[]}`, `{}`} {
		if got, err := parseRegList(json.RawMessage(raw)); err != nil || len(got) != 0 {
			t.Errorf("%q: %v %v", raw, got, err)
		}
	}
	if _, err := parseRegList(json.RawMessage(`[1`)); err == nil {
		t.Error("an unreadable list is no error")
	}
}

// Lifting a ban on an IPv6 address removes the script's key under the
// form OpenSIPS writes the address in ($si) as well as the usual one (T-28).
func TestOpenSIPSUnbanIPv6(t *testing.T) {
	for in, want := range map[string][]string{
		"fd30::21":         {"fd30::21", "FD30:0:0:0:0:0:0:21"},
		"2001:db8:0:1::a0": {"2001:db8:0:1::a0", "2001:DB8:0:1:0:0:0:A0"},
		"203.0.113.7":      {"203.0.113.7"},
		"::ffff:10.0.0.1":  {"::ffff:10.0.0.1"},
	} {
		got := banForms(in)
		if len(got) != len(want) || got[0] != want[0] || (len(got) > 1 && got[1] != want[1]) {
			t.Errorf("banForms(%q) = %v, want %v", in, got, want)
		}
	}

	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	asked := make(chan map[string]any, 3)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			var req map[string]any
			json.Unmarshal(buf[:n], &req)
			asked <- req
			conn.WriteTo([]byte(`{"jsonrpc":"2.0","result":"OK","id":1}`), from)
		}
	}()
	if err := OpenSIPSUnban(conn.LocalAddr().String())(context.Background(), "fd30::21"); err != nil {
		t.Fatal(err)
	}
	var keys []any
	for i := 0; i < 3; i++ {
		req := <-asked
		if req["method"] == "cache_remove" {
			keys = append(keys, req["params"].(map[string]any)["attr"])
		}
	}
	if len(keys) != 2 || keys[0] != "ban:fd30::21" || keys[1] != "ban:FD30:0:0:0:0:0:0:21" {
		t.Errorf("removed %v", keys)
	}
}
