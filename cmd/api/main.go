// SPDX-License-Identifier: AGPL-3.0-only

// Command api serves the management REST API.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"softswitch/core/pkg/api"
	"softswitch/core/pkg/clickhouse"
	"softswitch/core/pkg/mail"
	"softswitch/core/pkg/metrics"
	"softswitch/core/pkg/store"
	"softswitch/core/pkg/sysinfo"
)

func main() {
	ctx := context.Background()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Fatal("DATABASE_URL is required")
	}
	pool, err := store.Open(ctx, url)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()
	if err := store.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if key := os.Getenv("BOOTSTRAP_API_KEY"); key != "" {
		if err := api.Bootstrap(ctx, pool, key); err != nil {
			log.Fatalf("bootstrap: %v", err)
		}
	}
	addr := os.Getenv("LISTEN")
	if addr == "" {
		addr = "127.0.0.1:58080"
	}
	server := api.New(pool)
	server.SetEngine(os.Getenv("ENGINE_URL"))
	server.SetEngineToken(os.Getenv("ENGINE_TOKEN"))
	server.SetRealm(os.Getenv("SIP_REALM"))
	// E-mail is on when RESEND_API_KEY or SMTP_URL is set.
	sender, err := mail.FromEnv()
	if err != nil {
		log.Fatal(err)
	}
	if sender != nil {
		if os.Getenv("PANEL_URL") == "" {
			log.Fatal("PANEL_URL is required with e-mail: messages link to the panel")
		}
		server.SetMail(sender, os.Getenv("PANEL_URL"))
		go server.RunMail(ctx, 5*time.Second)
	}
	// The tools' system page: the machine is sampled every five seconds and
	// ten minutes are kept. SYSTEM_DISKS lists the paths whose disks to
	// show, SYSTEM_SERVICES the parts to ask besides those the API knows
	// ("Name=http://address/path, …").
	disks := strings.Split(os.Getenv("SYSTEM_DISKS"), ",")
	if os.Getenv("SYSTEM_DISKS") == "" {
		disks = []string{"/"}
	}
	sys := sysinfo.New(120, disks...)
	go sys.Run(ctx, 5*time.Second)
	server.SetSystem(sys, api.ParseServices(os.Getenv("SYSTEM_SERVICES")))
	// Behind a reverse proxy the caller's address is in X-Forwarded-For; it
	// is believed only when the program is told a proxy sets it.
	server.SetTrustProxy(os.Getenv("TRUST_PROXY") == "1")
	// Two-step sign-in secrets and the passwords the switch signs in to
	// vendors with are encrypted in the database with this key; the
	// route-engine needs the same key to read the latter.
	if key := os.Getenv("SECRET_KEY"); key != "" {
		if err := server.SetSecretKey(key); err != nil {
			log.Fatalf("SECRET_KEY: %v", err)
		}
	} else {
		log.Print("SECRET_KEY is not set: two-step sign-in secrets and vendors' sign-in passwords are stored unencrypted (set 64 hexadecimal digits, e.g. from `openssl rand -hex 32`)")
	}
	// The audit log is kept for a year unless told otherwise; 0 keeps it for ever.
	if v := os.Getenv("AUDIT_RETENTION_DAYS"); v != "" {
		days, err := strconv.Atoi(v)
		if err != nil || days < 0 {
			log.Fatal("AUDIT_RETENTION_DAYS must be a number of days, or 0 to keep the audit log for ever")
		}
		server.SetAuditRetention(days)
	}
	go server.RunAuditRetention(ctx, time.Hour)
	go server.RunBalanceAlerts(ctx, 10*time.Second)
	go server.RunReportSchedules(ctx, time.Minute)
	// Rates of exchange may follow the European Central Bank's daily
	// reference rates (FX_SOURCE=ecb; off by default). The loop looks every
	// ten minutes and fetches on start and once a day.
	feed, err := api.ExchangeRateFeedFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	server.SetExchangeRateFeed(feed)
	go server.RunExchangeRates(ctx, 10*time.Minute)
	if target := os.Getenv("CLICKHOUSE_URL"); target != "" {
		ch, err := clickhouse.New(target)
		if err != nil {
			log.Fatalf("clickhouse: %v", err)
		}
		server.SetAnalytics(ch)
		// The list of call records too, when asked for: needed once
		// PostgreSQL no longer keeps them all (the rater's CDR_KEEP_DAYS).
		server.ListCDRsFromAnalytics(os.Getenv("CDR_LIST_SOURCE") == "clickhouse")
	}
	// Metrics have a listener of their own: this one faces the panel's users.
	if addr := os.Getenv("METRICS_LISTEN"); addr != "" {
		server.RegisterMetrics()
		go func() {
			if err := metrics.Serve(ctx, addr); err != nil {
				log.Fatalf("metrics: %v", err)
			}
		}()
	}
	var handler http.Handler = server
	if dir := os.Getenv("WEB_DIR"); dir != "" {
		handler = api.WithPanel(handler, dir)
	}
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("api listening on %s", addr)
	log.Fatal(srv.ListenAndServe())
}
