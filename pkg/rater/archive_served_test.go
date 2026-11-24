// SPDX-License-Identifier: AGPL-3.0-only

package rater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"softswitch/core/pkg/api"
	"softswitch/core/pkg/clickhouse"
	pgstore "softswitch/core/pkg/store"
)

// TestArchiveServed archives call records and asks the API for them again:
// the single record, the list (paged across the archive horizon) and the
// CSV must answer exactly as they did while PostgreSQL still had them
// (T-24). Needs what TestArchive needs: `make test-archive`.
func TestArchiveServed(t *testing.T) {
	pgURL, chURL := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_CLICKHOUSE_URL")
	if pgURL == "" || chURL == "" {
		t.Skip("TEST_DATABASE_URL and TEST_CLICKHOUSE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgstore.Open(ctx, pgURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pgstore.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	ch, err := clickhouse.New(chURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := clickhouse.Migrate(ctx, ch); err != nil {
		t.Fatal(err)
	}

	// An account of its own, with a client, a vendor and a trunk; another
	// account must see none of its records.
	tag := fmt.Sprint(time.Now().UnixNano())
	var reseller, other, client, vendor, conn, trunk int64
	for _, q := range []struct {
		sql  string
		args []any
		into *int64
	}{
		{`insert into resellers (name) values ('served ' || $1) returning id`, []any{tag}, &reseller},
		{`insert into resellers (name) values ('elsewhere ' || $1) returning id`, []any{tag}, &other},
		{`insert into clients (reseller_id, name) values ($1, 'Served client') returning id`, []any{&reseller}, &client},
		{`insert into vendors (reseller_id, name) values ($1, 'Served vendor ' || $2) returning id`, []any{&reseller, tag}, &vendor},
		{`insert into vendor_connections (vendor_id, name, host) values ($1, 'served-a', '10.9.9.9') returning id`, []any{&vendor}, &conn},
		{`insert into client_trunks (client_id, name, auth_type, username) values ($1, 'served trunk', 'digest', 'served-' || $2) returning id`, []any{&client, tag}, &trunk},
	} {
		args := make([]any, len(q.args))
		for i, a := range q.args {
			if p, ok := a.(*int64); ok {
				a = *p
			}
			args[i] = a
		}
		if err := pool.QueryRow(ctx, q.sql, args...).Scan(q.into); err != nil {
			t.Fatal(err)
		}
	}
	key, otherKey := "served-"+tag, "elsewhere-"+tag
	for k, r := range map[string]int64{key: reseller, otherKey: other} {
		if _, err := pool.Exec(ctx, `insert into api_keys (reseller_id, name, key_hash) values ($1, 'served', $2)`, r, api.HashKey(k)); err != nil {
			t.Fatal(err)
		}
	}

	// Three records old enough to archive, two that stay. They carry every
	// kind of value: an answer with a fraction of a second, a refusal with
	// no vendor, a call delivered to a trunk.
	const day = 24 * time.Hour
	now := time.Now().UTC().Truncate(time.Second)
	add := func(name string, age time.Duration, answered bool, vendorLeg bool, toTrunk bool) int64 {
		start := now.Add(-age).Add(123456 * time.Microsecond)
		var answer *time.Time
		code, dur := 403, 0
		if answered {
			a := start.Add(2500 * time.Millisecond)
			answer, code, dur = &a, 200, 61
		}
		var v, c, dt any
		if vendorLeg {
			v, c = vendor, conn
		}
		if toTrunk {
			dt = trunk
		}
		var id int64
		err := pool.QueryRow(ctx, `
			insert into cdrs (call_id, leg, client_id, trunk_id, vendor_id, connection_id, src, dst, src_ip, sell_prefix, buy_prefix,
			  start_at, answer_at, end_at, duration_sec, sip_code, sell_billed_sec, buy_billed_sec, sell_amount, buy_amount,
			  sell_base, buy_base, pdd_ms, bundle_sec, dst_trunk_id)
			values ($1, 1, $2, $3, $4, $5, '4420', '905321112233', '10.0.0.5', '9053', case when $4::bigint is null then null else '90' end,
			  $6, $7, $6::timestamptz + interval '64 seconds', $8, $9, 120, 61, 1234567, 345678, 1234560, 345670, 1800, 60, $10)
			returning id`,
			fmt.Sprintf("%s-%s", name, tag), client, trunk, v, c, start, answer, dur, code, dt).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `insert into cdr_export_queue (cdr_id) values ($1) on conflict do nothing`, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	old := []int64{add("old-answered", 100*day, true, true, false), add("old-refused", 110*day, false, false, false), add("old-delivered", 120*day, true, false, true)}
	recent := []int64{add("recent-a", 10*day, true, true, false), add("recent-b", 20*day, false, true, false)}
	for {
		n, err := exportBatchOnce(ctx, pool, ch)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}

	srv := api.New(pool)
	srv.SetAnalytics(ch)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	get := func(k, path string, want int) string {
		t.Helper()
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+k)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("GET %s: %d, want %d: %s", path, resp.StatusCode, want, body)
		}
		return string(body)
	}
	cl := fmt.Sprint(client)
	from := url.QueryEscape(now.Add(-200 * day).Format(time.RFC3339))
	lists := []string{
		"client_id=" + cl, "client_id=" + cl + "&limit=2", "client_id=" + cl + "&limit=2&offset=2", "client_id=" + cl + "&limit=2&offset=4",
		"client_id=" + cl + "&limit=1&offset=3", "client_id=" + cl + "&from=" + from, "client_id=" + cl + "&sip_code=200",
		"client_id=" + cl + "&vendor_id=" + fmt.Sprint(vendor), "client_id=" + cl + "&dst_trunk_id=" + fmt.Sprint(trunk),
		"call_id=old-refused-" + tag, "client_id=" + cl + "&to=" + url.QueryEscape(now.Add(-105*day).Format(time.RFC3339)),
		"client_id=" + cl + "&dst=9053211", "client_id=" + cl + "&client_id=abc",
	}
	csvs := []string{"client_id=" + cl, "client_id=" + cl + "&from=" + from, "client_id=" + cl + "&sip_code=403", "call_id=old-delivered-" + tag}
	type answers struct{ lists, csvs, records []string }
	ask := func() (a answers) {
		for _, q := range lists {
			a.lists = append(a.lists, get(key, "/v1/cdrs?"+q, 200))
		}
		for _, q := range csvs {
			a.csvs = append(a.csvs, get(key, "/v1/cdrs/export?"+q, 200))
		}
		for _, id := range append(old, recent...) {
			a.records = append(a.records, get(key, fmt.Sprintf("/v1/cdrs/%d", id), 200))
			get(otherKey, fmt.Sprintf("/v1/cdrs/%d", id), 404)
		}
		return a
	}
	before := ask()
	if n := strings.Count(before.lists[0], `"call_id"`); n != 5 {
		t.Fatalf("before archiving the list shows %d records, want 5", n)
	}

	deleted, _, err := archiveOnce(ctx, pool, ch, now.Add(-90*day))
	if err != nil || deleted < 3 {
		t.Fatalf("archiving: deleted %d, %v", deleted, err)
	}
	var kept int
	pool.QueryRow(ctx, `select count(*) from cdrs where client_id = $1`, client).Scan(&kept)
	if kept != 2 {
		t.Fatalf("%d of the client's records left in PostgreSQL, want 2", kept)
	}
	var horizon time.Time
	if err := pool.QueryRow(ctx, `select horizon from cdr_archive`).Scan(&horizon); err != nil || horizon.Before(now.Add(-90*day)) {
		t.Fatalf("archive horizon %v, %v", horizon, err)
	}

	after := ask()
	// Keys and values alike; only the order of keys in an object may differ.
	sameJSON := func(what, a, b string) {
		t.Helper()
		var x, y any
		if err := json.Unmarshal([]byte(a), &x); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		json.Unmarshal([]byte(b), &y)
		xs, _ := json.Marshal(x)
		ys, _ := json.Marshal(y)
		if string(xs) != string(ys) {
			t.Errorf("%s differs after archiving:\n before %s\n after  %s", what, ys, xs)
		}
	}
	for i, q := range lists {
		sameJSON("list "+q, after.lists[i], before.lists[i])
	}
	for i, id := range append(old, recent...) {
		sameJSON(fmt.Sprintf("record %d", id), after.records[i], before.records[i])
	}
	for i, q := range csvs {
		if after.csvs[i] != before.csvs[i] {
			t.Errorf("CSV %s differs after archiving:\n before %q\n after  %q", q, before.csvs[i], after.csvs[i])
		}
	}
	if lines := strings.Count(after.csvs[0], "\n"); lines != 6 {
		t.Errorf("CSV has %d lines, want a header and 5 records", lines)
	}
	get(key, "/v1/cdrs/999999999999", 404)
	if got := get(otherKey, "/v1/cdrs?client_id="+cl, 200); got != "[]" {
		t.Errorf("another account sees archived records: %s", got)
	}
	if got := get(otherKey, "/v1/cdrs/export?client_id="+cl, 200); strings.Count(got, "\n") != 1 {
		t.Errorf("another account exports archived records: %q", got)
	}
}
