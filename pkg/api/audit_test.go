// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"softswitch/core/pkg/store"
	"softswitch/core/pkg/totp"
	"softswitch/core/pkg/version"
)

// freshServer wipes the test database and starts an API on it.
func freshServer(t *testing.T, exts ...Extension) (context.Context, *pgxpool.Pool, *Server, client, string) {
	t.Helper()
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
	t.Cleanup(pool.Close)
	for _, q := range []string{`drop schema public cascade`, `create schema public`} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	const root = "root-test-key"
	if err := Bootstrap(ctx, pool, root); err != nil {
		t.Fatal(err)
	}
	srv := New(pool, exts...)
	ts := httptest.NewServer(conform(t, srv))
	t.Cleanup(ts.Close)
	return ctx, pool, srv, client{t, ts.URL}, root
}

// audit returns the log as the key sees it, newest first, filtered by query.
func (c client) audit(key, query string) []map[string]any {
	c.t.Helper()
	list, _ := c.raw(key, "GET", "/v1/audit-log?"+query, nil, 200).([]any)
	out := make([]map[string]any, len(list))
	for i, item := range list {
		out[i], _ = item.(map[string]any)
	}
	return out
}

// one returns the single record a query must match.
func (c client) one(key, query string) map[string]any {
	c.t.Helper()
	list := c.audit(key, query)
	if len(list) != 1 {
		c.t.Fatalf("audit log %q: %d records, want 1: %v", query, len(list), list)
	}
	return list[0]
}

// change returns a field's old and new value in a record, as JSON text.
func change(rec map[string]any, field string) string {
	ch, ok := rec["changes"].(map[string]any)[field]
	if !ok {
		return "absent"
	}
	b, _ := json.Marshal(ch)
	return string(b)
}

func TestAuditLog(t *testing.T) {
	type poke struct{ Password, Note string }
	ext := func(s *Server) {
		// An extension's operation that does not record itself…
		s.Handle("POST /v1/widgets/{id}/poke", true, func(w http.ResponseWriter, r *http.Request, c Caller) error {
			w.WriteHeader(204)
			return nil
		})
		// …one that fails…
		s.Handle("POST /v1/widgets/{id}/break", true, func(w http.ResponseWriter, r *http.Request, c Caller) error {
			return Errf(422, "broken")
		})
		// …and one that does, carelessly passing secrets along.
		s.Handle("POST /v1/widgets/{id}/tell", true, func(w http.ResponseWriter, r *http.Request, c Caller) error {
			return s.Audit(r.Context(), s.DB(), r, c, AuditEntry{Resource: "widgets", RowID: 9, Action: "tell", Label: "nine",
				Changes: map[string]AuditChange{"api_key": {New: json.RawMessage(`"leaked-change"`)}, "colour": {Old: json.RawMessage(`"red"`), New: json.RawMessage(`"blue"`)}},
				Detail: map[string]any{"password": "leaked-detail", "note": "fine",
					"nested": map[string]any{"session_token": "leaked-nested", "list": []any{map[string]any{"totp_secret": "leaked-list"}}}}})
		})
	}
	ctx, pool, srv, c, root := freshServer(t, ext)
	count := func() int64 {
		t.Helper()
		var n int64
		if err := pool.QueryRow(ctx, `select count(*) from audit_log`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// Everything the log holds, as text, to look for what must not be there.
	everything := func() string {
		t.Helper()
		var all string
		if err := pool.QueryRow(ctx, `select coalesce(string_agg(to_jsonb(a)::text, ' '), '') from audit_log a`).Scan(&all); err != nil {
			t.Fatal(err)
		}
		return all
	}
	eq(t, "empty log", count(), 0)

	// --- generic create, update and delete, with old and new values ---
	started := time.Now().Add(-time.Second)
	cl := num(c.do(root, "POST", "/v1/clients", map[string]any{"name": "Acme", "max_calls": 10}, 201), "id")
	rec := c.one(root, "resource=clients&action=create")
	if rec["actor_type"] != "key" || rec["actor_name"] != "bootstrap" || rec["ip"] != "127.0.0.1" || num(rec, "row_id") != cl ||
		rec["label"] != "Acme" || change(rec, "name") != `{"new":"Acme"}` || change(rec, "max_calls") != `{"new":10}` {
		t.Errorf("create record: %v", rec)
	}
	if at, err := time.Parse(time.RFC3339, rec["at"].(string)); err != nil || at.Before(started) || at.After(time.Now().Add(time.Second)) {
		t.Errorf("create record: at %v", rec["at"])
	}
	c.do(root, "PATCH", fmt.Sprintf("/v1/clients/%d", cl), map[string]any{"name": "Acme Ltd", "max_calls": 25}, 200)
	rec = c.one(root, "resource=clients&action=update")
	if change(rec, "name") != `{"new":"Acme Ltd","old":"Acme"}` || change(rec, "max_calls") != `{"new":25,"old":10}` ||
		len(rec["changes"].(map[string]any)) != 2 || rec["label"] != "Acme Ltd" {
		t.Errorf("update record: %v", rec)
	}
	// A value set to and from nothing shows as null.
	c.do(root, "PATCH", fmt.Sprintf("/v1/clients/%d", cl), map[string]any{"low_balance": 5_000_000}, 200)
	c.do(root, "PATCH", fmt.Sprintf("/v1/clients/%d", cl), map[string]any{"low_balance": nil}, 200)
	if list := c.audit(root, "resource=clients&action=update&limit=2"); len(list) != 2 ||
		change(list[0], "low_balance") != `{"new":null,"old":5000000}` || change(list[1], "low_balance") != `{"new":5000000,"old":null}` {
		t.Errorf("null changes: %v", list)
	}
	// An update that changes nothing, a refused one and a read leave no record.
	before := count()
	c.do(root, "PATCH", fmt.Sprintf("/v1/clients/%d", cl), map[string]any{"name": "Acme Ltd"}, 200)
	c.do(root, "PATCH", fmt.Sprintf("/v1/clients/%d", cl), map[string]any{"billing_mode": "nonsense"}, 422)
	c.do(root, "POST", "/v1/clients", map[string]any{"name": "Elsewhere", "reseller_id": 999}, 409)
	c.do(root, "GET", fmt.Sprintf("/v1/clients/%d", cl), nil, 200)
	eq(t, "records after nothing happened", count(), before)

	// --- hidden columns are masked, whoever writes them ---
	const ha1 = "0123456789abcdef0123456789abcdef"
	trunk := num(c.do(root, "POST", "/v1/client-trunks", map[string]any{"client_id": cl, "name": "office", "auth_type": "digest", "username": "acme", "password_ha1": ha1}, 201), "id")
	rec = c.one(root, "resource=client-trunks&action=create")
	if change(rec, "password_ha1") != `{"new":"***"}` || change(rec, "username") != `{"new":"acme"}` {
		t.Errorf("trunk create record: %v", rec)
	}
	c.do(root, "PATCH", fmt.Sprintf("/v1/client-trunks/%d", trunk), map[string]any{"password_ha1": strings.Repeat("f", 32), "name": "hq"}, 200)
	rec = c.one(root, "resource=client-trunks&action=update")
	if change(rec, "password_ha1") != `{"new":"***","old":"***"}` || change(rec, "name") != `{"new":"hq","old":"office"}` {
		t.Errorf("trunk update record: %v", rec)
	}
	// A custom operation: the password the API issues is returned, not logged.
	issued := c.do(root, "POST", fmt.Sprintf("/v1/client-trunks/%d/password", trunk), nil, 200)["password"].(string)
	rec = c.one(root, "resource=client-trunks&action=password_issued")
	if num(rec, "row_id") != trunk || rec["label"] != "hq" || rec["detail"].(map[string]any)["username"] != "acme" {
		t.Errorf("password record: %v", rec)
	}
	c.do(root, "DELETE", fmt.Sprintf("/v1/client-trunks/%d", trunk), nil, 204)
	rec = c.one(root, "resource=client-trunks&action=delete")
	if change(rec, "password_ha1") != `{"old":"***"}` || change(rec, "name") != `{"old":"hq"}` || rec["label"] != "hq" {
		t.Errorf("trunk delete record: %v", rec)
	}

	// --- custom operations ---
	c.do(root, "POST", "/v1/payments", map[string]any{"party_type": "client", "party_id": cl, "amount": 50_000_000, "reference": "bank-1"}, 201)
	rec = c.one(root, "resource=clients&action=payment")
	if d := rec["detail"].(map[string]any); num(d, "amount") != 50_000_000 || num(d, "balance_after") != 50_000_000 || d["reference"] != "bank-1" || rec["label"] != "Acme Ltd" {
		t.Errorf("payment record: %v", rec)
	}
	c.do(root, "POST", "/v1/payments", map[string]any{"party_type": "client", "party_id": cl, "amount": -1}, 422)
	eq(t, "payments recorded", int64(len(c.audit(root, "action=payment"))), 1)
	tariff := num(c.do(root, "POST", "/v1/tariffs", map[string]any{"name": "Sell", "kind": "sell"}, 201), "id")
	c.do(root, "POST", fmt.Sprintf("/v1/tariffs/%d/rates/import?dry_run=1", tariff), "prefix,price\n90,0.02\n", 200)
	eq(t, "a dry run is not a change", int64(len(c.audit(root, "resource=tariffs"))), 1) // its creation
	c.do(root, "POST", fmt.Sprintf("/v1/tariffs/%d/rates/import", tariff), "prefix,price\n90,0.02\n44,0.01\nx,1\n", 201)
	rec = c.one(root, "action=rates_import")
	if d := rec["detail"].(map[string]any); rec["resource"] != "tariffs" || num(rec, "row_id") != tariff || num(d, "rates") != 2 || num(d, "invalid") != 1 {
		t.Errorf("import record: %v", rec)
	}
	// API keys: that one was made and by whom, never its value.
	made := c.do(root, "POST", "/v1/api-keys", map[string]any{"name": "reporting", "role": "readonly"}, 201)
	ro := made["key"].(string)
	rec = c.one(root, "resource=api-keys&action=create")
	if rec["label"] != "reporting" || rec["detail"].(map[string]any)["role"] != "readonly" {
		t.Errorf("key record: %v", rec)
	}
	extra := num(c.do(root, "POST", "/v1/api-keys", map[string]any{"name": "temporary"}, 201), "id")
	c.do(root, "DELETE", fmt.Sprintf("/v1/api-keys/%d", extra), nil, 204)
	if rec = c.one(root, "resource=api-keys&action=delete"); rec["label"] != "temporary" {
		t.Errorf("key delete record: %v", rec)
	}

	// --- people: who was made, changed, signed in, and failed to ---
	const adaPassword, bobPassword = "correct horse 1", "battery staple 1"
	ada := num(c.do(root, "POST", "/v1/users", map[string]any{"email": "ada@example.test", "name": "Ada", "role": "admin", "password": adaPassword}, 201), "id")
	rec = c.one(root, "resource=users&action=create")
	if change(rec, "password_hash") != `{"new":"***"}` || change(rec, "email") != `{"new":"ada@example.test"}` || rec["label"] != "ada@example.test" {
		t.Errorf("user create record: %v", rec)
	}
	c.do("", "POST", "/v1/login", map[string]any{"email": "ada@example.test", "password": "wrong password 1"}, 401)
	rec = c.one(root, "action=login_failed")
	if rec["actor_type"] != "user" || num(rec, "actor_id") != ada || rec["actor_name"] != "ada@example.test" ||
		rec["detail"].(map[string]any)["reason"] != "password" || rec["detail"].(map[string]any)["locked"] != false || rec["ip"] != "127.0.0.1" {
		t.Errorf("failed sign-in record: %v", rec)
	}
	// An address nobody has is refused the same way and leaves nothing.
	before = count()
	c.do("", "POST", "/v1/login", map[string]any{"email": "nobody@example.test", "password": "wrong password 1"}, 401)
	eq(t, "records after an unknown address", count(), before)
	adaToken := c.do("", "POST", "/v1/login", map[string]any{"email": "ada@example.test", "password": adaPassword}, 200)["token"].(string)
	if rec = c.one(root, "action=login"); rec["actor_name"] != "ada@example.test" || num(rec, "row_id") != ada {
		t.Errorf("sign-in record: %v", rec)
	}
	// What a signed-in user does is in that user's name.
	bob := num(c.do(adaToken, "POST", "/v1/users", map[string]any{"email": "bob@example.test", "name": "Bob", "password": bobPassword}, 201), "id")
	if rec = c.one(root, fmt.Sprintf("resource=users&action=create&row_id=%d", bob)); rec["actor_type"] != "user" || rec["actor_name"] != "ada@example.test" {
		t.Errorf("record of a user's act: %v", rec)
	}
	c.do(adaToken, "PATCH", fmt.Sprintf("/v1/users/%d", bob), map[string]any{"role": "admin", "password": "reset by admin 1"}, 200)
	rec = c.one(root, "resource=users&action=update")
	if change(rec, "role") != `{"new":"admin","old":"readonly"}` || change(rec, "password_hash") != `{"new":"***","old":"***"}` || len(rec["changes"].(map[string]any)) != 2 {
		t.Errorf("user update record: %v", rec)
	}
	c.do(adaToken, "POST", "/v1/me/password", map[string]any{"current": adaPassword, "new": "brand new secret 1"}, 204)
	if rec = c.one(root, "action=password_changed"); num(rec, "row_id") != ada {
		t.Errorf("password change record: %v", rec)
	}
	c.do(adaToken, "DELETE", fmt.Sprintf("/v1/users/%d", bob), nil, 204)
	if rec = c.one(root, "resource=users&action=delete"); change(rec, "email") != `{"old":"bob@example.test"}` || change(rec, "password_hash") != `{"old":"***"}` {
		t.Errorf("user delete record: %v", rec)
	}

	// --- no write route can leave the log out; secrets are masked in one place ---
	c.do(root, "POST", "/v1/widgets/7/poke", nil, 204)
	if rec = c.one(root, "resource=widgets&action=poke"); num(rec, "row_id") != 7 || rec["actor_name"] != "bootstrap" {
		t.Errorf("fallback record: %v", rec)
	}
	c.do(root, "POST", "/v1/widgets/7/break", nil, 422)
	eq(t, "a failed operation is not recorded", int64(len(c.audit(root, "action=break"))), 0)
	c.do(root, "POST", "/v1/widgets/9/tell", nil, 200)
	rec = c.one(root, "resource=widgets&action=tell")
	if d := rec["detail"].(map[string]any); change(rec, "api_key") != `{"new":"***"}` || change(rec, "colour") != `{"new":"blue","old":"red"}` ||
		d["password"] != "***" || d["note"] != "fine" || d["nested"].(map[string]any)["session_token"] != "***" ||
		d["nested"].(map[string]any)["list"].([]any)[0].(map[string]any)["totp_secret"] != "***" {
		t.Errorf("masked record: %v", rec)
	}
	eq(t, "one record for an operation that records itself", int64(len(c.audit(root, "resource=widgets"))), 2)
	all := everything()
	for what, secret := range map[string]string{
		"a trunk's digest hash": ha1, "an issued trunk password": issued, "an API key": ro, "the bootstrap key": root,
		"a password": adaPassword, "a new password": "brand new secret 1", "a wrong password": "wrong password 1", "an admin-set password": "reset by admin 1",
		"a session token": adaToken, "a leaked value": "leaked-", "a bcrypt hash": "$2a$",
	} {
		if strings.Contains(all, secret) {
			t.Errorf("the audit log contains %s", what)
		}
	}

	// --- reading: filters, search, paging ---
	total := count()
	req, _ := http.NewRequest("GET", c.url+"/v1/audit-log?limit=3&offset=2", nil)
	req.Header.Set("Authorization", "Bearer "+ro) // a read-only key reads the log
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var page []map[string]any
	json.NewDecoder(resp.Body).Decode(&page)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("X-Total-Count") != fmt.Sprint(total) || len(page) != 3 {
		t.Errorf("page: status %d, total %s (want %d), %d rows", resp.StatusCode, resp.Header.Get("X-Total-Count"), total, len(page))
	}
	if whole := c.audit(root, "limit=1000"); int64(len(whole)) != total || num(whole[2], "id") != num(page[0], "id") || num(whole[0], "id") < num(whole[1], "id") {
		t.Errorf("order and paging: %d rows", len(whole))
	}
	eq(t, "by actor", int64(len(c.audit(root, fmt.Sprintf("actor_type=user&actor_id=%d", ada)))), 6)
	eq(t, "free text: a value that changed", int64(len(c.audit(root, "q=Acme+Ltd"))), 4)
	eq(t, "free text: an operation's detail", int64(len(c.audit(root, "q=bank-1"))), 1)
	eq(t, "free text: no match", int64(len(c.audit(root, "q=no+such+thing"))), 0)
	eq(t, "from now on", int64(len(c.audit(root, "from="+time.Now().Add(time.Minute).UTC().Format(time.RFC3339)))), 0)
	eq(t, "until now", int64(len(c.audit(root, "limit=1000&to="+time.Now().Add(time.Minute).UTC().Format(time.RFC3339)))), total)
	c.do(root, "GET", "/v1/audit-log?nonsense=1", nil, 400)
	one := c.do(root, "GET", fmt.Sprintf("/v1/audit-log/%d", num(page[0], "id")), nil, 200)
	if num(one, "id") != num(page[0], "id") {
		t.Errorf("one record: %v", one)
	}

	// --- the log cannot be changed through the API ---
	id := num(page[0], "id")
	c.do(root, "POST", "/v1/audit-log", map[string]any{"resource": "clients", "action": "create"}, 405)
	c.do(root, "PATCH", fmt.Sprintf("/v1/audit-log/%d", id), map[string]any{"action": "nothing"}, 405)
	c.do(root, "DELETE", fmt.Sprintf("/v1/audit-log/%d", id), nil, 405)
	c.do(root, "DELETE", "/v1/audit-log", nil, 405)
	eq(t, "records after trying to change the log", count(), total)

	// --- another account reads its own records, and only those ---
	if _, err := pool.Exec(ctx, `with r as (insert into resellers (name) values ('elsewhere') returning id)
		insert into api_keys (reseller_id, name, key_hash) select id, 'elsewhere', $1 from r`, HashKey("elsewhere-key")); err != nil {
		t.Fatal(err)
	}
	eq(t, "another account's view", int64(len(c.audit("elsewhere-key", ""))), 0)
	c.do("elsewhere-key", "GET", fmt.Sprintf("/v1/audit-log/%d", id), nil, 404)
	c.do("elsewhere-key", "POST", "/v1/tariffs", map[string]any{"name": "Theirs", "kind": "sell"}, 201)
	if rec = c.one("elsewhere-key", ""); rec["label"] != "Theirs" || rec["actor_name"] != "elsewhere" {
		t.Errorf("another account's record: %v", rec)
	}
	eq(t, "the first account does not see it", int64(len(c.audit(root, "q=Theirs"))), 0)
	eq(t, "nor by asking for that account", int64(len(c.audit(root, fmt.Sprintf("reseller_id=%d", num(rec, "reseller_id"))))), 0)

	// --- old records go after the retention period ---
	total = count()
	if _, err := pool.Exec(ctx, `insert into audit_log (at, reseller_id, actor_type, resource, action) values
		(now() - interval '366 days', 1, 'system', 'clients', 'old'), (now() - interval '364 days', 1, 'system', 'clients', 'recent')`); err != nil {
		t.Fatal(err)
	}
	srv.SetAuditRetention(0) // for ever
	if n, err := srv.SweepAuditLog(ctx); err != nil || n != 0 {
		t.Errorf("sweep with no limit: %d %v", n, err)
	}
	srv.SetAuditRetention(DefaultAuditDays)
	if n, err := srv.SweepAuditLog(ctx); err != nil || n != 1 {
		t.Errorf("sweep: %d %v", n, err)
	}
	eq(t, "records after the sweep", count(), total+1)
	eq(t, "the old record is gone", int64(len(c.audit(root, "action=old"))), 0)
	srv.SetAuditRetention(30)
	if n, err := srv.SweepAuditLog(ctx); err != nil || n != 1 {
		t.Errorf("sweep at 30 days: %d %v", n, err)
	}
	eq(t, "today's records stay", count(), total)
}

func TestAuditDiff(t *testing.T) {
	row := func(s string) (r AuditRow) { json.Unmarshal([]byte(s), &r); return }
	text := func(m map[string]AuditChange) string { b, _ := json.Marshal(m); return string(b) }
	for _, tc := range []struct{ name, before, after, want string }{
		{"created", ``, `{"id":1,"name":"a","note":null,"token":"t"}`, `{"id":{"new":1},"name":{"new":"a"},"token":{"new":"***"}}`},
		{"deleted", `{"id":1,"name":"a","note":null,"shown":"s"}`, ``, `{"id":{"old":1},"name":{"old":"a"},"shown":{"old":"***"}}`},
		{"changed", `{"id":1,"name":"a","n":null,"shown":"x"}`, `{"id":1,"name":"b","n":2,"shown":"y"}`,
			`{"n":{"old":null,"new":2},"name":{"old":"a","new":"b"},"shown":{"old":"***","new":"***"}}`},
		{"same", `{"id":1,"password_hash":"h"}`, `{"id":1,"password_hash":"h"}`, `{}`},
	} {
		if got := text(AuditDiff(row(tc.before), row(tc.after), "shown")); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.name, got, tc.want)
		}
	}
	for field, secret := range map[string]bool{
		"password": true, "password_hash": true, "password_ha1": true, "key_hash": true, "api_key": true, "key": true, "token_hash": true,
		"totp_secret": true, "engine_token": true, "recovery_codes": true, "secret_key": true,
		"name": false, "currency": false, "monkey": false, "keyboard": false, "sip_code": false, "status": false, "max_calls": false,
	} {
		if secretField.MatchString(field) != secret {
			t.Errorf("%s: secret = %v, want %v", field, !secret, secret)
		}
	}
}

func TestTwoStep(t *testing.T) {
	ctx, pool, srv, c, root := freshServer(t)
	if err := srv.SetSecretKey("short"); err == nil {
		t.Fatal("a short secret key was accepted")
	}
	if err := srv.SetSecretKey(strings.Repeat("3c", 32)); err != nil {
		t.Fatal(err)
	}
	// The clock the codes are checked against moves as the test says.
	now := time.Now()
	twoStepNow = func() time.Time { return now }
	defer func() { twoStepNow = time.Now }()
	const password = "correct horse 1"
	ada := num(c.do(root, "POST", "/v1/users", map[string]any{"email": "ada@example.test", "name": "Ada", "role": "admin", "password": password}, 201), "id")
	bob := num(c.do(root, "POST", "/v1/users", map[string]any{"email": "bob@example.test", "name": "Bob", "password": password}, 201), "id")
	login := func(email string) map[string]any {
		t.Helper()
		return c.do("", "POST", "/v1/login", map[string]any{"email": email, "password": password}, 200)
	}
	challenge := func() string {
		t.Helper()
		out := login("ada@example.test")
		ch, _ := out["challenge"].(string)
		if out["two_step"] != true || out["token"] != nil || !strings.HasPrefix(ch, "tsc_") {
			t.Fatalf("sign-in with two steps on: %v", out)
		}
		return ch
	}
	verify := func(code string, want int) map[string]any {
		t.Helper()
		return c.do("", "POST", "/v1/login/verify", map[string]any{"challenge": challenge(), "code": code}, want)
	}
	var secret string
	code := func() string {
		t.Helper()
		v, err := totp.At(secret, now)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	token, _ := login("ada@example.test")["token"].(string)

	// --- it is for people: a key has no second step ---
	c.do(root, "GET", "/v1/me/two-step", nil, 400)
	c.do(root, "POST", "/v1/me/two-step/setup", nil, 400)
	c.do("", "GET", "/v1/me/two-step", nil, 401)

	// --- turning it on: a secret, a code that proves the app has it, recovery codes ---
	if st := c.do(token, "GET", "/v1/me/two-step", nil, 200); st["enabled"] != false || num(st, "recovery_codes_left") != 0 {
		t.Errorf("status before: %v", st)
	}
	c.do(token, "POST", "/v1/me/two-step/confirm", map[string]any{"code": "123456"}, 409) // nothing started
	c.do(token, "POST", "/v1/me/two-step/disable", map[string]any{"password": password, "code": "123456"}, 409)
	first := c.do(token, "POST", "/v1/me/two-step/setup", nil, 200)
	setup := c.do(token, "POST", "/v1/me/two-step/setup", nil, 200) // asking again replaces the secret
	secret, _ = setup["secret"].(string)
	if raw, err := totp.Base32.DecodeString(secret); err != nil || len(raw) != totp.SecretBytes || secret == first["secret"] ||
		setup["uri"] != "otpauth://totp/Telrics%20Switch:ada%40example.test?secret="+secret+"&issuer=Telrics%20Switch&algorithm=SHA1&digits=6&period=30" {
		t.Fatalf("setup: %v", setup)
	}
	if out := login("ada@example.test"); out["token"] == nil {
		t.Errorf("a set-up that was not confirmed changed sign-in: %v", out)
	}
	wrong := "000000"
	if code() == wrong {
		wrong = "000001"
	}
	c.do(token, "POST", "/v1/me/two-step/confirm", map[string]any{"code": wrong}, 422)
	confirmed := c.do(token, "POST", "/v1/me/two-step/confirm", map[string]any{"code": code()[:3] + " " + code()[3:]}, 200)
	codes, _ := confirmed["recovery_codes"].([]any)
	if len(codes) != 10 || len(codes[0].(string)) != 11 {
		t.Fatalf("recovery codes: %v", confirmed)
	}
	c.do(token, "POST", "/v1/me/two-step/confirm", map[string]any{"code": code()}, 409)
	c.do(token, "POST", "/v1/me/two-step/setup", nil, 409)
	if st := c.do(token, "GET", "/v1/me/two-step", nil, 200); st["enabled"] != true || num(st, "recovery_codes_left") != 10 {
		t.Errorf("status after: %v", st)
	}
	if sess := c.do(token, "GET", "/v1/session", nil, 200); sess["user"].(map[string]any)["two_step"] != true || sess["version"] != version.Get() {
		t.Errorf("session: %v", sess)
	}
	for _, u := range c.raw(root, "GET", "/v1/users", nil, 200).([]any) {
		if u := u.(map[string]any); u["two_step"] != (num(u, "id") == ada) || u["totp_secret"] != nil {
			t.Errorf("user list: %v", u)
		}
	}
	// At rest: the secret is sealed, the recovery codes are hashed.
	var stored []byte
	var hashes string
	raw, _ := totp.Base32.DecodeString(secret)
	if err := pool.QueryRow(ctx, `select totp_secret, (select string_agg(encode(code_hash, 'escape'), ' ') from user_recovery_codes) from users where id = $1`, ada).Scan(&stored, &hashes); err != nil {
		t.Fatal(err)
	}
	if len(stored) < 30 || stored[0] != 1 || bytes.Contains(stored, raw) || strings.Contains(hashes, totp.PlainCode(codes[0].(string))) {
		t.Errorf("at rest: secret %x", stored)
	}
	// The session that turned it on goes on; the one opened meanwhile too.
	c.do(token, "GET", "/v1/clients", nil, 200)

	// --- signing in: the password earns a challenge, the code a session ---
	c.do("", "POST", "/v1/login/verify", map[string]any{"challenge": "tsc_" + strings.Repeat("0", 64), "code": code()}, 410)
	c.do("", "POST", "/v1/login/verify", map[string]any{"challenge": "nonsense", "code": code()}, 400)
	c.do("", "POST", "/v1/login/verify", map[string]any{"challenge": challenge()}, 400)
	// The code that confirmed the set-up is spent: it does not sign anyone in.
	verify(code(), 401)
	now = now.Add(30 * time.Second)
	ch := challenge()
	c.do(ch, "GET", "/v1/clients", nil, 401) // a challenge is not a session
	signed := c.do("", "POST", "/v1/login/verify", map[string]any{"challenge": ch, "code": code()}, 200)["token"].(string)
	c.do(signed, "GET", "/v1/clients", nil, 200)
	// Neither the code nor the challenge works a second time.
	c.do("", "POST", "/v1/login/verify", map[string]any{"challenge": ch, "code": code()}, 410)
	verify(code(), 401)
	// A clock one step off either way is accepted, two steps is not.
	earlier := code()
	now = now.Add(60 * time.Second)
	later := code()
	now = now.Add(-30 * time.Second)
	verify(later, 200) // the app's clock runs a step ahead
	verify(earlier, 401)
	now = now.Add(120 * time.Second)
	behind, _ := totp.At(secret, now.Add(-30*time.Second))
	tooOld, _ := totp.At(secret, now.Add(-60*time.Second))
	verify(tooOld, 401)
	verify(behind, 200)

	// --- a recovery code stands in for the app, once ---
	recovery := codes[0].(string)
	verify(strings.ToUpper(recovery), 200)
	verify(recovery, 401)
	if st := c.do(token, "GET", "/v1/me/two-step", nil, 200); num(st, "recovery_codes_left") != 9 {
		t.Errorf("recovery codes left: %v", st)
	}
	// New ones replace the old; that takes a code from the app.
	now = now.Add(30 * time.Second)
	c.do(token, "POST", "/v1/me/two-step/recovery-codes", map[string]any{"code": codes[1]}, 403)
	c.do(token, "POST", "/v1/me/two-step/recovery-codes", map[string]any{"code": wrong}, 403)
	fresh, _ := c.do(token, "POST", "/v1/me/two-step/recovery-codes", map[string]any{"code": code()}, 200)["recovery_codes"].([]any)
	if len(fresh) != 10 || fresh[0] == codes[1] {
		t.Fatalf("new recovery codes: %v", fresh)
	}
	verify(codes[1].(string), 401)

	// --- wrong codes count towards the lock, like wrong passwords ---
	// Four failures so far since the last success; the fifth locks.
	verify(wrong, 401)
	now = now.Add(30 * time.Second)
	c.do("", "POST", "/v1/login", map[string]any{"email": "ada@example.test", "password": password}, 429)
	var failed int
	if err := pool.QueryRow(ctx, `select count(*) from audit_log where action = 'login_failed' and detail->>'locked' = 'true'`).Scan(&failed); err != nil || failed != 1 {
		t.Errorf("failures that locked: %d %v", failed, err)
	}
	c.do(token, "POST", "/v1/me/two-step/disable", map[string]any{"password": password, "code": code()}, 429)
	c.do(root, "PATCH", fmt.Sprintf("/v1/users/%d", ada), map[string]any{"unlock": true}, 200)
	// A challenge handed out before the lock answers 429 while it lasts.
	ch = challenge()
	for range 5 {
		c.do("", "POST", "/v1/login/verify", map[string]any{"challenge": ch, "code": wrong}, 401)
	}
	c.do("", "POST", "/v1/login/verify", map[string]any{"challenge": ch, "code": code()}, 429)
	c.do(root, "PATCH", fmt.Sprintf("/v1/users/%d", ada), map[string]any{"unlock": true}, 200)
	c.do("", "POST", "/v1/login/verify", map[string]any{"challenge": ch, "code": code()}, 200)

	// --- an administrator removes another user's set-up; nobody their own that way ---
	bobToken, _ := login("bob@example.test")["token"].(string)
	c.do(bobToken, "POST", fmt.Sprintf("/v1/users/%d/two-step/reset", ada), nil, 403) // read-only
	c.do(token, "POST", fmt.Sprintf("/v1/users/%d/two-step/reset", ada), nil, 422)
	c.do(root, "POST", "/v1/users/999/two-step/reset", nil, 404)
	c.do(root, "POST", fmt.Sprintf("/v1/users/%d/two-step/reset", ada), nil, 204)
	if out := login("ada@example.test"); out["token"] == nil || out["two_step"] != nil {
		t.Errorf("sign-in after a reset: %v", out)
	}
	rec := c.one(root, "action=two_step_reset")
	if rec["actor_name"] != "bootstrap" || num(rec, "row_id") != ada || rec["label"] != "ada@example.test" || rec["detail"].(map[string]any)["was_on"] != true {
		t.Errorf("reset record: %v", rec)
	}
	var left int
	if err := pool.QueryRow(ctx, `select (select count(*) from user_recovery_codes) + (select count(*) from user_login_challenges) + count(totp_secret) from users`).Scan(&left); err != nil || left != 0 {
		t.Errorf("left behind by a reset: %d %v", left, err)
	}

	// --- turning it off takes the password and a code ---
	old := secret
	secret, _ = c.do(token, "POST", "/v1/me/two-step/setup", nil, 200)["secret"].(string)
	now = now.Add(30 * time.Second)
	codes, _ = c.do(token, "POST", "/v1/me/two-step/confirm", map[string]any{"code": code()}, 200)["recovery_codes"].([]any)
	now = now.Add(30 * time.Second)
	c.do(token, "POST", "/v1/me/two-step/disable", map[string]any{"password": "not the password", "code": code()}, 403)
	c.do(token, "POST", "/v1/me/two-step/disable", map[string]any{"password": password, "code": wrong}, 403)
	c.do(token, "POST", "/v1/me/two-step/disable", map[string]any{"password": password}, 403)
	if oldCode, _ := totp.At(old, now); oldCode != code() {
		c.do(token, "POST", "/v1/me/two-step/disable", map[string]any{"password": password, "code": oldCode}, 403) // the secret before the reset
	}
	c.do(root, "PATCH", fmt.Sprintf("/v1/users/%d", ada), map[string]any{"unlock": true}, 200)
	challenge() // still on
	c.do(token, "POST", "/v1/me/two-step/disable", map[string]any{"password": password, "code": code()}, 204)
	if out := login("ada@example.test"); out["token"] == nil {
		t.Errorf("sign-in after turning it off: %v", out)
	}
	if st := c.do(token, "GET", "/v1/me/two-step", nil, 200); st["enabled"] != false || num(st, "recovery_codes_left") != 0 {
		t.Errorf("status at the end: %v", st)
	}
	// A recovery code turns it off too, for a lost phone.
	secret, _ = c.do(token, "POST", "/v1/me/two-step/setup", nil, 200)["secret"].(string)
	now = now.Add(30 * time.Second)
	codes, _ = c.do(token, "POST", "/v1/me/two-step/confirm", map[string]any{"code": code()}, 200)["recovery_codes"].([]any)
	c.do(token, "POST", "/v1/me/two-step/disable", map[string]any{"password": password, "code": codes[3]}, 204)

	// --- what the audit log tells of it, and what it does not ---
	for action, want := range map[string]int{"two_step_on": 3, "two_step_off": 2, "two_step_reset": 1, "recovery_codes_renewed": 1} {
		if got := len(c.audit(root, "action="+action)); got != want {
			t.Errorf("%s records: %d, want %d", action, got, want)
		}
	}
	var withCode, byRecovery, wrongCodes int
	if err := pool.QueryRow(ctx, `select count(*) filter (where action = 'login' and detail->>'two_step' = 'true'),
		count(*) filter (where action = 'login' and detail->>'method' = 'recovery'),
		count(*) filter (where action = 'login_failed' and detail->>'reason' = 'code') from audit_log`).Scan(&withCode, &byRecovery, &wrongCodes); err != nil {
		t.Fatal(err)
	}
	if withCode != 5 || byRecovery != 1 || wrongCodes < 10 {
		t.Errorf("sign-ins with a code %d, with a recovery code %d, wrong codes %d", withCode, byRecovery, wrongCodes)
	}
	var all string
	if err := pool.QueryRow(ctx, `select string_agg(to_jsonb(a)::text, ' ') from audit_log a`).Scan(&all); err != nil {
		t.Fatal(err)
	}
	for what, v := range map[string]string{"the secret": secret, "an earlier secret": old, "a recovery code": codes[0].(string),
		"a recovery code as stored": totp.PlainCode(codes[0].(string)), "a challenge": ch, "a session token": token, "the password": password, "a code": `"` + code() + `"`} {
		if strings.Contains(all, v) {
			t.Errorf("the audit log contains %s", what)
		}
	}
	_ = bob
}

func TestTwoStepRequired(t *testing.T) {
	ctx, pool, srv, c, root := freshServer(t)
	if err := srv.SetSecretKey(strings.Repeat("5a", 32)); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	twoStepNow = func() time.Time { return now }
	defer func() { twoStepNow = time.Now }()
	const password = "correct horse 2"
	for _, u := range []struct{ email, role string }{{"ada@example.test", "admin"}, {"bob@example.test", "readonly"}, {"cy@example.test", "admin"}} {
		c.do(root, "POST", "/v1/users", map[string]any{"email": u.email, "name": u.email[:3], "role": u.role, "password": password}, 201)
	}
	login := func(email string) map[string]any {
		t.Helper()
		return c.do("", "POST", "/v1/login", map[string]any{"email": email, "password": password}, 200)
	}
	tokenOf := func(email string) string {
		t.Helper()
		tok, _ := login(email)["token"].(string)
		return tok
	}
	// enrol sets two-step sign-in up for the session's user and returns the secret.
	enrol := func(token string) string {
		t.Helper()
		secret, _ := c.do(token, "POST", "/v1/me/two-step/setup", nil, 200)["secret"].(string)
		now = now.Add(30 * time.Second)
		code, err := totp.At(secret, now)
		if err != nil {
			t.Fatal(err)
		}
		c.do(token, "POST", "/v1/me/two-step/confirm", map[string]any{"code": code}, 200)
		return secret
	}
	held := func(token, method, path string, body any) {
		t.Helper()
		if out := c.do(token, method, path, body, 403); out["code"] != "two_step_required" {
			t.Errorf("%s %s while set-up is required: %v", method, path, out)
		}
	}
	state := func(key string) map[string]any { return c.do(key, "GET", "/v1/account/two-step", nil, 200) }
	ada, bob, cy := tokenOf("ada@example.test"), tokenOf("bob@example.test"), tokenOf("cy@example.test")

	// --- off (the default): nothing changes ---
	if st := state(bob); st["required"] != false || num(st, "users_without") != 3 {
		t.Errorf("state at first: %v", st)
	}
	if out := login("bob@example.test"); out["two_step_setup_required"] != nil {
		t.Errorf("sign-in with the requirement off: %v", out)
	}
	c.do(bob, "GET", "/v1/clients", nil, 200)
	if u := c.do(bob, "GET", "/v1/session", nil, 200)["user"].(map[string]any); u["two_step_setup_required"] != false {
		t.Errorf("session with the requirement off: %v", u)
	}

	// --- turning it on: an administrator with their own second step on ---
	c.do(ada, "PUT", "/v1/account/two-step", map[string]any{}, 400)
	if out := c.do(ada, "PUT", "/v1/account/two-step", map[string]any{"required": true}, 409); out["code"] != "own_two_step_off" {
		t.Errorf("turning it on without one's own: %v", out)
	}
	c.do(bob, "PUT", "/v1/account/two-step", map[string]any{"required": true}, 403) // read-only
	enrol(ada)
	if st := c.do(ada, "PUT", "/v1/account/two-step", map[string]any{"required": true}, 200); st["required"] != true || num(st, "users_without") != 2 {
		t.Errorf("turned on: %v", st)
	}
	c.do(ada, "PUT", "/v1/account/two-step", map[string]any{"required": true}, 200) // already on: no second record
	rec := c.one(root, "action=require_two_step_on")
	if rec["actor_name"] != "ada@example.test" || rec["resource"] != "account" || rec["label"] != "root" || change(rec, "require_two_step") != `{"new":true,"old":false}` {
		t.Errorf("record of turning it on: %v", rec)
	}

	// --- a session already open is held back from its next request ---
	held(bob, "GET", "/v1/clients", nil)
	held(bob, "GET", "/v1/audit-log", nil)
	held(bob, "GET", "/v1/audit-log/export", nil)
	held(bob, "POST", "/v1/me/password", map[string]any{"current": password, "new": "another horse 2"})
	held(bob, "GET", "/v1/account/two-step", nil)
	held(cy, "POST", "/v1/users", map[string]any{"email": "dan@example.test", "name": "Dan", "password": password})
	held(cy, "PUT", "/v1/account/two-step", map[string]any{"required": false}) // nobody held back switches it off
	c.do(bob, "GET", "/v1/me", nil, 200)
	if u := c.do(bob, "GET", "/v1/session", nil, 200)["user"].(map[string]any); u["two_step_setup_required"] != true {
		t.Errorf("session held back: %v", u)
	}
	if st := c.do(bob, "GET", "/v1/me/two-step", nil, 200); st["enabled"] != false {
		t.Errorf("own state while held back: %v", st)
	}
	// A new sign-in says so at once, and the log notes it.
	if out := login("cy@example.test"); out["token"] == nil || out["two_step_setup_required"] != true {
		t.Errorf("sign-in with the requirement on: %v", out)
	}
	var noted int
	if err := pool.QueryRow(ctx, `select count(*) from audit_log where action = 'login' and label = 'cy@example.test' and detail->>'two_step_setup_required' = 'true'`).Scan(&noted); err != nil || noted != 1 {
		t.Errorf("sign-ins noted as held back: %d %v", noted, err)
	}
	c.do(cy, "POST", "/v1/logout", nil, 204)
	c.do(cy, "GET", "/v1/session", nil, 401)
	// The enrolled user and API keys go on as before.
	c.do(ada, "GET", "/v1/clients", nil, 200)
	c.do(root, "GET", "/v1/clients", nil, 200)
	reader := c.do(root, "POST", "/v1/api-keys", map[string]any{"name": "reader", "role": "readonly"}, 201)["key"].(string)
	c.do(reader, "GET", "/v1/audit-log", nil, 200)

	// --- setting it up lifts the hold on the same session ---
	enrol(bob)
	c.do(bob, "GET", "/v1/clients", nil, 200)
	if u := c.do(bob, "GET", "/v1/session", nil, 200)["user"].(map[string]any); u["two_step_setup_required"] != false || u["two_step"] != true {
		t.Errorf("session after the set-up: %v", u)
	}
	if st := state(ada); num(st, "users_without") != 1 {
		t.Errorf("users without after bob: %v", st)
	}
	// Signing in now takes the code, as for anyone with it on.
	if out := login("bob@example.test"); out["two_step"] != true || out["token"] != nil {
		t.Errorf("sign-in after the set-up: %v", out)
	}
	// An administrator's reset puts the user back to the set-up.
	bobID := int64(0)
	if err := pool.QueryRow(ctx, `select id from users where email = 'bob@example.test'`).Scan(&bobID); err != nil {
		t.Fatal(err)
	}
	c.do(ada, "POST", fmt.Sprintf("/v1/users/%d/two-step/reset", bobID), nil, 204)
	held(bob, "GET", "/v1/clients", nil)

	// --- turning it off: an API key may, as may any administrator ---
	if st := c.do(root, "PUT", "/v1/account/two-step", map[string]any{"required": false}, 200); st["required"] != false {
		t.Errorf("turned off: %v", st)
	}
	c.do(bob, "GET", "/v1/clients", nil, 200)
	if out := login("cy@example.test"); out["two_step_setup_required"] != nil {
		t.Errorf("sign-in after turning it off: %v", out)
	}
	rec = c.one(root, "action=require_two_step_off")
	if rec["actor_type"] != "key" || change(rec, "require_two_step") != `{"new":false,"old":true}` {
		t.Errorf("record of turning it off: %v", rec)
	}
	// A key turns it on without a second step of its own: keys have none.
	c.do(root, "PUT", "/v1/account/two-step", map[string]any{"required": true}, 200)
	held(bob, "GET", "/v1/clients", nil)
	if n := len(c.audit(root, "resource=account")); n != 3 {
		t.Errorf("account records: %d, want 3", n)
	}
}

// flushCounter is a response writer that counts how often the answer was
// pushed out before the handler finished.
type flushCounter struct {
	httptest.ResponseRecorder
	flushes int
}

func (f *flushCounter) Flush() { f.flushes++ }

func TestAuditExport(t *testing.T) {
	ctx, pool, srv, c, root := freshServer(t)
	const password = "correct horse 3"
	c.do(root, "POST", "/v1/users", map[string]any{"email": "ada@example.test", "name": "Ada", "role": "readonly", "password": password}, 201)
	c.do(root, "POST", "/v1/clients", map[string]any{"name": "=HYPERLINK(\"x\")", "currency": "USD"}, 201)
	c.do(root, "POST", "/v1/clients", map[string]any{"name": "Plain, \"quoted\"", "currency": "USD"}, 201)
	reader := c.do(root, "POST", "/v1/api-keys", map[string]any{"name": "reader", "role": "readonly"}, 201)["key"].(string)
	ada, _ := c.do("", "POST", "/v1/login", map[string]any{"email": "ada@example.test", "password": password}, 200)["token"].(string)

	export := func(key, query string, want int) [][]string {
		t.Helper()
		req, _ := http.NewRequest("GET", c.url+"/v1/audit-log/export?"+query, nil)
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("export %q: %d, want %d", query, resp.StatusCode, want)
		}
		if want != 200 {
			return nil
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
			t.Errorf("content type %q", ct)
		}
		rows, err := csv.NewReader(resp.Body).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(rows[0], ",") != "id,at,actor_type,actor_id,actor_name,ip,resource,row_id,action,label,changes,detail" {
			t.Errorf("header: %v", rows[0])
		}
		return rows[1:]
	}
	// The file holds what the list holds, in the same order, for the same filters.
	for _, query := range []string{"", "resource=users", "action=create", "q=quoted", "actor_type=user", "resource=clients&action=create"} {
		list := c.audit(root, query+"&limit=1000")
		rows := export(root, query, 200)
		if len(rows) != len(list) || len(rows) == 0 && query != "actor_type=user" {
			t.Fatalf("export %q: %d rows, list %d", query, len(rows), len(list))
		}
		for i, rec := range list {
			row := rows[i]
			if row[0] != fmt.Sprint(num(rec, "id")) || row[6] != rec["resource"] || row[8] != rec["action"] || row[4] != rec["actor_name"] {
				t.Errorf("export %q row %d: %v, list %v", query, i, row, rec)
			}
			for col, field := range map[int]string{10: "changes", 11: "detail"} {
				var got any
				if row[col] != "" {
					if err := json.Unmarshal([]byte(row[col]), &got); err != nil {
						t.Errorf("%s of row %d: %v", field, i, err)
					}
				}
				if a, b := fmt.Sprint(got), fmt.Sprint(rec[field]); a != b && !(row[col] == "" && rec[field] == nil) {
					t.Errorf("%s of row %d: %s, list %s", field, i, a, b)
				}
			}
		}
	}
	// A time window, as in the list.
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if rows := export(root, "from="+future, 200); len(rows) != 0 {
		t.Errorf("records after now: %d", len(rows))
	}
	export(root, "from=yesterday", 400)
	// Secrets are masked as in the list; a name that looks like a formula is not one.
	rows := export(root, "resource=users&action=create", 200)
	if len(rows) != 1 || !strings.Contains(rows[0][10], `"password_hash": {"new": "***"}`) {
		t.Errorf("user created: %v", rows)
	}
	all := export(root, "", 200)
	var text strings.Builder
	csv.NewWriter(&text).WriteAll(all)
	for what, v := range map[string]string{"the password": password, "a session token": ada, "an API key": reader, "the bootstrap key": root} {
		if strings.Contains(text.String(), v) {
			t.Errorf("the export contains %s", what)
		}
	}
	formula := export(root, "resource=clients&q=HYPERLINK", 200)
	if len(formula) != 1 || formula[0][9] != `'=HYPERLINK("x")` {
		t.Errorf("formula-like name: %v", formula)
	}
	if quoted := export(root, "q=quoted&resource=clients", 200); len(quoted) != 1 || quoted[0][9] != `Plain, "quoted"` {
		t.Errorf("name with a comma and quotes: %v", quoted)
	}
	// Whoever may read the list may export it: read-only users and keys too.
	export(reader, "", 200)
	export(ada, "resource=clients", 200)
	export("", "", 401)

	// --- a large log goes out as it is read, not gathered first ---
	const many = 25_000
	if _, err := pool.Exec(ctx, `insert into audit_log (reseller_id, actor_type, resource, action, label, detail)
		select 1, 'system', 'bulk', 'create', 'row ' || g, jsonb_build_object('n', g) from generate_series(1, $1) g`, many); err != nil {
		t.Fatal(err)
	}
	w := &flushCounter{ResponseRecorder: *httptest.NewRecorder()}
	req := httptest.NewRequest("GET", "/v1/audit-log/export?resource=bulk", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+root)
	srv.ServeHTTP(w, req)
	got, err := csv.NewReader(w.Body).ReadAll()
	if err != nil || w.Code != 200 || len(got) != many+1 {
		t.Fatalf("bulk export: %d, %d lines, %v", w.Code, len(got), err)
	}
	if w.flushes < many/auditFlushEvery {
		t.Errorf("bulk export pushed out %d times while running, want at least %d", w.flushes, many/auditFlushEvery)
	}
	if got[1][9] != "row 25000" || got[many][9] != "row 1" {
		t.Errorf("bulk export order: first %v, last %v", got[1], got[many])
	}
}
