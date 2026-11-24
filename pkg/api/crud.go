// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Resource describes one table exposed through generic CRUD. Scope
// expressions use {a} for the row alias and yield the owning account id.
type Resource struct {
	Table      string
	Cols       []string          // writable columns
	Owner      string            // column defaulted to the caller's account on create
	Scope      string            // who may read a row
	WriteScope string            // who may change a row; defaults to Scope
	Refs       map[string]string // column -> resource it must point into the caller's scope of
	Hide       []string          // columns never returned
	Filters    []string          // extra read-only columns allowed as list filters
	ReadOnly   bool
	ListOnly   bool // no generic GET by id; the owner registers its own

	// List behaviour.
	Search  []string                     // columns matched by ?q= (case-insensitive substring)
	Prefix  []string                     // filter columns matched as "starts with" instead of equality
	TimeCol string                       // column bounded by ?from= and ?to= (RFC 3339)
	Named   map[string]map[string]string // ?name=value -> SQL condition on alias t
	Order   string                       // sort expression on alias t; defaults to t.id
	NoCount bool                         // do not count matches (large tables)

	srv  *Server
	name string
}

const (
	viaTariff = `(select reseller_id from tariffs where id = {a}.tariff_id)`
	viaPlan   = `(select reseller_id from routing_plans where id = {a}.routing_plan_id)`
	viaVendor = `(select reseller_id from vendors where id = {a}.vendor_id)`
	viaClient = `(select reseller_id from clients where id = {a}.client_id)`
)

func coreResources() map[string]*Resource {
	return map[string]*Resource{
		"resellers": {Table: "resellers", Scope: `{a}.id`, ReadOnly: true},
		"tariffs": {
			Table: "tariffs", Owner: "reseller_id", Scope: `{a}.reseller_id`,
			Cols:   []string{"reseller_id", "name", "kind", "currency", "timezone"},
			Search: []string{"name"},
		},
		"rates": {
			Table: "rates", Scope: viaTariff,
			Cols:   []string{"tariff_id", "prefix", "price_per_min", "connect_fee", "initial_sec", "increment_sec", "grace_sec", "blocked", "effective_from", "effective_to", "days", "time_from", "time_to"},
			Prefix: []string{"prefix"}, Order: "t.prefix, t.effective_from desc, t.id",
			Named: map[string]map[string]string{"validity": {
				"current": "t.effective_from <= now() and (t.effective_to is null or t.effective_to > now())",
				"future":  "t.effective_from > now()",
				"expired": "t.effective_to <= now()",
			}},
		},
		"routing-plans": {
			Table: "routing_plans", Owner: "reseller_id", Scope: `{a}.reseller_id`,
			Cols:   []string{"reseller_id", "name", "policy", "allow_loss", "timezone"},
			Search: []string{"name"},
		},
		"routing-entries": {
			Table: "routing_entries", Scope: viaPlan,
			Cols:  []string{"routing_plan_id", "prefix", "connection_id", "priority", "weight", "enabled", "days", "time_from", "time_to"},
			Refs:  map[string]string{"connection_id": "vendor-connections"},
			Order: "t.prefix, t.priority, t.id",
		},
		"cli-rules": {
			Table: "cli_rules", Owner: "reseller_id", Scope: `{a}.reseller_id`,
			Cols:   []string{"reseller_id", "client_id", "number_source", "prefix", "action", "strip_digits", "add_prefix", "note", "enabled"},
			Refs:   map[string]string{"client_id": "clients"},
			Prefix: []string{"prefix"}, Search: []string{"note"},
			Order: "t.client_id nulls first, t.number_source, t.prefix, t.id",
		},
		"firewall-rules": {
			Table: "firewall_rules", Owner: "reseller_id", Scope: `{a}.reseller_id`,
			Cols:   []string{"reseller_id", "cidr", "action", "note", "enabled", "expires_at"},
			Search: []string{"cidr", "note"}, Order: "t.action, t.cidr",
		},
		// Public holidays: dates whose windows count them as another day
		// of the week (T-19, ADR-0044).
		"holidays": {
			Table: "holidays", Owner: "reseller_id", Scope: `{a}.reseller_id`,
			Cols:   []string{"reseller_id", "day", "counts_as", "name"},
			Search: []string{"name"}, TimeCol: "day", Order: "t.day, t.id",
		},
		"report-schedules": {
			Table: "report_schedules", Owner: "reseller_id", Scope: `{a}.reseller_id`,
			Cols:   []string{"reseller_id", "name", "group_by", "period", "recipients", "timezone", "send_hour", "enabled"},
			Search: []string{"name", "recipients"},
		},
		"exchange-rates": {
			Table: "exchange_rates", Owner: "reseller_id", Scope: `{a}.reseller_id`,
			Cols: []string{"reseller_id", "currency", "rate"}, Order: "t.currency",
		},
		"vendors": {
			Table: "vendors", Owner: "reseller_id", Scope: `{a}.reseller_id`,
			Cols:    []string{"reseller_id", "name", "currency", "tariff_id", "email", "tax_id", "address", "status"},
			Refs:    map[string]string{"tariff_id": "tariffs"},
			Filters: []string{"balance"},
			Search:  []string{"name", "email", "tax_id"},
		},
		"vendor-connections": {
			Table: "vendor_connections", Scope: viaVendor,
			Cols: []string{"vendor_id", "name", "host", "port", "transport", "tls_name", "tech_prefix", "strip_digits", "max_calls", "max_cps", "enabled",
				"number_plus", "cli_strip_digits", "cli_add_prefix", "pai_mode", "ring_timeout_sec", "max_duration_sec",
				"failover_mode", "failover_codes", "options_interval_sec", "codecs", "transcode",
				"register", "register_expires_sec", "register_domain"},
			// The sign-in name and password are set together through an
			// operation of their own (connections.go); the sealed password
			// is never returned, only whether there is one (auth_set).
			Hide:    []string{"auth_secret"},
			Filters: []string{"auth_username", "auth_set"},
		},
		"clients": {
			Table: "clients", Owner: "reseller_id", Scope: `{a}.reseller_id`,
			Cols: []string{"reseller_id", "name", "billing_mode", "currency", "credit_limit", "tariff_id", "routing_plan_id",
				"max_calls", "max_cps", "email", "tax_id", "address", "status", "low_balance"},
			Refs:    map[string]string{"tariff_id": "tariffs", "routing_plan_id": "routing-plans"},
			Filters: []string{"balance"},
			Search:  []string{"name", "email", "tax_id"},
		},
		"client-trunks": {
			Table: "client_trunks", Scope: viaClient,
			Cols: []string{"client_id", "name", "auth_type", "addresses", "username", "password_ha1", "tech_prefix", "security", "tls_cert_name", "max_contacts", "enabled",
				"number_rules", "number_strip", "number_add", "max_duration_sec", "max_calls", "max_cps", "codecs", "transcode"},
			// number_rules is the list the engine uses; number_strip and
			// number_add are kept in step with it by a trigger for clients
			// written before the list (0025_trunk_number_rules.sql).
			Hide: []string{"password_ha1"},
		},
		"bundles": {
			Table: "bundles", Scope: viaClient,
			Cols:    []string{"client_id", "name", "prefixes", "seconds", "enabled"},
			Filters: []string{"client_id"}, Search: []string{"name"}, Order: "t.client_id, t.id",
		},
		"balance-alerts": {
			Table: "balance_alerts", Scope: viaClient, ReadOnly: true,
			Filters: []string{"client_id"}, TimeCol: "raised_at", Order: "t.raised_at desc, t.id desc",
			Named: map[string]map[string]string{"state": {
				"open": "t.cleared_at is null", "cleared": "t.cleared_at is not null",
			}},
		},
		// Who changed what: written by Audit, read here, changed by nobody.
		"audit-log": {
			Table: "audit_log", Scope: `{a}.reseller_id`, ReadOnly: true,
			Filters: []string{"reseller_id", "actor_type", "actor_id", "resource", "action", "row_id"},
			Search:  []string{"actor_name", "label", "resource", "action", "ip", "changes::text", "detail::text"},
			TimeCol: "at", Order: "t.at desc, t.id desc",
		},
		"cdrs": {
			Table: "cdrs", Scope: viaClient, ReadOnly: true,
			Filters: []string{"client_id", "vendor_id", "connection_id", "dst_trunk_id", "call_id", "dst", "src", "direction", "sip_code"},
			Prefix:  []string{"dst", "src"}, TimeCol: "start_at", Order: "t.start_at desc, t.id desc", NoCount: true,
		},
	}
}

// AddResource registers a resource, replacing any existing one of that name.
func (s *Server) AddResource(name string, res *Resource) {
	res.srv, res.name = s, name
	s.resources[name] = res
}

// Resource returns a registered resource so that an extension can use or
// extend it; it panics on an unknown name.
func (s *Server) Resource(name string) *Resource {
	res, ok := s.resources[name]
	if !ok {
		panic("api: unknown resource " + name)
	}
	return res
}

func (res *Resource) scopeFor(alias string, write bool) string {
	expr := res.Scope
	if write && res.WriteScope != "" {
		expr = res.WriteScope
	}
	return strings.ReplaceAll(expr, "{a}", alias)
}

// audit records a generic change to a row: what it is now against what it
// was before (nil for a new row; a deleted one is no longer there). The
// resource's hidden columns are masked. An update that changed nothing
// leaves no record.
func (res *Resource) audit(ctx context.Context, tx pgx.Tx, r *http.Request, c Caller, id int64, action string, before AuditRow) error {
	var after AuditRow
	if action != "delete" {
		var err error
		if after, err = AuditSnapshot(ctx, tx, res.Table, id); err != nil {
			return err
		}
	}
	e := AuditEntry{Resource: res.name, RowID: id, Action: action, Changes: AuditDiff(before, after, res.Hide...)}
	if e.Label = after.Label(); e.Label == "" {
		e.Label = before.Label()
	}
	if action == "update" && len(e.Changes) == 0 {
		AuditNothing(r)
		return nil
	}
	return res.srv.Audit(ctx, tx, r, c, e)
}

func (s *Server) registerCRUD() {
	for name, res := range s.resources {
		s.Handle("GET /v1/"+name, false, s.list(res), genericOp())
		if !res.ListOnly {
			s.Handle("GET /v1/"+name+"/{id}", false, s.get(res), genericOp())
		}
		if res.ReadOnly {
			continue
		}
		s.Handle("POST /v1/"+name, true, s.create(res), genericOp())
		s.Handle("PATCH /v1/"+name+"/{id}", true, s.update(res), genericOp())
		s.Handle("DELETE /v1/"+name+"/{id}", true, s.remove(res), genericOp())
	}
}

// Querier is satisfied by both a pool and a transaction.
type Querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// RowJSON returns one row as JSON if the caller may read it.
func (res *Resource) RowJSON(ctx context.Context, q Querier, c Caller, id int64) (string, error) {
	var out string
	err := q.QueryRow(ctx, fmt.Sprintf(
		`select (to_jsonb(t) - coalesce($3::text[], '{}'))::text from %s t where t.id = $2 and %s in %s`,
		res.Table, res.scopeFor("t", false), res.srv.scope), c.ResellerID, id, res.Hide).Scan(&out)
	return out, err
}

// Allowed reports whether the caller may read (or change) the row.
func (res *Resource) Allowed(ctx context.Context, q Querier, c Caller, id int64, write bool) error {
	var ok bool
	err := q.QueryRow(ctx, fmt.Sprintf(`select exists(select 1 from %s t where t.id = $2 and %s in %s)`,
		res.Table, res.scopeFor("t", write), res.srv.scope), c.ResellerID, id).Scan(&ok)
	if err == nil && !ok {
		err = pgx.ErrNoRows
	}
	return err
}

// checkRefs rejects rows that point at objects outside the caller's scope.
func (res *Resource) checkRefs(ctx context.Context, q Querier, c Caller, id int64) error {
	for col, target := range res.Refs {
		ref := res.srv.Resource(target)
		var ok bool
		err := q.QueryRow(ctx, fmt.Sprintf(
			`select not exists(select 1 from %s t where t.id = $2 and t.%s is not null
			   and not exists(select 1 from %s r where r.id = t.%s and %s in %s))`,
			res.Table, col, ref.Table, col, ref.scopeFor("r", false), res.srv.scope), c.ResellerID, id).Scan(&ok)
		if err != nil {
			return err
		}
		if !ok {
			return Errf(403, "%s refers to an object outside your account", col)
		}
	}
	return nil
}

// payload validates the body against the writable columns and returns the
// column list plus the JSON to feed jsonb_populate_record.
func (res *Resource) payload(r *http.Request, c Caller, create bool) (string, string, error) {
	var body map[string]json.RawMessage
	if err := ReadJSON(r, &body); err != nil {
		return "", "", err
	}
	if create && res.Owner != "" {
		if _, ok := body[res.Owner]; !ok {
			body[res.Owner] = json.RawMessage(strconv.FormatInt(c.ResellerID, 10))
		}
	}
	var cols []string
	for k := range body {
		if !slices.Contains(res.Cols, k) {
			return "", "", Errf(400, "unknown or read-only field %q", k)
		}
		cols = append(cols, k)
	}
	if len(cols) == 0 {
		return "", "", Errf(400, "no fields given")
	}
	slices.Sort(cols)
	raw, _ := json.Marshal(body)
	return strings.Join(cols, ", "), string(raw), nil
}

// likeEscaper makes user input literal inside a LIKE pattern.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// list serves a page of a resource. Query parameters: limit, offset, q,
// from/to, the resource's named filters, and any column as a filter. Unless
// the resource opts out, the number of matches is sent in X-Total-Count.
// listQuery turns a list request into the condition that selects its rows:
// the caller's visibility plus the filters in the query string.
func (s *Server) listQuery(res *Resource, r *http.Request, c Caller) (cond string, args []any, limit, offset int, err error) {
	limit, offset = 100, 0
	where := []string{res.scopeFor("t", false) + " in " + s.scope}
	args = []any{c.ResellerID}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	for k, v := range r.URL.Query() {
		val := v[0]
		switch {
		case k == "limit" || k == "offset":
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 {
				return "", nil, 0, 0, Errf(400, "invalid %s", k)
			}
			if k == "limit" {
				limit = min(n, 1000)
			} else {
				offset = n
			}
		case k == "q" && len(res.Search) > 0:
			if val == "" {
				continue
			}
			p := arg("%" + likeEscaper.Replace(val) + "%")
			var ors []string
			for _, col := range res.Search {
				ors = append(ors, fmt.Sprintf("t.%s ilike %s", col, p))
			}
			where = append(where, "("+strings.Join(ors, " or ")+")")
		case (k == "from" || k == "to") && res.TimeCol != "":
			at, err := time.Parse(time.RFC3339, val)
			if err != nil {
				return "", nil, 0, 0, Errf(400, "%s must be an RFC 3339 timestamp", k)
			}
			op := ">="
			if k == "to" {
				op = "<"
			}
			where = append(where, fmt.Sprintf("t.%s %s %s", res.TimeCol, op, arg(at)))
		case res.Named[k] != nil:
			cond, ok := res.Named[k][val]
			if !ok {
				return "", nil, 0, 0, Errf(400, "invalid %s", k)
			}
			where = append(where, "("+cond+")")
		case slices.Contains(res.Prefix, k):
			where = append(where, fmt.Sprintf("t.%s like %s", k, arg(likeEscaper.Replace(val)+"%")))
		case k == "id" || slices.Contains(res.Cols, k) || slices.Contains(res.Filters, k):
			where = append(where, fmt.Sprintf("t.%s::text = %s", k, arg(val)))
		default:
			return "", nil, 0, 0, Errf(400, "unknown filter %q", k)
		}
	}
	return strings.Join(where, " and "), args, limit, offset, nil
}

func (s *Server) list(res *Resource) Handler {
	return func(w http.ResponseWriter, r *http.Request, c Caller) error {
		if res.Table == "cdrs" {
			if done, err := s.listCDRsFromClickHouse(w, r, c); done || err != nil {
				return err
			}
		}
		cond, args, limit, offset, err := s.listQuery(res, r, c)
		if err != nil {
			return err
		}
		if res.Table == "cdrs" {
			// Records older than the archive horizon are in ClickHouse only.
			if done, err := s.listCDRsAcrossArchive(w, r, c, res, cond, args, limit, offset); done || err != nil {
				return err
			}
		}
		arg := func(v any) string {
			args = append(args, v)
			return "$" + strconv.Itoa(len(args))
		}
		order := res.Order
		if order == "" {
			order = "t.id"
		}
		ctx := r.Context()
		if !res.NoCount {
			var total int64
			if err := s.db.QueryRow(ctx, fmt.Sprintf(`select count(*) from %s t where %s`, res.Table, cond), args...).Scan(&total); err != nil {
				return err
			}
			w.Header().Set("X-Total-Count", strconv.FormatInt(total, 10))
		}
		var out string
		err = s.db.QueryRow(ctx, fmt.Sprintf(
			`select coalesce(jsonb_agg(x.j order by x.n), '[]')::text from (
			   select row_number() over (order by %[3]s) as n, to_jsonb(t) - %[4]s::text[] as j
			   from %[1]s t where %[2]s order by %[3]s limit %[5]d offset %[6]d) x`,
			res.Table, cond, order, arg(append([]string{}, res.Hide...)), limit, offset), args...).Scan(&out)
		if err != nil {
			return err
		}
		WriteRaw(w, 200, out)
		return nil
	}
}

func (s *Server) get(res *Resource) Handler {
	return func(w http.ResponseWriter, r *http.Request, c Caller) error {
		id, err := PathID(r)
		if err != nil {
			return err
		}
		out, err := res.RowJSON(r.Context(), s.db, c, id)
		if errors.Is(err, pgx.ErrNoRows) && res.Table == "cdrs" && s.analytics != nil {
			// The record may have left PostgreSQL for the archive.
			archived, cherr := s.archivedCDR(r.Context(), c, id)
			if cherr != nil {
				log.Printf("api: call record %d from ClickHouse: %v", id, cherr)
			} else if archived != "" {
				out, err = archived, nil
			}
		}
		if err != nil {
			return err
		}
		WriteRaw(w, 200, out)
		return nil
	}
}

func (s *Server) create(res *Resource) Handler {
	return func(w http.ResponseWriter, r *http.Request, c Caller) error {
		cols, raw, err := res.payload(r, c, true)
		if err != nil {
			return err
		}
		ctx := r.Context()
		var out string
		err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
			var id int64
			if err := tx.QueryRow(ctx, fmt.Sprintf(
				`insert into %[1]s (%[2]s) select %[2]s from jsonb_populate_record(null::%[1]s, $1::jsonb) returning id`,
				res.Table, cols), raw).Scan(&id); err != nil {
				return err
			}
			if err := res.Allowed(ctx, tx, c, id, true); err != nil {
				return Errf(403, "parent object is outside your account")
			}
			if err := res.checkRefs(ctx, tx, c, id); err != nil {
				return err
			}
			if err := res.audit(ctx, tx, r, c, id, "create", nil); err != nil {
				return err
			}
			out, err = res.RowJSON(ctx, tx, c, id)
			return err
		})
		if err != nil {
			return err
		}
		WriteRaw(w, 201, out)
		return nil
	}
}

func (s *Server) update(res *Resource) Handler {
	return func(w http.ResponseWriter, r *http.Request, c Caller) error {
		id, err := PathID(r)
		if err != nil {
			return err
		}
		cols, raw, err := res.payload(r, c, false)
		if err != nil {
			return err
		}
		ctx := r.Context()
		var out string
		err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
			if err := res.Allowed(ctx, tx, c, id, true); err != nil {
				return err
			}
			before, err := AuditSnapshot(ctx, tx, res.Table, id)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, fmt.Sprintf(
				`update %[1]s set (%[2]s) = (select %[2]s from jsonb_populate_record(null::%[1]s, $1::jsonb)) where id = $2`,
				res.Table, cols), raw, id); err != nil {
				return err
			}
			if err := res.Allowed(ctx, tx, c, id, true); err != nil {
				return Errf(403, "cannot move object outside your account")
			}
			if err := res.checkRefs(ctx, tx, c, id); err != nil {
				return err
			}
			if err := res.audit(ctx, tx, r, c, id, "update", before); err != nil {
				return err
			}
			out, err = res.RowJSON(ctx, tx, c, id)
			return err
		})
		if err != nil {
			return err
		}
		WriteRaw(w, 200, out)
		return nil
	}
}

func (s *Server) remove(res *Resource) Handler {
	return func(w http.ResponseWriter, r *http.Request, c Caller) error {
		id, err := PathID(r)
		if err != nil {
			return err
		}
		ctx := r.Context()
		err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
			if err := res.Allowed(ctx, tx, c, id, true); err != nil {
				return err
			}
			before, err := AuditSnapshot(ctx, tx, res.Table, id)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, fmt.Sprintf(`delete from %s where id = $1`, res.Table), id); err != nil {
				return err
			}
			return res.audit(ctx, tx, r, c, id, "delete", before)
		})
		if err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	}
}
