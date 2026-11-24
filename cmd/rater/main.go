// SPDX-License-Identifier: AGPL-3.0-only

// Command rater prices finished call legs from the queue and stores them.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	"softswitch/core/pkg/api"
	"softswitch/core/pkg/engine"
	"softswitch/core/pkg/metrics"
	"softswitch/core/pkg/rater"
	"softswitch/core/pkg/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := store.Open(ctx, mustEnv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()
	if err := store.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	nc, err := nats.Connect(mustEnv("NATS_URL"), nats.MaxReconnects(-1), nats.RetryOnFailedConnect(true))
	if err != nil {
		log.Fatalf("nats: %v", err)
	}
	defer nc.Close()
	serveMetrics(ctx)
	log.Print("rater running")
	// With ClickHouse the rater also copies stored records there.
	if target := os.Getenv("CLICKHOUSE_URL"); target != "" {
		go func() {
			if err := rater.Export(ctx, pool, target); err != nil && ctx.Err() == nil {
				log.Printf("export: %v", err)
			}
		}()
	}
	// CDR_KEEP_DAYS: how long PostgreSQL keeps a call record once ClickHouse
	// has it. Unset or 0: for good.
	if v := os.Getenv("CDR_KEEP_DAYS"); v != "" && v != "0" {
		days, err := strconv.Atoi(v)
		switch {
		case err != nil || days < 1:
			log.Fatal("CDR_KEEP_DAYS must be a number of days")
		case os.Getenv("CLICKHOUSE_URL") == "":
			log.Fatal("CDR_KEEP_DAYS needs CLICKHOUSE_URL: records leave PostgreSQL only once ClickHouse has them")
		}
		go func() {
			if err := rater.Archive(ctx, pool, os.Getenv("CLICKHOUSE_URL"), time.Duration(days)*24*time.Hour, time.Hour); err != nil && ctx.Err() == nil {
				log.Printf("archive: %v", err)
			}
		}()
	}
	// With Redis the rater reports charged calls so the engine lets go of
	// the money it holds for them; without it the holds simply expire.
	var settler rater.Settler
	if url := os.Getenv("REDIS_URL"); url != "" {
		opt, err := redis.ParseURL(url)
		if err != nil {
			log.Fatalf("redis: %v", err)
		}
		rdb := redis.NewClient(opt)
		defer rdb.Close()
		settler = engine.NewSettler(rdb)
	}
	if err := rater.Run(ctx, api.New(pool), nc, settler); err != nil && ctx.Err() == nil {
		log.Fatalf("rater: %v", err)
	}
}

// serveMetrics answers Prometheus on METRICS_LISTEN, when that is set.
func serveMetrics(ctx context.Context) {
	addr := os.Getenv("METRICS_LISTEN")
	if addr == "" {
		return
	}
	go func() {
		if err := metrics.Serve(ctx, addr); err != nil && ctx.Err() == nil {
			log.Fatalf("metrics: %v", err)
		}
	}()
}

func mustEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("%s is required", name)
	}
	return v
}
