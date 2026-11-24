// SPDX-License-Identifier: AGPL-3.0-only

// Package store owns the PostgreSQL connection, migrations and the loader
// that builds routing snapshots.
package store

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"sort"
	"time"
	_ "time/tzdata" // tariffs and plans name time zones; the image may have none

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"softswitch/core/db"
	"softswitch/core/pkg/rating"
	"softswitch/core/pkg/routing"
)

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// Migrate applies the core schema.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return MigrateFS(ctx, pool, db.Postgres, "migrations/postgres")
}

// MigrateFS applies every migration in dir that has not run yet, in file
// name order, each in its own transaction. File names must be unique across
// all migration sets applied to a database.
func MigrateFS(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, dir string) error {
	if _, err := pool.Exec(ctx, `create table if not exists schema_migrations (name text primary key, applied_at timestamptz not null default now())`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	// Several services migrate on start; the lock makes them take turns, so
	// one waits for the other instead of failing on a half-applied step.
	if _, err := conn.Exec(ctx, `select pg_advisory_lock(hashtext('softswitch_migrations'))`); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `select pg_advisory_unlock(hashtext('softswitch_migrations'))`)
	for _, e := range entries {
		var done bool
		if err := conn.QueryRow(ctx, `select exists(select 1 from schema_migrations where name = $1)`, e.Name()).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sql, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return err
		}
		script := "begin;\n" + string(sql) + ";\ninsert into schema_migrations (name) values ('" + e.Name() + "');\ncommit;"
		if _, err := conn.Conn().PgConn().Exec(ctx, script).ReadAll(); err != nil {
			conn.Conn().PgConn().Exec(ctx, "rollback").ReadAll()
			return fmt.Errorf("migration %s: %w", e.Name(), err)
		}
	}
	return nil
}

// Querier is satisfied by both a pool and a transaction.
type Querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// zonesOf reads the time zone and the owning account of every row of table
// (tariffs or plans). A zone this system does not know is read as UTC and
// logged.
func zonesOf(ctx context.Context, q Querier, table string) (map[int64]*time.Location, map[int64]int64, error) {
	rows, err := q.Query(ctx, `select id, timezone, reseller_id from `+table)
	if err != nil {
		return nil, nil, err
	}
	zones, owners := map[int64]*time.Location{}, map[int64]int64{}
	var id, owner int64
	var name string
	_, err = pgx.ForEachRow(rows, []any{&id, &name, &owner}, func() error {
		loc, err := time.LoadLocation(name)
		if err != nil {
			log.Printf("store: %s %d: time zone %q unknown here, UTC used", table, id, name)
			loc = time.UTC
		}
		zones[id], owners[id] = loc, owner
		return nil
	})
	return zones, owners, err
}

// calendars reads every account's public holidays (T-19). The table is
// small, a few rows a year, so all of it is kept: the simulator may ask
// about any date.
func calendars(ctx context.Context, q Querier) (map[int64]rating.Calendar, error) {
	rows, err := q.Query(ctx, `select reseller_id, day, counts_as from holidays`)
	if err != nil {
		return nil, fmt.Errorf("holidays: %w", err)
	}
	cals := map[int64]rating.Calendar{}
	var owner int64
	var day time.Time
	var as int
	_, err = pgx.ForEachRow(rows, []any{&owner, &day, &as}, func() error {
		if cals[owner] == nil {
			cals[owner] = rating.Calendar{}
		}
		cals[owner][rating.DateOf(day)] = time.Weekday(as % 7) // Sunday is 7 in the table, 0 here
		return nil
	})
	return cals, err
}

// LoadSnapshot reads the whole routing configuration into memory. Rates are
// read again only for the tariffs whose rates changed since the previous
// load in this process, and all of them every FullReloadEvery (rates.go).
func LoadSnapshot(ctx context.Context, q Querier) (*routing.Snapshot, error) {
	return loadSnapshot(ctx, q, true)
}

// loadSnapshot is LoadSnapshot; without cached it reads every rate and
// leaves what the next load may keep as it was.
func loadSnapshot(ctx context.Context, q Querier, cached bool) (*routing.Snapshot, error) {
	s := &routing.Snapshot{
		Clients:     map[int64]routing.Client{},
		Tariffs:     map[int64]map[string]rating.Rate{},
		Plans:       map[int64]routing.Plan{},
		Connections: map[int64]routing.Connection{},
	}

	// A trunk is known by its addresses and networks, or signs in with a
	// user name; one that signs in but has no password yet cannot be used.
	rows, err := q.Query(ctx, `
		select id, client_id, case when auth_type = 'ip' then addresses::text[] else '{}' end,
		       case when auth_type = 'digest' then username else '' end, coalesce(password_ha1, ''), tech_prefix,
		       security, tls_cert_name, max_contacts, number_rules, max_duration_sec, max_calls, max_cps, codecs, transcode
		from client_trunks where enabled and (auth_type = 'ip' or password_ha1 is not null)`)
	if err != nil {
		return nil, err
	}
	if s.Trunks, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (t routing.Trunk, err error) {
		return t, r.Scan(&t.ID, &t.ClientID, &t.Addrs, &t.Username, &t.HA1, &t.TechPrefix, &t.Security, &t.CertName, &t.MaxContacts,
			&t.NumberRules, &t.MaxDuration, &t.MaxCalls, &t.MaxCPS, &t.Codecs, &t.Transcode)
	}); err != nil {
		return nil, err
	}
	// The table keeps the rules sorted already; sorting here as well keeps
	// the decision right whatever wrote them.
	for _, t := range s.Trunks {
		routing.SortNumberRules(t.NumberRules)
	}

	// A client is inactive while the account that owns it is suspended.
	rows, err = q.Query(ctx, `
		select c.id, c.reseller_id, c.status = 'active' and r.status = 'active',
		       c.balance, c.credit_limit, coalesce(c.tariff_id, 0), coalesce(c.routing_plan_id, 0), c.max_calls, c.max_cps,
		       coalesce((select x.rate from exchange_rates x where x.reseller_id = c.reseller_id and x.currency = c.currency and c.currency <> r.currency), 0)
		from clients c join resellers r on r.id = c.reseller_id`)
	if err != nil {
		return nil, err
	}
	clients, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (c routing.Client, err error) {
		return c, r.Scan(&c.ID, &c.ResellerID, &c.Active, &c.Balance, &c.CreditLimit, &c.TariffID, &c.RoutingPlanID, &c.MaxCalls, &c.MaxCPS, &c.Fx)
	})
	if err != nil {
		return nil, err
	}
	for _, c := range clients {
		s.Clients[c.ID] = c
	}

	// Every rate in force, by tariff and prefix. A prefix with one rate for
	// all hours is looked up directly; one with rates by day or hour keeps
	// the list to choose from call by call. Only the tariffs whose rates
	// changed are read (rates.go).
	zones, tariffOwners, err := zonesOf(ctx, q, "tariffs")
	if err != nil {
		return nil, err
	}
	cals, err := calendars(ctx, q)
	if err != nil {
		return nil, err
	}
	s.Timed = map[int64]routing.TimedTariff{}
	if err := loadRates(ctx, q, s, zones, tariffOwners, cals, cached); err != nil {
		return nil, err
	}

	planZones, planOwners, err := zonesOf(ctx, q, "routing_plans")
	if err != nil {
		return nil, err
	}
	rows, err = q.Query(ctx, `select id, policy, allow_loss from routing_plans`)
	if err != nil {
		return nil, err
	}
	var plan routing.Plan
	if _, err = pgx.ForEachRow(rows, []any{&plan.ID, &plan.Policy, &plan.AllowLoss}, func() error {
		p := plan
		p.Loc = planZones[p.ID]
		p.Cal = cals[planOwners[p.ID]]
		p.Entries = map[string][]routing.Entry{}
		s.Plans[p.ID] = p
		return nil
	}); err != nil {
		return nil, err
	}

	rows, err = q.Query(ctx, `select routing_plan_id, prefix, connection_id, priority, weight, days, time_from, time_to from routing_entries where enabled`)
	if err != nil {
		return nil, err
	}
	var pid int64
	var prefix string
	var entry routing.Entry
	if _, err = pgx.ForEachRow(rows, []any{&pid, &prefix, &entry.ConnectionID, &entry.Priority, &entry.Weight, &entry.Days, &entry.From, &entry.To}, func() error {
		if p, ok := s.Plans[pid]; ok {
			p.Entries[prefix] = append(p.Entries[prefix], entry)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	rows, err = q.Query(ctx, `
		select vc.id, vc.vendor_id, vc.host, vc.port, vc.transport, vc.tls_name, vc.tech_prefix, vc.strip_digits,
		       vc.max_calls, vc.max_cps, coalesce(v.tariff_id, 0), vc.enabled and v.status = 'active',
		       coalesce((select x.rate from exchange_rates x join resellers r on r.id = x.reseller_id
		                 where x.reseller_id = v.reseller_id and x.currency = v.currency and v.currency <> r.currency), 0),
		       vc.auth_username, vc.auth_secret, vc.number_plus, vc.cli_strip_digits, vc.cli_add_prefix, vc.pai_mode,
		       vc.ring_timeout_sec, vc.max_duration_sec, vc.failover_mode, vc.failover_codes, vc.options_interval_sec, vc.codecs, vc.transcode,
		       vc.register, vc.register_expires_sec, vc.register_domain
		from vendor_connections vc join vendors v on v.id = vc.vendor_id`)
	if err != nil {
		return nil, err
	}
	var c routing.Connection
	var failover []int32
	if _, err = pgx.ForEachRow(rows, []any{&c.ID, &c.VendorID, &c.Host, &c.Port, &c.Transport, &c.TLSName, &c.TechPrefix, &c.StripDigits, &c.MaxCalls, &c.MaxCPS, &c.VendorTariffID, &c.Enabled, &c.Fx,
		&c.AuthUser, &c.AuthSealed, &c.NumberPlus, &c.CLIStrip, &c.CLIAdd, &c.PAIMode,
		&c.RingTimeout, &c.MaxDuration, &c.FailoverMode, &failover, &c.OptionsInterval, &c.Codecs, &c.Transcode,
		&c.Register, &c.RegisterExpires, &c.RegisterDomain}, func() error {
		c.FailoverCodes = make([]int, len(failover))
		for i, code := range failover {
			c.FailoverCodes[i] = int(code)
		}
		s.Connections[c.ID] = c
		// Scanned afresh for the next row.
		c.AuthSealed, c.Codecs, failover = nil, nil, nil
		return nil
	}); err != nil {
		return nil, err
	}

	// The firewall of the account that runs the system; nobody else's rules
	// may drop traffic.
	rows, err = q.Query(ctx, `
		select cidr, action from firewall_rules
		where enabled and (expires_at is null or expires_at > now())
		  and reseller_id = (select id from resellers order by id limit 1)`)
	if err != nil {
		return nil, err
	}
	var deny, allow []string
	var cidr, action string
	if _, err = pgx.ForEachRow(rows, []any{&cidr, &action}, func() error {
		if action == "deny" {
			deny = append(deny, cidr)
		} else {
			allow = append(allow, cidr)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	var unread []string
	if s.Firewall, unread = routing.NewFirewall(deny, allow); len(unread) > 0 {
		log.Printf("store: firewall rules that cannot be read, left out: %v", unread)
	}

	rows, err = q.Query(ctx, `select reseller_id, coalesce(client_id, 0), number_source, prefix, action, strip_digits, add_prefix from cli_rules where enabled`)
	if err != nil {
		return nil, err
	}
	s.CLIRules = routing.CLIRules{}
	var rid, cid int64
	var source string
	var rule routing.CLIRule
	if _, err = pgx.ForEachRow(rows, []any{&rid, &cid, &source, &prefix, &rule.Action, &rule.StripDigits, &rule.AddPrefix}, func() error {
		s.CLIRules.Add(rid, cid, source, prefix, rule)
		return nil
	}); err != nil {
		return nil, err
	}

	// Bundles of minutes, with what the current period has used of each. The
	// period is a month in the zone of the client's tariff; the month before
	// is read too so that a zone still in it finds its count.
	now := time.Now()
	rows, err = q.Query(ctx, `select bundle_id, period, used_sec from bundle_usage where period >= $1`,
		rating.Period(now.AddDate(0, 0, -2), time.UTC))
	if err != nil {
		return nil, err
	}
	type usageKey struct {
		bundle int64
		period string
	}
	used := map[usageKey]int64{}
	var uk usageKey
	var sec int64
	if _, err = pgx.ForEachRow(rows, []any{&uk.bundle, &uk.period, &sec}, func() error {
		used[uk] = sec
		return nil
	}); err != nil {
		return nil, err
	}
	rows, err = q.Query(ctx, `select b.id, b.client_id, b.prefixes, b.seconds, coalesce(c.tariff_id, 0)
		from bundles b join clients c on c.id = b.client_id where b.enabled order by b.id`)
	if err != nil {
		return nil, err
	}
	s.Bundles = map[int64][]routing.Bundle{}
	var b routing.Bundle
	var tid int64
	if _, err = pgx.ForEachRow(rows, []any{&b.ID, &cid, &b.Prefixes, &b.Seconds, &tid}, func() error {
		b.Loc = zones[tid]
		b.Period = rating.Period(now, b.Loc)
		b.Used = used[usageKey{b.ID, b.Period}]
		s.Bundles[cid] = append(s.Bundles[cid], b)
		b.Prefixes = nil // scanned afresh for the next row
		return nil
	}); err != nil {
		return nil, err
	}
	return s, nil
}
