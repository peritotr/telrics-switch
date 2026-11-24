// SPDX-License-Identifier: AGPL-3.0-only

package rater

import (
	"bytes"
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"softswitch/core/pkg/clickhouse"
	"softswitch/core/pkg/metrics"
)

const exportBatch = 5000

// Export copies stored call records to ClickHouse until ctx ends. target is
// the HTTP address of the server with the database as its path, such as
// http://user:password@host:8123/softswitch.
//
// Records leave the queue in the transaction that is committed only after
// ClickHouse has accepted them, so each arrives at least once; the table
// merges the repeats. Several exporters may run side by side.
func Export(ctx context.Context, pool *pgxpool.Pool, target string) error {
	ch, err := clickhouse.New(target)
	if err != nil {
		return err
	}
	for {
		if err = clickhouse.Migrate(ctx, ch); err == nil {
			err = enableExport(ctx, pool)
		}
		if err == nil {
			break
		}
		log.Printf("export: setting up: %v (retrying)", err)
		if !sleep(ctx, 5*time.Second) {
			return ctx.Err()
		}
	}
	log.Print("export: copying call records to ClickHouse")
	metrics.NewGauge("softswitch_export_queue_pending", "Call records waiting to be copied to ClickHouse.", func() (float64, bool) {
		qctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var n int64
		if err := pool.QueryRow(qctx, `select count(*) from cdr_export_queue`).Scan(&n); err != nil {
			return 0, false
		}
		return float64(n), true
	})
	for ctx.Err() == nil {
		n, err := exportBatchOnce(ctx, pool, ch)
		if err == nil {
			mExported.Add(n)
		}
		if err != nil && ctx.Err() == nil {
			mExportErrors.Inc()
			log.Printf("export: %v (will retry)", err)
			sleep(ctx, 5*time.Second)
		} else if n < exportBatch {
			sleep(ctx, time.Second)
		}
	}
	return ctx.Err()
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// enableExport turns queueing on and, the first time, queues every record
// stored so far. The lock keeps a record from slipping in unqueued between
// the two.
func enableExport(ctx context.Context, pool *pgxpool.Pool) error {
	var on bool
	if err := pool.QueryRow(ctx, `select exists (select 1 from cdr_export_enabled)`).Scan(&on); err != nil || on {
		return err
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `lock table cdrs in share row exclusive mode`); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `insert into cdr_export_enabled default values on conflict do nothing`)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		_, err = tx.Exec(ctx, `insert into cdr_export_queue (cdr_id) select id from cdrs on conflict do nothing`)
		return err
	})
}

func exportBatchOnce(ctx context.Context, pool *pgxpool.Pool, ch *clickhouse.Client) (n int, err error) {
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		// Whole rows as JSON: columns ClickHouse does not have (an
		// extension's, say) are skipped there, missing values take the
		// column default.
		rows, err := tx.Query(ctx, `
			with taken as (
			  delete from cdr_export_queue where cdr_id in (
			    select cdr_id from cdr_export_queue order by cdr_id limit $1 for update skip locked)
			  returning cdr_id)
			select to_jsonb(c)::text from cdrs c join taken t on t.cdr_id = c.id`, exportBatch)
		if err != nil {
			return err
		}
		var body bytes.Buffer
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				return err
			}
			body.WriteString(line)
			body.WriteByte('\n')
			n++
		}
		if err := rows.Err(); err != nil || n == 0 {
			return err
		}
		return ch.Exec(ctx, "insert into cdrs format JSONEachRow", &body)
	})
	return n, err
}
