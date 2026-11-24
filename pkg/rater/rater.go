// SPDX-License-Identifier: AGPL-3.0-only

// Package rater consumes finished call legs from the queue, prices them and
// charges the parties. Delivery is at least once; storing a leg twice is a
// no-op because records are unique by (call_id, leg).
package rater

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"softswitch/core/pkg/api"
	"softswitch/core/pkg/engine"
	"softswitch/core/pkg/metrics"
)

const batchSize = 200

// Settler is told which calls have been charged, so that the money the
// engine holds for them can be let go once the balance shows the charge.
type Settler interface {
	Settled(ctx context.Context, callsByClient map[int64][]string) error
}

// BundleSettler is a Settler that is also told which calls have had their
// seconds counted against a bundle of minutes, by bundle ledger (see
// engine.BundleLedger), so that the seconds the engine holds for them can
// be let go as well.
type BundleSettler interface {
	BundlesSettled(ctx context.Context, callsByLedger map[string][]string) error
}

// Run consumes until ctx ends. srv carries the pricing rules, including any
// an extension registered. settler may be nil.
func Run(ctx context.Context, srv *api.Server, nc *nats.Conn, settler Settler) error {
	js, err := engine.EnsureCDRStream(ctx, nc)
	if err != nil {
		return err
	}
	cons, err := js.CreateOrUpdateConsumer(ctx, engine.CDRStream, jetstream.ConsumerConfig{
		Durable: "rater", AckPolicy: jetstream.AckExplicitPolicy,
		AckWait: 30 * time.Second, MaxAckPending: 10 * batchSize,
	})
	if err != nil {
		return err
	}
	caller, err := srv.RootCaller(ctx)
	if err != nil {
		return err
	}
	metrics.NewGauge("softswitch_rater_queue_pending", "Call records queued and not yet stored.", func() (float64, bool) {
		ictx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		info, err := cons.Info(ictx)
		if err != nil {
			return 0, false
		}
		return float64(info.NumPending) + float64(info.NumAckPending), true
	})
	for ctx.Err() == nil {
		batch, err := cons.Fetch(batchSize, jetstream.FetchMaxWait(time.Second))
		if err != nil {
			if !errors.Is(err, nats.ErrTimeout) {
				mFetchErrors.Inc()
				log.Printf("rater: fetch: %v", err)
				time.Sleep(time.Second)
			}
			continue
		}
		var msgs []jetstream.Msg
		for m := range batch.Messages() {
			msgs = append(msgs, m)
		}
		if len(msgs) > 0 {
			store(ctx, srv, caller, msgs, settler)
		}
	}
	return ctx.Err()
}

// store writes a batch in one transaction; if that fails it falls back to
// one record at a time so a single bad record cannot hold up the rest.
func store(ctx context.Context, srv *api.Server, caller api.Caller, msgs []jetstream.Msg, settler Settler) {
	// Charged calls are reported once their transaction has committed. A
	// lost report only keeps the money held longer; it is never let go early.
	charged := map[int64][]string{}
	note := func(rec map[string]json.RawMessage) {
		var r struct {
			CallID   string  `json:"call_id"`
			ClientID int64   `json:"client_id"`
			AnswerAt *string `json:"answer_at"`
		}
		if b, err := json.Marshal(rec); err == nil && json.Unmarshal(b, &r) == nil && r.AnswerAt != nil {
			charged[r.ClientID] = append(charged[r.ClientID], r.CallID)
		}
	}
	bundled := map[string][]string{}
	noteBundles := func(used []api.BundleUse) {
		for _, u := range used {
			ledger := engine.BundleLedger(u.BundleID, u.Period)
			bundled[ledger] = append(bundled[ledger], u.CallID)
		}
	}
	defer func() {
		if settler == nil {
			return
		}
		if len(charged) > 0 {
			if err := settler.Settled(ctx, charged); err != nil {
				mSettleErrors.Inc()
				log.Printf("rater: reporting settled calls: %v", err)
			}
		}
		if bs, ok := settler.(BundleSettler); ok && len(bundled) > 0 {
			if err := bs.BundlesSettled(ctx, bundled); err != nil {
				mSettleErrors.Inc()
				log.Printf("rater: reporting settled bundle seconds: %v", err)
			}
		}
	}()

	raw := make([]map[string]json.RawMessage, 0, len(msgs))
	ok := make([]jetstream.Msg, 0, len(msgs))
	for _, m := range msgs {
		var rec map[string]json.RawMessage
		if err := json.Unmarshal(m.Data(), &rec); err != nil {
			log.Printf("rater: dropping unreadable record: %v", err)
			mDropped.Inc()
			m.Term()
			continue
		}
		raw, ok = append(raw, rec), append(ok, m)
	}
	// IngestCDRs takes fields out of the records it reads.
	keys := make([]map[string]json.RawMessage, len(raw))
	for i, rec := range raw {
		keys[i] = map[string]json.RawMessage{"call_id": rec["call_id"], "client_id": rec["client_id"], "answer_at": rec["answer_at"]}
	}
	if _, _, used, err := srv.IngestCDRsBundles(ctx, caller, raw); err == nil {
		noteBundles(used)
		for i, m := range ok {
			note(keys[i])
			m.Ack()
		}
		mStored.Add(len(ok))
		return
	}
	for i, m := range ok {
		_, _, used, err := srv.IngestCDRsBundles(ctx, caller, raw[i:i+1])
		switch {
		case err == nil:
			noteBundles(used)
			note(keys[i])
			m.Ack()
			mStored.Inc()
		case api.Rejected(err):
			log.Printf("rater: dropping record %s: %v", m.Data(), err)
			mDropped.Inc()
			m.Term()
		default:
			mRetried.Inc()
			log.Printf("rater: %v (will retry)", err)
			m.NakWithDelay(5 * time.Second)
		}
	}
}
