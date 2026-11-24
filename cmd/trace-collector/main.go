// SPDX-License-Identifier: AGPL-3.0-only

// Command trace-collector takes the copy of every call's signalling the SIP
// core sends over HEP and stores it in ClickHouse, where the API reads a
// call's messages from. It is not on the call path: when it is down or
// slow, the SIP core's copies go nowhere and calls go on.
package main

import (
	"context"
	"log"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"softswitch/core/pkg/metrics"
	"softswitch/core/pkg/trace"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg := trace.Config{Listen: os.Getenv("HEP_LISTEN"), ClickHouse: os.Getenv("CLICKHOUSE_URL")}
	if cfg.Listen == "" {
		cfg.Listen = "0.0.0.0:9060"
	}
	if cfg.ClickHouse == "" {
		log.Fatal("CLICKHOUSE_URL is required")
	}
	// TRACE_KEEP_DAYS: how long a call's signalling is kept.
	if v := os.Getenv("TRACE_KEEP_DAYS"); v != "" {
		days, err := strconv.Atoi(v)
		if err != nil || days < 1 {
			log.Fatal("TRACE_KEEP_DAYS must be a number of days, 1 or more")
		}
		cfg.KeepDays = days
	}
	// SIP_ADDRS: the SIP core's own addresses (comma separated), for when
	// its copies do not arrive from the address it serves SIP on.
	for _, a := range strings.Split(os.Getenv("SIP_ADDRS"), ",") {
		if a = strings.TrimSpace(a); a == "" {
			continue
		}
		addr, err := netip.ParseAddr(a)
		if err != nil {
			log.Fatalf("SIP_ADDRS: %q is not an address", a)
		}
		cfg.Local = append(cfg.Local, addr)
	}
	c, err := trace.Listen(cfg)
	if err != nil {
		log.Fatalf("trace: %v", err)
	}
	if addr := os.Getenv("METRICS_LISTEN"); addr != "" {
		go func() {
			if err := metrics.Serve(ctx, addr); err != nil && ctx.Err() == nil {
				log.Fatalf("metrics: %v", err)
			}
		}()
	}
	log.Printf("trace collector listening on %s (HEP over UDP)", c.Addr())
	if err := c.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("trace: %v", err)
	}
}
