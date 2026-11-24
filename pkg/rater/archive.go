// SPDX-License-Identifier: AGPL-3.0-only

package rater

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"softswitch/core/pkg/clickhouse"
)

// archiveBatch is how many records one pass looks at.
const archiveBatch = 5000

// Archive removes call records older than keep from PostgreSQL, once
// ClickHouse holds them, until ctx ends. PostgreSQL then stays small while
// ClickHouse keeps the whole history. The cutoff of each pass that deletes
// is kept as the archive horizon (table cdr_archive): the API's single
// record, list and CSV read records older than it from ClickHouse.
//
// A record is deleted only after ClickHouse has been asked for it by id and
// answered that it is there. One that is not there stays, is queued for
// export again and counted: no record is ever dropped on trust.
func Archive(ctx context.Context, pool *pgxpool.Pool, target string, keep, every time.Duration) error {
	ch, err := clickhouse.New(target)
	if err != nil {
		return err
	}
	log.Printf("archive: call records older than %s leave PostgreSQL once ClickHouse has them", keep)
	for {
		for {
			deleted, missing, err := archiveOnce(ctx, pool, ch, time.Now().Add(-keep))
			if err != nil {
				mArchiveErrors.Inc()
				log.Printf("archive: %v", err)
				break
			}
			mArchived.Add(deleted)
			if missing > 0 {
				mArchiveMissing.Add(missing)
				log.Printf("archive: %d old records are not in ClickHouse; kept and queued for export again", missing)
			}
			if deleted == 0 {
				break
			}
		}
		if !sleep(ctx, every) {
			return ctx.Err()
		}
	}
}

// archiveOnce handles one batch of records that started before cutoff and
// are not waiting to be exported.
func archiveOnce(ctx context.Context, pool *pgxpool.Pool, ch *clickhouse.Client, cutoff time.Time) (deleted, missing int, err error) {
	rows, err := pool.Query(ctx, `
		select c.id from cdrs c
		where c.start_at < $1 and not exists (select 1 from cdr_export_queue q where q.cdr_id = c.id)
		order by c.start_at limit $2`, cutoff, archiveBatch)
	if err != nil {
		return 0, 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil || len(ids) == 0 {
		return 0, 0, err
	}
	list := make([]string, len(ids))
	for i, id := range ids {
		list[i] = strconv.FormatInt(id, 10)
	}
	// Which of them ClickHouse really has.
	out, err := ch.Query(ctx, fmt.Sprintf(`select id from cdrs where id in (%s) group by id format TSV`, strings.Join(list, ",")), nil)
	if err != nil {
		return 0, 0, err
	}
	var there, absent []int64
	have := map[int64]bool{}
	for _, line := range strings.Fields(string(out)) {
		id, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("unexpected answer from ClickHouse: %q", line)
		}
		have[id] = true
	}
	for _, id := range ids {
		if have[id] {
			there = append(there, id)
		} else {
			absent = append(absent, id)
		}
	}
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if len(absent) > 0 {
			if _, err := tx.Exec(ctx, `insert into cdr_export_queue (cdr_id) select unnest($1::bigint[]) on conflict do nothing`, absent); err != nil {
				return err
			}
		}
		// Again "not waiting to be exported": a record may have been queued
		// since it was chosen.
		tag, err := tx.Exec(ctx, `
			delete from cdrs c where c.id = any($1) and c.start_at < $2
			  and not exists (select 1 from cdr_export_queue q where q.cdr_id = c.id)`, there, cutoff)
		if err != nil {
			return err
		}
		deleted = int(tag.RowsAffected())
		if deleted == 0 {
			return nil
		}
		// Every record from cutoff on is still here: the API reads older
		// ones from ClickHouse. The horizon only moves forward, so that a
		// shorter CDR_KEEP_DAYS used once is not forgotten.
		_, err = tx.Exec(ctx, `
			insert into cdr_archive (horizon) values ($1)
			on conflict (one) do update set horizon = greatest(cdr_archive.horizon, excluded.horizon)`, cutoff)
		return err
	})
	return deleted, len(absent), err
}
