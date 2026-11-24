// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFxDue(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for now, want := range map[string]string{
		"2026-11-23T15:29:59Z":      "2026-11-22T15:30:00Z", // before today's publication: yesterday's
		"2026-11-23T15:30:00Z":      "2026-11-23T15:30:00Z",
		"2026-11-23T23:59:00Z":      "2026-11-23T15:30:00Z",
		"2026-11-24T01:00:00+03:00": "2026-11-23T15:30:00Z", // the zone of now does not matter
	} {
		if got := fxDue(at(now)); !got.Equal(at(want)) {
			t.Errorf("due at %s: %s, want %s", now, got.UTC().Format(time.RFC3339), want)
		}
	}
}

func TestExchangeRateFeedFromEnv(t *testing.T) {
	for _, c := range []struct {
		source, url, want string
		ok                bool
	}{
		{"", "", "", true},
		{"off", "", "", true},
		{"ecb", "", "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml", true},
		{"ecb", "http://127.0.0.1:9/daily.xml", "http://127.0.0.1:9/daily.xml", true},
		{"ecb", "ftp://example.org/x", "", false},
		{"", "http://127.0.0.1:9/daily.xml", "", false},
		{"fed", "", "", false},
	} {
		t.Setenv("FX_SOURCE", c.source)
		t.Setenv("FX_URL", c.url)
		got, err := ExchangeRateFeedFromEnv()
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("FX_SOURCE=%q FX_URL=%q: %q, %v", c.source, c.url, got, err)
		}
	}
}

// ecbDay builds a published file for a day from "CUR=rate" pairs.
func ecbDay(day string, rates ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref"><Cube><Cube time='` + day + `'>`)
	for _, r := range rates {
		cur, rate, _ := strings.Cut(r, "=")
		fmt.Fprintf(&b, `<Cube currency='%s' rate='%s'/>`, cur, rate)
	}
	b.WriteString(`</Cube></Cube></gesmes:Envelope>`)
	return b.String()
}

// testExchangeRateFeed runs in TestEndToEnd once the account (in USD) has a
// typed-in EUR rate (1.1) and a client in EUR (paris, 0.03 EUR a minute to
// 90) with one rated call (fx-1, 0.036 USD). The published rates come from
// a local server: tests never ask the bank.
func testExchangeRateFeed(t *testing.T, ctx context.Context, pool *pgxpool.Pool, srv *Server, c client, root, ro string, eur int64, changed func(bool, string)) {
	simulate := func() int64 {
		return num(c.do(root, "POST", "/v1/route/simulate", map[string]any{"src_ip": "10.0.0.88", "dst": "905321112233"}, 200), "sell_price_base")
	}
	rateOf := func(id int64) map[string]any {
		return c.do(root, "GET", fmt.Sprintf("/v1/exchange-rates/%d", id), nil, 200)
	}

	// Off by default: nothing is fetched, nothing can follow.
	if st := c.do(root, "GET", "/v1/exchange-rate-feed", nil, 200); st["enabled"] != false || st["published"] != nil {
		t.Errorf("feed off: %v", st)
	}
	c.do(root, "POST", "/v1/exchange-rate-feed/fetch", nil, 409)
	c.do(root, "POST", "/v1/exchange-rate-feed/follow", map[string]any{"currency": "EUR"}, 409)
	if same := rateOf(eur); same["source"] != "manual" || same["published"] != nil {
		t.Errorf("a rate from before is typed in: %v", same)
	}

	day1, err := os.ReadFile("../fx/testdata/eurofxref-daily.xml") // 2026-11-20: USD 1.0939, JPY 174.01, GBP 0.83673
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	status, body := 200, string(day1)
	serve := func(code int, file string) { mu.Lock(); status, body = code, file; mu.Unlock() }
	bank := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	defer bank.Close()
	srv.SetExchangeRateFeed(bank.URL + "/eurofxref-daily.xml")
	defer srv.SetExchangeRateFeed("")
	gbp := num(c.do(root, "POST", "/v1/exchange-rates", map[string]any{"currency": "GBP", "rate": 1_250_000}, 201), "id")
	c.do(root, "POST", "/v1/exchange-rate-feed/follow", map[string]any{"currency": "EUR"}, 409) // nothing fetched yet
	changed(true, "adding a rate")

	// The first fetch keeps the table; every rate is still typed in.
	if res := c.do(root, "POST", "/v1/exchange-rate-feed/fetch", nil, 200); res["published"] != "2026-11-20" || num(res, "changed") != 0 {
		t.Errorf("first fetch: %v", res)
	}
	changed(true, "a fetch through the API")
	st := c.do(root, "GET", "/v1/exchange-rate-feed", nil, 200)
	if st["enabled"] != true || st["source"] != "ecb" || st["published"] != "2026-11-20" || st["error"] != "" || st["fetched_at"] == nil {
		t.Errorf("status after a fetch: %v", st)
	}
	if cs, _ := st["currencies"].([]any); len(cs) != 11 || cs[0] != "CHF" || cs[2] != "EUR" {
		t.Errorf("currencies published: %v", st["currencies"])
	}
	eq(t, "typed-in rate after a fetch", num(rateOf(eur), "rate"), 1_100_000)

	// Following: the rate is crossed into the account's USD at once.
	c.do(ro, "POST", "/v1/exchange-rate-feed/follow", map[string]any{"currency": "EUR"}, 403)
	c.do(root, "POST", "/v1/exchange-rate-feed/follow", map[string]any{"currency": "eur"}, 422)
	c.do(root, "POST", "/v1/exchange-rate-feed/follow", map[string]any{"currency": "XYZ"}, 422)
	f := c.do(root, "POST", "/v1/exchange-rate-feed/follow", map[string]any{"currency": "EUR"}, 200)
	if num(f, "id") != eur || num(f, "rate") != 1_093_900 || f["source"] != "ecb" || f["published"] != "2026-11-20" {
		t.Errorf("following EUR: %v", f)
	}
	eq(t, "decision at the published rate", simulate(), 32_817) // 0.03 EUR × 1.0939
	jpy := num(c.do(root, "POST", "/v1/exchange-rate-feed/follow", map[string]any{"currency": "JPY"}, 200), "id")
	eq(t, "JPY crossed through the euro", num(rateOf(jpy), "rate"), 6_286) // 1.0939 / 174.01
	var audited int
	if err := pool.QueryRow(ctx, `select count(*) from audit_log where resource = 'exchange-rates' and row_id = $1 and action = 'update' and actor_type <> 'system'`, eur).Scan(&audited); err != nil || audited < 2 {
		t.Errorf("following is audited: %d, %v", audited, err)
	}
	changed(true, "following")

	// The scheduled fetch is not due again the same day.
	if res, err := srv.FetchExchangeRates(ctx); err != nil || res.Fetched {
		t.Errorf("scheduled fetch right after one: %+v, %v", res, err)
	}

	// A new day: following rates move, the typed-in GBP stays. The engine
	// hears of it from the fetch itself.
	serve(200, ecbDay("2026-11-23", "USD=1.1050", "JPY=170.00", "GBP=0.8500"))
	res, err := srv.fetchExchangeRates(ctx, fxNow, time.Now(), nil, Caller{})
	if err != nil || !res.Fetched || res.Changed != 2 || len(res.Missing) != 0 {
		t.Errorf("second day: %+v, %v", res, err)
	}
	changed(true, "a background fetch that changed rates")
	eq(t, "EUR on the second day", num(rateOf(eur), "rate"), 1_105_000)
	eq(t, "JPY on the second day", num(rateOf(jpy), "rate"), 6_500)
	if g := rateOf(gbp); num(g, "rate") != 1_250_000 || g["source"] != "manual" {
		t.Errorf("a typed-in rate was overwritten: %v", g)
	}
	eq(t, "decision after the feed moved", simulate(), 33_150)
	if err := pool.QueryRow(ctx, `select count(*) from audit_log where resource = 'exchange-rates' and row_id = $1 and actor_type = 'system' and changes ? 'rate'`, eur).Scan(&audited); err != nil || audited != 1 {
		t.Errorf("the feed's change is audited: %d, %v", audited, err)
	}
	// The same file again changes nothing and tells nobody.
	if res, err := srv.fetchExchangeRates(ctx, fxNow, time.Now(), nil, Caller{}); err != nil || res.Changed != 0 {
		t.Errorf("same file again: %+v, %v", res, err)
	}
	changed(false, "a fetch that changed nothing")

	// A call rated before keeps its amount in the account's currency.
	rec := c.raw(root, "GET", "/v1/cdrs?call_id=fx-1", nil, 200).([]any)[0].(map[string]any)
	eq(t, "an old call record after the rates moved", num(rec, "sell_base"), 36_000)

	// The source fails: the last rates stay, the failure is counted and shown.
	failures := mFXFailures.Value()
	serve(503, "maintenance")
	c.do(root, "POST", "/v1/exchange-rate-feed/fetch", nil, 502)
	if mFXFailures.Value() != failures+1 {
		t.Errorf("failure not counted: %d → %d", failures, mFXFailures.Value())
	}
	st = c.do(root, "GET", "/v1/exchange-rate-feed", nil, 200)
	if msg, _ := st["error"].(string); !strings.Contains(msg, "503") || strings.Contains(msg, bank.URL) || st["failed_at"] == nil || st["published"] != "2026-11-23" {
		t.Errorf("status after a failure: %v", st)
	}
	eq(t, "rate kept when the source fails", num(rateOf(eur), "rate"), 1_105_000)
	serve(200, "<html>not the file</html>")
	if _, err := srv.fetchExchangeRates(ctx, fxNow, time.Now(), nil, Caller{}); err == nil {
		t.Error("an unreadable file was taken")
	}
	eq(t, "rate kept when the file is unreadable", num(rateOf(eur), "rate"), 1_105_000)
	// A scheduled fetch after a failure waits an hour, even when due.
	if _, err := pool.Exec(ctx, `update exchange_rate_fetch set fetched_at = now() - interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	serve(200, ecbDay("2026-11-24", "USD=1.1050", "GBP=0.8500"))
	if res, err := srv.FetchExchangeRates(ctx); err != nil || res.Fetched {
		t.Errorf("scheduled fetch within an hour of a failure: %+v, %v", res, err)
	}
	if _, err := pool.Exec(ctx, `update exchange_rate_fetch set failed_at = now() - interval '2 hours'`); err != nil {
		t.Fatal(err)
	}
	// Due now: JPY is no longer published, so its last rate stays and is named.
	res, err = srv.FetchExchangeRates(ctx)
	if err != nil || !res.Fetched || res.Changed != 0 || len(res.Missing) != 1 || res.Missing[0] != "JPY/USD" {
		t.Errorf("scheduled fetch when due: %+v, %v", res, err)
	}
	st = c.do(root, "GET", "/v1/exchange-rate-feed", nil, 200)
	if m, _ := st["missing"].([]any); st["error"] != "" || len(m) != 1 || m[0] != "JPY/USD" {
		t.Errorf("status with a missing currency: %v", st)
	}
	if j := rateOf(jpy); num(j, "rate") != 6_500 || j["published"] != "2026-11-23" {
		t.Errorf("unpublished currency: %v", j)
	}
	if e := rateOf(eur); e["published"] != "2026-11-24" {
		t.Errorf("same rate on a new day moves the date: %v", e)
	}
	// An older file (a stale mirror) changes nothing.
	serve(200, ecbDay("2026-11-20", "USD=2.0000", "JPY=100.00"))
	if res, err := srv.fetchExchangeRates(ctx, fxNow, time.Now(), nil, Caller{}); err != nil || res.Changed != 0 {
		t.Errorf("older file: %+v, %v", res, err)
	}
	eq(t, "rate after an older file", num(rateOf(eur), "rate"), 1_105_000)

	// Typing a rate in makes it manual, even at the same value; the next
	// fetch leaves it alone. Following again takes it back.
	if e := c.do(root, "PATCH", fmt.Sprintf("/v1/exchange-rates/%d", eur), map[string]any{"rate": 1_105_000}, 200); e["source"] != "manual" || e["published"] != nil {
		t.Errorf("typed in over a following rate: %v", e)
	}
	c.do(root, "PATCH", fmt.Sprintf("/v1/exchange-rates/%d", eur), map[string]any{"rate": 1_200_000}, 200)
	serve(200, ecbDay("2026-11-25", "USD=1.0000"))
	if res, err := srv.fetchExchangeRates(ctx, fxNow, time.Now(), nil, Caller{}); err != nil || !res.Fetched {
		t.Errorf("fetch after typing in: %+v, %v", res, err)
	}
	eq(t, "typed-in rate after a fetch", num(rateOf(eur), "rate"), 1_200_000)
	if e := c.do(root, "POST", "/v1/exchange-rate-feed/follow", map[string]any{"currency": "EUR"}, 200); num(e, "rate") != 1_000_000 || e["source"] != "ecb" {
		t.Errorf("following again: %v", e)
	}
	// The source column cannot be written through the generic routes.
	c.do(root, "PATCH", fmt.Sprintf("/v1/exchange-rates/%d", gbp), map[string]any{"source": "ecb"}, 400)
	// Leave the account as the rest of the test expects it.
	c.do(root, "PATCH", fmt.Sprintf("/v1/exchange-rates/%d", eur), map[string]any{"rate": 1_100_000}, 200)
	c.do(root, "DELETE", fmt.Sprintf("/v1/exchange-rates/%d", jpy), nil, 204)
	c.do(root, "DELETE", fmt.Sprintf("/v1/exchange-rates/%d", gbp), nil, 204)
	changed(true, "edits")
}
