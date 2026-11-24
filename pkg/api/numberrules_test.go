// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"io/fs"
	"os"
	"strings"
	"testing"

	"softswitch/core/db"
	"softswitch/core/pkg/store"
)

// before is the migration set up to, not including, one file: the schema
// as an older release left it.
type before struct {
	fs.FS
	first string
}

func (b before) ReadDir(name string) ([]fs.DirEntry, error) {
	all, err := fs.ReadDir(b.FS, name)
	var out []fs.DirEntry
	for _, e := range all {
		if e.Name() < b.first {
			out = append(out, e)
		}
	}
	return out, err
}

// Trunks written before number rules keep how they turn numbers: their
// two settings become a list of one rule, or none (T-17).
func TestNumberRulesMigration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	if !strings.HasSuffix(strings.SplitN(url, "?", 2)[0], "_test") {
		t.Fatalf("TEST_DATABASE_URL must point at a database whose name ends in _test")
	}
	ctx := context.Background()
	pool, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `drop schema public cascade; create schema public`); err != nil {
		t.Fatal(err)
	}
	const dir = "migrations/postgres"
	if err := store.MigrateFS(ctx, pool, before{db.Postgres, "0025_"}, dir); err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(ctx, pool, "root-test-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into clients (reseller_id, name) select id, 'old' from resellers;
		insert into client_trunks (client_id, name, addresses, number_strip, number_add)
		  select c.id, x.name, array[x.addr::inet], x.strip, x.add from clients c,
		    (values ('plain', '10.9.0.1', '', ''), ('zeros', '10.9.0.2', '00', ''), ('national', '10.9.0.3', '0', '90'), ('front', '10.9.0.4', '', '90'))
		    as x(name, addr, strip, add)`); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `select name || ' ' || number_rules::text || ' ' || number_strip || '/' || number_add from client_trunks order by id`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		got = append(got, s)
	}
	same(t, "trunks after the migration", strings.Join(got, "; "),
		`plain [] /; zeros [{"add": "", "strip": "00"}] 00/; national [{"add": "90", "strip": "0"}] 0/90; front [{"add": "90", "strip": ""}] /90`)
	snap, err := store.LoadSnapshot(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	// As the two settings did: 00… and 0… written numbers, and a code in
	// front of every number.
	want := map[string]string{
		"10.9.0.1/32": "00905321112233 05321112233 5321112233",
		"10.9.0.2/32": "905321112233 05321112233 5321112233",
		"10.9.0.3/32": "900905321112233 905321112233 5321112233",
		"10.9.0.4/32": "9000905321112233 9005321112233 905321112233",
	}
	if len(snap.Trunks) != len(want) {
		t.Fatalf("trunks in the snapshot: %d", len(snap.Trunks))
	}
	for _, tk := range snap.Trunks {
		got := tk.Dialled("00905321112233") + " " + tk.Dialled("05321112233") + " " + tk.Dialled("5321112233")
		same(t, "numbers of the trunk at "+tk.Addrs[0], got, want[tk.Addrs[0]])
	}
}
