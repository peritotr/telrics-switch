// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// configChannel carries "the configuration changed" from whoever writes it
// to the services that keep it in memory.
const configChannel = "softswitch_config"

// NotifyConfig tells the listening services that configuration or a balance
// was changed. It carries no detail: listeners load again, and the load
// itself finds which rates changed (rate_changes, rates.go), so that writes
// which never notify are caught as well.
func NotifyConfig(ctx context.Context, q Querier) error {
	rows, err := q.Query(ctx, `select pg_notify($1, '')`, configChannel)
	if err != nil {
		return err
	}
	rows.Close()
	return rows.Err()
}

// ListenConfig returns a channel that receives whenever NotifyConfig is
// called anywhere, until ctx ends. Notifications that arrive together are
// merged. After a lost connection it signals once, since changes may have
// been missed meanwhile.
func ListenConfig(ctx context.Context, pool *pgxpool.Pool) <-chan struct{} {
	out := make(chan struct{}, 1)
	signal := func() {
		select {
		case out <- struct{}{}:
		default:
		}
	}
	listen := func() error {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			return err
		}
		// The connection is not returned to the pool: it stays subscribed.
		c := conn.Hijack()
		defer c.Close(context.Background())
		if _, err := c.Exec(ctx, "listen "+configChannel); err != nil {
			return err
		}
		signal()
		for {
			if _, err := c.WaitForNotification(ctx); err != nil {
				return err
			}
			signal()
		}
	}
	go func() {
		for ctx.Err() == nil {
			if err := listen(); err != nil && ctx.Err() == nil {
				log.Printf("store: configuration notifications: %v (reconnecting)", err)
				select {
				case <-ctx.Done():
				case <-time.After(2 * time.Second):
				}
			}
		}
	}()
	return out
}
