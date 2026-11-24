// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The audit log: who changed what, and when. Every change made through the
// API leaves a row in audit_log, written with Audit in the transaction
// that makes the change. Three things keep it complete without anyone
// having to remember it:
//
//   - the generic create, update and delete of every resource, an
//     extension's included, record themselves with the fields that changed;
//   - a write route (Handle with write = true) whose handler recorded
//     nothing gets a plain record of the call once it has succeeded;
//   - what is written passes through one function that masks secrets by the
//     field's name and by the resource's hidden columns.
//
// The log is read as the resource "audit-log" and has no route that changes
// it; old rows go after the retention period.

// AuditEntry is one thing somebody did.
type AuditEntry struct {
	Resource string // as in the API's paths: "clients", "users", …
	RowID    int64  // 0 = no particular row
	Action   string // "create", "update", "delete" or a named operation: "payment", "login", …
	Label    string // what people call the row
	Changes  map[string]AuditChange
	Detail   map[string]any // what else is worth knowing about an operation; never a secret
}

// AuditChange is a field's value before and after. A created row has only
// new values, a deleted one only old ones.
type AuditChange struct {
	Old json.RawMessage `json:"old,omitempty"`
	New json.RawMessage `json:"new,omitempty"`
}

// auditMasked stands in the log for a value that must not be there.
const auditMasked = "***"

var maskedJSON = json.RawMessage(`"` + auditMasked + `"`)

// secretField recognises by name the fields whose values never enter the
// log: passwords and their hashes, keys, tokens, two-step secrets and codes.
var secretField = regexp.MustCompile(`(?i)pass|secret|token|hash|ha1|otp|credential|(^|_)key($|_)|(^|_)(recovery|backup)_codes?$`)

type auditMarkKey struct{}

// auditMark notes, per request, that the handler recorded what it did.
type auditMark struct{ done bool }

// SetTrustProxy says the program runs behind a reverse proxy that sets
// X-Forwarded-For. It is off unless the program is told so: the header is
// whatever the caller likes when nothing in front rewrites it.
func (s *Server) SetTrustProxy(on bool) { s.trustProxy = on }

// ClientIP is the address a request came from. Behind a proxy it is the
// last entry of X-Forwarded-For: the one the proxy itself appended, the
// address that connected to it. Earlier entries are the caller's claim.
func (s *Server) ClientIP(r *http.Request) string {
	if s.trustProxy {
		list := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		if ip := net.ParseIP(strings.TrimSpace(list[len(list)-1])); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return host
}

// Audit records what c did. Pass the transaction that makes the change, so
// that the record exists if and only if the change does. r is the request
// behind it (nil when the system acts by itself). Secrets are masked here,
// whatever the caller passed.
func (s *Server) Audit(ctx context.Context, q Execer, r *http.Request, c Caller, e AuditEntry) error {
	actor, actorID, ip := "system", int64(0), ""
	switch {
	case c.UserID != 0:
		actor, actorID = "user", c.UserID
	case c.KeyID != 0:
		actor, actorID = "key", c.KeyID
	}
	if r != nil {
		ip = s.ClientIP(r)
	}
	var changes, detail []byte
	if len(e.Changes) > 0 {
		masked := make(map[string]AuditChange, len(e.Changes))
		for field, ch := range e.Changes {
			if secretField.MatchString(field) {
				ch = maskChange(ch)
			}
			masked[field] = ch
		}
		changes, _ = json.Marshal(masked)
	}
	if len(e.Detail) > 0 {
		detail, _ = json.Marshal(maskDetail(e.Detail))
	}
	var rowID *int64
	if e.RowID != 0 {
		rowID = &e.RowID
	}
	_, err := q.Exec(ctx, `
		insert into audit_log (reseller_id, actor_type, actor_id, actor_name, ip, resource, row_id, action, label, changes, detail)
		values ($1, $2, nullif($3, 0), coalesce(
		    case $2 when 'user' then (select email from users where id = $3)
		            when 'key' then (select name from api_keys where id = $3) end, ''),
		  $4, $5, $6, $7, $8, $9::jsonb, $10::jsonb)`,
		c.ResellerID, actor, actorID, ip, e.Resource, rowID, e.Action, e.Label, nullJSON(changes), nullJSON(detail))
	if err == nil && r != nil {
		AuditNothing(r) // recorded: the net under the route has nothing to add
	}
	return err
}

// AuditNothing says a write route succeeded without changing anything (a
// dry run, a preview), so that no record is made of the call.
func AuditNothing(r *http.Request) {
	if mark, ok := r.Context().Value(auditMarkKey{}).(*auditMark); ok {
		mark.done = true
	}
}

func nullJSON(b []byte) *string {
	if b == nil {
		return nil
	}
	s := string(b)
	return &s
}

func maskChange(ch AuditChange) AuditChange {
	if ch.Old != nil {
		ch.Old = maskedJSON
	}
	if ch.New != nil {
		ch.New = maskedJSON
	}
	return ch
}

// maskDetail replaces the value of every field named like a secret, at any
// depth.
func maskDetail(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			if secretField.MatchString(k) {
				out[k] = auditMasked
			} else {
				out[k] = maskDetail(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = maskDetail(item)
		}
		return out
	}
	return v
}

// AuditRow is a row as the log sees it: every column, by name.
type AuditRow map[string]json.RawMessage

// AuditSnapshot reads a row for comparing before and after a change; in a
// transaction it also locks the row until the change is made. A row that
// is not there comes back as nil.
func AuditSnapshot(ctx context.Context, q Querier, table string, id int64) (AuditRow, error) {
	var raw string
	err := q.QueryRow(ctx, fmt.Sprintf(`select to_jsonb(t)::text from %s t where t.id = $1 for update`, table), id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var row AuditRow
	return row, json.Unmarshal([]byte(raw), &row)
}

// AuditDiff lists the fields that differ between two snapshots of a row;
// either may be nil (a row created or deleted), and then every field that
// has a value is listed. The values of the columns in hide are masked, as
// are those of fields named like a secret.
func AuditDiff(before, after AuditRow, hide ...string) map[string]AuditChange {
	out := map[string]AuditChange{}
	null := func(v json.RawMessage) bool { return v == nil || string(v) == "null" }
	note := func(field string, ch AuditChange) {
		if slices.Contains(hide, field) || secretField.MatchString(field) {
			ch = maskChange(ch)
		}
		out[field] = ch
	}
	for field, old := range before {
		switch now, ok := after[field]; {
		case after == nil:
			if !null(old) {
				note(field, AuditChange{Old: old})
			}
		case !ok || string(now) != string(old):
			if now == nil {
				now = json.RawMessage("null")
			}
			note(field, AuditChange{Old: old, New: now})
		}
	}
	for field, now := range after {
		if _, ok := before[field]; ok || null(now) {
			continue
		}
		if before == nil {
			note(field, AuditChange{New: now})
		} else {
			note(field, AuditChange{Old: json.RawMessage("null"), New: now})
		}
	}
	return out
}

// Label is what people call the row: its name, or what stands for one.
func (row AuditRow) Label() string {
	for _, field := range []string{"name", "email", "number", "cidr", "prefix", "currency"} {
		var v string
		if json.Unmarshal(row[field], &v) == nil && v != "" {
			return v
		}
	}
	return ""
}

// auditFallback records a successful write nobody recorded: the resource
// and the action are read from the route. It is the net under handlers,
// an extension's included, that do not call Audit themselves; it cannot
// be in their transaction, so a failure here is logged, not returned.
func (s *Server) auditFallback(r *http.Request, c Caller, pattern string) {
	method, path, _ := strings.Cut(pattern, " ")
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/v1/"), "/"), "/")
	e := AuditEntry{Resource: parts[0], Action: map[string]string{"POST": "create", "PATCH": "update", "PUT": "update", "DELETE": "delete"}[method]}
	if last := parts[len(parts)-1]; len(parts) > 1 && !strings.HasPrefix(last, "{") {
		e.Action = last
	}
	if e.Action == "" {
		e.Action = strings.ToLower(method)
	}
	fmt.Sscan(r.PathValue("id"), &e.RowID)
	if err := s.Audit(context.WithoutCancel(r.Context()), s.db, r, c, e); err != nil {
		log.Printf("api: audit log: %s: %v", pattern, err)
	}
}

// SetAuditRetention sets how many days the audit log is kept; 0 keeps it
// for ever. The default is DefaultAuditDays.
func (s *Server) SetAuditRetention(days int) { s.auditDays = days }

// DefaultAuditDays is how long the audit log is kept unless told otherwise.
const DefaultAuditDays = 365

// SweepAuditLog deletes the records older than the retention period.
func (s *Server) SweepAuditLog(ctx context.Context) (int64, error) {
	if s.auditDays <= 0 {
		return 0, nil
	}
	tag, err := s.db.Exec(ctx, `delete from audit_log where at < now() - make_interval(days => $1)`, s.auditDays)
	return tag.RowsAffected(), err
}

// RunAuditRetention sweeps the audit log until ctx ends.
func (s *Server) RunAuditRetention(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if _, err := s.SweepAuditLog(ctx); err != nil && ctx.Err() == nil {
			log.Printf("api: audit log retention: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// auditFlushEvery is how many exported rows go out before the answer is
// pushed to the client, so that a long export arrives as it is read.
const auditFlushEvery = 1000

// exportAuditLog streams the records a list request with the same filters
// would show, all of them, as CSV. Nothing is masked here: secrets never
// reached the table (Audit masks them on the way in), so the file holds
// exactly what the list shows. Rows go out as the database hands them over
// and are not gathered first; unlike call records there is no cap, as
// retention bounds the log and a record is small.
func (s *Server) exportAuditLog(w http.ResponseWriter, r *http.Request, c Caller) error {
	cond, args, _, _, err := s.listQuery(s.resources["audit-log"], r, c)
	if err != nil {
		return err
	}
	rows, err := s.db.Query(r.Context(), fmt.Sprintf(`
		select t.id, t.at, t.actor_type, coalesce(t.actor_id, 0), t.actor_name, t.ip, t.resource, coalesce(t.row_id, 0), t.action, t.label,
		       coalesce(t.changes::text, ''), coalesce(t.detail::text, '')
		from audit_log t where %s order by t.at desc, t.id desc`, cond), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-log.csv"`)
	flush := http.NewResponseController(w)
	out := csv.NewWriter(w)
	out.Write([]string{"id", "at", "actor_type", "actor_id", "actor_name", "ip", "resource", "row_id", "action", "label", "changes", "detail"})
	id0 := func(v int64) string {
		if v == 0 {
			return ""
		}
		return strconv.FormatInt(v, 10)
	}
	n := 0
	for rows.Next() {
		var id, actorID, rowID int64
		var at time.Time
		var actorType, actorName, ip, resource, action, label, changes, detail string
		if err := rows.Scan(&id, &at, &actorType, &actorID, &actorName, &ip, &resource, &rowID, &action, &label, &changes, &detail); err != nil {
			break // the header is out; the file simply ends early
		}
		out.Write([]string{strconv.FormatInt(id, 10), at.UTC().Format("2006-01-02T15:04:05.000000Z"), actorType, id0(actorID),
			cellText(actorName), ip, resource, id0(rowID), action, cellText(label), changes, detail})
		if n++; n%auditFlushEvery == 0 {
			out.Flush()
			flush.Flush() // a writer that cannot flush still gets every row, later
		}
	}
	out.Flush()
	if err := rows.Err(); err != nil {
		log.Printf("api: audit log export cut short: %v", err)
	}
	return nil
}

// cellText keeps a spreadsheet from reading a name as a formula: text that
// starts like one gets a leading apostrophe, which spreadsheets show as
// plain text. Names are typed by people; the log's own fields are not.
func cellText(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}
