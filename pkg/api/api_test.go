// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"softswitch/core/pkg/clickhouse"
	"softswitch/core/pkg/mail"
	"softswitch/core/pkg/rating"
	"softswitch/core/pkg/store"
	"softswitch/core/pkg/sysinfo"
)

type client struct {
	t   *testing.T
	url string
}

// do sends a request and checks the status. A string body is sent as CSV.
func (c client) do(key, method, path string, body any, want int) map[string]any {
	c.t.Helper()
	out := c.raw(key, method, path, body, want)
	m, _ := out.(map[string]any)
	return m
}

func (c client) raw(key, method, path string, body any, want int) any {
	c.t.Helper()
	var rd io.Reader
	if s, ok := body.(string); ok {
		rd = strings.NewReader(s)
	} else if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.url+path, rd)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		c.t.Fatalf("%s %s: got %d want %d: %s", method, path, resp.StatusCode, want, data)
	}
	var out any
	json.Unmarshal(data, &out)
	return out
}

// outbox is a mail server that fails a set number of times, then keeps
// what it is given.
type outbox struct {
	failures int
	sent     []mail.Message
}

func (o *outbox) Send(_ context.Context, m mail.Message) error {
	if o.failures > 0 {
		o.failures--
		return errors.New("mail server is down")
	}
	o.sent = append(o.sent, m)
	return nil
}

func num(m map[string]any, k string) int64 { f, _ := m[k].(float64); return int64(f) }

func (c client) balance(key, res string, id int64) int64 {
	c.t.Helper()
	return num(c.do(key, "GET", fmt.Sprintf("/v1/%s/%d", res, id), nil, 200), "balance")
}

func same(t *testing.T, what string, got any, want string) {
	t.Helper()
	if fmt.Sprint(got) != want {
		t.Errorf("%s: got %v want %s", what, got, want)
	}
}

func eq(t *testing.T, what string, got, want int64) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %d want %d", what, got, want)
	}
}

func TestEndToEnd(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	// The test wipes the schema, so refuse anything that is not a test database.
	if !strings.HasSuffix(strings.SplitN(url, "?", 2)[0], "_test") {
		t.Fatalf("TEST_DATABASE_URL must point at a database whose name ends in _test")
	}
	ctx := context.Background()
	pool, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `drop schema public cascade`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `create schema public`); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	const root = "root-test-key"
	if err := Bootstrap(ctx, pool, root); err != nil {
		t.Fatal(err)
	}
	srv := New(pool)
	ts := httptest.NewServer(conform(t, srv))
	defer ts.Close()
	c := client{t, ts.URL}

	// --- access control ---
	c.do("", "GET", "/healthz", nil, 200)
	c.do("", "GET", "/v1/clients", nil, 401)
	c.do("wrong", "GET", "/v1/clients", nil, 401)
	me := num(c.do(root, "GET", "/v1/me", nil, 200), "id")

	// --- a write tells the listening services, a read or a refusal does not ---
	changes := store.ListenConfig(ctx, pool)
	changed := func(want bool, what string) {
		t.Helper()
		select {
		case <-changes:
			if !want {
				t.Errorf("%s: unexpected change notification", what)
			}
		case <-time.After(300 * time.Millisecond):
			if want {
				t.Errorf("%s: no change notification", what)
			}
		}
	}
	changed(true, "subscribing")
	ro := c.do(root, "POST", "/v1/api-keys", map[string]any{"name": "readonly", "role": "readonly"}, 201)["key"].(string)
	changed(true, "a write")
	c.do(ro, "POST", "/v1/clients", map[string]any{"name": "x"}, 403)
	c.do(ro, "GET", "/v1/clients", nil, 200)
	changed(false, "a refused write and a read")

	// --- vendors, buy rates and routing ---
	buy1 := num(c.do(root, "POST", "/v1/tariffs", map[string]any{"name": "V1 buy", "kind": "buy"}, 201), "id")
	buy2 := num(c.do(root, "POST", "/v1/tariffs", map[string]any{"name": "V2 buy", "kind": "buy"}, 201), "id")
	eq(t, "rates imported", num(c.do(root, "POST", fmt.Sprintf("/v1/tariffs/%d/rates/import?effective_from=2020-01-01T00:00:00Z", buy1), "prefix,price_per_min\n90,0.01\n9053,0.012\n", 201), "rates"), 2)
	c.do(root, "POST", fmt.Sprintf("/v1/tariffs/%d/rates/import?effective_from=2020-01-01T00:00:00Z", buy2), "prefix,price_per_min\n90,0.008\n", 201)
	c.do(root, "POST", fmt.Sprintf("/v1/tariffs/%d/rates/import", buy2), "prefix,price_per_min\n90,abc\n", 422)
	v1 := num(c.do(root, "POST", "/v1/vendors", map[string]any{"name": "Vendor One", "tariff_id": buy1}, 201), "id")
	v2 := num(c.do(root, "POST", "/v1/vendors", map[string]any{"name": "Vendor Two", "tariff_id": buy2}, 201), "id")
	c.do(root, "POST", "/v1/vendors", map[string]any{"name": "Vendor Two"}, 409)
	c1 := num(c.do(root, "POST", "/v1/vendor-connections", map[string]any{"vendor_id": v1, "name": "v1-a", "host": "10.1.1.1"}, 201), "id")
	c2 := num(c.do(root, "POST", "/v1/vendor-connections", map[string]any{"vendor_id": v2, "name": "v2-a", "host": "10.1.1.2", "tech_prefix": "99"}, 201), "id")
	plan := num(c.do(root, "POST", "/v1/routing-plans", map[string]any{"name": "default"}, 201), "id")
	for _, conn := range []int64{c1, c2} {
		c.do(root, "POST", "/v1/routing-entries", map[string]any{"routing_plan_id": plan, "prefix": "90", "connection_id": conn}, 201)
	}

	// --- client account ---
	retail := num(c.do(root, "POST", "/v1/tariffs", map[string]any{"name": "retail", "kind": "sell"}, 201), "id")
	c.do(root, "POST", fmt.Sprintf("/v1/tariffs/%d/rates/import?effective_from=2020-01-01T00:00:00Z", retail), "prefix,price_per_min,initial_sec,increment_sec\n90,0.02,60,60\n9050,0.5,60,60\n", 201)
	cl := c.do(root, "POST", "/v1/clients", map[string]any{"name": "Acme Telecom", "tariff_id": retail, "routing_plan_id": plan, "tax_id": "1234567890"}, 201)
	clID := num(cl, "id")
	eq(t, "client owner defaults to caller", num(cl, "reseller_id"), me)
	trunk := c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": clID, "name": "main", "addresses": []string{"10.0.0.5"}, "password_ha1": "secret"}, 201)
	if _, leaked := trunk["password_ha1"]; leaked {
		t.Error("trunk secret returned by the API")
	}
	c.do(root, "POST", "/v1/payments", map[string]any{"party_type": "client", "party_id": clID, "amount": 10_000_000, "reference": "wire-1"}, 201)
	eq(t, "balance after top-up", c.balance(root, "clients", clID), 10_000_000)
	c.do(root, "PATCH", fmt.Sprintf("/v1/clients/%d", clID), map[string]any{"balance": 999}, 400)
	c.do(root, "PATCH", fmt.Sprintf("/v1/clients/%d", clID), map[string]any{"billing_mode": "barter"}, 422)
	c.do(root, "PATCH", fmt.Sprintf("/v1/clients/%d", clID), map[string]any{"reseller_id": 999}, 409)

	// --- Enterprise features are absent from the core build ---
	c.do(root, "POST", "/v1/resellers", map[string]any{"name": "sub"}, 405)
	c.do(root, "GET", "/v1/invoices", nil, 404)
	c.do(root, "GET", "/v1/dids", nil, 404)
	c.do(root, "POST", "/v1/payments", map[string]any{"party_type": "reseller", "party_id": me, "amount": 5}, 400)
	c.do(root, "PATCH", fmt.Sprintf("/v1/clients/%d", clID), map[string]any{"tax_rate_bp": 2000}, 400)

	// --- routing: cheapest vendor first ---
	d := c.do(root, "POST", "/v1/route/simulate", map[string]any{"src_ip": "10.0.0.5", "dst": "905321112233"}, 200)
	routes, _ := d["routes"].([]any)
	if d["allowed"] != true || len(routes) != 2 {
		t.Fatalf("route decision: %v", d)
	}
	first := routes[0].(map[string]any)
	eq(t, "cheapest connection", num(first, "connection_id"), c2)
	if first["uri"] != "sip:99905321112233@10.1.1.2:5060;transport=udp" {
		t.Errorf("uri: %v", first["uri"])
	}
	eq(t, "max duration", num(d, "max_duration_sec"), 7200)
	// --- rules about the caller's number ---
	sim := func(src string) map[string]any {
		return c.do(root, "POST", "/v1/route/simulate", map[string]any{"src_ip": "10.0.0.5", "dst": "905321112233", "src": src}, 200)
	}
	all := num(c.do(root, "POST", "/v1/cli-rules", map[string]any{"prefix": "90212666", "action": "block"}, 201), "id")
	c.do(root, "POST", "/v1/cli-rules", map[string]any{"prefix": "90212666", "action": "allow"}, 409)
	c.do(root, "POST", "/v1/cli-rules", map[string]any{"prefix": "9x", "action": "block"}, 422)
	c.do(root, "POST", "/v1/cli-rules", map[string]any{"prefix": "91", "action": "drop"}, 422)
	c.do(root, "POST", "/v1/cli-rules", map[string]any{"prefix": "91", "action": "block", "add_prefix": "0"}, 422)
	c.do(root, "POST", "/v1/cli-rules", map[string]any{"client_id": 999999, "prefix": "91", "action": "block"}, 409)
	own := num(c.do(root, "POST", "/v1/cli-rules", map[string]any{"client_id": clID, "prefix": "90850", "action": "rewrite", "strip_digits": 2, "add_prefix": "0"}, 201), "id")
	eq(t, "refused caller", num(sim("902126665000"), "sip_code"), 403)
	if d := sim("+908505550000"); d["allowed"] != true || d["caller"] != "08505550000" {
		t.Errorf("rewritten caller: %v", d)
	}
	if d := sim("905325550000"); d["allowed"] != true || d["caller"] != nil {
		t.Errorf("caller no rule matches: %v", d)
	}
	c.do(root, "PATCH", fmt.Sprintf("/v1/cli-rules/%d", all), map[string]any{"enabled": false}, 200)
	eq(t, "rule switched off", num(sim("902126665000"), "sip_code"), 200)
	c.do(root, "DELETE", fmt.Sprintf("/v1/cli-rules/%d", all), nil, 204)
	c.do(root, "DELETE", fmt.Sprintf("/v1/cli-rules/%d", own), nil, 204)

	// Rules on the asserted number (P-Asserted-Identity), and the refusal
	// of callers without a number. A rule looks at From unless told.
	simA := func(src, asserted string) map[string]any {
		return c.do(root, "POST", "/v1/route/simulate", map[string]any{"src_ip": "10.0.0.5", "dst": "905321112233", "src": src, "asserted": asserted}, 200)
	}
	fromRule := c.do(root, "POST", "/v1/cli-rules", map[string]any{"client_id": clID, "prefix": "90216", "action": "block"}, 201)
	same(t, "a rule looks at From unless told", fromRule["number_source"], "from")
	pai := num(c.do(root, "POST", "/v1/cli-rules", map[string]any{"client_id": clID, "number_source": "pai", "prefix": "90216", "action": "block"}, 201), "id")
	anon := num(c.do(root, "POST", "/v1/cli-rules", map[string]any{"client_id": clID, "number_source": "pai", "action": "anonymous"}, 201), "id")
	// Beside a rule with an empty prefix on the same number, but once only.
	empty := num(c.do(root, "POST", "/v1/cli-rules", map[string]any{"client_id": clID, "number_source": "pai", "action": "allow"}, 201), "id")
	c.do(root, "POST", "/v1/cli-rules", map[string]any{"client_id": clID, "number_source": "pai", "action": "anonymous"}, 409)
	c.do(root, "POST", "/v1/cli-rules", map[string]any{"client_id": clID, "number_source": "to", "prefix": "91", "action": "block"}, 422)
	c.do(root, "POST", "/v1/cli-rules", map[string]any{"client_id": clID, "number_source": "pai", "prefix": "91", "action": "rewrite", "add_prefix": "0"}, 422)
	c.do(root, "POST", "/v1/cli-rules", map[string]any{"client_id": clID, "prefix": "91", "action": "anonymous"}, 422)
	eq(t, "refused by the rule on the asserted number", num(simA("905325550000", "902165550000"), "sip_code"), 403)
	eq(t, "refused by the rule on From", num(simA("902165550000", "905325550000"), "sip_code"), 403)
	eq(t, "neither number refused", num(simA("905325550000", "905325550000"), "sip_code"), 200)
	if d := simA("905325550000", ""); d["sip_code"] != float64(433) || d["reason"] != "anonymity disallowed" {
		t.Errorf("no asserted number: %v", d)
	}
	c.do(root, "DELETE", fmt.Sprintf("/v1/cli-rules/%d", num(fromRule, "id")), nil, 204)
	for _, id := range []int64{pai, anon, empty} {
		c.do(root, "DELETE", fmt.Sprintf("/v1/cli-rules/%d", id), nil, 204)
	}

	eq(t, "blocked source", num(c.do(root, "POST", "/v1/route/simulate", map[string]any{"src_ip": "10.9.9.9", "dst": "90532"}, 200), "sip_code"), 403)
	eq(t, "no sell rate", num(c.do(root, "POST", "/v1/route/simulate", map[string]any{"src_ip": "10.0.0.5", "dst": "4420"}, 200), "sip_code"), 403)

	// --- a trunk's addresses and networks ---
	same(t, "trunk addresses as stored", trunk["addresses"], "[10.0.0.5]")
	c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": clID, "name": "old", "ip": "10.20.0.1"}, 400)
	c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": clID, "name": "none"}, 422)
	c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": clID, "name": "none", "addresses": []string{}}, 422)
	for _, bad := range []any{[]string{"10.20.0.1", "not an address"}, []string{"10.20.0.0/33"}, []string{"10.20.0.300"}, []any{"10.20.0.1", nil},
		[]string{"0.0.0.0/0"}, []string{"10.0.0.0/7"}, []string{"2001::/15"}, "10.20.0.1"} {
		c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": clID, "name": "bad", "addresses": bad}, 422)
	}
	netCl := num(c.do(root, "POST", "/v1/clients", map[string]any{"name": "Network Co", "tariff_id": retail, "routing_plan_id": plan}, 201), "id")
	c.do(root, "POST", "/v1/payments", map[string]any{"party_type": "client", "party_id": netCl, "amount": 10_000_000}, 201)
	// Stored tidy: sorted, nothing twice, a network without its host bits.
	wide := c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": netCl, "name": "wide",
		"addresses": []string{"10.20.1.77/24", "10.20.0.9", "2001:db8:20::/48", "10.20.0.9/32", "10.20.0.8"}}, 201)
	wideID := num(wide, "id")
	same(t, "addresses tidied", wide["addresses"], "[10.20.0.8 10.20.0.9 10.20.1.0/24 2001:db8:20::/48]")
	same(t, "addresses in the list", c.raw(root, "GET", fmt.Sprintf("/v1/client-trunks?id=%d", wideID), nil, 200).([]any)[0].(map[string]any)["addresses"],
		"[10.20.0.8 10.20.0.9 10.20.1.0/24 2001:db8:20::/48]")
	// No two enabled trunks answer to the same address or network with the
	// same tech prefix, whichever spelling; another prefix is another trunk.
	c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": clID, "name": "dup", "addresses": []string{"10.20.9.9", "10.20.0.9"}}, 409)
	c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": clID, "name": "dup", "addresses": []string{"10.20.1.5/24"}}, 409)
	c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": clID, "name": "dup", "addresses": []string{"2001:db8:20:0::/48"}}, 409)
	c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": clID, "name": "dup", "addresses": []string{"10.0.0.5"}}, 409)
	narrow := num(c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": clID, "name": "narrow", "addresses": []string{"10.20.1.16/28"}}, 201), "id")
	spare := num(c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": clID, "name": "spare", "addresses": []string{"10.20.1.0/24"}, "enabled": false}, 201), "id")
	c.do(root, "PATCH", fmt.Sprintf("/v1/client-trunks/%d", spare), map[string]any{"enabled": true}, 409)
	c.do(root, "PATCH", fmt.Sprintf("/v1/client-trunks/%d", spare), map[string]any{"enabled": true, "tech_prefix": "88"}, 200)
	c.do(root, "PATCH", fmt.Sprintf("/v1/client-trunks/%d", spare), map[string]any{"tech_prefix": ""}, 409)
	c.do(root, "PATCH", fmt.Sprintf("/v1/client-trunks/%d", narrow), map[string]any{"addresses": []string{"10.20.0.8"}}, 409)
	c.do(root, "PATCH", fmt.Sprintf("/v1/client-trunks/%d", narrow), map[string]any{"addresses": []string{}}, 422)
	from := func(src, dst string) map[string]any {
		return c.do(root, "POST", "/v1/route/simulate", map[string]any{"src_ip": src, "dst": dst}, 200)
	}
	for src, want := range map[string]int64{"10.20.0.8": netCl, "10.20.0.9": netCl, "10.20.1.200": netCl, "2001:db8:20::7": netCl,
		"10.20.1.17": clID, "10.0.0.5": clID} {
		if d := from(src, "905321112233"); d["allowed"] != true || num(d, "client_id") != want {
			t.Errorf("call from %s: %v, want client %v", src, d, want)
		}
	}
	for _, src := range []string{"10.20.0.7", "10.20.2.0", "2001:db8:21::7"} {
		if d := from(src, "905321112233"); d["reason"] != "unknown source" {
			t.Errorf("call from %s: %v", src, d)
		}
	}
	eq(t, "tech prefix within a network", num(from("10.20.1.200", "88905321112233"), "trunk_id"), spare)
	// A trunk switched off or deleted gives its addresses up.
	c.do(root, "PATCH", fmt.Sprintf("/v1/client-trunks/%d", wideID), map[string]any{"enabled": false}, 200)
	same(t, "switched off", from("10.20.0.8", "905321112233")["reason"], "unknown source")
	taker := num(c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": clID, "name": "taker", "addresses": []string{"10.20.0.8"}}, 201), "id")
	c.do(root, "PATCH", fmt.Sprintf("/v1/client-trunks/%d", wideID), map[string]any{"enabled": true}, 409)
	c.do(root, "DELETE", fmt.Sprintf("/v1/client-trunks/%d", taker), nil, 204)
	c.do(root, "PATCH", fmt.Sprintf("/v1/client-trunks/%d", wideID), map[string]any{"enabled": true}, 200)
	// A trunk that signs in needs no address and claims none.
	signs := c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": netCl, "name": "signs", "auth_type": "digest", "username": "netco", "addresses": []string{"10.20.0.8"}}, 201)
	eq(t, "a signing trunk's address is not a source", num(from("10.20.0.8", "905321112233"), "trunk_id"), wideID)
	c.do(root, "PATCH", fmt.Sprintf("/v1/client-trunks/%d", num(signs, "id")), map[string]any{"auth_type": "ip"}, 409)
	c.do(root, "DELETE", fmt.Sprintf("/v1/client-trunks/%d", num(signs, "id")), nil, 204)

	// --- rates by day and hour: the simulator (the engine's code) and the
	// rater pick the same rate for the same moment ---
	c.do(root, "POST", "/v1/tariffs", map[string]any{"name": "nowhere", "kind": "sell", "timezone": "Mars/Olympus"}, 422)
	timed := num(c.do(root, "POST", "/v1/tariffs", map[string]any{"name": "timed", "kind": "sell", "timezone": "Europe/Istanbul"}, 201), "id")
	rate := func(prefix string, price int64, more map[string]any, want int) {
		body := map[string]any{"tariff_id": timed, "prefix": prefix, "price_per_min": price, "initial_sec": 60, "increment_sec": 60, "effective_from": "2020-01-01T00:00:00Z"}
		for k, v := range more {
			body[k] = v
		}
		c.do(root, "POST", "/v1/rates", body, want)
	}
	rate("90", 60_000, nil, 201)
	rate("90", 30_000, map[string]any{"time_from": 22 * 60, "time_to": 6 * 60}, 201) // nights
	rate("90", 30_000, map[string]any{"time_from": 22 * 60, "time_to": 6 * 60}, 409) // the same rate twice
	rate("9053", 20_000, map[string]any{"days": 96}, 201)                            // weekends
	rate("90", 1, map[string]any{"days": 0}, 422)
	rate("90", 1, map[string]any{"time_to": 1440}, 422)
	owl := num(c.do(root, "POST", "/v1/clients", map[string]any{"name": "Night Owl", "tariff_id": timed, "routing_plan_id": plan}, 201), "id")
	c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": owl, "name": "main", "addresses": []string{"10.0.0.77"}}, 201)
	c.do(root, "POST", "/v1/payments", map[string]any{"party_type": "client", "party_id": owl, "amount": 5_000_000}, 201)
	var owed int64
	for i, m := range []struct {
		at     string // UTC; Istanbul is three hours ahead
		prefix string
		price  int64
	}{
		{"2026-06-22T09:00:00Z", "90", 60_000},   // Monday noon
		{"2026-06-22T20:30:00Z", "90", 30_000},   // Monday 23:30
		{"2026-06-21T09:00:00Z", "9053", 20_000}, // Sunday noon
		{"2026-06-21T21:30:00Z", "90", 30_000},   // Monday 00:30 there: the weekend is over, the night is not
	} {
		d := c.do(root, "POST", "/v1/route/simulate", map[string]any{"src_ip": "10.0.0.77", "dst": "905321112233", "at": m.at}, 200)
		if d["allowed"] != true || d["sell_prefix"] != m.prefix || num(d, "sell_price_per_min") != m.price {
			t.Errorf("simulated at %s: %v", m.at, d)
		}
		at, _ := time.Parse(time.RFC3339, m.at)
		id := fmt.Sprintf("timed-%d", i)
		c.do(root, "POST", "/v1/cdrs", []map[string]any{{"call_id": id, "client_id": owl, "src": "1", "dst": "905321112233",
			"start_at": at, "answer_at": at, "end_at": at.Add(30 * time.Second)}}, 201)
		rec := c.raw(root, "GET", "/v1/cdrs?call_id="+id, nil, 200).([]any)[0].(map[string]any)
		if rec["sell_prefix"] != m.prefix || num(rec, "sell_amount") != m.price {
			t.Errorf("rated at %s: prefix %v, amount %v, want %s %d", m.at, rec["sell_prefix"], rec["sell_amount"], m.prefix, m.price)
		}
		owed += m.price
	}
	eq(t, "balance after calls at four rates", c.balance(root, "clients", owl), 5_000_000-owed)

	// --- public holidays (T-19): a holiday counts as a Sunday, or as the
	// day chosen; the date is read in the tariff's time zone, and the
	// simulator and the rater still pick the same rate. July 2026 is far
	// from the moments the other tests use. ---
	c.do(ro, "POST", "/v1/holidays", map[string]any{"day": "2026-07-15"}, 403)
	c.do(root, "POST", "/v1/holidays", map[string]any{"day": "2026-07-15", "counts_as": 0}, 422)
	c.do(root, "POST", "/v1/holidays", map[string]any{"day": "2026-07-15", "counts_as": 8}, 422)
	c.do(root, "POST", "/v1/holidays", map[string]any{"day": "2026-02-30"}, 422)
	c.do(root, "POST", "/v1/holidays", map[string]any{"day": "15.07.2026"}, 422)
	hol := c.do(root, "POST", "/v1/holidays", map[string]any{"day": "2026-07-15", "name": "Demokrasi ve Millî Birlik Günü"}, 201)
	eq(t, "a holiday counts as a Sunday by default", num(hol, "counts_as"), 7)
	c.do(root, "POST", "/v1/holidays", map[string]any{"day": "2026-07-15", "counts_as": 6}, 409)
	sat := num(c.do(root, "POST", "/v1/holidays", map[string]any{"day": "2026-07-17", "counts_as": 1, "name": "bridge"}, 201), "id")
	c.do(root, "PATCH", fmt.Sprintf("/v1/holidays/%d", sat), map[string]any{"counts_as": 6}, 200)
	if n := len(c.raw(root, "GET", "/v1/holidays?from=2026-07-01T00:00:00Z&to=2026-08-01T00:00:00Z", nil, 200).([]any)); n != 2 {
		t.Errorf("holidays in July: %d", n)
	}
	if logged, _ := c.raw(root, "GET", fmt.Sprintf("/v1/audit-log?resource=holidays&row_id=%d", sat), nil, 200).([]any); len(logged) != 2 {
		t.Errorf("holiday audit: %v", logged)
	}
	for i, m := range []struct {
		at     string // UTC; Istanbul is three hours ahead
		prefix string
		price  int64
	}{
		{"2026-07-08T09:00:00Z", "90", 60_000},   // an ordinary Wednesday noon
		{"2026-07-15T09:00:00Z", "9053", 20_000}, // the holiday's noon: a Sunday
		{"2026-07-14T21:30:00Z", "9053", 20_000}, // 00:30 on the holiday there, still the 14th in UTC
		{"2026-07-15T21:30:00Z", "90", 30_000},   // 00:30 on the 16th: the night begun on the holiday
		{"2026-07-17T09:00:00Z", "9053", 20_000}, // a Friday counted as a Saturday
	} {
		d := c.do(root, "POST", "/v1/route/simulate", map[string]any{"src_ip": "10.0.0.77", "dst": "905321112233", "at": m.at}, 200)
		if d["allowed"] != true || d["sell_prefix"] != m.prefix || num(d, "sell_price_per_min") != m.price {
			t.Errorf("simulated on holiday test %s: %v", m.at, d)
		}
		at, _ := time.Parse(time.RFC3339, m.at)
		id := fmt.Sprintf("holiday-%d", i)
		c.do(root, "POST", "/v1/cdrs", []map[string]any{{"call_id": id, "client_id": owl, "src": "1", "dst": "905321112233",
			"start_at": at, "answer_at": at, "end_at": at.Add(30 * time.Second)}}, 201)
		rec := c.raw(root, "GET", "/v1/cdrs?call_id="+id, nil, 200).([]any)[0].(map[string]any)
		if rec["sell_prefix"] != m.prefix || num(rec, "sell_amount") != m.price {
			t.Errorf("rated on holiday test %s: prefix %v, amount %v, want %s %d", m.at, rec["sell_prefix"], rec["sell_amount"], m.prefix, m.price)
		}
		owed += m.price
	}
	eq(t, "balance after the holiday calls", c.balance(root, "clients", owl), 5_000_000-owed)
	// Without the holiday the same moment is an ordinary Wednesday again.
	c.do(root, "DELETE", fmt.Sprintf("/v1/holidays/%d", num(hol, "id")), nil, 204)
	eq(t, "the holiday removed", num(c.do(root, "POST", "/v1/route/simulate", map[string]any{"src_ip": "10.0.0.77", "dst": "905321112233", "at": "2026-07-15T09:00:00Z"}, 200), "sell_price_per_min"), 60_000)

	// --- a rate sheet with windows: days, time_from and time_to, checked
	// line by line; a sheet without them loads as before (above). ---
	sheetTariff := num(c.do(root, "POST", "/v1/tariffs", map[string]any{"name": "sheet windows", "kind": "sell", "timezone": "Europe/Istanbul"}, 201), "id")
	wImp := fmt.Sprintf("/v1/tariffs/%d/rates/import?effective_from=2020-01-01T00:00:00Z", sheetTariff)
	wSheet := "prefix,price,days,time_from,time_to\n" +
		"90,0.06,,,\n" + // every day, all day
		"90,0.03,,22:00,06:00\n" + // nights
		"90,0.02,96,,\n" + // weekends
		"90,0.01,96,22:00,24:00\n" + // weekend evenings, to midnight
		"90,0.05,,22:00,06:00\n" + // the night again
		"91,0.01,0,,\n" + "91,0.01,128,,\n" + "91,0.01,weekend,,\n" +
		"92,0.01,,8:00,\n" + "92,0.01,,,18:00\n" + "92,0.01,,25:00,06:00\n" + "92,0.01,,08:00,24:00x\n" + "92,0.01,,08:00,08:00\n" + "92,0.01,,24:00,06:00\n"
	wDry := c.do(root, "POST", wImp+"&dry_run=1", wSheet, 200)
	wCodes := ""
	for _, e := range wDry["errors"].([]any) {
		wCodes += e.(map[string]any)["code"].(string) + " "
	}
	if wCodes != "duplicate_prefix days_invalid days_invalid days_invalid time_pair time_pair time_invalid time_invalid time_pair time_invalid " {
		t.Errorf("window import errors: %s", wCodes)
	}
	eq(t, "window sheet valid", num(wDry, "valid"), 4)
	eq(t, "one prefix, four windows", num(wDry, "new_prefixes"), 1)
	eq(t, "window sheet loaded", num(c.do(root, "POST", wImp, wSheet, 201), "rates"), 4)
	var windows string
	if err := pool.QueryRow(ctx, `select string_agg(days || ' ' || time_from || '-' || time_to || ' ' || price_per_min, ', ' order by price_per_min)
		from rates where tariff_id = $1`, sheetTariff).Scan(&windows); err != nil {
		t.Fatal(err)
	}
	same(t, "windows written", windows, "96 1320-0 10000, 96 0-0 20000, 127 1320-360 30000, 127 0-0 60000")
	// Loading the sheet again replaces every window of the prefix.
	again := c.do(root, "POST", fmt.Sprintf("/v1/tariffs/%d/rates/import?effective_from=2021-01-01T00:00:00Z", sheetTariff), "prefix,price\n90,0.07\n", 201)
	eq(t, "a new sheet closes every window of the prefix", num(again, "closed"), 1)
	var open int64
	if err := pool.QueryRow(ctx, `select count(*) from rates where tariff_id = $1 and effective_to is null`, sheetTariff).Scan(&open); err != nil {
		t.Fatal(err)
	}
	eq(t, "rates left open", open, 1)

	// --- several currencies: a party and its tariff share a currency, and
	// reports add up in the account's ---
	c.do(root, "POST", "/v1/clients", map[string]any{"name": "No rate yet", "currency": "EUR"}, 422)
	c.do(root, "POST", "/v1/exchange-rates", map[string]any{"currency": "eur", "rate": 1_200_000}, 422)
	c.do(root, "POST", "/v1/exchange-rates", map[string]any{"currency": "EUR", "rate": 0}, 422)
	eur := num(c.do(root, "POST", "/v1/exchange-rates", map[string]any{"currency": "EUR", "rate": 1_200_000}, 201), "id")
	c.do(root, "POST", "/v1/exchange-rates", map[string]any{"currency": "EUR", "rate": 1_300_000}, 409)
	c.do(root, "POST", "/v1/clients", map[string]any{"name": "Wrong tariff", "currency": "EUR", "tariff_id": retail}, 422)
	euro := num(c.do(root, "POST", "/v1/tariffs", map[string]any{"name": "retail EUR", "kind": "sell", "currency": "EUR"}, 201), "id")
	c.do(root, "POST", "/v1/rates", map[string]any{"tariff_id": euro, "prefix": "90", "price_per_min": 30_000, "effective_from": "2020-01-01T00:00:00Z"}, 201)
	paris := num(c.do(root, "POST", "/v1/clients", map[string]any{"name": "Paris Telecom", "currency": "EUR", "tariff_id": euro, "routing_plan_id": plan}, 201), "id")
	c.do(root, "PATCH", fmt.Sprintf("/v1/clients/%d", paris), map[string]any{"tariff_id": retail}, 422)
	c.do(root, "PATCH", fmt.Sprintf("/v1/clients/%d", paris), map[string]any{"currency": "USD"}, 422)
	c.do(root, "PATCH", fmt.Sprintf("/v1/tariffs/%d", euro), map[string]any{"currency": "USD"}, 422)
	c.do(root, "DELETE", fmt.Sprintf("/v1/exchange-rates/%d", eur), nil, 409)
	c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": paris, "name": "main", "addresses": []string{"10.0.0.88"}}, 201)
	c.do(root, "POST", "/v1/payments", map[string]any{"party_type": "client", "party_id": paris, "amount": 1_000_000}, 201)
	// 0.03 EUR is 0.036 for the account: both vendors (0.008 and 0.01) are
	// below it, and the decision says so in the account's currency.
	if d := c.do(root, "POST", "/v1/route/simulate", map[string]any{"src_ip": "10.0.0.88", "dst": "905321112233"}, 200); d["allowed"] != true ||
		num(d, "sell_price_per_min") != 30_000 || num(d, "sell_price_base") != 36_000 {
		t.Errorf("client in another currency: %v", d)
	}
	fxAt, _ := time.Parse(time.RFC3339, "2026-06-01T10:00:00Z")
	c.do(root, "POST", "/v1/cdrs", []map[string]any{{"call_id": "fx-1", "client_id": paris, "src": "1", "dst": "905321112233",
		"start_at": fxAt, "answer_at": fxAt, "end_at": fxAt.Add(60 * time.Second)}}, 201)
	rec := c.raw(root, "GET", "/v1/cdrs?call_id=fx-1", nil, 200).([]any)[0].(map[string]any)
	if num(rec, "sell_amount") != 30_000 || num(rec, "sell_base") != 36_000 {
		t.Errorf("amounts of a call in another currency: %v / %v", rec["sell_amount"], rec["sell_base"])
	}
	eq(t, "charged in the client's own currency", c.balance(root, "clients", paris), 1_000_000-30_000)
	for _, row := range c.raw(root, "GET", "/v1/reports/summary?group_by=client&from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z", nil, 200).([]any) {
		if r := row.(map[string]any); r["key"] == fmt.Sprint(paris) && num(r, "revenue") != 36_000 {
			t.Errorf("report revenue in the account's currency: %v", r)
		}
	}
	// A new rate of exchange changes what later calls come to, not this one.
	c.do(root, "PATCH", fmt.Sprintf("/v1/exchange-rates/%d", eur), map[string]any{"rate": 1_100_000}, 200)
	eq(t, "decision after the rate changed", num(c.do(root, "POST", "/v1/route/simulate", map[string]any{"src_ip": "10.0.0.88", "dst": "905321112233"}, 200), "sell_price_base"), 33_000)
	// Rates that follow the published reference rates (T-20).
	testExchangeRateFeed(t, ctx, pool, srv, c, root, ro, eur, changed)
	eq(t, "decision after the feed's tests", num(c.do(root, "POST", "/v1/route/simulate", map[string]any{"src_ip": "10.0.0.88", "dst": "905321112233"}, 200), "sell_price_base"), 33_000)

	// --- bundles of minutes: the bundle pays first, the balance after it ---
	bt := num(c.do(root, "POST", "/v1/tariffs", map[string]any{"name": "per second", "kind": "sell", "timezone": "Europe/Istanbul"}, 201), "id")
	c.do(root, "POST", "/v1/rates", map[string]any{"tariff_id": bt, "prefix": "90", "price_per_min": 60_000, "effective_from": "2020-01-01T00:00:00Z"}, 201)
	c.do(root, "POST", "/v1/rates", map[string]any{"tariff_id": bt, "prefix": "44", "price_per_min": 0, "effective_from": "2020-01-01T00:00:00Z"}, 201)
	pack := num(c.do(root, "POST", "/v1/clients", map[string]any{"name": "Bundle Co", "tariff_id": bt, "routing_plan_id": plan}, 201), "id")
	c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": pack, "name": "main", "addresses": []string{"10.0.0.99"}}, 201)
	c.do(root, "POST", "/v1/bundles", map[string]any{"client_id": pack, "prefixes": []string{"9x"}, "seconds": 100}, 422)
	c.do(root, "POST", "/v1/bundles", map[string]any{"client_id": pack, "prefixes": []string{}, "seconds": 100}, 422)
	c.do(root, "POST", "/v1/bundles", map[string]any{"client_id": pack, "prefixes": []string{"9053"}, "seconds": 0}, 422)
	c.do(root, "POST", "/v1/bundles", map[string]any{"client_id": 999999, "prefixes": []string{"9053"}, "seconds": 100}, 409)
	c.do(ro, "POST", "/v1/bundles", map[string]any{"client_id": pack, "prefixes": []string{"9053"}, "seconds": 100}, 403)
	bundle := num(c.do(root, "POST", "/v1/bundles", map[string]any{"client_id": pack, "name": "Mobile 100", "prefixes": []string{"9053", "9054", "44"}, "seconds": 100}, 201), "id")
	ist, _ := time.LoadLocation("Europe/Istanbul")
	// Earlier today, clear of the hour the report test below looks at. (In
	// the first hours of a month in Istanbul this is last month's period,
	// which the simulator no longer counts: the checks on it then fail.)
	bAt := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	bSim := func(dst string) map[string]any {
		return c.do(root, "POST", "/v1/route/simulate", map[string]any{"src_ip": "10.0.0.99", "dst": dst, "at": bAt}, 200)
	}
	// No balance at all: the bundle carries the call, and only to its numbers.
	if d := bSim("905321112233"); d["allowed"] != true || num(d, "bundle_id") != bundle || num(d, "bundle_free_sec") != 100 || num(d, "max_duration_sec") != 100 {
		t.Errorf("call on a bundle without a balance: %v", d)
	}
	eq(t, "outside the bundle without a balance", num(bSim("902161112233"), "sip_code"), 402)
	bCall := func(id, dst string, at time.Time, sec int, want map[string]int) map[string]any {
		t.Helper()
		got := c.do(root, "POST", "/v1/cdrs", []map[string]any{{"call_id": id, "client_id": pack, "src": "1", "dst": dst,
			"start_at": at, "answer_at": at, "end_at": at.Add(time.Duration(sec) * time.Second)}}, 201)
		for k, v := range want {
			eq(t, id+" "+k, num(got, k), int64(v))
		}
		return c.raw(root, "GET", "/v1/cdrs?call_id="+id, nil, 200).([]any)[0].(map[string]any)
	}
	used := func(period string) (n int64) {
		t.Helper()
		if err := pool.QueryRow(ctx, `select coalesce(sum(used_sec), 0) from bundle_usage where bundle_id = $1 and period = $2`, bundle, period).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	bPeriod := rating.Period(bAt, ist)
	// The bundle is enough: nothing is charged, the seconds are counted.
	rec = bCall("pack-1", "905321112233", bAt, 60, map[string]int{"inserted": 1})
	if num(rec, "sell_billed_sec") != 60 || num(rec, "bundle_sec") != 60 || num(rec, "bundle_id") != bundle || num(rec, "sell_amount") != 0 {
		t.Errorf("call inside the bundle: %v", rec)
	}
	eq(t, "balance after a call inside the bundle", c.balance(root, "clients", pack), 0)
	eq(t, "seconds used", used(bPeriod), 60)
	// The same record again takes nothing a second time.
	bCall("pack-1", "905321112233", bAt, 60, map[string]int{"inserted": 0, "duplicates": 1})
	eq(t, "seconds used after a repeat", used(bPeriod), 60)
	eq(t, "what the simulator sees left", num(bSim("905321112233"), "bundle_free_sec"), 40)
	// The bundle ends inside the call: the rest is charged.
	rec = bCall("pack-2", "905421112233", bAt, 70, nil)
	if num(rec, "sell_billed_sec") != 70 || num(rec, "bundle_sec") != 40 || num(rec, "sell_amount") != 30_000 {
		t.Errorf("call the bundle ends in: %v", rec)
	}
	eq(t, "balance after the rest was charged", c.balance(root, "clients", pack), -30_000)
	// Used up: calls cost what the tariff says, and need a balance again.
	rec = bCall("pack-3", "905321112233", bAt, 10, nil)
	if num(rec, "bundle_sec") != 0 || rec["bundle_id"] != nil || num(rec, "sell_amount") != 10_000 {
		t.Errorf("call after the bundle: %v", rec)
	}
	eq(t, "seconds used never pass the bundle", used(bPeriod), 100)
	eq(t, "used-up bundle without a balance", num(bSim("905321112233"), "sip_code"), 402)
	// A number outside the bundle and a call that costs nothing leave it alone.
	if rec = bCall("pack-4", "902161112233", bAt.AddDate(0, 2, 0), 10, nil); num(rec, "bundle_sec") != 0 || num(rec, "sell_amount") != 10_000 {
		t.Errorf("call outside the bundle: %v", rec)
	}
	if rec = bCall("pack-5", "441112223344", bAt.AddDate(0, 2, 0), 10, nil); num(rec, "bundle_sec") != 0 || num(rec, "sell_amount") != 0 {
		t.Errorf("free call: %v", rec)
	}
	eq(t, "seconds used by calls that take none", used(rating.Period(bAt.AddDate(0, 2, 0), ist)), 0)
	// What the panel shows of the current period.
	for _, row := range c.raw(root, "GET", fmt.Sprintf("/v1/clients/%d/bundles", pack), nil, 200).([]any) {
		if b := row.(map[string]any); num(b, "id") != bundle || b["period"] != rating.Period(time.Now(), ist) ||
			(b["period"] == bPeriod && num(b, "used_sec") != 100) || num(b, "seconds") != 100 {
			t.Errorf("bundle with its usage: %v", b)
		}
	}
	// A new period starts from nothing. A call counts to the month it
	// starts in, in the tariff's time zone, however long it runs.
	edge := time.Date(2027, 1, 31, 20, 59, 30, 0, time.UTC) // 23:59:30 in Istanbul
	if rec = bCall("pack-6", "905321112233", edge, 50, nil); num(rec, "bundle_sec") != 50 || num(rec, "sell_amount") != 0 {
		t.Errorf("call across the end of a month: %v", rec)
	}
	eq(t, "the month the call started in", used("2027-01"), 50)
	eq(t, "the month it ended in", used("2027-02"), 0)
	bCall("pack-7", "905321112233", edge.Add(40*time.Second), 20, nil) // 00:00:10 on 1 February there, still January in UTC
	eq(t, "the next month, by the tariff's clock", used("2027-02"), 20)
	// Two records stored side by side cannot take the same seconds.
	side := time.Date(2027, 3, 10, 9, 0, 0, 0, time.UTC)
	done := make(chan error, 2)
	for i := range 2 {
		go func() {
			at := side.Add(time.Duration(i) * time.Minute)
			body, _ := json.Marshal(map[string]any{"call_id": fmt.Sprintf("pack-side-%d", i), "client_id": pack, "src": "1", "dst": "905321112233",
				"start_at": at, "answer_at": at, "end_at": at.Add(60 * time.Second)})
			var raw map[string]json.RawMessage
			json.Unmarshal(body, &raw)
			_, _, used, err := srv.IngestCDRsBundles(ctx, Caller{ResellerID: me, Admin: true}, []map[string]json.RawMessage{raw})
			if err == nil && (len(used) != 1 || used[0].BundleID != bundle || used[0].Period != "2027-03") {
				err = fmt.Errorf("bundle use reported: %+v", used)
			}
			done <- err
		}()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	var sideSec, sideAmount int64
	if err := pool.QueryRow(ctx, `select sum(bundle_sec), sum(sell_amount) from cdrs where call_id like 'pack-side-%'`).Scan(&sideSec, &sideAmount); err != nil {
		t.Fatal(err)
	}
	eq(t, "bundle seconds of two calls at once", sideSec, 100)
	eq(t, "what the two were charged", sideAmount, 20_000)
	eq(t, "seconds used by the two", used("2027-03"), 100)
	// A repeat is told as having drawn on the bundle, so a lost report can be made again.
	if _, dup, again, err := srv.IngestCDRsBundles(ctx, Caller{ResellerID: me, Admin: true}, []map[string]json.RawMessage{{
		"call_id": json.RawMessage(`"pack-side-0"`), "client_id": json.RawMessage(fmt.Sprint(pack)), "dst": json.RawMessage(`"905321112233"`),
		"start_at": json.RawMessage(`"2027-03-10T09:00:00Z"`), "answer_at": json.RawMessage(`"2027-03-10T09:00:00Z"`), "end_at": json.RawMessage(`"2027-03-10T09:01:00Z"`)}}); err != nil || dup != 1 || len(again) != 1 || again[0].Period != "2027-03" {
		t.Errorf("repeat of a record that drew on the bundle: %d duplicates, %+v, %v", dup, again, err)
	}
	eq(t, "seconds used after that repeat", used("2027-03"), 100)
	// A bundle switched off pays for nothing; a removed one leaves the records.
	c.do(root, "PATCH", fmt.Sprintf("/v1/bundles/%d", bundle), map[string]any{"enabled": false}, 200)
	if rec = bCall("pack-8", "905321112233", side.AddDate(0, 1, 0), 10, nil); num(rec, "bundle_sec") != 0 || num(rec, "sell_amount") != 10_000 {
		t.Errorf("call with the bundle switched off: %v", rec)
	}
	if d := bSim("905321112233"); num(d, "bundle_id") != 0 {
		t.Errorf("decision with the bundle switched off: %v", d)
	}
	eq(t, "bundles listed for the client", int64(len(c.raw(root, "GET", fmt.Sprintf("/v1/bundles?client_id=%d", pack), nil, 200).([]any))), 1)
	c.do(root, "DELETE", fmt.Sprintf("/v1/bundles/%d", bundle), nil, 204)
	if rec = c.raw(root, "GET", "/v1/cdrs?call_id=pack-1", nil, 200).([]any)[0].(map[string]any); rec["bundle_id"] != nil || num(rec, "bundle_sec") != 60 {
		t.Errorf("record after its bundle was removed: %v", rec)
	}

	// --- the report's quality figures: three attempts on one day, one
	// answered after 1.2 s of post-dial delay, one busy after 0.8 s, one
	// failed by the network ---
	qAt, _ := time.Parse(time.RFC3339, "2026-05-04T10:00:00Z")
	c.do(root, "POST", "/v1/cdrs", []map[string]any{
		{"call_id": "q-1", "client_id": owl, "src": "1", "dst": "905321112233", "start_at": qAt, "answer_at": qAt, "end_at": qAt.Add(60 * time.Second), "pdd_ms": 1200},
		{"call_id": "q-2", "client_id": owl, "src": "1", "dst": "905321112233", "start_at": qAt, "end_at": qAt, "sip_code": 486, "pdd_ms": 800},
		{"call_id": "q-3", "client_id": owl, "src": "1", "dst": "905321112233", "start_at": qAt, "end_at": qAt, "sip_code": 503},
	}, 201)
	quality := c.raw(root, "GET", "/v1/reports/summary?group_by=day&from=2026-05-04T00:00:00Z&to=2026-05-05T00:00:00Z", nil, 200).([]any)
	if len(quality) != 1 {
		t.Fatalf("quality report: %v", quality)
	}
	if r := quality[0].(map[string]any); num(r, "calls") != 3 || num(r, "failed_network") != 1 || r["ner_pct"] != 66.67 || r["asr_pct"] != 33.33 ||
		num(r, "pdd_ms") != 1000 || num(r, "pdd_samples") != 2 || num(r, "pdd_sum_ms") != 2000 {
		t.Errorf("quality figures: %v", r)
	}

	// --- the API's description of itself ---
	doc := c.do(root, "GET", "/v1/openapi.json", nil, 200)
	apiPaths, _ := doc["paths"].(map[string]any)
	apiSchemas, _ := doc["components"].(map[string]any)["schemas"].(map[string]any)
	if doc["openapi"] != "3.0.3" || len(apiPaths) < 40 {
		t.Fatalf("openapi: version %v, %d paths", doc["openapi"], len(apiPaths))
	}
	for path, methods := range map[string][]string{
		"/v1/clients": {"get", "post"}, "/v1/clients/{id}": {"get", "patch", "delete"}, "/v1/cdrs": {"get", "post"},
		"/v1/cli-rules": {"get", "post"}, "/v1/exchange-rates/{id}": {"patch"}, "/v1/route/simulate": {"post"}, "/v1/login": {"post"},
	} {
		item, _ := apiPaths[path].(map[string]any)
		for _, m := range methods {
			if item[m] == nil {
				t.Errorf("openapi: %s %s is missing", m, path)
			}
		}
	}
	if item := apiPaths["/v1/cdrs/{id}"].(map[string]any); item["patch"] != nil || item["delete"] != nil {
		t.Error("openapi: call records are described as writable")
	}
	if sec, _ := apiPaths["/v1/login"].(map[string]any)["post"].(map[string]any)["security"].([]any); len(sec) != 0 {
		t.Error("openapi: signing in is described as needing a token")
	}
	if sec, _ := apiPaths["/v1/clients"].(map[string]any)["get"].(map[string]any)["security"].([]any); len(sec) != 1 {
		t.Error("openapi: listing clients is described as open")
	}
	clientProps := apiSchemas["clients"].(map[string]any)["properties"].(map[string]any)
	if b := clientProps["balance"].(map[string]any); b["type"] != "integer" || b["readOnly"] != true {
		t.Errorf("openapi: client balance: %v", b)
	}
	if n := clientProps["name"].(map[string]any); n["type"] != "string" || n["readOnly"] != nil {
		t.Errorf("openapi: client name: %v", n)
	}
	if _, leaked := apiSchemas["client-trunks"].(map[string]any)["properties"].(map[string]any)["password_ha1"]; leaked {
		t.Error("openapi: a hidden column is described")
	}

	// --- CDR rating for client and vendor ---
	now := time.Now().UTC()
	start := now.Add(-10 * time.Minute)
	answer := start.Add(5 * time.Second)
	cdrs := []map[string]any{
		{"call_id": "call-1", "client_id": clID, "connection_id": c2, "src": "1000", "dst": "905321112233",
			"start_at": start, "answer_at": answer, "end_at": answer.Add(90 * time.Second)},
		{"call_id": "call-2", "client_id": clID, "connection_id": c2, "src": "1000", "dst": "905321112244",
			"start_at": start, "end_at": start.Add(20 * time.Second), "sip_code": 486},
	}
	eq(t, "cdrs inserted", num(c.do(root, "POST", "/v1/cdrs", cdrs, 201), "inserted"), 2)
	eq(t, "cdrs deduplicated", num(c.do(root, "POST", "/v1/cdrs", cdrs, 201), "duplicates"), 2)
	// 90 s billed 60/60 = 120 s at 0.02/min; vendor 90 s at 0.008/min.
	eq(t, "client balance after call", c.balance(root, "clients", clID), 10_000_000-40_000)
	eq(t, "vendor payable after call", c.balance(root, "vendors", v2), 12_000)
	c.do(root, "POST", "/v1/cdrs", []map[string]any{{"call_id": "x", "client_id": 999, "dst": "90", "start_at": start, "end_at": start}}, 422)
	c.do(root, "POST", "/v1/cdrs", []map[string]any{{"call_id": "y", "client_id": clID, "did_id": 1, "dst": "90", "start_at": start, "end_at": start}}, 400)
	if n := len(c.raw(root, "GET", "/v1/cdrs?call_id=call-1", nil, 200).([]any)); n != 1 {
		t.Errorf("cdr lookup: %d", n)
	}

	// --- paying a vendor reduces what we owe ---
	c.do(root, "POST", "/v1/payments", map[string]any{"party_type": "vendor", "party_id": v2, "amount": 12_000}, 201)
	eq(t, "vendor settled", c.balance(root, "vendors", v2), 0)

	// --- reports and ledger ---
	q := fmt.Sprintf("?group_by=client&from=%s&to=%s", now.Add(-time.Hour).Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339))
	rep := c.raw(root, "GET", "/v1/reports/summary"+q, nil, 200).([]any)
	if len(rep) != 1 {
		t.Fatalf("report rows: %v", rep)
	}
	row := rep[0].(map[string]any)
	eq(t, "report calls", num(row, "calls"), 2)
	eq(t, "report answered", num(row, "answered"), 1)
	eq(t, "report ASR", num(row, "asr_pct"), 50)

	// --- the same filters as the list, every matching record, as CSV ---
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/v1/cdrs/export?client_id=%d&from=2020-01-01T00:00:00Z", ts.URL, clID), nil)
	req.Header.Set("Authorization", "Bearer "+root)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	sheetRows, err := csv.NewReader(resp.Body).ReadAll()
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 || len(sheetRows) != 3 {
		t.Fatalf("export: %d %v %v", resp.StatusCode, err, sheetRows)
	}
	col := map[string]int{}
	for i, name := range sheetRows[0] {
		col[name] = i
	}
	var answered []string
	for _, r := range sheetRows[1:] {
		if r[col["client"]] != "Acme Telecom" {
			t.Errorf("export client: %v", r)
		}
		if r[col["answer_at"]] != "" {
			answered = r
		}
	}
	if answered == nil || !strings.Contains(answered[col["sell_amount"]], ".") || answered[col["vendor"]] == "" {
		t.Errorf("export answered row: %v", answered)
	}
	c.do(root, "GET", "/v1/cdrs/export?nonsense=1", nil, 400)
	eq(t, "report revenue", num(row, "revenue"), 40_000)
	eq(t, "report margin", num(row, "margin"), 28_000)
	if n := len(c.raw(root, "GET", fmt.Sprintf("/v1/ledger?party_type=client&party_id=%d", clID), nil, 200).([]any)); n != 1 {
		t.Errorf("ledger entries: %d", n)
	}

	// --- what the panel needs: session, keys, list search, rate sheet validation ---
	ses := c.do(ro, "GET", "/v1/session", nil, 200)
	if ses["edition"] != "community" || ses["key"].(map[string]any)["role"] != "readonly" {
		t.Errorf("session: %v", ses)
	}
	if !strings.HasPrefix(ro, KeyPrefix) || len(ro) != len(KeyPrefix)+32 {
		t.Errorf("key format: %q", ro)
	}
	keys := c.raw(root, "GET", "/v1/api-keys", nil, 200).([]any)
	if len(keys) != 2 {
		t.Fatalf("keys: %v", keys)
	}
	var roID, rootID int64
	for _, k := range keys {
		m := k.(map[string]any)
		if _, leaked := m["key_hash"]; leaked {
			t.Error("key hash returned")
		}
		if m["current"] == true {
			rootID = num(m, "id")
		} else {
			roID = num(m, "id")
		}
	}
	c.do(root, "DELETE", fmt.Sprintf("/v1/api-keys/%d", rootID), nil, 409)
	c.do(ro, "DELETE", fmt.Sprintf("/v1/api-keys/%d", roID), nil, 403)
	c.do(root, "DELETE", fmt.Sprintf("/v1/api-keys/%d", roID), nil, 204)
	c.do(ro, "GET", "/v1/clients", nil, 401)

	if n := len(c.raw(root, "GET", "/v1/clients?q=acme", nil, 200).([]any)); n != 1 {
		t.Errorf("client search: %d", n)
	}
	if n := len(c.raw(root, "GET", "/v1/clients?q=100%25", nil, 200).([]any)); n != 0 {
		t.Errorf("search wildcard not escaped: %d", n)
	}
	if n := len(c.raw(root, "GET", fmt.Sprintf("/v1/cdrs?client_id=%d&dst=9053211122&from=", clID)+now.Add(-time.Hour).Format(time.RFC3339), nil, 200).([]any)); n != 2 {
		t.Errorf("cdr prefix filter: %d", n)
	}
	if n := len(c.raw(root, "GET", fmt.Sprintf("/v1/cdrs?client_id=%d&to=", clID)+now.Add(-time.Hour).Format(time.RFC3339), nil, 200).([]any)); n != 0 {
		t.Errorf("cdr time filter: %d", n)
	}
	// --- a leg delivered to a client's trunk, and a field an extension adds ---
	// Records from before the field (call-1) read with it empty; a leg to a
	// trunk names the trunk and no vendor. An edition that allows a field of
	// its own gets it in OnCDRStored, through the same ingest the rater uses.
	mainTrunk := num(trunk, "id")
	if rec := c.raw(root, "GET", "/v1/cdrs?call_id=call-1", nil, 200).([]any)[0].(map[string]any); rec["dst_trunk_id"] != nil {
		t.Errorf("vendor leg names a trunk: %v", rec["dst_trunk_id"])
	}
	if _, err := pool.Exec(ctx, `alter table cdrs add column ext_tag text`); err != nil {
		t.Fatal(err)
	}
	ext := New(pool)
	ext.AllowCDRField("ext_tag")
	var seen []string
	ext.OnCDRStored(func(ctx context.Context, tx pgx.Tx, ev CDRStored) error {
		var tag string
		if err := json.Unmarshal(ev.Record.Extra["ext_tag"], &tag); err != nil {
			return err
		}
		seen = append(seen, tag)
		_, err := tx.Exec(ctx, `update cdrs set ext_tag = $2 where id = $1`, ev.ID, tag)
		return err
	})
	extTS := httptest.NewServer(ext)
	defer extTS.Close()
	ce := client{t, extTS.URL}
	delivered := []map[string]any{
		{"call_id": "deliver-1", "client_id": netCl, "trunk_id": num(wide, "id"), "dst_trunk_id": mainTrunk, "connection_id": nil, "src": "1000",
			"dst": "905321112233", "start_at": start, "end_at": start.Add(30 * time.Second), "sip_code": 408, "ext_tag": "did-7"},
		{"call_id": "deliver-1", "leg": 2, "client_id": netCl, "connection_id": c2, "src": "1000",
			"dst": "905321112233", "start_at": start, "end_at": start.Add(40 * time.Second), "sip_code": 486, "ext_tag": "did-7"},
	}
	c.do(root, "POST", "/v1/cdrs", delivered, 400) // the core alone does not know the extension's field
	eq(t, "delivered legs stored", num(ce.do(root, "POST", "/v1/cdrs", delivered, 201), "inserted"), 2)
	same(t, "extension field reached the hook", seen, "[did-7 did-7]")
	legs := c.raw(root, "GET", "/v1/cdrs?call_id=deliver-1", nil, 200).([]any)
	byLeg := map[int64]map[string]any{}
	for _, l := range legs {
		byLeg[num(l.(map[string]any), "leg")] = l.(map[string]any)
	}
	if l := byLeg[1]; l == nil || num(l, "dst_trunk_id") != mainTrunk || l["connection_id"] != nil || l["vendor_id"] != nil || l["ext_tag"] != "did-7" {
		t.Errorf("delivered leg: %v", l)
	}
	if l := byLeg[2]; l == nil || l["dst_trunk_id"] != nil || num(l, "connection_id") != c2 {
		t.Errorf("vendor leg after it: %v", l)
	}
	if got := c.raw(root, "GET", fmt.Sprintf("/v1/cdrs?dst_trunk_id=%d", mainTrunk), nil, 200).([]any); len(got) != 1 || got[0].(map[string]any)["call_id"] != "deliver-1" {
		t.Errorf("filter by destination trunk: %v", got)
	}
	req, _ = http.NewRequest("GET", ts.URL+"/v1/cdrs/export?call_id=deliver-1", nil)
	req.Header.Set("Authorization", "Bearer "+root)
	if resp, err = http.DefaultClient.Do(req); err != nil {
		t.Fatal(err)
	}
	sheetRows, err = csv.NewReader(resp.Body).ReadAll()
	resp.Body.Close()
	if err != nil || len(sheetRows) != 3 || sheetRows[0][len(sheetRows[0])-1] != "dst_trunk" {
		t.Fatalf("export of delivered legs: %v %v", err, sheetRows)
	}
	var trunks []string
	for _, r := range sheetRows[1:] {
		trunks = append(trunks, r[len(r)-1])
	}
	same(t, "export names the destination trunk", trunks, "[ main]")

	if n := len(c.raw(root, "GET", fmt.Sprintf("/v1/rates?tariff_id=%d&prefix=905&validity=current", retail), nil, 200).([]any)); n != 1 {
		t.Errorf("rate prefix filter: %d", n)
	}
	c.do(root, "GET", "/v1/rates?validity=sometime", nil, 400)

	sheet := "prefix,price,initial,increment\n90,0.021,60,60\n44,0.01,1,1\n44,0.02,1,1\n9x,0.01,1,1\n33,-1,1,1\n34,0.0000001,1,1\n35,0.01,1,0\n,0.01,1,1\n"
	imp := fmt.Sprintf("/v1/tariffs/%d/rates/import?effective_from=%s", retail, now.Add(24*time.Hour).Format(time.RFC3339))
	dry := c.do(root, "POST", imp+"&dry_run=1", sheet, 200)
	eq(t, "dry run valid", num(dry, "valid"), 2)
	eq(t, "dry run invalid", num(dry, "invalid"), 6)
	eq(t, "dry run closes", num(dry, "closed"), 1)
	eq(t, "dry run new", num(dry, "new_prefixes"), 1)
	codes := ""
	for _, e := range dry["errors"].([]any) {
		codes += e.(map[string]any)["code"].(string) + " "
	}
	if codes != "duplicate_prefix prefix_not_digits price_negative too_many_decimals increment_min prefix_empty " {
		t.Errorf("import errors: %s", codes)
	}
	if n := len(c.raw(root, "GET", fmt.Sprintf("/v1/rates?tariff_id=%d", retail), nil, 200).([]any)); n != 2 {
		t.Errorf("dry run wrote rates: %d", n)
	}
	eq(t, "keep mode rejects prefix in force", num(c.do(root, "POST", imp+"&dry_run=1&mode=keep", sheet, 200), "valid"), 1)
	eq(t, "rates loaded", num(c.do(root, "POST", imp, sheet, 201), "rates"), 2)
	if n := len(c.raw(root, "GET", fmt.Sprintf("/v1/rates?tariff_id=%d&validity=future", retail), nil, 200).([]any)); n != 2 {
		t.Errorf("future rates: %d", n)
	}
	// The old rate for 90 stays in force until the new one starts.
	eq(t, "current sell price", num(c.do(root, "POST", "/v1/route/simulate", map[string]any{"src_ip": "10.0.0.5", "dst": "905321112233"}, 200), "sell_price_per_min"), 20_000)
	// --- people sign in with an e-mail address and a password ---
	c.do(root, "POST", "/v1/users", map[string]any{"email": "ada@example.test", "name": "Ada", "role": "admin", "password": "short"}, 422)
	ada := num(c.do(root, "POST", "/v1/users", map[string]any{"email": "ada@example.test", "name": "Ada", "role": "admin", "password": "correct horse 1"}, 201), "id")
	c.do(root, "POST", "/v1/users", map[string]any{"email": "ADA@example.test", "name": "Other", "password": "correct horse 2"}, 409)
	bob := num(c.do(root, "POST", "/v1/users", map[string]any{"email": "bob@example.test", "name": "Bob", "password": "battery staple 1"}, 201), "id")
	users := c.raw(root, "GET", "/v1/users", nil, 200).([]any)
	if len(users) != 2 || users[0].(map[string]any)["password_hash"] != nil {
		t.Errorf("users: %v", users)
	}
	c.do("", "POST", "/v1/login", map[string]any{"email": "ada@example.test", "password": "wrong password"}, 401)
	c.do("", "POST", "/v1/login", map[string]any{"email": "nobody@example.test", "password": "correct horse 1"}, 401)
	adaToken := c.do("", "POST", "/v1/login", map[string]any{"email": "Ada@Example.test", "password": "correct horse 1"}, 200)["token"].(string)
	sess := c.do(adaToken, "GET", "/v1/session", nil, 200)
	if sess["user"].(map[string]any)["email"] != "ada@example.test" || sess["key"].(map[string]any)["role"] != "admin" {
		t.Errorf("session: %v", sess)
	}
	// A read-only user reads but does not write.
	bobToken := c.do("", "POST", "/v1/login", map[string]any{"email": "bob@example.test", "password": "battery staple 1"}, 200)["token"].(string)
	c.do(bobToken, "GET", "/v1/clients", nil, 200)
	c.do(bobToken, "POST", "/v1/clients", map[string]any{"name": "x"}, 403)
	// Nobody removes their own rights; an admin manages the others.
	c.do(adaToken, "PATCH", fmt.Sprintf("/v1/users/%d", ada), map[string]any{"role": "readonly"}, 422)
	c.do(adaToken, "DELETE", fmt.Sprintf("/v1/users/%d", ada), nil, 422)
	c.do(bobToken, "POST", "/v1/me/password", map[string]any{"current": "nope", "new": "another secret 1"}, 403)
	c.do(bobToken, "POST", "/v1/me/password", map[string]any{"current": "battery staple 1", "new": "another secret 1"}, 204)
	c.do(bobToken, "GET", "/v1/clients", nil, 200) // the session that changed it goes on
	c.do("", "POST", "/v1/login", map[string]any{"email": "bob@example.test", "password": "battery staple 1"}, 401)
	// A new password set by an admin, or a disabled account, ends the sessions.
	c.do(adaToken, "PATCH", fmt.Sprintf("/v1/users/%d", bob), map[string]any{"password": "reset by admin 1"}, 200)
	c.do(bobToken, "GET", "/v1/clients", nil, 401)
	bobToken = c.do("", "POST", "/v1/login", map[string]any{"email": "bob@example.test", "password": "reset by admin 1"}, 200)["token"].(string)
	c.do(adaToken, "PATCH", fmt.Sprintf("/v1/users/%d", bob), map[string]any{"status": "disabled"}, 200)
	c.do(bobToken, "GET", "/v1/clients", nil, 401)
	c.do("", "POST", "/v1/login", map[string]any{"email": "bob@example.test", "password": "reset by admin 1"}, 401)
	// Five wrong passwords in a row lock the account, even for the right one.
	c.do(adaToken, "PATCH", fmt.Sprintf("/v1/users/%d", bob), map[string]any{"status": "active", "unlock": true}, 200)
	for range 5 {
		c.do("", "POST", "/v1/login", map[string]any{"email": "bob@example.test", "password": "guess guess guess"}, 401)
	}
	c.do("", "POST", "/v1/login", map[string]any{"email": "bob@example.test", "password": "reset by admin 1"}, 429)
	if locked := c.do(adaToken, "PATCH", fmt.Sprintf("/v1/users/%d", bob), map[string]any{"unlock": true}, 200)["locked"]; locked != false {
		t.Errorf("still locked: %v", locked)
	}
	c.do("", "POST", "/v1/login", map[string]any{"email": "bob@example.test", "password": "reset by admin 1"}, 200)
	c.do(adaToken, "POST", "/v1/logout", nil, 204)
	c.do(adaToken, "GET", "/v1/session", nil, 401)
	c.do(root, "DELETE", fmt.Sprintf("/v1/users/%d", bob), nil, 204)

	// --- a trunk that signs in gets its password from the API, once ---
	c.do(root, "POST", fmt.Sprintf("/v1/client-trunks/%d/password", num(trunk, "id")), nil, 409) // known by its address
	office := num(c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": clID, "name": "office", "auth_type": "digest", "username": "acme-office"}, 201), "id")
	if snap, _ := srv.LoadSnapshot(ctx); snap != nil {
		if _, ok := snap.Credentials("acme-office"); ok {
			t.Error("a trunk without a password must not be usable")
		}
	}
	c.do(ro, "POST", fmt.Sprintf("/v1/client-trunks/%d/password", office), nil, 401) // that key was revoked above
	issued := c.do(root, "POST", fmt.Sprintf("/v1/client-trunks/%d/password", office), nil, 200)
	pw, _ := issued["password"].(string)
	if len(pw) != 20 || issued["username"] != "acme-office" || issued["realm"] != DefaultRealm {
		t.Fatalf("issued password: %v", issued)
	}
	want := md5.Sum([]byte("acme-office:" + DefaultRealm + ":" + pw))
	snap, err := srv.LoadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ha1, ok := snap.Credentials("acme-office"); !ok || ha1 != hex.EncodeToString(want[:]) {
		t.Errorf("stored hash does not match the issued password: %q %v", ha1, ok)
	}
	if text := fmt.Sprint(c.raw(root, "GET", "/v1/client-trunks", nil, 200)); strings.Contains(text, hex.EncodeToString(want[:])) {
		t.Error("the trunk list leaks the password hash")
	}
	if again := c.do(root, "POST", fmt.Sprintf("/v1/client-trunks/%d/password", office), nil, 200)["password"]; again == pw {
		t.Error("a new password must differ from the old one")
	}

	// --- e-mail: queued with the change that causes it, sent with retries ---
	c.do("", "POST", "/v1/password/forgot", map[string]any{"email": "ada@example.test"}, 503) // no mail server yet
	post := &outbox{failures: 1}
	srv.SetMail(post, "https://panel.example.test")
	c.do("", "POST", "/v1/password/forgot", map[string]any{"email": "nobody@example.test"}, 204) // says nothing
	c.do("", "POST", "/v1/password/forgot", map[string]any{"email": "ADA@example.test"}, 204)
	c.do("", "POST", "/v1/password/forgot", map[string]any{"email": "ada@example.test"}, 204) // too soon: no second link
	if n, err := srv.SendQueuedMail(ctx); n != 1 || err != nil || len(post.sent) != 0 {
		t.Fatalf("first try must fail and stay queued: %d %v %v", n, err, post.sent)
	}
	if n, _ := srv.SendQueuedMail(ctx); n != 0 {
		t.Error("a failed message must wait before the next try")
	}
	if _, err := pool.Exec(ctx, `update mail_outbox set next_try_at = now()`); err != nil {
		t.Fatal(err)
	}
	if n, err := srv.SendQueuedMail(ctx); n != 1 || err != nil || len(post.sent) != 1 || post.sent[0].To != "ada@example.test" {
		t.Fatalf("second try: %d %v %+v", n, err, post.sent)
	}
	link := post.sent[0].Body[strings.Index(post.sent[0].Body, "https://panel.example.test/reset?token="):]
	token := strings.Fields(strings.TrimPrefix(link, "https://panel.example.test/reset?token="))[0]
	c.do("", "POST", "/v1/password/reset", map[string]any{"token": token, "password": "short"}, 422)
	c.do("", "POST", "/v1/password/reset", map[string]any{"token": "not-a-token", "password": "brand new secret 1"}, 410)
	c.do("", "POST", "/v1/password/reset", map[string]any{"token": token, "password": "brand new secret 1"}, 204)
	c.do("", "POST", "/v1/password/reset", map[string]any{"token": token, "password": "brand new secret 2"}, 410) // once only
	c.do("", "POST", "/v1/login", map[string]any{"email": "ada@example.test", "password": "correct horse 1"}, 401)
	c.do("", "POST", "/v1/login", map[string]any{"email": "ada@example.test", "password": "brand new secret 1"}, 200)

	// --- scheduled reports: each period goes out once, at its hour ---
	c.do(root, "POST", "/v1/report-schedules", map[string]any{"name": "bad", "group_by": "client", "period": "hourly", "recipients": "a@example.test"}, 422)
	c.do(root, "POST", "/v1/report-schedules", map[string]any{"name": "bad", "group_by": "client", "period": "daily", "recipients": "not an address"}, 422)
	sched := num(c.do(root, "POST", "/v1/report-schedules", map[string]any{"name": "Günlük müşteri", "group_by": "client", "period": "daily",
		"recipients": "ops@example.test, cfo@example.test", "send_hour": 8}, 201), "id")
	due := func(at string, want int) {
		t.Helper()
		now, _ := time.Parse(time.RFC3339, at)
		if n, err := srv.SweepReportSchedules(ctx, now); err != nil || n != want {
			t.Errorf("reports due at %s: %d (%v), want %d", at, n, err, want)
		}
	}
	before := len(post.sent)
	due("2026-05-05T07:59:00Z", 0) // the day is over, the hour has not come
	due("2026-05-05T08:00:00Z", 1)
	due("2026-05-05T09:00:00Z", 0) // sent already
	if n, err := srv.SendQueuedMail(ctx); n != 2 || err != nil || len(post.sent) != before+2 {
		t.Fatalf("report messages: %d %v", n, err)
	}
	report := post.sent[before]
	// The three attempts of 4 May: one answered, one failed by the network.
	for _, want := range []string{"Günlük rapor: Günlük müşteri", "04.05.2026", "Night Owl", "33,3", "66,7", "Toplam", "https://panel.example.test"} {
		if !strings.Contains(report.Subject+"\n"+report.Body, want) {
			t.Errorf("report lacks %q:\n%s", want, report.Body)
		}
	}
	if strings.Contains(report.Body, "Acme Telecom") {
		t.Error("the report lists a client without calls in the period")
	}
	due("2026-05-06T08:00:00Z", 1) // the next day's, though it has no calls
	c.do(root, "PATCH", fmt.Sprintf("/v1/report-schedules/%d", sched), map[string]any{"enabled": false}, 200)
	due("2026-05-07T08:00:00Z", 0)
	eq(t, "sent on request", num(c.do(root, "POST", fmt.Sprintf("/v1/report-schedules/%d/send", sched), nil, 200), "recipients"), 2)
	if _, err := srv.SendQueuedMail(ctx); err != nil {
		t.Fatal(err)
	}

	// --- the tools' system page ---
	c.do(root, "GET", "/v1/system", nil, 404) // not set up
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer alive.Close()
	srv.SetSystem(sysinfo.New(10, "/"), ParseServices("Alive = "+alive.URL+" , Gone=http://127.0.0.1:1/ ,nonsense"))
	system := c.do(root, "GET", "/v1/system", nil, 200)
	if disks, _ := system["machine"].(map[string]any)["disks"].([]any); len(disks) != 1 {
		t.Errorf("system disks: %v", system["machine"])
	}
	up := map[string]bool{}
	for _, s := range system["services"].([]any) {
		up[s.(map[string]any)["name"].(string)] = s.(map[string]any)["up"].(bool)
	}
	if len(up) != 3 || !up["PostgreSQL"] || !up["Alive"] || up["Gone"] {
		t.Errorf("services: %v", up)
	}
	if strings.Contains(fmt.Sprint(system), "127.0.0.1") {
		t.Error("the system page shows a service's address")
	}

	// --- the firewall's rules ---
	c.do(root, "POST", "/v1/firewall-rules", map[string]any{"cidr": "not an address", "action": "deny"}, 422)
	c.do(root, "POST", "/v1/firewall-rules", map[string]any{"cidr": "10.0.0.5", "action": "drop"}, 422)
	wall := num(c.do(root, "POST", "/v1/firewall-rules", map[string]any{"cidr": "10.0.0.0/24", "action": "deny", "note": "test"}, 201), "id")
	c.do(root, "POST", "/v1/firewall-rules", map[string]any{"cidr": "10.0.0.0/24", "action": "allow"}, 409)
	if d := c.do(root, "POST", "/v1/route/simulate", map[string]any{"src_ip": "10.0.0.5", "dst": "905321112233"}, 200); d["reason"] != "source blocked" || num(d, "sip_code") != 403 {
		t.Errorf("a call from a denied network: %v", d)
	}
	// A rule that has run out no longer applies; nor does one switched off.
	c.do(root, "PATCH", fmt.Sprintf("/v1/firewall-rules/%d", wall), map[string]any{"expires_at": "2020-01-01T00:00:00Z"}, 200)
	if d := c.do(root, "POST", "/v1/route/simulate", map[string]any{"src_ip": "10.0.0.5", "dst": "905321112233"}, 200); d["allowed"] != true {
		t.Errorf("after the rule ran out: %v", d)
	}
	c.do(root, "DELETE", fmt.Sprintf("/v1/firewall-rules/%d", wall), nil, 204)
	c.do(root, "GET", "/v1/firewall/bans", nil, 503) // no engine in this test

	// --- low balance alerts follow the balance and the threshold ---
	low := num(c.do(root, "POST", "/v1/clients", map[string]any{"name": "Thin Ice Ltd", "low_balance": 1_000_000, "email": "billing@thinice.test"}, 201), "id")
	sweep := func(wantRaised, wantCleared int64) {
		t.Helper()
		raised, cleared, err := srv.SweepBalanceAlerts(ctx)
		if err != nil || raised != wantRaised || cleared != wantCleared {
			t.Errorf("sweep: raised %d cleared %d (%v), want %d and %d", raised, cleared, err, wantRaised, wantCleared)
		}
	}
	alerts := func(state string) []any {
		return c.raw(root, "GET", fmt.Sprintf("/v1/balance-alerts?client_id=%d&state=%s", low, state), nil, 200).([]any)
	}
	// An extension may say a client is not to be mailed; the alert is raised all the same.
	quiet := num(c.do(root, "POST", "/v1/clients", map[string]any{"name": "Quiet Ltd", "low_balance": 1, "email": "billing@quiet.test"}, 201), "id")
	srv.OnAlertMail(func(_ context.Context, _ pgx.Tx, id int64) (bool, error) { return id != quiet, nil })
	sweep(2, 0) // both balances are zero, below their thresholds
	sweep(0, 0) // and stays one alert however often it is looked at
	if n, err := srv.SendQueuedMail(ctx); n != 1 || err != nil || post.sent[len(post.sent)-1].To != "billing@thinice.test" ||
		!strings.Contains(post.sent[len(post.sent)-1].Body, "Eşik: 1,00 USD") {
		t.Errorf("alert mail: %d %v %+v", n, err, post.sent[len(post.sent)-1])
	}
	if open := alerts("open"); len(open) != 1 || num(open[0].(map[string]any), "threshold") != 1_000_000 {
		t.Errorf("open alerts: %v", open)
	}
	c.do(root, "POST", "/v1/payments", map[string]any{"party_type": "client", "party_id": low, "amount": 5_000_000}, 201)
	sweep(0, 1)
	if len(alerts("open")) != 0 || len(alerts("cleared")) != 1 {
		t.Errorf("after payment: %v %v", alerts("open"), alerts("cleared"))
	}
	c.do(root, "PATCH", fmt.Sprintf("/v1/clients/%d", low), map[string]any{"low_balance": 9_000_000}, 200)
	sweep(1, 0)
	c.do(root, "PATCH", fmt.Sprintf("/v1/clients/%d", low), map[string]any{"low_balance": nil}, 200)
	sweep(0, 1) // no threshold, no alert
	eq(t, "alert history", int64(len(alerts("cleared"))), 2)

	// --- live views: read from the route-engine, named from the database ---
	c.do(root, "GET", "/v1/live", nil, 503) // no engine configured
	var hungUp string
	bound := []string{"sip:pbx@10.0.0.5:5060", "sip:pbx@10.0.0.6:5060"}
	var unbound []string
	const engineToken = "test-engine-token"
	var probed bytes.Buffer // what the engine was asked to test
	var asked int64         // the connection with detailed settings
	eng := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The stand-in engine wants the token on every request, as the real one does.
		if r.Header.Get("Authorization") != "Bearer "+engineToken {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch r.URL.Path {
		// Answers are written with the API's types for the engine's
		// (engineviews.go), which TestEngineViewsMatch holds to the engine's.
		case "/v1/live":
			json.NewEncoder(w).Encode(liveTraffic{V: 1, Calls: 3, Seconds: []liveSecond{}, SIPCodes: []liveShare{},
				Clients: []liveClient{{ClientID: clID, Calls: 3}}, Connections: []liveConnection{{ConnectionID: c1}},
				Countries: []liveCountry{{Country: "TR", Prefixes: []liveShare{}, Connections: []liveShare{{Key: fmt.Sprint(c2), Count: 1}}}}})
		case "/v1/calls":
			json.NewEncoder(w).Encode(map[string]any{"v": 1, "total": 1, "calls": []liveCall{{CallID: "x@y", ClientID: clID, ConnectionID: c1}}})
		case "/v1/hangup":
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			if hungUp, _ = in["call_id"].(string); hungUp == "gone" {
				http.Error(w, "no such call", 404)
				return
			}
			w.WriteHeader(204)
		case "/v1/registrations":
			// The office trunk is registered from two places, over IPv6: the
			// audit record names where from as [address]:port (T-28).
			list := []string{}
			for _, id := range r.URL.Query()["trunk_id"] {
				if id == fmt.Sprint(office) {
					for _, ct := range bound {
						list = append(list, fmt.Sprintf(`{"trunk_id":%d,"contact":%q,"src_ip":"2001:db8::50","src_port":40000,"transport":"udp","nat":true,"since":"2026-11-22T10:00:00Z","until":"2026-11-22T10:01:00Z","user_agent":"pbx"}`, office, ct))
					}
				}
			}
			fmt.Fprintf(w, `{"v":1,"registrations":[%s]}`, strings.Join(list, ","))
		case "/v1/unregister":
			var in struct {
				TrunkID int64  `json:"trunk_id"`
				Contact string `json:"contact"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			unbound = append(unbound, fmt.Sprintf("%d %s", in.TrunkID, in.Contact))
			var left []string
			for _, ct := range bound {
				if in.Contact != "" && ct != in.Contact {
					left = append(left, ct)
				}
			}
			bound = left
			io.WriteString(w, `{"v":1,"registrations":[]}`)
		case "/v1/probe":
			io.Copy(&probed, r.Body)
			io.WriteString(w, `{"v":1,"outcome":"answered","sip_code":200,"sip_reason":"OK","ms":12}`)
		case "/v1/peers":
			json.NewEncoder(w).Encode(map[string]any{"v": 1,
				"connections": map[string]peerHealth{fmt.Sprint(asked): {State: "down", Last: probeResult{Outcome: "no_answer", Ms: 3000}},
					"999999": {State: "up"}},
				"registrations": map[string]vendorRegistration{fmt.Sprint(asked): {State: "failing", Reason: "credentials"},
					"999999": {State: "registered"}}})
		}
	}))
	defer eng.Close()
	srv.SetEngine(eng.URL)
	ro = c.do(root, "POST", "/v1/api-keys", map[string]any{"name": "live readonly", "role": "readonly"}, 201)["key"].(string)
	// Without the engine's token, or with another one, the engine refuses
	// the API. The caller is told the service is unavailable, not that its
	// own key is bad, and neither token appears in the answer.
	for _, wrong := range []string{"", "not-the-token"} {
		srv.SetEngineToken(wrong)
		refused := c.do(ro, "GET", "/v1/live", nil, 503)
		if msg := fmt.Sprint(refused); strings.Contains(msg, engineToken) || (wrong != "" && strings.Contains(msg, wrong)) {
			t.Errorf("the engine token leaked into an error: %s", msg)
		}
	}
	srv.SetEngineToken(engineToken)
	live := c.do(ro, "GET", "/v1/live", nil, 200)
	eq(t, "live calls", num(live["live"].(map[string]any), "calls"), 3)
	if name := live["clients"].(map[string]any)[fmt.Sprint(clID)]; name != "Acme Telecom" {
		t.Errorf("live client name: %v", name)
	}
	conns := live["connections"].(map[string]any)
	if len(conns) != 2 || conns[fmt.Sprint(c1)].(map[string]any)["vendor"] != "Vendor One" {
		t.Errorf("live connection names: %v", conns)
	}
	list := c.do(ro, "GET", fmt.Sprintf("/v1/live/calls?client_id=%d&limit=10", clID), nil, 200)
	eq(t, "live call list", num(list, "total"), 1)
	c.do(ro, "GET", "/v1/live/calls?limit=abc", nil, 400)
	c.do(ro, "POST", "/v1/live/calls/hangup", map[string]any{"call_id": "x@y"}, 403)
	c.do(root, "POST", "/v1/live/calls/hangup", map[string]any{"call_id": "x@y"}, 204)
	if hungUp != "x@y" {
		t.Errorf("hangup reached the engine as %q", hungUp)
	}

	// --- a connection's settings ---
	// The detailed settings are plain fields; what the database does not
	// take is refused.
	adv := c.do(root, "POST", "/v1/vendor-connections", map[string]any{"vendor_id": v1, "name": "detailed", "host": "sip.vendor.example",
		"number_plus": true, "cli_strip_digits": 2, "cli_add_prefix": "+", "pai_mode": "caller", "ring_timeout_sec": 20, "max_duration_sec": 3600,
		"failover_mode": "custom", "failover_codes": []int{486, 503}, "options_interval_sec": 30, "codecs": []string{"PCMA", "G729"}, "transcode": true,
		"register": true, "register_expires_sec": 600, "register_domain": "voip.vendor.example"}, 201)
	asked = num(adv, "id")
	if adv["auth_set"] != false || adv["auth_username"] != "" {
		t.Errorf("a new connection has no sign-in: %v", adv)
	}
	// Codec translation is off unless asked for (T-09).
	plainConn := c.do(root, "POST", "/v1/vendor-connections", map[string]any{"vendor_id": v1, "name": "plain", "host": "sip2.vendor.example"}, 201)
	if plainConn["transcode"] != false || adv["transcode"] != true {
		t.Errorf("transcode as the API shows it: %v / %v", plainConn["transcode"], adv["transcode"])
	}
	// Registering with the vendor is off unless asked for (T-14), for an
	// hour by default.
	if plainConn["register"] != false || num(plainConn, "register_expires_sec") != 3600 || plainConn["register_domain"] != "" || adv["register"] != true {
		t.Errorf("registration as the API shows it: %v / %v", plainConn, adv["register"])
	}
	for field, bad := range map[string]any{"codecs": []string{"MP3"}, "pai_mode": "hide", "ring_timeout_sec": 2, "max_duration_sec": 9000,
		"failover_mode": "sometimes", "failover_codes": []int{200}, "options_interval_sec": 3, "cli_add_prefix": "abc", "transcode": "maybe",
		"register_expires_sec": 30, "register_domain": "a b"} {
		c.do(root, "PATCH", fmt.Sprintf("/v1/vendor-connections/%d", asked), map[string]any{field: bad}, 422)
	}
	// The sign-in password cannot be written as a field, only through its
	// own operation, and nothing gives it back.
	c.do(root, "PATCH", fmt.Sprintf("/v1/vendor-connections/%d", asked), map[string]any{"auth_secret": "x"}, 400)
	c.do(root, "PATCH", fmt.Sprintf("/v1/vendor-connections/%d", asked), map[string]any{"auth_username": "x"}, 400)
	signIn := fmt.Sprintf("/v1/vendor-connections/%d/sign-in", asked)
	c.do(root, "PUT", signIn, map[string]any{"username": "acme", "password": ""}, 422)
	c.do(root, "PUT", signIn, map[string]any{"username": "ac me", "password": "x"}, 422)
	c.do(ro, "PUT", signIn, map[string]any{"username": "acme", "password": "vendor-Secret-1"}, 403)
	c.do(root, "PUT", "/v1/vendor-connections/999999/sign-in", map[string]any{"username": "acme", "password": "vendor-Secret-1"}, 404)
	c.do(root, "PUT", signIn, map[string]any{"username": "acme", "password": "vendor-Secret-1"}, 200)
	got := c.do(root, "GET", fmt.Sprintf("/v1/vendor-connections/%d", asked), nil, 200)
	if raw := fmt.Sprint(got); got["auth_set"] != true || got["auth_username"] != "acme" || strings.Contains(raw, "auth_secret") || strings.Contains(raw, "vendor-Secret-1") {
		t.Errorf("sign-in as the API shows it: %v", got)
	}
	var sealed []byte
	if err := pool.QueryRow(ctx, `select auth_secret from vendor_connections where id = $1`, asked).Scan(&sealed); err != nil || len(sealed) < 2 || bytes.Contains(sealed, []byte("vendor-Secret-1")) != (sealed[0] == 0) {
		t.Errorf("stored sign-in password: version %v, err %v", sealed[:1], err)
	}
	// The engine's loader opens it; a row copied onto another connection
	// does not open there.
	snap, err = srv.LoadSnapshot(ctx)
	if err != nil || snap.Connections[asked].AuthUser != "acme" || snap.Connections[asked].AuthPassword != "vendor-Secret-1" || snap.Connections[asked].AuthSealed != nil {
		t.Errorf("sign-in in the snapshot: %+v, %v", snap.Connections[asked], err)
	}
	if cn := snap.Connections[asked]; !cn.NumberPlus || cn.CLIStrip != 2 || cn.CLIAdd != "+" || cn.PAIMode != "caller" || cn.RingTimeout != 20 || cn.MaxDuration != 3600 ||
		cn.FailoverMode != "custom" || fmt.Sprint(cn.FailoverCodes) != "[486 503]" || cn.OptionsInterval != 30 || fmt.Sprint(cn.Codecs) != "[PCMA G729]" || !cn.Transcode ||
		!cn.Register || cn.RegisterExpires != 600 || cn.RegisterDomain != "voip.vendor.example" {
		t.Errorf("settings in the snapshot: %+v", cn)
	}
	if cn := snap.Connections[num(plainConn, "id")]; cn.Transcode {
		t.Errorf("a connection without the setting translates: %+v", cn)
	}
	c.do(root, "DELETE", fmt.Sprintf("/v1/vendor-connections/%d", num(plainConn, "id")), nil, 204)
	var audited int
	var leaked bool
	pool.QueryRow(ctx, `select count(*), coalesce(bool_or(detail::text like '%vendor-Secret-1%' or changes::text like '%vendor-Secret-1%'), false)
		from audit_log where resource = 'vendor-connections' and row_id = $1`, asked).Scan(&audited, &leaked)
	if audited < 2 || leaked {
		t.Errorf("audit of the connection: %d records, password in them: %v", audited, leaked)
	}
	c.do(root, "DELETE", signIn, nil, 200)
	got = c.do(root, "GET", fmt.Sprintf("/v1/vendor-connections/%d", asked), nil, 200)
	if got["auth_set"] != false || got["auth_username"] != "" {
		t.Errorf("sign-in after removal: %v", got)
	}
	// Testing a connection asks the engine about the connection's own
	// address and changes nothing; the health of a vendor's connections is
	// the engine's list cut down to that vendor's.
	auditsBefore := 0
	pool.QueryRow(ctx, `select count(*) from audit_log`).Scan(&auditsBefore)
	tried := c.do(root, "POST", fmt.Sprintf("/v1/vendor-connections/%d/test", asked), nil, 200)
	if tried["outcome"] != "answered" || num(tried, "sip_code") != 200 || !strings.Contains(probed.String(), `"host":"sip.vendor.example"`) || !strings.Contains(probed.String(), `"port":5060`) {
		t.Errorf("test of a connection: %v, asked %s", tried, probed.String())
	}
	c.do(ro, "POST", fmt.Sprintf("/v1/vendor-connections/%d/test", asked), nil, 403)
	c.do(root, "POST", "/v1/vendor-connections/999999/test", nil, 404)
	auditsAfter := 0
	pool.QueryRow(ctx, `select count(*) from audit_log`).Scan(&auditsAfter)
	eq(t, "audit records left by tests of a connection", int64(auditsAfter-auditsBefore), 0)
	healthAll := c.do(ro, "GET", fmt.Sprintf("/v1/vendor-connections/health?vendor_id=%d", v1), nil, 200)
	health := healthAll["connections"].(map[string]any)
	if len(health) != 1 || health[fmt.Sprint(asked)].(map[string]any)["state"] != "down" {
		t.Errorf("health of a vendor's connections: %v", health)
	}
	// So is the state of the switch's registrations with the vendor.
	if regs := healthAll["registrations"].(map[string]any); len(regs) != 1 || regs[fmt.Sprint(asked)].(map[string]any)["reason"] != "credentials" {
		t.Errorf("registrations of a vendor's connections: %v", regs)
	}
	c.do(ro, "GET", "/v1/vendor-connections/health", nil, 400)
	c.do(root, "DELETE", fmt.Sprintf("/v1/vendor-connections/%d", asked), nil, 204)
	// A trunk's settings.
	detailed := c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": clID, "name": "detailed", "addresses": []string{"10.77.0.1"},
		"number_strip": "00", "number_add": "", "max_duration_sec": 1800, "max_calls": 30, "max_cps": 5, "codecs": []string{"PCMU"}, "transcode": true}, 201)
	for field, bad := range map[string]any{"number_strip": "abc", "number_add": "+90", "max_duration_sec": 3, "max_calls": -1, "codecs": []string{"pcma"}} {
		c.do(root, "PATCH", fmt.Sprintf("/v1/client-trunks/%d", num(detailed, "id")), map[string]any{field: bad}, 422)
	}
	snap, _ = srv.LoadSnapshot(ctx)
	if tk, ok := snap.Trunk(num(detailed, "id")); !ok || fmt.Sprint(tk.NumberRules) != "[{00 }]" || tk.MaxDuration != 1800 || tk.MaxCalls != 30 || tk.MaxCPS != 5 || fmt.Sprint(tk.Codecs) != "[PCMU]" || !tk.Transcode {
		t.Errorf("trunk settings in the snapshot: %+v", tk)
	}
	// Number rules (T-17): the list is kept longest strip first, the two
	// old fields show it while it has one rule and still write one rule;
	// a bad list is refused whole.
	same(t, "old fields written as a list of one", detailed["number_rules"], "[map[add: strip:00]]")
	dPath := fmt.Sprintf("/v1/client-trunks/%d", num(detailed, "id"))
	multi := c.do(root, "PATCH", dPath, map[string]any{"number_rules": []map[string]string{{"strip": "0", "add": "90"}, {"strip": "+", "add": ""}, {"strip": "00", "add": ""}}}, 200)
	same(t, "rules sorted longest strip first", multi["number_rules"], "[map[add: strip:00] map[add: strip:+] map[add:90 strip:0]]")
	same(t, "old fields with several rules", fmt.Sprint(multi["number_strip"], "/", multi["number_add"]), "/")
	var ruleAudit string
	pool.QueryRow(ctx, `select changes::text from audit_log where resource = 'client-trunks' and row_id = $1 and action = 'update' order by id desc limit 1`, num(detailed, "id")).Scan(&ruleAudit)
	if !strings.Contains(ruleAudit, `"number_rules"`) || !strings.Contains(ruleAudit, `"strip": "+"`) {
		t.Errorf("audit of a change of number rules: %s", ruleAudit)
	}
	snap, _ = srv.LoadSnapshot(ctx)
	if tk, _ := snap.Trunk(num(detailed, "id")); fmt.Sprint(tk.NumberRules) != "[{00 } {+ } {0 90}]" || tk.Dialled("00905321112233") != "905321112233" ||
		tk.Dialled("+905321112233") != "905321112233" || tk.Dialled("05321112233") != "905321112233" {
		t.Errorf("number rules in the snapshot: %+v", tk.NumberRules)
	}
	// Other fields written alone leave the list as it is.
	same(t, "rules kept over an unrelated change", c.do(root, "PATCH", dPath, map[string]any{"max_calls": 31}, 200)["number_rules"],
		"[map[add: strip:00] map[add: strip:+] map[add:90 strip:0]]")
	for what, bad := range map[string]any{
		"not a list": map[string]string{"strip": "00"}, "letters": []map[string]string{{"strip": "ab", "add": ""}},
		"plus in add": []map[string]string{{"strip": "0", "add": "+90"}}, "nothing to do": []map[string]string{{"strip": "", "add": ""}},
		"same strip twice": []map[string]string{{"strip": "00", "add": ""}, {"strip": "00", "add": "1"}},
		"unknown key":      []map[string]string{{"strip": "00", "prefix": "1"}}, "not an object": []string{"00"}, "null": nil,
	} {
		if got := c.do(root, "PATCH", dPath, map[string]any{"number_rules": bad}, 422); got["error"] == "" {
			t.Errorf("%s: no reason given", what)
		}
	}
	tooMany := []map[string]string{}
	for i := 0; i < 21; i++ {
		tooMany = append(tooMany, map[string]string{"strip": fmt.Sprint(100 + i), "add": ""})
	}
	c.do(root, "PATCH", dPath, map[string]any{"number_rules": tooMany}, 422)
	// An old client that writes the two fields replaces the list with one
	// rule; both empty clears it.
	same(t, "old fields over several rules", c.do(root, "PATCH", dPath, map[string]any{"number_strip": "0", "number_add": "49"}, 200)["number_rules"], "[map[add:49 strip:0]]")
	same(t, "old fields cleared", c.do(root, "PATCH", dPath, map[string]any{"number_strip": "", "number_add": ""}, 200)["number_rules"], "[]")
	same(t, "add alone: every number", c.do(root, "PATCH", dPath, map[string]any{"number_add": "90"}, 200)["number_rules"], "[map[add:90 strip:]]")
	same(t, "list cleared", fmt.Sprint(c.do(root, "PATCH", dPath, map[string]any{"number_rules": []any{}}, 200)["number_add"]), "")
	nr := c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": clID, "name": "rules-new", "addresses": []string{"10.77.0.2"},
		"number_rules": []map[string]string{{"strip": "+"}, {"strip": "00", "add": ""}}}, 201)
	same(t, "a new trunk with a list", fmt.Sprint(nr["number_rules"], nr["number_strip"]), "[map[add: strip:00] map[add: strip:+]]")
	c.do(root, "DELETE", fmt.Sprintf("/v1/client-trunks/%d", num(nr, "id")), nil, 204)
	c.do(root, "DELETE", fmt.Sprintf("/v1/client-trunks/%d", num(detailed, "id")), nil, 204)
	c.do(root, "POST", "/v1/live/calls/hangup", map[string]any{"call_id": "gone"}, 404)

	// --- registrations: where a trunk that signs in is reached right now ---
	// Kept by the engine, shown per client and per trunk, and removed on an
	// administrator's order, which the audit log records.
	regs := func(key, path string) []any {
		t.Helper()
		list, _ := c.do(key, "GET", path, nil, 200)["registrations"].([]any)
		return list
	}
	c.do(ro, "GET", "/v1/registrations", nil, 400) // of which client?
	c.do(ro, "GET", "/v1/registrations?client_id=999999", nil, 404)
	if list := regs(ro, fmt.Sprintf("/v1/registrations?client_id=%d", clID)); len(list) != 2 ||
		num(list[0].(map[string]any), "trunk_id") != office || list[0].(map[string]any)["src_ip"] != "2001:db8::50" ||
		list[0].(map[string]any)["until"] != "2026-11-22T10:01:00Z" || list[0].(map[string]any)["nat"] != true {
		t.Errorf("registrations of the client: %v", list)
	}
	eq(t, "registrations of the trunk", int64(len(regs(ro, fmt.Sprintf("/v1/client-trunks/%d/registrations", office)))), 2)
	eq(t, "a trunk known by its address has none", int64(len(regs(ro, fmt.Sprintf("/v1/client-trunks/%d/registrations", num(trunk, "id"))))), 0)
	c.do(ro, "GET", "/v1/client-trunks/999999/registrations", nil, 404)
	c.do(ro, "DELETE", fmt.Sprintf("/v1/client-trunks/%d/registrations", office), nil, 403)
	c.do(root, "DELETE", fmt.Sprintf("/v1/client-trunks/%d/registrations?contact=sip%%3Anobody%%4010.9.9.9", office), nil, 404)
	c.do(root, "DELETE", fmt.Sprintf("/v1/client-trunks/%d/registrations?contact=sip%%3Apbx%%4010.0.0.6%%3A5060", office), nil, 200)
	c.do(root, "DELETE", fmt.Sprintf("/v1/client-trunks/%d/registrations", office), nil, 200)
	same(t, "what the engine was told to remove", strings.Join(unbound, "|"), fmt.Sprintf("%d sip:pbx@10.0.0.6:5060|%d ", office, office))
	c.do(root, "DELETE", fmt.Sprintf("/v1/client-trunks/%d/registrations", office), nil, 404) // nothing left
	logged, _ := c.raw(root, "GET", fmt.Sprintf("/v1/audit-log?resource=client-trunks&action=unregister&row_id=%d", office), nil, 200).([]any)
	if len(logged) != 2 {
		t.Fatalf("removals in the audit log: %v", logged)
	}
	if first := logged[1].(map[string]any); first["label"] != "office" || !strings.Contains(fmt.Sprint(first["detail"]), "sip:pbx@10.0.0.6:5060") ||
		!strings.Contains(fmt.Sprint(first["detail"]), "[2001:db8::50]:40000") || strings.Contains(fmt.Sprint(first["detail"]), "10.0.0.5") {
		t.Errorf("audit record of a removal: %v", first)
	}
	// How many places a trunk may be registered from at once.
	eq(t, "contacts by default", num(c.do(root, "GET", fmt.Sprintf("/v1/client-trunks/%d", office), nil, 200), "max_contacts"), 1)
	c.do(root, "PATCH", fmt.Sprintf("/v1/client-trunks/%d", office), map[string]any{"max_contacts": 3}, 200)
	c.do(root, "PATCH", fmt.Sprintf("/v1/client-trunks/%d", office), map[string]any{"max_contacts": 0}, 422)
	c.do(root, "PATCH", fmt.Sprintf("/v1/client-trunks/%d", office), map[string]any{"max_contacts": 11}, 422)

	// --- call traces ---
	// Read from ClickHouse, which a stand-in plays here: it has two messages
	// for any call and no archived records.
	c.do(root, "GET", "/v1/trace?call_id=fx-1", nil, 503) // no ClickHouse
	stored := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q, _ := io.ReadAll(r.Body)
		if strings.Contains(string(q), "from cdrs") {
			io.WriteString(w, "0\n")
			return
		}
		raw := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
		fmt.Fprintf(w, `{"at":1795000000000001,"seq":1,"direction":"in","transport":"udp","src_ip":"192.0.2.1","src_port":5060,"dst_ip":"192.0.2.10","dst_port":5060,"raw":"%s"}`+"\n",
			raw("INVITE sip:1@192.0.2.10 SIP/2.0\r\nCall-ID: fx-1\r\n\r\n"))
		fmt.Fprintf(w, `{"at":1795000000020000,"seq":2,"direction":"out","transport":"tls","src_ip":"192.0.2.10","src_port":5061,"dst_ip":"192.0.2.1","dst_port":40000,"raw":"%s"}`+"\n",
			raw("SIP/2.0 200 OK\r\nCall-ID: fx-1\r\n\r\n"))
	}))
	defer stored.Close()
	ch, err := clickhouse.New(stored.URL + "/softswitch")
	if err != nil {
		t.Fatal(err)
	}
	srv.SetAnalytics(ch)
	c.do(root, "GET", "/v1/trace", nil, 400)
	tr := c.do(ro, "GET", "/v1/trace?call_id=fx-1", nil, 200)
	msgs, _ := tr["messages"].([]any)
	if len(msgs) != 2 || tr["truncated"] != false || msgs[0].(map[string]any)["line"] != "INVITE sip:1@192.0.2.10 SIP/2.0" ||
		msgs[0].(map[string]any)["at"] != "2026-11-18T11:06:40.000001Z" || msgs[1].(map[string]any)["transport"] != "tls" {
		t.Errorf("trace: %v", tr)
	}
	// An account that sees every client also sees a call without a record.
	c.do(root, "GET", "/v1/trace?call_id=in-progress", nil, 200)
	// The live calls screen links each row to its trace (T-25): a read-only
	// caller who may list live calls may read the trace of one, too.
	c.do(ro, "GET", "/v1/trace?call_id=in-progress", nil, 200)
	pcapReq, _ := http.NewRequest("GET", ts.URL+"/v1/trace/pcap?call_id=fx-1", nil)
	pcapReq.Header.Set("Authorization", "Bearer "+root)
	pcap, err := http.DefaultClient.Do(pcapReq)
	if err != nil {
		t.Fatal(err)
	}
	file, _ := io.ReadAll(pcap.Body)
	pcap.Body.Close()
	if pcap.StatusCode != 200 || pcap.Header.Get("Content-Type") != "application/vnd.tcpdump.pcap" ||
		pcap.Header.Get("Content-Disposition") != "attachment; filename=fx-1.pcap" || len(file) < 24 || !bytes.HasPrefix(file, []byte{0xd4, 0xc3, 0xb2, 0xa1}) {
		t.Errorf("capture file: status %d, headers %v, %d bytes", pcap.StatusCode, pcap.Header, len(file))
	}
	// Another account has no record of the call among its clients: for it
	// the call does not exist, whether it does or not.
	if _, err := pool.Exec(ctx, `with r as (insert into resellers (name) values ('elsewhere') returning id)
		insert into api_keys (reseller_id, name, key_hash) select id, 'elsewhere', $1 from r`, HashKey("elsewhere-key")); err != nil {
		t.Fatal(err)
	}
	c.do("elsewhere-key", "GET", "/v1/trace?call_id=fx-1", nil, 404)
	c.do("elsewhere-key", "GET", "/v1/trace/pcap?call_id=fx-1", nil, 404)
	c.do("elsewhere-key", "GET", "/v1/trace?call_id=in-progress", nil, 404)
	// Nor does it get the live calls, so no link leads it to a refused trace.
	c.do("elsewhere-key", "GET", "/v1/live/calls", nil, 403)
}
