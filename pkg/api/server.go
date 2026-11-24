// SPDX-License-Identifier: AGPL-3.0-only

// Package api is the management REST API. Every key belongs to an account
// (a row in resellers) and sees only what that account owns. Community has a
// single account; extensions may widen the visibility rule and add resources,
// routes and CDR hooks through the exported Server methods.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"softswitch/core/pkg/clickhouse"
	"softswitch/core/pkg/mail"
	"softswitch/core/pkg/routing"
	"softswitch/core/pkg/store"
	"softswitch/core/pkg/totp"
)

// ownScope is the default visibility rule: only rows owned by the caller's
// own account. A scope is an SQL expression usable as "<owner id> in <scope>"
// where $1 is the caller's account id.
const ownScope = `($1::bigint)`

// Extension customises a Server before its routes are registered.
type Extension func(*Server)

type Server struct {
	db          *pgxpool.Pool
	mux         *http.ServeMux
	scope       string
	resources   map[string]*Resource
	parties     map[string]string // party type -> resource holding its balance
	snapshot    func(context.Context, store.Querier) (*routing.Snapshot, error)
	edition     string
	engine      string // route-engine address for the live views; "" = none
	engineToken string // what the engine expects from its callers; "" = nothing
	analytics   *clickhouse.Client
	cdrsFromCH  bool         // list call records from ClickHouse
	cdrShape    cdrColumns   // how a ClickHouse record matches PostgreSQL's row
	mailer      mail.Sender  // nil = no e-mail
	panelURL    string       // where people reach the panel, for links in e-mail
	realm       string       // SIP realm trunk passwords are bound to
	trustProxy  bool         // believe X-Forwarded-For
	auditDays   int          // how long the audit log is kept; 0 = for ever
	sealer      *totp.Sealer // seals two-step secrets and vendors' passwords; nil = stored as they are

	system *systemView // the tools' system page; nil = off

	fxURL    string       // where the published rates of exchange are fetched; "" = not followed
	fxClient *http.Client // fetches them

	routes []routeInfo // every route registered, for the API's description
	apiDoc *openAPIDoc

	cdrFields []string
	cdrPrice  []CDRPricer
	cdrStored []CDRStoredFunc
	alertMail []func(context.Context, pgx.Tx, int64) (bool, error)
}

// Caller is the authenticated account behind a request.
type Caller struct {
	ResellerID int64
	KeyID      int64 // the API key used; 0 for a signed-in user
	UserID     int64 // the signed-in user; 0 for an API key
	Admin      bool
	// NeedsTwoStep marks a signed-in user whose account requires two-step
	// sign-in and who has not set it up: such a session reaches only the
	// routes that set it up (twoStepSetupRoutes). It is read from the
	// database on every request, so turning the requirement on restricts
	// sessions already open, and setting up lifts it at once.
	NeedsTwoStep bool
}

type Handler func(w http.ResponseWriter, r *http.Request, c Caller) error

type httpError struct {
	code   int
	msg    string
	reason string // a fixed word a program can act on; "" = none
}

func (e httpError) Error() string { return e.msg }

// Errf builds an error that is reported to the client with the given status.
func Errf(code int, format string, a ...any) error {
	return httpError{code: code, msg: fmt.Sprintf(format, a...)}
}

// errCoded is Errf with a fixed word in the answer's "code", for a refusal
// a client must tell apart from others with the same status.
func errCoded(code int, reason, format string, a ...any) error {
	return httpError{code: code, msg: fmt.Sprintf(format, a...), reason: reason}
}

func New(db *pgxpool.Pool, exts ...Extension) *Server {
	s := &Server{
		db: db, mux: http.NewServeMux(), scope: ownScope,
		resources: map[string]*Resource{},
		parties:   map[string]string{"client": "clients", "vendor": "vendors"},
		snapshot:  store.LoadSnapshot,
		realm:     DefaultRealm,
		edition:   "community",
		auditDays: DefaultAuditDays,
		apiDoc:    &openAPIDoc{},
	}
	for name, res := range coreResources() {
		s.AddResource(name, res)
	}
	for _, ext := range exts {
		ext(s)
	}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(r.Context()); err != nil {
			writeErr(w, Errf(503, "database unavailable"))
			return
		}
		WriteJSON(w, 200, map[string]string{"status": "ok"})
	})
	s.registerCRUD()
	s.registerOps()
	s.registerLive()
	s.registerUsers()
	s.registerMail()
	s.registerTrunks()
	s.registerConnections()
	s.registerRegistrations()
	s.registerTrace()
	s.registerExchangeRateFeed()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: 200}
	s.mux.ServeHTTP(rec, r)
	observeRequest(rec.status, time.Since(start))
}

func (s *Server) DB() *pgxpool.Pool { return s.db }

// Scope returns the visibility rule in force; see ownScope for its shape.
func (s *Server) Scope() string { return s.scope }

// SetScope replaces the visibility rule.
func (s *Server) SetScope(sql string) { s.scope = sql }

// SetSnapshotLoader replaces the loader used to build routing snapshots.
func (s *Server) SetSnapshotLoader(f func(context.Context, store.Querier) (*routing.Snapshot, error)) {
	s.snapshot = f
}

// RootCaller is the root account acting with full rights, for services
// that work on behalf of the system rather than an API key.
func (s *Server) RootCaller(ctx context.Context) (Caller, error) {
	c := Caller{Admin: true}
	err := s.db.QueryRow(ctx, `select id from resellers order by id limit 1`).Scan(&c.ResellerID)
	return c, err
}

// SetAnalytics makes reports read the copy of the call records in
// ClickHouse instead of PostgreSQL. Without it they read PostgreSQL.
func (s *Server) SetAnalytics(c *clickhouse.Client) { s.analytics = c }

// SetEdition names the product edition reported to clients such as the panel.
func (s *Server) SetEdition(name string) { s.edition = name }

// AddParty registers another kind of balance holder for payments and the
// ledger; resource names the resource whose table carries the balance.
func (s *Server) AddParty(party, resource string) { s.parties[party] = resource }

// Handle registers an authenticated route. Write routes need an admin key.
// The options describe the route for the API's description (schema.go);
// every route but a resource's generic ones should say what it takes and
// gives.
func (s *Server) Handle(pattern string, write bool, h Handler, doc ...OpOption) {
	s.note(pattern, write, false, doc)
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		c, err := s.auth(r)
		if err == nil && c.NeedsTwoStep && !twoStepSetupRoutes[pattern] {
			err = errTwoStepRequired
		}
		if err == nil && write && !c.Admin {
			err = Errf(403, "read-only key")
		}
		mark := &auditMark{}
		if err == nil {
			r = r.WithContext(context.WithValue(r.Context(), auditMarkKey{}, mark))
			err = h(w, r, c)
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		if write {
			// A change nobody recorded is recorded here, so that no write
			// route can leave the audit log out.
			if !mark.done {
				s.auditFallback(r, c, pattern)
			}
			// route-engine reloads on this instead of waiting for its
			// interval. A lost notification only delays the change.
			if err := store.NotifyConfig(context.WithoutCancel(r.Context()), s.db); err != nil {
				log.Printf("api: notifying configuration change: %v", err)
			}
		}
	})
}

// HandlePublic registers a route this server does not authenticate: the
// handler decides who may call it. An extension with its own kind of
// account (a customer portal, say) builds on it.
func (s *Server) HandlePublic(pattern string, h http.HandlerFunc, doc ...OpOption) {
	s.note(pattern, false, true, doc)
	s.mux.HandleFunc(pattern, h)
}

// note keeps a route for the API's description of itself.
func (s *Server) note(pattern string, write, public bool, doc []OpOption) {
	if method, path, ok := strings.Cut(pattern, " "); ok {
		rt := routeInfo{method: method, path: path, write: write, public: public}
		for _, o := range doc {
			o(&rt.doc)
		}
		s.routes = append(s.routes, rt)
	}
}

// WriteErr answers a request with the status and message err stands for.
func WriteErr(w http.ResponseWriter, err error) { writeErr(w, err) }

func HashKey(key string) []byte {
	h := sha256.Sum256([]byte(key))
	return h[:]
}

func (s *Server) auth(r *http.Request) (Caller, error) {
	key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || key == "" {
		return Caller{}, Errf(401, "missing bearer token")
	}
	var c Caller
	var role string
	if strings.HasPrefix(key, sessionPrefix) {
		err := s.db.QueryRow(r.Context(), `
			select u.reseller_id, u.id, u.role, r.require_two_step and not u.totp_on from user_sessions s join users u on u.id = s.user_id
			join resellers r on r.id = u.reseller_id
			where s.token_hash = $1 and s.expires_at > now() and u.status = 'active' and r.status = 'active'`,
			HashKey(key)).Scan(&c.ResellerID, &c.UserID, &role, &c.NeedsTwoStep)
		if errors.Is(err, pgx.ErrNoRows) {
			return Caller{}, Errf(401, "invalid token")
		}
		c.Admin = role == "admin"
		return c, err
	}
	err := s.db.QueryRow(r.Context(),
		`select k.reseller_id, k.id, k.role from api_keys k join resellers r on r.id = k.reseller_id
		 where k.key_hash = $1 and r.status = 'active'`, HashKey(key)).Scan(&c.ResellerID, &c.KeyID, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return Caller{}, Errf(401, "invalid token")
	}
	c.Admin = role == "admin"
	return c, err
}

// Bootstrap creates the root account and its first admin key on an empty
// database. It does nothing once any account exists.
func Bootstrap(ctx context.Context, db *pgxpool.Pool, key string) error {
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `select count(*) from resellers`).Scan(&n); err != nil || n > 0 {
			return err
		}
		var id int64
		if err := tx.QueryRow(ctx, `insert into resellers (name) values ('root') returning id`).Scan(&id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into api_keys (reseller_id, name, key_hash) values ($1, 'bootstrap', $2)`, id, HashKey(key))
		return err
	})
}

func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// WriteRaw sends a body that is already JSON.
func WriteRaw(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	io.WriteString(w, body)
}

func writeErr(w http.ResponseWriter, err error) {
	code, msg, reason := 500, "internal error", ""
	var he httpError
	var pe *pgconn.PgError
	switch {
	case errors.As(err, &he):
		code, msg, reason = he.code, he.msg, he.reason
	case errors.Is(err, pgx.ErrNoRows):
		code, msg = 404, "not found"
	case errors.As(err, &pe):
		switch {
		case pe.Code == "23505":
			code, msg = 409, "already exists: "+pe.ConstraintName
		case pe.Code == "23503":
			code, msg = 409, "referenced row missing or still in use: "+pe.ConstraintName
		case strings.HasPrefix(pe.Code, "23"), strings.HasPrefix(pe.Code, "22"):
			code, msg = 422, pe.Message
		}
	}
	if code == 500 {
		log.Printf("api: %v", err)
	}
	WriteJSON(w, code, ErrorBody{Error: msg, Code: reason})
}

// ErrorBody is every refusal's answer: what went wrong, in a sentence, and
// for the few refusals a client acts on, a fixed word in code
// ("two_step_required": set up two-step sign-in first).
type ErrorBody struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

func PathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, Errf(400, "invalid id")
	}
	return id, nil
}

// ReadJSON decodes the request body, rejecting unknown fields.
func ReadJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 32<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return Errf(400, "invalid JSON body: %v", err)
	}
	return nil
}
