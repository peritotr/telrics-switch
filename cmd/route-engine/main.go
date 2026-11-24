// SPDX-License-Identifier: AGPL-3.0-only

// Command route-engine answers the SIP core's per-call questions.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"softswitch/core/pkg/api"
	"softswitch/core/pkg/engine"
	"softswitch/core/pkg/sipctl"
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
	reload := 10
	if v := os.Getenv("RELOAD_SEC"); v != "" {
		if reload, err = strconv.Atoi(v); err != nil || reload < 1 {
			log.Fatal("RELOAD_SEC must be a positive number of seconds")
		}
	}
	listen := os.Getenv("LISTEN")
	if listen == "" {
		listen = "127.0.0.1:58090"
	}
	reserve := 0
	if v := os.Getenv("RESERVE_SEC"); v != "" {
		if reserve, err = strconv.Atoi(v); err != nil || reserve < 1 {
			log.Fatal("RESERVE_SEC must be a positive number of seconds")
		}
	}
	// Without a way to end calls the engine cannot stop one whose funds
	// went to other calls; only the SIP core's own timer remains.
	var hangup engine.Hangup
	var unban engine.Unban
	var vendorRegs *engine.VendorRegConfig
	if addr := os.Getenv("OPENSIPS_MI"); addr != "" {
		hangup = sipctl.OpenSIPS(addr)
		unban = sipctl.OpenSIPSUnban(addr)
		// How the SIP core's registrations with vendors stand (ADR-0035).
		if vendorRegs, err = engine.VendorRegConfigFromEnv(sipctl.OpenSIPSRegistrations(addr)); err != nil {
			log.Fatal(err)
		}
	} else {
		log.Print("OPENSIPS_MI not set: calls cannot be ended when their reservation runs out")
	}
	// Vendors reached over TLS must show a certificate made out to them.
	peers, err := engine.PeerCheckFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	// How long a trunk's registration may last.
	regs, err := engine.RegLimitsFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	// The most calls whose audio the media relay may translate at once.
	transcodeMax, err := engine.TranscodeMaxFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	// The passwords the switch signs in to vendors with are sealed in the
	// database with the API's SECRET_KEY; without the same key here they
	// cannot be read and those vendors' challenges go unanswered.
	server := api.New(pool)
	if key := os.Getenv("SECRET_KEY"); key != "" {
		if err := server.SetSecretKey(key); err != nil {
			log.Fatalf("SECRET_KEY: %v", err)
		}
	}
	// Lab only: see lab.go.
	load, adjust := labDeliver(os.Getenv("LAB_DELIVER"), server.LoadSnapshot)
	adjust = labDelay(os.Getenv("LAB_DECIDE_DELAY"), adjust)
	err = engine.Serve(ctx, engine.Config{
		Listen: listen, RedisURL: mustEnv("REDIS_URL"), NATSURL: mustEnv("NATS_URL"),
		Reload:  time.Duration(reload) * time.Second,
		Reserve: reserve, Hangup: hangup, Peers: peers, Unban: unban,
		VendorRegs:    vendorRegs,
		Registrations: regs,
		TranscodeMax:  transcodeMax,
		Changes:       store.ListenConfig(ctx, pool),
		Token:         os.Getenv("ENGINE_TOKEN"),
		Load:          load, Adjust: adjust,
		Incoming: labInbound(os.Getenv("LAB_INBOUND")),
	})
	if err != nil && ctx.Err() == nil {
		log.Fatalf("route-engine: %v", err)
	}
}

func mustEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("%s is required", name)
	}
	return v
}
