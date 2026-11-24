// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"softswitch/core/pkg/fx"
	"softswitch/core/pkg/metrics"
	"softswitch/core/pkg/store"
)

// Rates of exchange that follow the European Central Bank's daily reference
// rates (T-20, ADR-0036). Off unless FX_SOURCE=ecb. When on, the API
// process fetches the file when it starts and then once a day, after the
// bank publishes; a rate marked as following the feed (source "ecb") is
// rewritten from it, crossed into its account's currency; a rate typed in
// (source "manual") is never touched. Call records keep the rate they were
// rated at (ADR-0011): nothing here reaches back.

var mFXFailures = metrics.NewCounter("softswitch_fx_fetch_failures_total",
	"Fetches of the published rates of exchange that failed: unreachable, refused or unreadable.")

// fxPublishedUTC is when the feed is due each day: the bank publishes at
// about 16:00 Central European Time, which is 15:00 UTC in winter and 14:00
// in summer. A fixed UTC time needs no time-zone data.
const fxPublishedUTC = 15*time.Hour + 30*time.Minute

// fxRetry is how long a failed scheduled fetch waits before the next try.
const fxRetry = time.Hour

// fxClaim is how long one process holds the fetch: longer than the request
// may take, so that two processes never fetch at once.
const fxClaim = 2 * time.Minute

// fxFresh keeps a restarting process from fetching again what another
// fetched moments ago.
const fxFresh = 10 * time.Minute

// ExchangeRateFeedFromEnv reads FX_SOURCE ("" or "off": no feed; "ecb":
// the European Central Bank) and FX_URL (another address for the same
// file, for tests and mirrors). It returns the address to fetch, or "".
func ExchangeRateFeedFromEnv() (string, error) {
	source, addr := strings.TrimSpace(os.Getenv("FX_SOURCE")), strings.TrimSpace(os.Getenv("FX_URL"))
	switch source {
	case "", "off":
		if addr != "" {
			return "", errors.New("FX_URL is set but FX_SOURCE is not: set FX_SOURCE=ecb to fetch rates of exchange")
		}
		return "", nil
	case "ecb":
		if addr == "" {
			return fx.ECBURL, nil
		}
		if u, err := url.Parse(addr); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", errors.New("FX_URL must be an http:// or https:// address")
		}
		return addr, nil
	}
	return "", fmt.Errorf("FX_SOURCE %q: the only source is ecb (or off)", source)
}

// SetExchangeRateFeed turns on following the published rates, fetched from
// addr; "" leaves it off.
func (s *Server) SetExchangeRateFeed(addr string) {
	s.fxURL = addr
	s.fxClient = &http.Client{Timeout: 30 * time.Second}
}

// fxDue is the last time the feed was due at or before now.
func fxDue(now time.Time) time.Time {
	now = now.UTC()
	due := now.Truncate(24 * time.Hour).Add(fxPublishedUTC)
	if due.After(now) {
		due = due.Add(-24 * time.Hour)
	}
	return due
}

type fxMode int

const (
	fxScheduled fxMode = iota // once a day, after publication; a failure waits fxRetry
	fxStart                   // on start, unless another process just fetched
	fxNow                     // asked for in the panel
)

// fxResult is what a fetch did.
type fxResult struct {
	Fetched   bool      // this process fetched (false: not due, or another is fetching)
	Published time.Time // the day of the rates fetched
	Changed   int       // rates of exchange rewritten
	Missing   []string  // following rates the feed could not give
}

// FetchExchangeRates fetches the published rates if they are due and
// rewrites the rates that follow them. Several processes may call it: one
// claims the fetch, the others do nothing.
func (s *Server) FetchExchangeRates(ctx context.Context) (fxResult, error) {
	return s.fetchExchangeRates(ctx, fxScheduled, time.Now(), nil, Caller{})
}

func (s *Server) fetchExchangeRates(ctx context.Context, mode fxMode, now time.Time, r *http.Request, c Caller) (fxResult, error) {
	var res fxResult
	if s.fxURL == "" {
		return res, nil
	}
	since := fxDue(now)
	switch mode {
	case fxStart:
		since = now.Add(-fxFresh)
	case fxNow:
		since = now
	}
	var claimed bool
	err := s.db.QueryRow(ctx, `
		update exchange_rate_fetch set claimed_until = now() + $3 * interval '1 second'
		where (claimed_until is null or claimed_until < now())
		  and (fetched_at is null or fetched_at < $1)
		  and (not $2 or failed_at is null or failed_at < now() - $4 * interval '1 second')
		returning true`, since, mode == fxScheduled, int64(fxClaim/time.Second), int64(fxRetry/time.Second)).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("claiming the fetch: %w", err)
	}
	res.Fetched = true
	tab, err := s.fetchFeed(ctx)
	if err != nil {
		mFXFailures.Inc()
		if _, dbErr := s.db.Exec(context.WithoutCancel(ctx), `
			update exchange_rate_fetch set claimed_until = null, failed_at = now(), error = $1`, err.Error()); dbErr != nil {
			log.Printf("api: exchange rates: recording the failure: %v", dbErr)
		}
		return res, err
	}
	res.Published = tab.Published
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		res.Changed, res.Missing, err = s.applyFeed(ctx, tx, tab)
		if err != nil {
			return err
		}
		if r != nil {
			return s.Audit(ctx, tx, r, c, AuditEntry{Resource: "exchange-rates", Action: "fetch", Label: "ECB",
				Detail: map[string]any{"published": tab.Published.Format(time.DateOnly), "changed": res.Changed, "missing": res.Missing}})
		}
		return nil
	})
	if err != nil {
		// Let the next tick try again rather than wait for the claim to lapse.
		if _, dbErr := s.db.Exec(context.WithoutCancel(ctx), `update exchange_rate_fetch set claimed_until = null`); dbErr != nil {
			log.Printf("api: exchange rates: releasing the fetch: %v", dbErr)
		}
		return res, fmt.Errorf("storing the published rates: %w", err)
	}
	if res.Changed > 0 {
		// The engine compares vendors with these rates: it reloads now
		// rather than at its next interval.
		if err := store.NotifyConfig(context.WithoutCancel(ctx), s.db); err != nil {
			log.Printf("api: notifying configuration change: %v", err)
		}
	}
	if len(res.Missing) > 0 {
		log.Printf("api: exchange rates: not in the rates published for %s: %s", tab.Published.Format(time.DateOnly), strings.Join(res.Missing, ", "))
	}
	return res, nil
}

// fetchFeed reads the published file. Its error says what went wrong
// without the address, which the operator configured and may not want in
// the panel.
func (s *Server) fetchFeed(ctx context.Context) (fx.Table, error) {
	ctx, cancel := context.WithTimeout(ctx, s.fxClient.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.fxURL, nil)
	if err != nil {
		return fx.Table{}, fmt.Errorf("fetching the published rates: %w", err)
	}
	req.Header.Set("User-Agent", "softswitch")
	resp, err := s.fxClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fx.Table{}, fmt.Errorf("fetching the published rates: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fx.Table{}, fmt.Errorf("fetching the published rates: the source answered %s", resp.Status)
	}
	return fx.ParseECB(io.LimitReader(resp.Body, 1<<20))
}

// applyFeed keeps the fetched table and rewrites from it every rate that
// follows the feed, in every account, in tx. A table older than the one
// kept (a stale mirror, say) changes nothing.
func (s *Server) applyFeed(ctx context.Context, tx pgx.Tx, tab fx.Table) (changed int, missing []string, err error) {
	// The marker lets the trigger tell the feed's writes from a person's.
	if _, err := tx.Exec(ctx, `select set_config('softswitch.fx_feed', 'on', true)`); err != nil {
		return 0, nil, err
	}
	var kept *time.Time
	if err := tx.QueryRow(ctx, `select published from exchange_rate_fetch for update`).Scan(&kept); err != nil {
		return 0, nil, err
	}
	if kept != nil && tab.Published.Before(*kept) {
		_, err := tx.Exec(ctx, `update exchange_rate_fetch set claimed_until = null, fetched_at = now(), error = ''`)
		return 0, nil, err
	}
	cur, per := make([]string, 0, len(tab.PerEUR)), make([]string, 0, len(tab.PerEUR))
	for c, v := range tab.PerEUR {
		cur, per = append(cur, c), append(per, v)
	}
	if _, err := tx.Exec(ctx, `delete from exchange_rate_feed`); err != nil {
		return 0, nil, err
	}
	if _, err := tx.Exec(ctx, `insert into exchange_rate_feed (currency, per_eur, published) select unnest($1::text[]), unnest($2::text[]), $3`,
		cur, per, tab.Published); err != nil {
		return 0, nil, err
	}
	rows, err := tx.Query(ctx, `
		select x.id, x.reseller_id, x.currency, r.currency, x.rate
		from exchange_rates x join resellers r on r.id = x.reseller_id
		where x.source = 'ecb' order by x.id for update of x`)
	if err != nil {
		return 0, nil, err
	}
	type follower struct {
		id, reseller   int64
		currency, base string
		rate           int64
	}
	var all []follower
	for rows.Next() {
		var f follower
		if err := rows.Scan(&f.id, &f.reseller, &f.currency, &f.base, &f.rate); err != nil {
			rows.Close()
			return 0, nil, err
		}
		all = append(all, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	for _, f := range all {
		rate, err := tab.Rate(f.currency, f.base)
		if err != nil {
			// The last rate stays; the panel shows which ones.
			missing = append(missing, f.currency+"/"+f.base)
			continue
		}
		if rate == f.rate {
			// The same rate on a new day: only the date moves, and that is
			// not worth an audit record a day per currency.
			if _, err := tx.Exec(ctx, `update exchange_rates set published = $2 where id = $1 and published is distinct from $2`, f.id, tab.Published); err != nil {
				return 0, nil, err
			}
			continue
		}
		before, err := AuditSnapshot(ctx, tx, "exchange_rates", f.id)
		if err != nil {
			return 0, nil, err
		}
		if _, err := tx.Exec(ctx, `update exchange_rates set rate = $2, published = $3 where id = $1`, f.id, rate, tab.Published); err != nil {
			return 0, nil, err
		}
		after, err := AuditSnapshot(ctx, tx, "exchange_rates", f.id)
		if err != nil {
			return 0, nil, err
		}
		if err := s.Audit(ctx, tx, nil, Caller{ResellerID: f.reseller}, AuditEntry{Resource: "exchange-rates", RowID: f.id, Action: "update",
			Label: after.Label(), Changes: AuditDiff(before, after), Detail: map[string]any{"source": "ecb"}}); err != nil {
			return 0, nil, err
		}
		changed++
	}
	if missing == nil {
		missing = []string{}
	}
	_, err = tx.Exec(ctx, `
		update exchange_rate_fetch set claimed_until = null, fetched_at = now(), published = $1, error = '', missing = $2`,
		tab.Published, missing)
	return changed, missing, err
}

// keptFeed is the table last fetched, as kept in the database.
func keptFeed(ctx context.Context, q pgx.Tx) (fx.Table, error) {
	tab := fx.Table{PerEUR: map[string]string{}}
	rows, err := q.Query(ctx, `select currency, per_eur, published from exchange_rate_feed`)
	if err != nil {
		return tab, err
	}
	defer rows.Close()
	for rows.Next() {
		var c, v string
		if err := rows.Scan(&c, &v, &tab.Published); err != nil {
			return tab, err
		}
		tab.PerEUR[c] = v
	}
	return tab, rows.Err()
}

// RunExchangeRates fetches on start and then whenever due, until ctx ends.
// It does nothing while the feed is off. every is how often it looks; the
// fetch itself happens once a day.
func (s *Server) RunExchangeRates(ctx context.Context, every time.Duration) {
	if s.fxURL == "" {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	mode := fxStart
	for {
		if _, err := s.fetchExchangeRates(ctx, mode, time.Now(), nil, Caller{}); err != nil && ctx.Err() == nil {
			log.Printf("api: exchange rates: %v; the last rates stay", err)
		}
		mode = fxScheduled
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

func (s *Server) registerExchangeRateFeed() {
	s.Handle("GET /v1/exchange-rate-feed", false, s.feedStatus,
		Summary("Whether rates of exchange follow the published reference rates (FX_SOURCE): last fetch, last failure, rates it could not give, currencies published."),
		Returns(200, feedState{}))
	s.Handle("POST /v1/exchange-rate-feed/fetch", true, s.feedFetch,
		Summary("Fetch the published reference rates now and rewrite the rates that follow them; typed-in rates stay."),
		TakesNothing(), Returns(200, feedFetched{}))
	s.Handle("POST /v1/exchange-rate-feed/follow", true, s.feedFollow,
		Summary("Make the account's rate for a currency follow the published rates; added if missing, set at once from the last fetch."),
		Takes(feedFollowRequest{}), ReturnsRow(200, "exchange-rates"))
}

// feedState is how the feed of published rates stands. Published is the
// day (YYYY-MM-DD) of the rates last fetched.
type feedState struct {
	Enabled    bool       `json:"enabled"`
	Source     string     `json:"source"` // "ecb", or "" when off
	FetchedAt  *time.Time `json:"fetched_at"`
	Published  *string    `json:"published"`
	FailedAt   *time.Time `json:"failed_at"`
	Error      string     `json:"error"`
	Missing    []string   `json:"missing"`    // following rates the feed could not give
	Currencies []string   `json:"currencies"` // what the feed publishes
}

// feedFetched is what a fetch did; Published is a day (YYYY-MM-DD).
type feedFetched struct {
	Published string   `json:"published"`
	Changed   int      `json:"changed"`
	Missing   []string `json:"missing"`
}

type feedFollowRequest struct {
	Currency string `json:"currency" openapi:"required"`
}

// feedStatus says whether the feed is on, when it last answered and failed,
// and which currencies it publishes.
func (s *Server) feedStatus(w http.ResponseWriter, r *http.Request, c Caller) error {
	out := feedState{Enabled: s.fxURL != ""}
	if s.fxURL != "" {
		out.Source = "ecb"
	}
	var (
		fetched, failed *time.Time
		published       *time.Time
		msg             string
		missing         []string
		currencies      []string
	)
	err := s.db.QueryRow(r.Context(), `
		select fetched_at, published, failed_at, error, missing,
		       coalesce((select array_agg(c order by c) from (select currency::text c from exchange_rate_feed union select $1::text) all_), '{}')
		from exchange_rate_fetch`, fx.Base).Scan(&fetched, &published, &failed, &msg, &missing, &currencies)
	if err != nil {
		return err
	}
	if published == nil {
		currencies = []string{}
	}
	if published != nil {
		day := published.Format(time.DateOnly)
		out.Published = &day
	}
	if missing == nil {
		missing = []string{}
	}
	out.FetchedAt, out.FailedAt, out.Error, out.Missing, out.Currencies = fetched, failed, msg, missing, currencies
	WriteJSON(w, 200, out)
	return nil
}

// feedFetch fetches the published rates now, whatever the schedule.
func (s *Server) feedFetch(w http.ResponseWriter, r *http.Request, c Caller) error {
	if s.fxURL == "" {
		return Errf(409, "the exchange rate feed is off (FX_SOURCE)")
	}
	res, err := s.fetchExchangeRates(r.Context(), fxNow, time.Now(), r, c)
	if err != nil {
		AuditNothing(r)
		return Errf(502, "%v; the last rates stay", err)
	}
	if !res.Fetched {
		AuditNothing(r)
		return Errf(409, "another process is fetching the rates; try again in a minute")
	}
	if res.Missing == nil {
		res.Missing = []string{}
	}
	WriteJSON(w, 200, feedFetched{Published: res.Published.Format(time.DateOnly), Changed: res.Changed, Missing: res.Missing})
	return nil
}

// feedFollow makes the caller's rate for a currency follow the feed: added
// if missing, switched if typed in, and set at once from the table last
// fetched.
func (s *Server) feedFollow(w http.ResponseWriter, r *http.Request, c Caller) error {
	if s.fxURL == "" {
		return Errf(409, "the exchange rate feed is off (FX_SOURCE)")
	}
	var body feedFollowRequest
	if err := ReadJSON(r, &body); err != nil {
		return err
	}
	if !currencyCode.MatchString(body.Currency) {
		return Errf(422, "currency must be a three-letter code in capitals, e.g. USD")
	}
	ctx := r.Context()
	res := s.Resource("exchange-rates")
	var out string
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select set_config('softswitch.fx_feed', 'on', true)`); err != nil {
			return err
		}
		tab, err := keptFeed(ctx, tx)
		if err != nil {
			return err
		}
		if len(tab.PerEUR) == 0 {
			return Errf(409, "no published rates have been fetched yet")
		}
		var base string
		if err := tx.QueryRow(ctx, `select currency from resellers where id = $1`, c.ResellerID).Scan(&base); err != nil {
			return err
		}
		rate, err := tab.Rate(body.Currency, base)
		if errors.Is(err, fx.ErrMissing) {
			return Errf(422, "%s in %s cannot be had from the published rates: %v", body.Currency, base, err)
		}
		if err != nil {
			return Errf(422, "%v", err)
		}
		var id int64
		var before AuditRow
		err = tx.QueryRow(ctx, `select id from exchange_rates where reseller_id = $1 and currency = $2 for update`, c.ResellerID, body.Currency).Scan(&id)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			err = tx.QueryRow(ctx, `insert into exchange_rates (reseller_id, currency, rate, source, published) values ($1, $2, $3, 'ecb', $4) returning id`,
				c.ResellerID, body.Currency, rate, tab.Published).Scan(&id)
		case err == nil:
			if before, err = AuditSnapshot(ctx, tx, "exchange_rates", id); err == nil {
				_, err = tx.Exec(ctx, `update exchange_rates set rate = $2, source = 'ecb', published = $3 where id = $1`, id, rate, tab.Published)
			}
		}
		if err != nil {
			return err
		}
		action := "update"
		if before == nil {
			action = "create"
		}
		if err := res.audit(ctx, tx, r, c, id, action, before); err != nil {
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
