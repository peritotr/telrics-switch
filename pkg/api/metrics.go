// SPDX-License-Identifier: AGPL-3.0-only
package api

import (
	"context"
	"net/http"
	"time"

	"softswitch/core/pkg/metrics"
)

var (
	mRequestTime = metrics.NewHistogram("softswitch_api_request_seconds", "Time to answer a management API request.",
		[]float64{0.005, 0.025, 0.1, 0.25, 1, 5})
	mRequests = map[int]*metrics.Counter{}
)

func init() {
	for class, label := range map[int]string{2: "2xx", 3: "3xx", 4: "4xx", 5: "5xx"} {
		mRequests[class] = metrics.NewCounter("softswitch_api_requests_total", "Management API requests, by class of answer.", "class="+label)
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the writer underneath.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func observeRequest(status int, took time.Duration) {
	mRequestTime.Observe(took)
	if c := mRequests[status/100]; c != nil {
		c.Inc()
	}
}

// RegisterMetrics exposes what only the database can tell. A process calls
// it once, for the server whose metrics it serves.
func (s *Server) RegisterMetrics() {
	count := func(sql string) func() (float64, bool) {
		return func() (float64, bool) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var n int64
			if err := s.db.QueryRow(ctx, sql).Scan(&n); err != nil {
				return 0, false
			}
			return float64(n), true
		}
	}
	metrics.NewGauge("softswitch_mail_outbox_pending", "E-mails waiting to be sent.",
		count(`select count(*) from mail_outbox where sent_at is null`))
	metrics.NewGauge("softswitch_mail_outbox_oldest_seconds", "Age of the oldest e-mail still waiting; 0 when none waits.",
		count(`select coalesce(extract(epoch from now() - min(created_at)), 0)::bigint from mail_outbox where sent_at is null`))
	metrics.NewGauge("softswitch_api_db_connections", "Database connections in use by the API.", func() (float64, bool) {
		return float64(s.db.Stat().AcquiredConns()), true
	})
	if s.fxURL != "" {
		// Left out until a fetch has succeeded once; the failure counter
		// speaks for that time.
		metrics.NewGauge("softswitch_fx_fetch_age_seconds", "Time since the published rates of exchange were last fetched.",
			count(`select extract(epoch from now() - fetched_at)::bigint from exchange_rate_fetch where fetched_at is not null`))
	}
}
