// SPDX-License-Identifier: AGPL-3.0-only

package rater

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"softswitch/core/pkg/clickhouse"
	pgstore "softswitch/core/pkg/store"
)

// Needs a PostgreSQL database with the schema and a ClickHouse database it
// may fill: `make test-archive` provides both.
func TestArchive(t *testing.T) {
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
	if err := ch.Exec(ctx, "truncate table cdrs", nil); err != nil {
		t.Fatal(err)
	}
	var client int64
	err = pool.QueryRow(ctx, `
		with r as (insert into resellers (name) values ('archive ' || gen_random_uuid()) returning id)
		insert into clients (reseller_id, name) select id, 'archive' from r returning id`).Scan(&client)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().Add(-90 * 24 * time.Hour)
	// dstTrunk is the client's trunk the call went to; nil for a vendor leg.
	add := func(name string, age time.Duration, dstTrunk *int64) int64 {
		var id int64
		at := time.Now().Add(-age)
		err := pool.QueryRow(ctx, `insert into cdrs (call_id, leg, direction, client_id, src, dst, start_at, end_at, sip_code, dst_trunk_id)
			values ($1, 1, 'outbound', $2, '1', '90', $3, $3, 200, $4) returning id`, fmt.Sprintf("%s-%d", name, time.Now().UnixNano()), client, at, dstTrunk).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	const day = 24 * time.Hour
	toTrunk := int64(4242)
	oldCopied, oldCopied2 := add("old-copied", 100*day, nil), add("old-copied", 200*day, nil)
	oldAbsent := add("old-absent", 150*day, &toTrunk) // ClickHouse never got it
	oldQueued := add("old-queued", 120*day, nil)      // still waiting to be exported
	recent := add("recent", 10*day, nil)              // inside the time to keep
	if _, err := pool.Exec(ctx, `insert into cdr_export_queue (cdr_id) values ($1)`, oldQueued); err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for _, id := range []int64{oldCopied, oldCopied2, oldQueued, recent} {
		fmt.Fprintf(&body, `{"id":%d,"call_id":"c%d","leg":1,"client_id":%d,"start_at":"2020-01-01 00:00:00.000000","end_at":"2020-01-01 00:00:00.000000"}`+"\n", id, id, client)
	}
	if err := ch.Exec(ctx, "insert into cdrs format JSONEachRow", strings.NewReader(body.String())); err != nil {
		t.Fatal(err)
	}

	deleted, missing, err := archiveOnce(ctx, pool, ch, cutoff)
	if err != nil || deleted != 2 || missing != 1 {
		t.Fatalf("first pass: deleted %d, missing %d, %v", deleted, missing, err)
	}
	left := func(id int64) bool {
		var n int
		pool.QueryRow(ctx, `select count(*) from cdrs where id = $1`, id).Scan(&n)
		return n == 1
	}
	if left(oldCopied) || left(oldCopied2) {
		t.Error("a record ClickHouse has was kept")
	}
	if !left(oldAbsent) || !left(oldQueued) || !left(recent) {
		t.Error("a record that must stay was removed")
	}
	var queued int
	pool.QueryRow(ctx, `select count(*) from cdr_export_queue where cdr_id = $1`, oldAbsent).Scan(&queued)
	if queued != 1 {
		t.Error("the record ClickHouse lacks was not queued for export again")
	}
	// Nothing more to do: what is left is queued or recent.
	if deleted, missing, err := archiveOnce(ctx, pool, ch, cutoff); err != nil || deleted != 0 || missing != 0 {
		t.Errorf("second pass: deleted %d, missing %d, %v", deleted, missing, err)
	}

	// The export carries the whole record, the trunk a call went to
	// included; once ClickHouse has the two queued records they go too.
	// (The copy of oldQueued written by hand above has another start, so
	// the two copies are not merged: grouped by id.)
	if _, err := exportBatchOnce(ctx, pool, ch); err != nil {
		t.Fatal(err)
	}
	out, err := ch.Query(ctx, fmt.Sprintf(`select id, max(dst_trunk_id) from cdrs where id in (%d, %d) group by id order by id format TSV`, oldAbsent, oldQueued), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(out)), fmt.Sprintf("%d\t4242\n%d\t0", oldAbsent, oldQueued); got != want {
		t.Errorf("exported records: got %q want %q", got, want)
	}
	if deleted, missing, err := archiveOnce(ctx, pool, ch, cutoff); err != nil || deleted != 2 || missing != 0 {
		t.Errorf("after the export: deleted %d, missing %d, %v", deleted, missing, err)
	}
	if left(oldAbsent) || left(oldQueued) || !left(recent) {
		t.Error("exported old records were kept, or the recent one went")
	}
}
