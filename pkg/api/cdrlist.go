// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Call records can be read from ClickHouse as well as from PostgreSQL: the
// same filters, the same order, rows of the same shape. Two things need it.
//
//   - CDR_LIST_SOURCE=clickhouse makes the whole list read ClickHouse, for
//     a PostgreSQL that holds too many records to search.
//   - Once the rater archives (CDR_KEEP_DAYS), PostgreSQL no longer holds
//     the records older than the archive horizon (table cdr_archive). The
//     single record, the list and the CSV then read those from ClickHouse
//     and the rest from PostgreSQL (T-24).

// ListCDRsFromAnalytics makes GET /v1/cdrs read from ClickHouse when one is
// configured. An edition that adds fields of its own to call records keeps
// reading PostgreSQL: ClickHouse does not have them.
func (s *Server) ListCDRsFromAnalytics(on bool) { s.cdrsFromCH = on }

// cdrFilters maps a list filter to its ClickHouse condition.
var cdrFilters = map[string]string{
	"id": "id = {id:UInt64}", "client_id": "client_id = {client_id:UInt64}", "vendor_id": "vendor_id = {vendor_id:UInt64}",
	"connection_id": "connection_id = {connection_id:UInt64}", "dst_trunk_id": "dst_trunk_id = {dst_trunk_id:UInt64}", "sip_code": "sip_code = {sip_code:UInt16}",
	"call_id": "call_id = {call_id:String}", "direction": "direction = {direction:String}",
	"dst": "startsWith(dst, {dst:String})", "src": "startsWith(src, {src:String})",
}

// chTime renders a ClickHouse time the way PostgreSQL's to_jsonb does in
// UTC: no trailing zeros in the fraction of a second, none at all for a
// whole second.
func chTime(col string) string {
	return fmt.Sprintf("concat(replaceOne(replaceRegexpOne(toString(%s), '[.]?0+$', ''), ' ', 'T'), '+00:00')", col)
}

// cdrCols renders a ClickHouse record the way PostgreSQL's to_jsonb does:
// the same names, nulls where there is no trunk (either side), vendor or
// answer, and times in RFC 3339. Columns of PostgreSQL's cdrs that are not
// here (an extension's) are added as nulls: see cdrSelect.
var cdrCols = []struct{ name, expr string }{
	{"id", "id"}, {"call_id", "call_id"}, {"leg", "leg"}, {"direction", "direction"}, {"client_id", "client_id"},
	{"trunk_id", "nullIf(trunk_id, 0)"}, {"vendor_id", "nullIf(vendor_id, 0)"}, {"connection_id", "nullIf(connection_id, 0)"},
	{"src", "src"}, {"dst", "dst"}, {"src_ip", "src_ip"},
	{"sell_prefix", "nullIf(sell_prefix, '')"}, {"buy_prefix", "nullIf(buy_prefix, '')"},
	{"start_at", chTime("start_at")},
	{"answer_at", "if(isNull(answer_at), null, " + chTime("assumeNotNull(answer_at)") + ")"},
	{"end_at", chTime("end_at")},
	{"duration_sec", "duration_sec"}, {"sip_code", "sip_code"}, {"sell_billed_sec", "sell_billed_sec"}, {"buy_billed_sec", "buy_billed_sec"},
	{"sell_amount", "sell_amount"}, {"buy_amount", "buy_amount"}, {"sell_base", "sell_base"}, {"buy_base", "buy_base"}, {"pdd_ms", "pdd_ms"},
	{"bundle_id", "nullIf(bundle_id, 0)"}, {"bundle_sec", "bundle_sec"}, {"dst_trunk_id", "nullIf(dst_trunk_id, 0)"},
}

// cdrColumns caches the columns of PostgreSQL's cdrs that ClickHouse lacks.
// The schema changes only as a process starts (migrations), so once read
// they stay.
type cdrColumns struct {
	mu    sync.Mutex
	extra []string
	read  bool
}

// cdrSelect is the select list that renders a ClickHouse record as the
// PostgreSQL row of the same record, without the resource's hidden columns.
// Columns only PostgreSQL has, which an extension adds (Enterprise's
// did_id, invoice ids), are null: ClickHouse never receives them, so an
// archived record has them empty.
func (s *Server) cdrSelect(ctx context.Context) (string, error) {
	c := &s.cdrShape
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.read {
		rows, err := s.db.Query(ctx, `
			select column_name::text from information_schema.columns
			where table_schema = current_schema() and table_name = 'cdrs' order by ordinal_position`)
		if err != nil {
			return "", fmt.Errorf("columns of cdrs: %w", err)
		}
		var extra []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return "", err
			}
			if !slices.ContainsFunc(cdrCols, func(col struct{ name, expr string }) bool { return col.name == name }) {
				extra = append(extra, name)
			}
		}
		if err := rows.Err(); err != nil {
			return "", err
		}
		c.extra, c.read = extra, true
	}
	hide := s.resources["cdrs"].Hide
	var out []string
	for _, col := range cdrCols {
		if !slices.Contains(hide, col.name) {
			out = append(out, col.expr+" as "+col.name)
		}
	}
	for _, name := range c.extra {
		if !slices.Contains(hide, name) && !strings.Contains(name, "`") {
			out = append(out, "null as `"+name+"`")
		}
	}
	return strings.Join(out, ", "), nil
}

// chStamp is how a time is passed to a DateTime64(6) parameter.
const chStamp = "2006-01-02 15:04:05.000000"

// chCDRQuery is a list request for call records as ClickHouse conditions.
type chCDRQuery struct {
	where         []string
	params        map[string]string
	limit, offset int
	from          *time.Time
	// none: a filter no record in ClickHouse can meet (a number filter that
	// is no number, or an extension's filter: ClickHouse has none of its
	// values).
	none bool
}

// parseCDRQuery turns a list request into ClickHouse conditions. A request
// PostgreSQL would refuse is refused here with the same status.
func parseCDRQuery(q url.Values, res *Resource) (chCDRQuery, error) {
	out := chCDRQuery{limit: 100, params: map[string]string{}}
	for k, v := range q {
		val := v[0]
		switch k {
		case "limit", "offset":
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 {
				return out, Errf(400, "invalid %s", k)
			}
			if k == "limit" {
				out.limit = min(n, 1000)
			} else {
				out.offset = n
			}
		case "from", "to":
			at, err := time.Parse(time.RFC3339, val)
			if err != nil {
				return out, Errf(400, "%s must be an RFC 3339 timestamp", k)
			}
			op := ">="
			if k == "to" {
				op = "<"
			} else {
				out.from = &at
			}
			out.where = append(out.where, fmt.Sprintf("start_at %s {%s:DateTime64(6, 'UTC')}", op, k))
			out.params[k] = at.UTC().Format(chStamp)
		default:
			cond, ok := cdrFilters[k]
			if !ok {
				if res != nil && slices.Contains(res.Filters, k) {
					out.none = true
					continue
				}
				return out, Errf(400, "unknown filter %q", k)
			}
			if strings.Contains(cond, ":UInt") {
				// PostgreSQL compares these as text and finds nothing for a
				// value that is no number; so must this.
				if _, err := strconv.ParseUint(val, 10, 64); err != nil {
					out.none = true
					continue
				}
			}
			out.where = append(out.where, cond)
			out.params[k] = val
		}
	}
	return out, nil
}

// listCDRsFromClickHouse answers a list request for call records, or
// reports false when the request is to be answered from PostgreSQL after
// all. A request ClickHouse would refuse is refused here with the same
// status the PostgreSQL path gives.
func (s *Server) listCDRsFromClickHouse(w http.ResponseWriter, r *http.Request, c Caller) (bool, error) {
	if !s.cdrsFromCH || s.analytics == nil || len(s.cdrFields) > 0 {
		return false, nil
	}
	q, err := parseCDRQuery(r.URL.Query(), nil)
	if err != nil {
		return true, err
	}
	if q.none {
		WriteRaw(w, 200, "[]")
		return true, nil
	}
	out, err := s.cdrsFromClickHouse(r.Context(), c, q.where, q.params, q.limit, q.offset)
	if err != nil {
		// The list still answers from the records of rating.
		log.Printf("api: call records from ClickHouse: %v (using PostgreSQL)", err)
		return false, nil
	}
	WriteRaw(w, 200, out)
	return true, nil
}

// scopedClients is the list of the caller's clients' ids for a ClickHouse
// condition, as in the reports; empty when the caller sees none.
func (s *Server) scopedClients(ctx context.Context, c Caller) (string, error) {
	var ids string
	err := s.db.QueryRow(ctx, fmt.Sprintf(
		`select coalesce(string_agg(id::text, ','), '') from clients t where t.reseller_id in %s`, s.scope), c.ResellerID).Scan(&ids)
	return ids, err
}

func (s *Server) cdrsFromClickHouse(ctx context.Context, c Caller, where []string, params map[string]string, limit, offset int) (string, error) {
	// Only the clients the caller may see.
	ids, err := s.scopedClients(ctx, c)
	if err != nil {
		return "", err
	}
	if ids == "" || limit == 0 {
		return "[]", nil
	}
	cols, err := s.cdrSelect(ctx)
	if err != nil {
		return "", err
	}
	where = append(slices.Clip(where), "client_id in ("+ids+")")
	rows, err := s.analytics.Query(ctx, fmt.Sprintf(`
		select %s from (
		  select * from cdrs final where %s order by start_at desc, id desc limit %d offset %d)
		order by start_at desc, id desc
		format JSONEachRow settings output_format_json_quote_64bit_integers = 0`,
		cols, strings.Join(where, " and "), limit, offset), params)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(rows)), "\n")
	if lines[0] == "" {
		return "[]", nil
	}
	return "[" + strings.Join(lines, ",") + "]", nil
}

// archiveHorizon is the time before which call records may have left
// PostgreSQL; ok is false when nothing was ever archived or there is no
// ClickHouse to read them from.
func (s *Server) archiveHorizon(ctx context.Context) (at time.Time, ok bool, err error) {
	if s.analytics == nil {
		return at, false, nil
	}
	err = s.db.QueryRow(ctx, `select horizon from cdr_archive`).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return at, false, nil
	}
	return at, err == nil, err
}

// archivedPart says whether a list request with these conditions reaches
// before the archive horizon, and so must read ClickHouse for its oldest
// records; q is its ClickHouse form.
func (s *Server) archivedPart(r *http.Request, res *Resource) (horizon time.Time, q chCDRQuery, ok bool, err error) {
	horizon, ok, err = s.archiveHorizon(r.Context())
	if err != nil || !ok {
		return horizon, q, false, err
	}
	q, err = parseCDRQuery(r.URL.Query(), res)
	if err != nil {
		return horizon, q, false, err
	}
	if q.none || (q.from != nil && !q.from.Before(horizon)) {
		return horizon, q, false, nil
	}
	q.where = append(q.where, "start_at < {horizon:DateTime64(6, 'UTC')}")
	q.params["horizon"] = horizon.UTC().Format(chStamp)
	return horizon, q, true, nil
}

// archivePage says which records of the archived part fill a page of the
// list. The list is ordered newest first, so the records PostgreSQL still
// keeps (from the horizon on) all come before the archived ones: a page
// takes got records from PostgreSQL, the rest from the archive. When the
// page starts past PostgreSQL's records (got is 0), how many there are
// (total) says how far into the archive it starts.
func archivePage(offset, limit, got int, total func() (int, error)) (archOffset, archLimit int, err error) {
	if got >= limit {
		return 0, 0, nil
	}
	if got > 0 {
		return 0, limit - got, nil
	}
	n, err := total()
	if err != nil {
		return 0, 0, err
	}
	return max(0, offset-n), limit, nil
}

// listCDRsAcrossArchive answers a list request that reaches before the
// archive horizon: PostgreSQL's records from the horizon on, then the
// archived ones from ClickHouse, paged as one list. It reports false when
// the request does not reach the archive. cond and args are the request's
// PostgreSQL conditions.
func (s *Server) listCDRsAcrossArchive(w http.ResponseWriter, r *http.Request, c Caller, res *Resource, cond string, args []any, limit, offset int) (bool, error) {
	horizon, q, ok, err := s.archivedPart(r, res)
	if err != nil || !ok {
		return false, err
	}
	ctx := r.Context()
	args = append(slices.Clip(args), horizon, res.Hide)
	condPG := fmt.Sprintf("%s and t.start_at >= $%d", cond, len(args)-1)
	var recent string
	err = s.db.QueryRow(ctx, fmt.Sprintf(
		`select coalesce(jsonb_agg(x.j order by x.n), '[]')::text from (
		   select row_number() over (order by %[2]s) as n, to_jsonb(t) - coalesce($%[3]d::text[], '{}') as j
		   from cdrs t where %[1]s order by %[2]s limit %[4]d offset %[5]d) x`,
		condPG, res.Order, len(args), limit, offset), args...).Scan(&recent)
	if err != nil {
		return true, err
	}
	var rows []json.RawMessage
	if err := json.Unmarshal([]byte(recent), &rows); err != nil {
		return true, err
	}
	archOffset, archLimit, err := archivePage(offset, limit, len(rows), func() (n int, err error) {
		err = s.db.QueryRow(ctx, fmt.Sprintf(`select count(*) from cdrs t where %s`, condPG), args[:len(args)-1]...).Scan(&n)
		return n, err
	})
	if err != nil {
		return true, err
	}
	if archLimit > 0 {
		old, err := s.cdrsFromClickHouse(ctx, c, q.where, q.params, archLimit, archOffset)
		if err != nil {
			// The page still shows what PostgreSQL keeps.
			log.Printf("api: archived call records from ClickHouse: %v (listing PostgreSQL's only)", err)
		} else if old != "[]" {
			if len(rows) == 0 {
				recent = old
			} else {
				recent = recent[:len(recent)-1] + "," + old[1:]
			}
		}
	}
	WriteRaw(w, 200, recent)
	return true, nil
}

// archivedCDR is one call record from ClickHouse, rendered as PostgreSQL
// renders it, if the caller may see it; "" when there is none. Copies of a
// record that reached ClickHouse twice are alike, so any one will do.
func (s *Server) archivedCDR(ctx context.Context, c Caller, id int64) (string, error) {
	ids, err := s.scopedClients(ctx, c)
	if err != nil || ids == "" {
		return "", err
	}
	cols, err := s.cdrSelect(ctx)
	if err != nil {
		return "", err
	}
	out, err := s.analytics.Query(ctx, fmt.Sprintf(`
		select %s from cdrs where id = {id:UInt64} and client_id in (%s) limit 1
		format JSONEachRow settings output_format_json_quote_64bit_integers = 0`, cols, ids),
		map[string]string{"id": strconv.FormatInt(id, 10)})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// archivedCSVRow is what the CSV export needs of an archived record.
type archivedCSVRow struct {
	Start, Answer, End                             *int64 // microseconds since 1970
	CallID, Src, Dst, SrcIP, SellPrefix, BuyPrefix string
	Leg, Dur, Code, SellSec, BuySec, BundleSec     int
	Client, Vendor, Connection, DstTrunk           int64
	Sell, Buy                                      int64
}

// archivedCSVCols selects archivedCSVRow's fields by name.
const archivedCSVCols = `toUnixTimestamp64Micro(start_at) as Start, if(isNull(answer_at), null, toUnixTimestamp64Micro(assumeNotNull(answer_at))) as Answer,
	toUnixTimestamp64Micro(end_at) as End, call_id as CallID, src as Src, dst as Dst, src_ip as SrcIP, sell_prefix as SellPrefix, buy_prefix as BuyPrefix,
	leg as Leg, duration_sec as Dur, sip_code as Code, sell_billed_sec as SellSec, buy_billed_sec as BuySec, bundle_sec as BundleSec,
	client_id as Client, vendor_id as Vendor, connection_id as Connection, dst_trunk_id as DstTrunk, sell_amount as Sell, buy_amount as Buy`

// countArchived is how many archived records meet q, up to limit+1.
func (s *Server) countArchived(ctx context.Context, c Caller, q chCDRQuery, limit int) (int, error) {
	ids, err := s.scopedClients(ctx, c)
	if err != nil || ids == "" {
		return 0, err
	}
	out, err := s.analytics.Query(ctx, fmt.Sprintf(`select count() from (select id from cdrs final where %s and client_id in (%s) limit %d) format TSV`,
		strings.Join(q.where, " and "), ids, limit+1), q.params)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// streamArchived calls row for each archived record that meets q, newest
// first, as the records arrive.
func (s *Server) streamArchived(ctx context.Context, c Caller, q chCDRQuery, row func(archivedCSVRow) error) error {
	ids, err := s.scopedClients(ctx, c)
	if err != nil || ids == "" {
		return err
	}
	body, err := s.analytics.QueryStream(ctx, fmt.Sprintf(`
		select %s from cdrs final where %s and client_id in (%s) order by start_at desc, id desc
		format JSONEachRow settings output_format_json_quote_64bit_integers = 0`,
		archivedCSVCols, strings.Join(q.where, " and "), ids), q.params)
	if err != nil {
		return err
	}
	defer body.Close()
	lines := bufio.NewScanner(body)
	lines.Buffer(make([]byte, 64<<10), 16<<20)
	for lines.Scan() {
		var rec archivedCSVRow
		if err := json.Unmarshal(lines.Bytes(), &rec); err != nil {
			return fmt.Errorf("archived record: %w", err)
		}
		if err := row(rec); err != nil {
			return err
		}
	}
	return lines.Err()
}

// partyNames maps the ids of a table to their names, for the CSV rows of
// archived records (PostgreSQL's rows get them by a join).
func (s *Server) partyNames(ctx context.Context, table string) (map[int64]string, error) {
	rows, err := s.db.Query(ctx, `select id, name from `+table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}
