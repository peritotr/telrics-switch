// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"softswitch/core/pkg/rating"
	"softswitch/core/pkg/routing"
	"softswitch/core/pkg/version"
)

func (s *Server) registerOps() {
	s.Handle("GET /v1/me", false, func(w http.ResponseWriter, r *http.Request, c Caller) error {
		out, err := s.Resource("resellers").RowJSON(r.Context(), s.db, c, c.ResellerID)
		if err == nil {
			WriteRaw(w, 200, out)
		}
		return err
	}, Summary("The account behind the credentials in use."), ReturnsRow(200, "resellers"))
	s.Handle("GET /v1/session", false, s.session, Summary("The signed-in user or key, its role, the edition and the version."),
		Returns(200, sessionInfo{}))
	s.Handle("GET /v1/api-keys", false, s.listAPIKeys, Summary("List API keys (never their values)."), Returns(200, []apiKey{}))
	s.Handle("POST /v1/api-keys", true, s.createAPIKey, Summary("Create an API key; its value is returned once."),
		Takes(newAPIKey{}), Returns(201, createdAPIKey{}))
	s.Handle("DELETE /v1/api-keys/{id}", true, s.deleteAPIKey, Summary("Revoke an API key."), ReturnsNothing())
	s.Handle("POST /v1/tariffs/{id}/rates/import", true, s.importRates,
		Summary("Load rates from CSV as a new version of the tariff's rates; invalid lines are skipped and reported."),
		TakesText("text/csv", "Columns: prefix, price_per_min (decimal), then optionally connect_fee (decimal), initial_sec, "+
			"increment_sec, grace_sec, blocked, and a window: days (day bits, 1-127), time_from and time_to (HH:MM in the tariff's time zone; both or neither)."),
		Query("effective_from", time.Time{}, false, "When the new rates start (RFC 3339); now when left out."),
		Query("mode", "", false, "close (the default) ends the rate in force for each prefix at effective_from; keep refuses prefixes that have one."),
		Query("dry_run", "", false, "1 to check the sheet without loading it."),
		Returns(201, rateImport{}), Returns(200, rateImport{}))
	s.Handle("POST /v1/payments", true, s.payment, Summary("Record a payment or adjustment on a client's or vendor's balance."),
		Takes(paymentRequest{}), Returns(201, paymentDone{}))
	s.Handle("GET /v1/ledger", false, s.ledger, Summary("Balance movements of a party, newest last (the last 1000)."),
		Query("party_type", "", true, "client or vendor (or a kind of party an extension adds)."),
		Query("party_id", int64(0), true, "The party."), ReturnsTableRows("ledger", nil))
	s.Handle("POST /v1/cdrs", true, s.ingestCDRs,
		Summary("Submit raw call records for rating. Duplicates by call_id and leg are ignored."),
		Takes([]CDR{}), Returns(201, cdrsIngested{}))
	s.Handle("GET /v1/cdrs/export", false, s.exportCDRs,
		Summary("Call records matching the list filters (as for GET /v1/cdrs), as CSV with the party names filled in."),
		ReturnsText("text/csv", "The records, one a line, with a header line."))
	s.Handle("GET /v1/audit-log/export", false, s.exportAuditLog,
		Summary("Audit records matching the list filters (as for GET /v1/audit-log), as CSV, newest first; secrets are masked as in the list."),
		ReturnsText("text/csv", "The records, one a line, with a header line; changes and detail as JSON text."))
	s.Handle("POST /v1/route/simulate", false, s.simulateRoute, Summary("What the engine would decide for a call."),
		Takes(simulateRequest{}), Returns(200, routing.Decision{}))
	s.Handle("GET /v1/reports/summary", false, s.reportSummary,
		Summary("Traffic, quality and money by client, vendor, prefix or day."),
		Query("group_by", "", true, "client, vendor, prefix or day."),
		Query("from", time.Time{}, true, "Earliest start (RFC 3339)."), Query("to", time.Time{}, true, "Latest start, exclusive."),
		Returns(200, []summaryRow{}))
	s.Handle("GET /v1/openapi.json", false, s.openAPI, Summary("This description."), Returns(200, map[string]any{}))
	s.Handle("GET /v1/system", false, s.systemState,
		Summary("Processor, memory, disks and network of the machine, and whether each service answers."), Returns(200, systemState{}))
	s.Handle("GET /v1/firewall/bans", false, s.firewallBans,
		Summary("Addresses the SIP core is ignoring for flooding, from the engine."), Returns(200, firewallBans{}))
	s.Handle("POST /v1/firewall/unban", true, s.firewallUnban, Summary("Lift such a ban before its time is up."),
		Takes(unbanRequest{}), ReturnsNothing())
	s.Handle("POST /v1/report-schedules/{id}/send", true, s.sendScheduleNow,
		Summary("Send a scheduled report for its last complete period now."), TakesNothing(), Returns(200, scheduleSent{}))
	s.Handle("GET /v1/clients/{id}/bundles", false, s.clientBundles,
		Summary("A client's bundles of minutes with the current period and the seconds it has used of each."),
		ReturnsTableRows("bundles", bundlePeriod{}))
}

// sessionInfo is who a client such as the panel is talking as. User is
// there for a signed-in person; Key names whoever is signed in, a person or
// an API key, and its role decides the rights.
type sessionInfo struct {
	Edition string `json:"edition"`
	// Version is the release the API runs (pkg/version). It is told to
	// signed-in callers only, here beside the edition, rather than on an
	// open endpoint: it would tell a scanner which known faults to try.
	Version string          `json:"version"`
	Account json.RawMessage `json:"account" openapi:"ref:resellers"`
	User    *sessionUser    `json:"user,omitempty"`
	Key     sessionKey      `json:"key"`
}

type sessionUser struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Role    string `json:"role"`
	TwoStep bool   `json:"two_step"`
	// TwoStepSetupRequired: the account requires two-step sign-in and this
	// user has not set it up; until they do, only the set-up is open.
	TwoStepSetupRequired bool `json:"two_step_setup_required"`
}

type sessionKey struct {
	ID   int64  `json:"id"` // 0 for a signed-in person
	Name string `json:"name"`
	Role string `json:"role"`
}

// apiKey is a key as listed; Current marks the one in use.
type apiKey struct {
	ID         int64     `json:"id"`
	ResellerID int64     `json:"reseller_id"`
	Name       string    `json:"name"`
	Role       string    `json:"role"`
	CreatedAt  time.Time `json:"created_at"`
	Current    bool      `json:"current"`
}

type newAPIKey struct {
	ResellerID int64  `json:"reseller_id,omitempty"` // the caller's account when left out
	Name       string `json:"name"`
	Role       string `json:"role,omitempty"` // admin when left out
}

// createdAPIKey carries the key's value, shown this once.
type createdAPIKey struct {
	ID         int64  `json:"id"`
	ResellerID int64  `json:"reseller_id"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	Key        string `json:"key"`
}

// paymentRequest: Amount in micro-units; Kind is payment (the default),
// adjustment or refund.
type paymentRequest struct {
	PartyType string `json:"party_type" openapi:"required"`
	PartyID   int64  `json:"party_id" openapi:"required"`
	Amount    int64  `json:"amount" openapi:"required"`
	Kind      string `json:"kind,omitempty"`
	Reference string `json:"reference,omitempty"`
	Note      string `json:"note,omitempty"`
}

type paymentDone struct {
	PartyType string `json:"party_type"`
	PartyID   int64  `json:"party_id"`
	Balance   int64  `json:"balance"`
}

// rateImport is what a rate sheet came to; Rates is what was written (0 in
// a dry run), Errors the first 1000 rejected lines.
type rateImport struct {
	TariffID      int64             `json:"tariff_id"`
	EffectiveFrom time.Time         `json:"effective_from"`
	Mode          string            `json:"mode"`
	DryRun        bool              `json:"dry_run"`
	Read          int               `json:"read"`
	Valid         int               `json:"valid"`
	Invalid       int               `json:"invalid"`
	Errors        []RateImportError `json:"errors"`
	Closed        int               `json:"closed"`
	NewPrefixes   int               `json:"new_prefixes"`
	Rates         int64             `json:"rates"`
}

type cdrsIngested struct {
	Inserted   int `json:"inserted"`
	Duplicates int `json:"duplicates"`
}

// summaryRow is one group of the summary report; amounts in micro-units of
// the account's currency.
type summaryRow struct {
	Key           string      `json:"key"`
	Calls         int64       `json:"calls"`
	Answered      int64       `json:"answered"`
	ASRPct        json.Number `json:"asr_pct"`
	ACDSec        int64       `json:"acd_sec"`
	DurationSec   int64       `json:"duration_sec"`
	NERPct        json.Number `json:"ner_pct"`
	PDDMs         int64       `json:"pdd_ms"`
	FailedNetwork int64       `json:"failed_network"`
	PDDSamples    int64       `json:"pdd_samples"`
	PDDSumMs      int64       `json:"pdd_sum_ms"`
	Revenue       int64       `json:"revenue"`
	Cost          int64       `json:"cost"`
	Margin        int64       `json:"margin"`
}

type unbanRequest struct {
	IP string `json:"ip" openapi:"required"`
}

// bundlePeriod is added to a bundle's row: the current period (YYYY-MM)
// and the seconds used in it.
type bundlePeriod struct {
	Period  string `json:"period"`
	UsedSec int64  `json:"used_sec"`
}

// ---- API keys ----

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request, c Caller) error {
	var in newAPIKey
	if err := ReadJSON(r, &in); err != nil {
		return err
	}
	if in.ResellerID == 0 {
		in.ResellerID = c.ResellerID
	}
	if in.Role == "" {
		in.Role = "admin"
	}
	ctx := r.Context()
	if err := s.Resource("resellers").Allowed(ctx, s.db, c, in.ResellerID, false); err != nil {
		return err
	}
	key, err := NewKey()
	if err != nil {
		return err
	}
	var id int64
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `insert into api_keys (reseller_id, name, key_hash, role) values ($1, $2, $3, $4) returning id`,
			in.ResellerID, in.Name, HashKey(key), in.Role).Scan(&id); err != nil {
			return err
		}
		return s.Audit(ctx, tx, r, c, AuditEntry{Resource: "api-keys", RowID: id, Action: "create", Label: in.Name,
			Detail: map[string]any{"role": in.Role, "reseller_id": in.ResellerID}})
	})
	if err != nil {
		return err
	}
	// The key is shown once; only its hash is stored.
	WriteJSON(w, 201, createdAPIKey{ID: id, ResellerID: in.ResellerID, Name: in.Name, Role: in.Role, Key: key})
	return nil
}

// KeyPrefix starts every generated key, so that keys are recognisable in
// logs and secret scanners.
const KeyPrefix = "tsw_live_"

// NewKey returns a fresh random API key.
func NewKey() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, 32)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		out[i] = alphabet[n.Int64()]
	}
	return KeyPrefix + string(out), nil
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request, c Caller) error {
	var out string
	err := s.db.QueryRow(r.Context(), fmt.Sprintf(
		`select coalesce(jsonb_agg(to_jsonb(x) order by x.id), '[]')::text from (
		   select k.id, k.reseller_id, k.name, k.role, k.created_at, k.id = $2 as current
		   from api_keys k where k.reseller_id in %s) x`, s.scope), c.ResellerID, c.KeyID).Scan(&out)
	if err != nil {
		return err
	}
	WriteRaw(w, 200, out)
	return nil
}

func (s *Server) deleteAPIKey(w http.ResponseWriter, r *http.Request, c Caller) error {
	id, err := PathID(r)
	if err != nil {
		return err
	}
	if id == c.KeyID {
		return Errf(409, "cannot revoke the key in use")
	}
	ctx := r.Context()
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var name, role string
		if err := tx.QueryRow(ctx, fmt.Sprintf(`delete from api_keys k where k.id = $2 and k.reseller_id in %s returning k.name, k.role`, s.scope),
			c.ResellerID, id).Scan(&name, &role); err != nil {
			return err
		}
		return s.Audit(ctx, tx, r, c, AuditEntry{Resource: "api-keys", RowID: id, Action: "delete", Label: name, Detail: map[string]any{"role": role}})
	})
	if err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}

// session tells a client such as the panel who it is talking as.
func (s *Server) session(w http.ResponseWriter, r *http.Request, c Caller) error {
	ctx := r.Context()
	account, err := s.Resource("resellers").RowJSON(ctx, s.db, c, c.ResellerID)
	if err != nil {
		return err
	}
	var name, role string
	out := sessionInfo{Edition: s.edition, Version: version.Get(), Account: json.RawMessage(account)}
	if c.UserID != 0 {
		u := &sessionUser{ID: c.UserID, TwoStepSetupRequired: c.NeedsTwoStep}
		if err := s.db.QueryRow(ctx, `select name, role, email, totp_on from users where id = $1`, c.UserID).Scan(&u.Name, &u.Role, &u.Email, &u.TwoStep); err != nil {
			return err
		}
		name, role, out.User = u.Name, u.Role, u
	} else if err := s.db.QueryRow(ctx, `select name, role from api_keys where id = $1`, c.KeyID).Scan(&name, &role); err != nil {
		return err
	}
	// "key" names whoever is signed in, a person or an API key: the role
	// in it is what decides the rights.
	out.Key = sessionKey{ID: c.KeyID, Name: name, Role: role}
	WriteJSON(w, 200, out)
	return nil
}

// ---- balances ----

// ApplyBalance changes a balance and records the change in the ledger.
// Per-call usage does not go through here: it is traceable through CDRs
// and would otherwise add one ledger row per call.
func (s *Server) ApplyBalance(ctx context.Context, tx pgx.Tx, party string, id, delta int64, kind, ref, note string) error {
	if delta == 0 {
		return nil
	}
	var after int64
	if err := tx.QueryRow(ctx, fmt.Sprintf(`update %s set balance = balance + $1 where id = $2 returning balance`, s.partyTable(party)),
		delta, id).Scan(&after); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `insert into ledger (party_type, party_id, amount, balance_after, kind, ref, note) values ($1,$2,$3,$4,$5,$6,$7)`,
		party, id, delta, after, kind, ref, note)
	return err
}

func (s *Server) partyTable(party string) string { return s.Resource(s.parties[party]).Table }

// PartyAllowed reports whether the caller may read (or change) a balance holder.
func (s *Server) PartyAllowed(ctx context.Context, q Querier, c Caller, party string, id int64, write bool) error {
	name, ok := s.parties[party]
	if !ok {
		return Errf(400, "unknown party_type")
	}
	return s.Resource(name).Allowed(ctx, q, c, id, write)
}

func (s *Server) payment(w http.ResponseWriter, r *http.Request, c Caller) error {
	var in paymentRequest
	if err := ReadJSON(r, &in); err != nil {
		return err
	}
	if in.Kind == "" {
		in.Kind = "payment"
	}
	if in.Kind != "payment" && in.Kind != "adjustment" && in.Kind != "refund" {
		return Errf(400, "kind must be payment, adjustment or refund")
	}
	if in.Amount == 0 || (in.Kind == "payment" && in.Amount < 0) {
		return Errf(422, "amount must be positive")
	}
	ctx := r.Context()
	var balance int64
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := s.PartyAllowed(ctx, tx, c, in.PartyType, in.PartyID, true); err != nil {
			return err
		}
		delta := in.Amount
		if in.PartyType == "vendor" { // paying a vendor reduces what we owe
			delta = -delta
		}
		if err := s.ApplyBalance(ctx, tx, in.PartyType, in.PartyID, delta, in.Kind, in.Reference, in.Note); err != nil {
			return err
		}
		var name string
		if err := tx.QueryRow(ctx, fmt.Sprintf(`select balance, name from %s where id = $1`, s.partyTable(in.PartyType)), in.PartyID).Scan(&balance, &name); err != nil {
			return err
		}
		return s.Audit(ctx, tx, r, c, AuditEntry{Resource: s.parties[in.PartyType], RowID: in.PartyID, Action: in.Kind, Label: name,
			Detail: map[string]any{"amount": in.Amount, "reference": in.Reference, "note": in.Note, "balance_after": balance}})
	})
	if err != nil {
		return err
	}
	WriteJSON(w, 201, paymentDone{PartyType: in.PartyType, PartyID: in.PartyID, Balance: balance})
	return nil
}

func (s *Server) ledger(w http.ResponseWriter, r *http.Request, c Caller) error {
	party := r.URL.Query().Get("party_type")
	id, _ := strconv.ParseInt(r.URL.Query().Get("party_id"), 10, 64)
	ctx := r.Context()
	if err := s.PartyAllowed(ctx, s.db, c, party, id, false); err != nil {
		return err
	}
	var out string
	if err := s.db.QueryRow(ctx, `select coalesce(jsonb_agg(to_jsonb(l) order by l.id), '[]')::text
		from (select * from ledger where party_type = $1 and party_id = $2 order by id desc limit 1000) l`, party, id).Scan(&out); err != nil {
		return err
	}
	WriteRaw(w, 200, out)
	return nil
}

// ---- rates ----

// RateImportError describes one rejected line of a rate sheet.
type RateImportError struct {
	Line   int    `json:"line"`
	Prefix string `json:"prefix"`
	Value  string `json:"value"`
	Code   string `json:"code"` // prefix_empty, prefix_not_digits, price_invalid, price_negative, too_many_decimals, increment_min, value_invalid, days_invalid, time_invalid, time_pair, duplicate_prefix, conflict
	Ref    int    `json:"ref_line,omitempty"`
}

// CSV header aliases accepted by importRates.
var rateColumns = map[string]string{
	"price": "price_per_min", "initial": "initial_sec", "increment": "increment_sec", "grace": "grace_sec",
}

// importRates loads a CSV rate sheet as a new version of the tariff's rates.
// Columns: prefix, price_per_min (decimal), then optionally connect_fee
// (decimal), initial_sec, increment_sec, grace_sec, blocked, and a window:
// days (the API's set of day bits, 1-127), time_from and time_to (HH:MM in
// the tariff's time zone; both or neither). A prefix may appear once per
// window. A sheet without the window columns loads as it always did. Invalid
// lines are skipped and reported. Query parameters:
//
//	effective_from  when the new rates start (RFC 3339, default now)
//	mode=close      end the rate currently in force for each prefix at effective_from (default)
//	mode=keep       leave existing rates alone; prefixes that already have a rate in force are rejected
//	dry_run=1       validate only
func (s *Server) importRates(w http.ResponseWriter, r *http.Request, c Caller) error {
	id, err := PathID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.Resource("tariffs").Allowed(ctx, s.db, c, id, true); err != nil {
		return err
	}
	q := r.URL.Query()
	effective := time.Now()
	if v := q.Get("effective_from"); v != "" {
		if effective, err = time.Parse(time.RFC3339, v); err != nil {
			return Errf(400, "effective_from must be RFC 3339")
		}
	}
	mode := q.Get("mode")
	if mode == "" {
		mode = "close"
	}
	if mode != "close" && mode != "keep" {
		return Errf(400, "mode must be close or keep")
	}
	dryRun := q.Get("dry_run") == "1" || q.Get("dry_run") == "true"

	rd := csv.NewReader(http.MaxBytesReader(nil, r.Body, 64<<20))
	rd.FieldsPerRecord = -1
	rd.TrimLeadingSpace = true
	records, err := rd.ReadAll()
	if err != nil {
		return Errf(400, "invalid CSV: %v", err)
	}
	if len(records) < 2 {
		return Errf(422, "CSV needs a header row and at least one rate")
	}
	col := map[string]int{}
	for i, h := range records[0] {
		h = strings.ToLower(strings.TrimSpace(h))
		if full, ok := rateColumns[h]; ok {
			h = full
		}
		col[h] = i
	}
	_, hasPrefix := col["prefix"]
	_, hasPrice := col["price_per_min"]
	if !hasPrefix || !hasPrice {
		return Errf(422, "CSV header must include prefix and price_per_min")
	}

	var errs []RateImportError
	var prefixes []string
	var lines []int
	var prices, fees []int64
	var initials, increments, graces []int32
	var blocked []bool
	var days, froms, tos []int32
	seen := map[string]int{} // prefix and window -> line
	for n, rec := range records[1:] {
		line := n + 2
		field := func(name, def string) string {
			if i, ok := col[name]; ok && i < len(rec) && strings.TrimSpace(rec[i]) != "" {
				return strings.TrimSpace(rec[i])
			}
			return def
		}
		prefix := field("prefix", "")
		reject := func(code, value string) {
			errs = append(errs, RateImportError{Line: line, Prefix: prefix, Value: value, Code: code})
		}
		money := func(name, def string) (int64, bool) {
			raw := field(name, def)
			v, err := rating.ParseMoney(raw)
			switch _, frac, _ := strings.Cut(raw, "."); {
			case err != nil && len(frac) > 6:
				reject("too_many_decimals", raw)
			case err != nil:
				reject("price_invalid", raw)
			case v < 0:
				reject("price_negative", raw)
			default:
				return v, true
			}
			return 0, false
		}
		if prefix == "" {
			reject("prefix_empty", "")
			continue
		}
		if strings.Trim(prefix, "0123456789") != "" {
			reject("prefix_not_digits", prefix)
			continue
		}
		set, err := strconv.Atoi(field("days", "127"))
		if err != nil || set < 1 || set > rating.EveryDay {
			reject("days_invalid", field("days", ""))
			continue
		}
		from, to := field("time_from", ""), field("time_to", "")
		fromMin, okFrom := clockMinutes(from, false)
		toMin, okTo := clockMinutes(to, true)
		switch {
		case from != "" && !okFrom:
			reject("time_invalid", from)
			continue
		case to != "" && !okTo:
			reject("time_invalid", to)
			continue
		case (from == "") != (to == "") || from != "" && fromMin == toMin:
			reject("time_pair", from+"-"+to)
			continue
		}
		key := fmt.Sprintf("%s/%d/%d/%d", prefix, set, fromMin, toMin)
		if first, dup := seen[key]; dup {
			errs = append(errs, RateImportError{Line: line, Prefix: prefix, Value: prefix, Code: "duplicate_prefix", Ref: first})
			continue
		}
		price, ok := money("price_per_min", "")
		if !ok {
			continue
		}
		fee, ok := money("connect_fee", "0")
		if !ok {
			continue
		}
		ini, err1 := strconv.Atoi(field("initial_sec", "1"))
		inc, err2 := strconv.Atoi(field("increment_sec", "1"))
		grace, err3 := strconv.Atoi(field("grace_sec", "0"))
		if err1 != nil || err2 != nil || err3 != nil || ini < 0 || grace < 0 {
			reject("value_invalid", field("initial_sec", "1")+"/"+field("increment_sec", "1")+"/"+field("grace_sec", "0"))
			continue
		}
		if inc < 1 {
			reject("increment_min", strconv.Itoa(inc))
			continue
		}
		blk, err := strconv.ParseBool(field("blocked", "false"))
		if err != nil {
			reject("value_invalid", field("blocked", ""))
			continue
		}
		seen[key] = line
		days, froms, tos = append(days, int32(set)), append(froms, int32(fromMin)), append(tos, int32(toMin))
		prefixes, lines = append(prefixes, prefix), append(lines, line)
		prices, fees = append(prices, price), append(fees, fee)
		initials, increments, graces = append(initials, int32(ini)), append(increments, int32(inc)), append(graces, int32(grace))
		blocked = append(blocked, blk)
	}

	// Prefixes whose rate is in force at the effective time: closed in
	// "close" mode, rejected in "keep" mode.
	const inForce = `tariff_id = $1 and prefix = any($2) and effective_from < $3 and (effective_to is null or effective_to > $3)`
	rows, err := s.db.Query(ctx, `select distinct prefix from rates where `+inForce, id, prefixes, effective)
	if err != nil {
		return err
	}
	existing, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if mode == "keep" && len(existing) > 0 {
		taken := map[string]bool{}
		for _, p := range existing {
			taken[p] = true
		}
		keep := 0
		for i, p := range prefixes {
			if taken[p] {
				errs = append(errs, RateImportError{Line: lines[i], Prefix: p, Value: p, Code: "conflict"})
				continue
			}
			prefixes[keep], prices[keep], fees[keep] = p, prices[i], fees[i]
			initials[keep], increments[keep], graces[keep], blocked[keep] = initials[i], increments[i], graces[i], blocked[i]
			days[keep], froms[keep], tos[keep] = days[i], froms[i], tos[i]
			keep++
		}
		prefixes, prices, fees = prefixes[:keep], prices[:keep], fees[:keep]
		initials, increments, graces, blocked = initials[:keep], increments[:keep], graces[:keep], blocked[:keep]
		days, froms, tos = days[:keep], froms[:keep], tos[:keep]
		existing = nil
	}
	if len(prefixes) == 0 {
		if len(errs) > 0 && !dryRun {
			e := errs[0]
			return Errf(422, "no valid rates; line %d: %s", e.Line, e.Code)
		}
		if !dryRun {
			return Errf(422, "no valid rates")
		}
	}
	slices.SortFunc(errs, func(a, b RateImportError) int { return a.Line - b.Line })
	out := rateImport{
		TariffID: id, EffectiveFrom: effective.UTC(), Mode: mode, DryRun: dryRun,
		Read: len(records) - 1, Valid: len(prefixes), Invalid: len(errs),
		Errors: errs[:min(len(errs), 1000)],
		Closed: len(existing), NewPrefixes: distinct(prefixes) - len(existing),
	}
	if errs == nil {
		out.Errors = []RateImportError{}
	}
	if dryRun {
		AuditNothing(r)
		WriteJSON(w, 200, out)
		return nil
	}
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if mode == "close" {
			if _, err := tx.Exec(ctx, `update rates set effective_to = $3 where `+inForce, id, prefixes, effective); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `
			insert into rates (tariff_id, prefix, price_per_min, connect_fee, initial_sec, increment_sec, grace_sec, blocked, days, time_from, time_to, effective_from)
			select $1, * , $12::timestamptz from unnest($2::text[], $3::bigint[], $4::bigint[], $5::int[], $6::int[], $7::int[], $8::bool[],
			  $9::smallint[], $10::smallint[], $11::smallint[])
			on conflict (tariff_id, prefix, effective_from, days, time_from, time_to) do update set
			  price_per_min = excluded.price_per_min, connect_fee = excluded.connect_fee, initial_sec = excluded.initial_sec,
			  increment_sec = excluded.increment_sec, grace_sec = excluded.grace_sec, blocked = excluded.blocked`,
			id, prefixes, prices, fees, initials, increments, graces, blocked, days, froms, tos, effective)
		out.Rates = tag.RowsAffected()
		if err != nil {
			return err
		}
		var name string
		if err := tx.QueryRow(ctx, `select name from tariffs where id = $1`, id).Scan(&name); err != nil {
			return err
		}
		return s.Audit(ctx, tx, r, c, AuditEntry{Resource: "tariffs", RowID: id, Action: "rates_import", Label: name, Detail: map[string]any{
			"effective_from": effective.UTC(), "mode": mode, "rates": tag.RowsAffected(), "closed": len(existing), "invalid": len(errs)}})
	})
	if err != nil {
		return err
	}
	WriteJSON(w, 201, out)
	return nil
}

// rateLookup reads every rate in force at $3 whose prefix starts number $2,
// with the tariff's time zone: which of them applies depends on the day and
// hour there, and is decided by the same code the engine uses.
const rateLookup = `
	select r.id, r.prefix, r.price_per_min, r.connect_fee, r.initial_sec, r.increment_sec, r.grace_sec, r.blocked,
	       r.days, r.time_from, r.time_to, r.effective_from, t.timezone
	from rates r join tariffs t on t.id = r.tariff_id
	where r.tariff_id = $1 and $2 like r.prefix || '%' and r.effective_from <= $3 and (r.effective_to is null or r.effective_to > $3)`

// RateAt finds the rate in force for number for a call starting at the
// given time: the longest prefix that has a rate for that day and hour.
func RateAt(ctx context.Context, tx pgx.Tx, tariff *int64, number string, at time.Time) (rating.Rate, bool, error) {
	if tariff == nil {
		return rating.Rate{}, false, nil
	}
	rows, err := tx.Query(ctx, rateLookup, *tariff, number, at)
	if err != nil {
		return rating.Rate{}, false, err
	}
	byPrefix := map[string][]rating.Rate{}
	var r rating.Rate
	var zone string
	if _, err := pgx.ForEachRow(rows, []any{&r.ID, &r.Prefix, &r.PricePerMin, &r.ConnectFee, &r.InitialSec, &r.IncrementSec, &r.GraceSec, &r.Blocked,
		&r.Days, &r.From, &r.To, &r.EffectiveFrom, &zone}, func() error {
		byPrefix[r.Prefix] = append(byPrefix[r.Prefix], r)
		return nil
	}); err != nil {
		return rating.Rate{}, false, err
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC // as the engine reads an unknown zone
	}
	local := at.In(loc)
	cal, err := holidaysNear(ctx, tx, *tariff, local, byPrefix)
	if err != nil {
		return rating.Rate{}, false, err
	}
	for i := len(number); i >= 0; i-- {
		if list, ok := byPrefix[number[:i]]; ok {
			if r, ok := rating.Select(rating.Tidy(list), local, cal); ok {
				return r, true, nil
			}
		}
	}
	return rating.Rate{}, false, nil
}

// holidaysNear reads the holidays of the tariff's account that a window
// may ask about for a call starting at local: its date and the day before
// (a window across midnight counts to the day it began). Nothing is read
// when no rate has a window, since only windows look at the calendar.
func holidaysNear(ctx context.Context, tx pgx.Tx, tariff int64, local time.Time, byPrefix map[string][]rating.Rate) (rating.Calendar, error) {
	limited := false
	for _, list := range byPrefix {
		for _, r := range list {
			limited = limited || r.Limited()
		}
	}
	if !limited {
		return nil, nil
	}
	day := local.Format(time.DateOnly)
	rows, err := tx.Query(ctx, `select day, counts_as from holidays
		where reseller_id = (select reseller_id from tariffs where id = $1) and day between $2::date - 1 and $2::date`, tariff, day)
	if err != nil {
		return nil, fmt.Errorf("holidays: %w", err)
	}
	cal := rating.Calendar{}
	var d time.Time
	var as int
	_, err = pgx.ForEachRow(rows, []any{&d, &as}, func() error {
		cal[rating.DateOf(d)] = time.Weekday(as % 7)
		return nil
	})
	return cal, err
}

// clockMinutes reads "HH:MM" as minutes since midnight. "24:00" is read as
// midnight where it may end a window (end), and is rejected elsewhere.
func clockMinutes(s string, end bool) (int, bool) {
	if end && s == "24:00" {
		return 0, true
	}
	h, m, ok := strings.Cut(s, ":")
	if !ok || len(h) < 1 || len(h) > 2 || len(m) != 2 {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 || strings.ContainsAny(s, "+- ") {
		return 0, false
	}
	return hh*60 + mm, true
}

// distinct counts the different values of list.
func distinct(list []string) int {
	set := map[string]bool{}
	for _, v := range list {
		set[v] = true
	}
	return len(set)
}

// ---- CDRs ----

// CDR is one raw call record as submitted for rating.
type CDR struct {
	CallID       string     `json:"call_id"`
	Leg          int        `json:"leg"`
	ClientID     int64      `json:"client_id"`
	TrunkID      *int64     `json:"trunk_id"`
	ConnectionID *int64     `json:"connection_id"`
	Src          string     `json:"src"`
	SrcIP        string     `json:"src_ip"`
	Dst          string     `json:"dst"`
	StartAt      time.Time  `json:"start_at"`
	AnswerAt     *time.Time `json:"answer_at"`
	EndAt        time.Time  `json:"end_at"`
	SIPCode      int        `json:"sip_code"`
	PDDMs        int64      `json:"pdd_ms"` // post-dial delay of the attempt; 0 = none
	// DstTrunkID is the client's trunk the attempt was delivered to instead
	// of a vendor; nil for a vendor leg and in records from before the field.
	DstTrunkID *int64 `json:"dst_trunk_id"`

	// Extra holds the fields extensions registered with AllowCDRField.
	Extra map[string]json.RawMessage `json:"-"`
}

// CDRPrice is how a record is charged: the rates for both sides and the
// vendor that carried it.
type CDRPrice struct {
	Direction string
	Sell, Buy rating.Rate
	VendorID  *int64
}

// CDRPricer may price a record itself; it returns nil to leave the record to
// the next pricer and finally to tariff rating.
type CDRPricer func(ctx context.Context, tx pgx.Tx, rec *CDR) (*CDRPrice, error)

// CDRStored describes a record that was just stored and charged.
type CDRStored struct {
	ID          int64
	Record      *CDR
	ResellerID  int64 // account owning the client
	Direction   string
	DurationSec int
}

// CDRStoredFunc runs in the transaction that stored the record.
type CDRStoredFunc func(ctx context.Context, tx pgx.Tx, ev CDRStored) error

// AllowCDRField lets submitted records carry an extra field, delivered to
// the hooks in CDR.Extra.
func (s *Server) AllowCDRField(name string) { s.cdrFields = append(s.cdrFields, name) }

func (s *Server) OnCDRPrice(f CDRPricer) { s.cdrPrice = append(s.cdrPrice, f) }

func (s *Server) OnCDRStored(f CDRStoredFunc) { s.cdrStored = append(s.cdrStored, f) }

// ingestCDRs rates raw call records and charges every party. Records are
// identified by (call_id, leg), so resending a batch is harmless.
func (s *Server) ingestCDRs(w http.ResponseWriter, r *http.Request, c Caller) error {
	var raw []map[string]json.RawMessage
	if err := ReadJSON(r, &raw); err != nil {
		return err
	}
	inserted, duplicates, err := s.IngestCDRs(r.Context(), c, raw)
	if err != nil {
		return err
	}
	// The records are stored by now, in a transaction that also serves the
	// rater; the note that somebody submitted them by hand follows it.
	if err := s.Audit(r.Context(), s.db, r, c, AuditEntry{Resource: "cdrs", Action: "submit",
		Detail: map[string]any{"inserted": inserted, "duplicates": duplicates}}); err != nil {
		return err
	}
	WriteJSON(w, 201, cdrsIngested{Inserted: inserted, Duplicates: duplicates})
	return nil
}

// BundleUse is what a stored call record took from a bundle of minutes.
type BundleUse struct {
	CallID   string
	BundleID int64
	Period   string
}

// IngestCDRs rates and stores a batch of raw records in one transaction on
// behalf of c. It is what both the HTTP route and the rater service call.
func (s *Server) IngestCDRs(ctx context.Context, c Caller, raw []map[string]json.RawMessage) (inserted, duplicates int, err error) {
	inserted, duplicates, _, err = s.IngestCDRsBundles(ctx, c, raw)
	return
}

// IngestCDRsBundles is IngestCDRs that also tells which records of the batch
// took seconds from a bundle, so that whoever holds those seconds for calls
// in progress can let them go.
func (s *Server) IngestCDRsBundles(ctx context.Context, c Caller, raw []map[string]json.RawMessage) (inserted, duplicates int, used []BundleUse, err error) {
	in := make([]CDR, len(raw))
	for i, m := range raw {
		in[i].Extra = map[string]json.RawMessage{}
		for _, f := range s.cdrFields {
			if v, ok := m[f]; ok {
				in[i].Extra[f] = v
				delete(m, f)
			}
		}
		rest, _ := json.Marshal(m)
		dec := json.NewDecoder(bytes.NewReader(rest))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in[i]); err != nil {
			return 0, 0, nil, Errf(400, "record %d: %v", i, err)
		}
	}
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		inserted, duplicates, used = 0, 0, nil
		for i := range in {
			ok, use, err := s.rateCDR(ctx, tx, c, &in[i])
			if err != nil {
				var he httpError
				if errors.As(err, &he) {
					return Errf(he.code, "record %d: %s", i, he.msg)
				}
				return err
			}
			if ok {
				inserted++
			} else {
				duplicates++
			}
			if use != nil {
				used = append(used, *use)
			}
		}
		return nil
	})
	if err != nil {
		used = nil
	}
	return inserted, duplicates, used, err
}

// Rejected reports whether err is a refusal of the input (and so will not go
// away on retry) rather than a failure of the service.
func Rejected(err error) bool {
	var he httpError
	return errors.As(err, &he) && he.code >= 400 && he.code < 500
}

// fxOf is, for a client or vendor t, what one unit of its currency is worth
// in its account's: the rate of exchange, or 0 when they are the same.
const fxOf = `coalesce((select x.rate from exchange_rates x join resellers r on r.id = x.reseller_id
	where x.reseller_id = t.reseller_id and x.currency = t.currency and t.currency <> r.currency), 0)`

// rateCDR prices one record, stores it and charges the parties. It reports
// whether the record was new and, if it took seconds from a bundle (now, or
// when it was first stored), which.
func (s *Server) rateCDR(ctx context.Context, tx pgx.Tx, c Caller, rec *CDR) (bool, *BundleUse, error) {
	if rec.CallID == "" || rec.Dst == "" || rec.EndAt.Before(rec.StartAt) {
		return false, nil, Errf(422, "call_id, dst and a valid time range are required")
	}
	if rec.Leg == 0 {
		rec.Leg = 1
	}
	if rec.SIPCode == 0 {
		rec.SIPCode = 200
	}
	var resellerID int64
	var sellTariff *int64
	var sellFx, buyFx int64 // the client's and the vendor's currency in the account's; 0 = the same
	var zone string         // of the client's tariff: bundle periods are months there
	err := tx.QueryRow(ctx, fmt.Sprintf(`select t.reseller_id, t.tariff_id, `+fxOf+`,
		coalesce((select timezone from tariffs where id = t.tariff_id), 'UTC')
		from clients t where t.id = $2 and t.reseller_id in %s`, s.scope),
		c.ResellerID, rec.ClientID).Scan(&resellerID, &sellTariff, &sellFx, &zone)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, Errf(422, "unknown client")
	}
	if err != nil {
		return false, nil, err
	}

	duration := 0
	if rec.AnswerAt != nil && rec.EndAt.After(*rec.AnswerAt) {
		duration = int((rec.EndAt.Sub(*rec.AnswerAt) + time.Second - 1) / time.Second)
	}

	var price *CDRPrice
	for _, f := range s.cdrPrice {
		if price, err = f(ctx, tx, rec); err != nil {
			return false, nil, err
		}
		if price != nil {
			break
		}
	}
	if price == nil {
		price = &CDRPrice{Direction: "outbound"}
		if price.Sell, _, err = RateAt(ctx, tx, sellTariff, rec.Dst, rec.StartAt); err != nil {
			return false, nil, err
		}
		if rec.ConnectionID != nil {
			var vid int64
			var buyTariff *int64
			err := tx.QueryRow(ctx, `select v.id, v.tariff_id from vendor_connections vc join vendors v on v.id = vc.vendor_id where vc.id = $1`,
				*rec.ConnectionID).Scan(&vid, &buyTariff)
			if errors.Is(err, pgx.ErrNoRows) {
				return false, nil, Errf(422, "unknown connection")
			}
			if err != nil {
				return false, nil, err
			}
			price.VendorID = &vid
			if price.Buy, _, err = RateAt(ctx, tx, buyTariff, rec.Dst, rec.StartAt); err != nil {
				return false, nil, err
			}
		}
	}
	// A bundle of minutes pays for the call before the balance does. The
	// count of the period is locked until this transaction ends, so calls
	// stored side by side cannot take the same seconds.
	use, bundleFree, err := bundleFor(ctx, tx, rec, price, zone, duration)
	if err != nil {
		return false, nil, err
	}
	sellSec, bundleSec, sellAmount := price.Sell.AmountAfter(duration, bundleFree)
	var bundleID *int64
	matched := use // the bundle the number belongs to, used up or not
	if bundleSec > 0 {
		bundleID = &use.BundleID
	} else {
		use = nil
	}
	buySec, buyAmount := price.Buy.Amount(duration)
	if price.VendorID != nil {
		if err := tx.QueryRow(ctx, `select `+fxOf+` from vendors t where t.id = $1`, *price.VendorID).Scan(&buyFx); err != nil {
			return false, nil, err
		}
	}

	var cdrID int64
	err = tx.QueryRow(ctx, `
		insert into cdrs (call_id, leg, direction, client_id, trunk_id, vendor_id, connection_id, src, dst,
		  sell_prefix, buy_prefix, start_at, answer_at, end_at, duration_sec, sip_code,
		  sell_billed_sec, buy_billed_sec, sell_amount, buy_amount, src_ip, sell_base, buy_base, pdd_ms, bundle_id, bundle_sec,
		  dst_trunk_id)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27)
		on conflict (call_id, leg) do nothing returning id`,
		rec.CallID, rec.Leg, price.Direction, rec.ClientID, rec.TrunkID, price.VendorID, rec.ConnectionID, rec.Src, rec.Dst,
		price.Sell.Prefix, price.Buy.Prefix, rec.StartAt, rec.AnswerAt, rec.EndAt, duration, rec.SIPCode,
		sellSec, buySec, sellAmount, buyAmount, rec.SrcIP,
		rating.Convert(sellAmount, sellFx), rating.Convert(buyAmount, buyFx), min(max(rec.PDDMs, 0), 3_600_000),
		bundleID, bundleSec, rec.DstTrunkID).Scan(&cdrID)
	if errors.Is(err, pgx.ErrNoRows) {
		// A record seen before: nothing is charged and nothing taken from
		// the bundle a second time. What it took the first time is told
		// again, in case the first telling was lost.
		var first int64
		err := tx.QueryRow(ctx, `select bundle_id from cdrs where call_id = $1 and leg = $2 and bundle_sec > 0 and bundle_id is not null`,
			rec.CallID, rec.Leg).Scan(&first)
		if err != nil || matched == nil {
			if errors.Is(err, pgx.ErrNoRows) {
				err = nil
			}
			return false, nil, err
		}
		return false, &BundleUse{CallID: rec.CallID, BundleID: first, Period: matched.Period}, nil
	}
	if err == nil && use != nil {
		_, err = tx.Exec(ctx, `update bundle_usage set used_sec = used_sec + $1 where bundle_id = $2 and period = $3`,
			bundleSec, use.BundleID, use.Period)
	}
	if err != nil {
		return false, nil, err
	}
	if _, err := tx.Exec(ctx, `insert into cdr_export_queue (cdr_id) select $1 where exists (select 1 from cdr_export_enabled)`, cdrID); err != nil {
		return false, nil, err
	}
	if _, err := tx.Exec(ctx, `update clients set balance = balance - $1 where id = $2`, sellAmount, rec.ClientID); err != nil {
		return false, nil, err
	}
	if price.VendorID != nil {
		if _, err := tx.Exec(ctx, `update vendors set balance = balance + $1 where id = $2`, buyAmount, *price.VendorID); err != nil {
			return false, nil, err
		}
	}
	ev := CDRStored{ID: cdrID, Record: rec, ResellerID: resellerID, Direction: price.Direction, DurationSec: duration}
	for _, f := range s.cdrStored {
		if err := f(ctx, tx, ev); err != nil {
			return false, nil, err
		}
	}
	return true, use, nil
}

// bundleFor finds the bundle a record draws on and how many seconds its
// period has left, and locks that period's count. It returns nil when the
// call takes nothing from a bundle: it was not answered, it is not an
// outbound call, it costs nothing, or the client has no bundle for the
// number.
func bundleFor(ctx context.Context, tx pgx.Tx, rec *CDR, price *CDRPrice, zone string, duration int) (*BundleUse, int64, error) {
	if price.Direction != "outbound" || price.Sell.Free() || price.Sell.BilledSeconds(duration) == 0 {
		return nil, 0, nil
	}
	// The longest matching prefix decides, as in the engine.
	var id, seconds int64
	err := tx.QueryRow(ctx, `select b.id, b.seconds from bundles b, unnest(b.prefixes) p
		where b.client_id = $1 and b.enabled and $2 like p || '%' order by length(p) desc, b.id limit 1`,
		rec.ClientID, rec.Dst).Scan(&id, &seconds)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC // as the engine reads an unknown zone
	}
	// The call counts to the period it started in.
	use := &BundleUse{CallID: rec.CallID, BundleID: id, Period: rating.Period(rec.StartAt, loc)}
	var used int64
	err = tx.QueryRow(ctx, `insert into bundle_usage (bundle_id, period) values ($1, $2)
		on conflict (bundle_id, period) do update set used_sec = bundle_usage.used_sec returning used_sec`,
		id, use.Period).Scan(&used)
	return use, max(seconds-used, 0), err
}

// clientBundles lists a client's bundles with what the current period has
// used of each.
func (s *Server) clientBundles(w http.ResponseWriter, r *http.Request, c Caller) error {
	id, err := PathID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.Resource("clients").Allowed(ctx, s.db, c, id, false); err != nil {
		return err
	}
	var zone string
	if err := s.db.QueryRow(ctx, `select coalesce((select timezone from tariffs where id = t.tariff_id), 'UTC') from clients t where t.id = $1`,
		id).Scan(&zone); err != nil {
		return err
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}
	period := rating.Period(time.Now(), loc)
	var out string
	if err := s.db.QueryRow(ctx, `select coalesce(jsonb_agg(to_jsonb(b) || jsonb_build_object('period', $2::text, 'used_sec',
		  coalesce((select u.used_sec from bundle_usage u where u.bundle_id = b.id and u.period = $2), 0)) order by b.id), '[]')::text
		from bundles b where b.client_id = $1`, id, period).Scan(&out); err != nil {
		return err
	}
	WriteRaw(w, 200, out)
	return nil
}

// ---- routing and reports ----

// simulateRequest is a call to decide on.
type simulateRequest struct {
	SrcIP string `json:"src_ip" openapi:"required"`
	Dst   string `json:"dst" openapi:"required"`
	// Src is the caller's number; without it the rules about callers
	// are left out of the answer.
	Src string `json:"src,omitempty"`
	// Asserted is the number the caller's side asserts (as in
	// P-Asserted-Identity), for the rules on that number; none when
	// left out.
	Asserted string `json:"asserted,omitempty"`
	// At is the moment the call would start, for rates and routes by
	// day or hour; now when left out.
	At time.Time `json:"at,omitzero"`
}

func (s *Server) simulateRoute(w http.ResponseWriter, r *http.Request, c Caller) error {
	var in simulateRequest
	if err := ReadJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	snap, err := s.snapshot(ctx, s.db)
	if err != nil {
		return err
	}
	// The firewall goes before everything, as it does for a real call.
	if blocked, _ := snap.Firewall.Verdict(in.SrcIP); blocked {
		WriteJSON(w, 200, routing.Decision{SIPCode: 403, Reason: "source blocked"})
		return nil
	}
	d := snap.AuthorizeFrom(routing.Source{IP: in.SrcIP, AnyTransport: true, Caller: in.Src, Asserted: in.Asserted,
		AnyCaller: in.Src == "" && in.Asserted == "", At: in.At}, in.Dst)
	if d.ClientID != 0 {
		if err := s.Resource("clients").Allowed(ctx, s.db, c, d.ClientID, false); err != nil {
			return Errf(403, "source belongs to another account")
		}
	}
	WriteJSON(w, 200, d)
	return nil
}

// exportLimit is the most call records one export may hold.
const exportLimit = 200_000

// microString writes an amount in micro-units as a plain decimal number.
func microString(v int64) string {
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	return fmt.Sprintf("%s%d.%06d", sign, v/rating.Micro, v%rating.Micro)
}

// exportCDRs streams the call records a list request with the same filters
// would show, all pages of it, as CSV with the party names filled in. When
// the request reaches before the archive horizon, the records PostgreSQL
// no longer keeps follow from ClickHouse, in the same order and form.
func (s *Server) exportCDRs(w http.ResponseWriter, r *http.Request, c Caller) error {
	res := s.resources["cdrs"]
	cond, args, _, _, err := s.listQuery(res, r, c)
	if err != nil {
		return err
	}
	ctx := r.Context()
	horizon, archived, withArchive, err := s.archivedPart(r, res)
	if err != nil {
		return err
	}
	if withArchive {
		args = append(slices.Clip(args), horizon)
		cond = fmt.Sprintf("%s and t.start_at >= $%d", cond, len(args))
	}
	var n int
	if err := s.db.QueryRow(ctx, fmt.Sprintf(
		`select count(*) from (select 1 from cdrs t where %s limit %d) x`, cond, exportLimit+1), args...).Scan(&n); err != nil {
		return err
	}
	if withArchive {
		old, err := s.countArchived(ctx, c, archived, exportLimit)
		if err != nil {
			// The file still holds what PostgreSQL keeps, as before archiving
			// was known.
			log.Printf("api: archived call records from ClickHouse: %v (exporting PostgreSQL's only)", err)
			withArchive = false
		}
		n += old
	}
	if n > exportLimit {
		return Errf(422, "more than %d records match; narrow the time range or the filters", exportLimit)
	}
	// The names of archived records' parties, read before the file starts.
	names := map[string]map[int64]string{}
	if withArchive {
		for _, table := range []string{"clients", "vendors", "vendor_connections", "client_trunks"} {
			if names[table], err = s.partyNames(ctx, table); err != nil {
				return err
			}
		}
	}
	rows, err := s.db.Query(ctx, fmt.Sprintf(`
		select t.start_at, t.answer_at, t.end_at, t.call_id, t.leg, t.src, t.dst, t.src_ip, t.duration_sec, t.sip_code,
		       coalesce(cl.name, ''), coalesce(v.name, ''), coalesce(vc.name, ''),
		       coalesce(t.sell_prefix, ''), t.sell_billed_sec, t.sell_amount, coalesce(t.buy_prefix, ''), t.buy_billed_sec, t.buy_amount,
		       t.bundle_sec, coalesce(dt.name, '')
		from cdrs t
		left join clients cl on cl.id = t.client_id
		left join client_trunks dt on dt.id = t.dst_trunk_id
		left join vendors v on v.id = t.vendor_id
		left join vendor_connections vc on vc.id = t.connection_id
		where %s order by t.start_at desc, t.id desc`, cond), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="cdrs.csv"`)
	out := csv.NewWriter(w)
	out.Write([]string{"start_at", "answer_at", "end_at", "call_id", "leg", "src", "dst", "src_ip", "duration_sec", "sip_code",
		"client", "vendor", "connection", "sell_prefix", "sell_billed_sec", "sell_amount", "buy_prefix", "buy_billed_sec", "buy_amount", "bundle_sec",
		"dst_trunk"})
	stamp := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	complete := true
	for rows.Next() {
		var start, end time.Time
		var answer *time.Time
		var callID, src, dst, srcIP, client, vendor, conn, sellPrefix, buyPrefix, dstTrunk string
		var leg, dur, code, sellSec, buySec, bundleSec int
		var sell, buy int64
		if err := rows.Scan(&start, &answer, &end, &callID, &leg, &src, &dst, &srcIP, &dur, &code,
			&client, &vendor, &conn, &sellPrefix, &sellSec, &sell, &buyPrefix, &buySec, &buy, &bundleSec, &dstTrunk); err != nil {
			complete = false
			break // the header is out; the file simply ends early
		}
		out.Write([]string{stamp(&start), stamp(answer), stamp(&end), callID, strconv.Itoa(leg), src, dst, srcIP,
			strconv.Itoa(dur), strconv.Itoa(code), client, vendor, conn,
			sellPrefix, strconv.Itoa(sellSec), microString(sell), buyPrefix, strconv.Itoa(buySec), microString(buy), strconv.Itoa(bundleSec), dstTrunk})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		complete = false
		log.Printf("api: call record export cut short: %v", err)
	}
	if withArchive && complete {
		micro := func(us *int64) *time.Time {
			if us == nil {
				return nil
			}
			t := time.UnixMicro(*us)
			return &t
		}
		err := s.streamArchived(ctx, c, archived, func(a archivedCSVRow) error {
			return out.Write([]string{stamp(micro(a.Start)), stamp(micro(a.Answer)), stamp(micro(a.End)), a.CallID, strconv.Itoa(a.Leg),
				a.Src, a.Dst, a.SrcIP, strconv.Itoa(a.Dur), strconv.Itoa(a.Code),
				names["clients"][a.Client], names["vendors"][a.Vendor], names["vendor_connections"][a.Connection],
				a.SellPrefix, strconv.Itoa(a.SellSec), microString(a.Sell), a.BuyPrefix, strconv.Itoa(a.BuySec), microString(a.Buy),
				strconv.Itoa(a.BundleSec), names["client_trunks"][a.DstTrunk]})
		})
		if err != nil {
			log.Printf("api: call record export cut short in the archive: %v", err)
		}
	}
	out.Flush()
	return nil
}

// The same keys as ClickHouse expressions.
var reportKeysCH = map[string]string{
	"client": "toString(client_id)", "vendor": "if(vendor_id = 0, '', toString(vendor_id))",
	"prefix": "sell_prefix", "day": "formatDateTime(start_at, '%Y-%m-%d')",
}

// reportFromClickHouse answers the summary report from the analytics copy
// of the call records, with the same fields as the PostgreSQL query below.
// Records reach that copy a moment after they are stored.
func (s *Server) reportFromClickHouse(ctx context.Context, c Caller, groupBy string, from, to time.Time) (string, error) {
	// Visibility is decided where the accounts live; the report is then
	// limited to the clients the caller may see.
	var ids string
	err := s.db.QueryRow(ctx, fmt.Sprintf(
		`select coalesce(string_agg(id::text, ','), '') from clients t where t.reseller_id in %s`, s.scope), c.ResellerID).Scan(&ids)
	if err != nil {
		return "", err
	}
	if ids == "" {
		return "[]", nil
	}
	const stamp = "2006-01-02 15:04:05.000000"
	rows, err := s.analytics.Query(ctx, fmt.Sprintf(`
		select key, calls, answered, intDiv(20000 * answered + calls, 2 * calls) / 100 as asr_pct, -- half up, in whole hundredths
		  if(answered = 0, 0, intDiv(dur, answered)) as acd_sec, dur as duration_sec,
		  intDiv(20000 * (calls - net) + calls, 2 * calls) / 100 as ner_pct,
		  if(rung = 0, 0, intDiv(pdd, rung)) as pdd_ms, net as failed_network, rung as pdd_samples, pdd as pdd_sum_ms,
		  rev as revenue, cst as cost, rev - cst as margin
		from (
		  select %s as key, count() as calls, countIf(duration_sec > 0) as answered,
		    sum(duration_sec) as dur, sum(sell_base) as rev, sum(buy_base) as cst,
		    countIf(`+netFailed+`) as net, countIf(pdd_ms > 0) as rung, sumIf(pdd_ms, pdd_ms > 0) as pdd
		  from cdrs final
		  where start_at >= {from:DateTime64(6, 'UTC')} and start_at < {to:DateTime64(6, 'UTC')} and client_id in (%s)
		  group by key)
		order by key format JSONEachRow`, reportKeysCH[groupBy], ids),
		map[string]string{"from": from.UTC().Format(stamp), "to": to.UTC().Format(stamp)})
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(rows)), "\n")
	if lines[0] == "" {
		return "[]", nil
	}
	return "[" + strings.Join(lines, ",") + "]", nil
}

// netFailed is the condition, in either database, for a leg the network
// ended rather than the far end or the switch's own rules: what the network
// effectiveness ratio counts against. The live view uses the same list.
const netFailed = `sip_code <> 200 and sip_code < 600 and sip_code not in (402, 403, 404, 480, 484, 486, 487)`

var reportKeys = map[string]string{
	"client": "t.client_id::text", "vendor": "t.vendor_id::text",
	"prefix": "t.sell_prefix", "day": "to_char(t.start_at, 'YYYY-MM-DD')",
}

func (s *Server) reportSummary(w http.ResponseWriter, r *http.Request, c Caller) error {
	q := r.URL.Query()
	_, ok := reportKeys[q.Get("group_by")]
	if !ok {
		return Errf(400, "group_by must be client, vendor, prefix or day")
	}
	from, err1 := time.Parse(time.RFC3339, q.Get("from"))
	to, err2 := time.Parse(time.RFC3339, q.Get("to"))
	if err1 != nil || err2 != nil {
		return Errf(400, "from and to must be RFC 3339 timestamps")
	}
	out, err := s.summary(r.Context(), c, q.Get("group_by"), from, to)
	if err != nil {
		return err
	}
	WriteRaw(w, 200, out)
	return nil
}

// summary is the summary report as JSON: from ClickHouse when there is one,
// else (or on its failure) from PostgreSQL. groupBy must be a key of
// reportKeys.
func (s *Server) summary(ctx context.Context, c Caller, groupBy string, from, to time.Time) (string, error) {
	key := reportKeys[groupBy]
	if s.analytics != nil {
		out, err := s.reportFromClickHouse(ctx, c, groupBy, from, to)
		if err == nil {
			return out, nil
		}
		// The report still answers from the records of rating.
		log.Printf("api: summary report from ClickHouse: %v (using PostgreSQL)", err)
	}
	var out string
	err := s.db.QueryRow(ctx, fmt.Sprintf(`
		select coalesce(jsonb_agg(to_jsonb(x) order by x.key), '[]')::text from (
		  select coalesce(%s, '') as key,
		    count(*) as calls,
		    count(*) filter (where duration_sec > 0) as answered,
		    round(100.0 * count(*) filter (where duration_sec > 0) / count(*), 2) as asr_pct,
		    coalesce(sum(duration_sec) / nullif(count(*) filter (where duration_sec > 0), 0), 0) as acd_sec,
		    sum(duration_sec) as duration_sec,
		    round(100.0 * (count(*) - count(*) filter (where `+netFailed+`)) / count(*), 2) as ner_pct,
		    coalesce(sum(pdd_ms) filter (where pdd_ms > 0) / nullif(count(*) filter (where pdd_ms > 0), 0), 0) as pdd_ms,
		    count(*) filter (where `+netFailed+`) as failed_network,
		    count(*) filter (where pdd_ms > 0) as pdd_samples, coalesce(sum(pdd_ms) filter (where pdd_ms > 0), 0) as pdd_sum_ms,
		    sum(coalesce(sell_base, sell_amount))::bigint as revenue, sum(coalesce(buy_base, buy_amount))::bigint as cost,
		    (sum(coalesce(sell_base, sell_amount)) - sum(coalesce(buy_base, buy_amount)))::bigint as margin
		  from cdrs t
		  where t.start_at >= $2 and t.start_at < $3
		    and (select reseller_id from clients where id = t.client_id) in %s
		  group by 1) x`, key, s.scope), c.ResellerID, from, to).Scan(&out)
	return out, err
}
