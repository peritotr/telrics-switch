// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	"softswitch/core/pkg/routing"
)

// Config is what a route-engine process needs to run.
type Config struct {
	Listen   string          // address the SIP core reaches the engine on
	RedisURL string          // redis://host:port/db
	NATSURL  string          // nats://host:port
	Reload   time.Duration   // snapshot refresh interval
	Changes  <-chan struct{} // signals a configuration change; may be nil
	Load     Loader
	Reserve  int        // reservation step in seconds; 0 = DefaultReserve
	Hangup   Hangup     // ends a call on the SIP core; nil = cannot
	MaxCalls func() int // system-wide call limit, asked per call; nil = none
	// TranscodeMax is the most calls the media relay may translate at once
	// (Engine.TranscodeMax): 0 = DefaultTranscodeMax, below 0 = none. See
	// TranscodeMaxFromEnv.
	TranscodeMax int
	Peers        *PeerCheck // checks the certificates of TLS vendors; nil = no check
	// VendorRegs reads how the SIP core's registrations with vendors stand;
	// nil = not read (GET /v1/peers then lists none).
	VendorRegs *VendorRegConfig
	Unban      Unban // lifts a ban on the SIP core; nil = cannot
	// Registrations bounds how long a trunk's registration lasts; the zero
	// value means DefaultRegLimits.
	Registrations RegLimits
	// Token is the shared secret callers must send as a bearer token; empty
	// leaves the interface open to whoever reaches it (a warning is logged).
	Token string
	// Adjust, Incoming and OnLeg are the engine's extension points: see
	// Engine.
	Adjust   func(CallInfo, routing.Decision) routing.Decision
	Incoming func(CallInfo, *routing.Snapshot) (routing.Inbound, bool)
	OnLeg    func(Leg)
	// Journal is where call records wait until NATS has them; nil reads
	// it from the environment (JournalFromEnv).
	Journal *JournalConfig
}

// Serve runs a route-engine until ctx ends.
func Serve(ctx context.Context, cfg Config) error {
	if err := CheckToken(cfg.Token); err != nil {
		return err
	}
	if cfg.Token == "" {
		log.Print("engine: WARNING: no token is set (ENGINE_TOKEN): anyone who can reach this listener can read trunk password hashes, end calls and lift bans. Set the same token here, on the SIP core and on the API")
	}
	opt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return err
	}
	rdb := redis.NewClient(opt)
	defer rdb.Close()
	nc, err := nats.Connect(cfg.NATSURL, nats.MaxReconnects(-1), nats.RetryOnFailedConnect(true))
	if err != nil {
		return err
	}
	defer nc.Close()
	jc := cfg.Journal
	if jc == nil {
		env, err := JournalFromEnv()
		if err != nil {
			return err
		}
		jc = &env
	}
	var journal *Journal
	if jc.Dir == "" {
		log.Print("engine: WARNING: no journal for call records (CDR_JOURNAL_DIR): records not yet in NATS are lost if the engine stops")
	} else if journal, err = OpenJournal(jc.Dir, jc.MaxBytes); err != nil {
		return err
	}
	sink := NewNATSSink(ctx, nc, 8, journal)
	// Last: what the call path queued while the listener shut down goes to
	// disk before the process ends.
	defer sink.Close()
	e := New(cfg.Load, NewRedisCalls(rdb), sink)
	// Calls placed under an engine from before ADR-0042 are counted where
	// the trunk's limit no longer looks; they join its one set.
	if n, err := MigrateTrunkCounts(ctx, rdb); err != nil {
		log.Printf("engine: WARNING: moving trunk counts to their new set: %v (calls in progress may go uncounted until they end)", err)
	} else if n > 0 {
		log.Printf("engine: moved the counts of %d trunks to their new set (ADR-0042)", n)
	}
	// Before the first load, so that the journal's state can be read while
	// the engine waits for the database.
	e.registerGauges(sink.Pending, journal)
	// The schema may still be on its way when the lab starts; keep trying.
	for {
		if err = e.Reload(ctx); err == nil {
			break
		}
		log.Printf("engine: first load: %v (retrying)", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if cfg.Reserve > 0 {
		e.Reserve = cfg.Reserve
	}
	e.Hangup = cfg.Hangup
	e.MaxCalls = cfg.MaxCalls
	if cfg.TranscodeMax != 0 {
		e.TranscodeMax = max(cfg.TranscodeMax, 0)
	}
	e.Unban = cfg.Unban
	e.Registry, e.Reg = NewRedisRegistry(rdb), cfg.Registrations
	e.syncRegSources(ctx)
	e.Token = cfg.Token
	e.Adjust, e.Incoming, e.OnLeg = cfg.Adjust, cfg.Incoming, cfg.OnLeg
	go e.Run(ctx, cfg.Reload, cfg.Changes)
	go e.RunSweep(ctx)
	go e.RunLive(ctx)
	// Vendors that are to be asked whether they are there (a setting of the
	// connection) are asked by the engine itself, with the same trust in
	// certificates as the check of their names.
	e.prober = &Prober{NoNames: cfg.Peers == nil}
	if cfg.Peers != nil {
		e.prober.Roots = cfg.Peers.Roots
		go e.RunPeerCheck(ctx, cfg.Peers)
	}
	go e.RunHealth(ctx, e.prober)
	if cfg.VendorRegs != nil {
		go e.RunVendorRegs(ctx, cfg.VendorRegs)
	}

	srv := &http.Server{Addr: cfg.Listen, Handler: e.Handler(), ReadHeaderTimeout: 5 * time.Second}
	shutDown := make(chan struct{})
	go func() {
		defer close(shutDown)
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shut)
	}()
	log.Printf("route-engine listening on %s", cfg.Listen)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	// Requests still being answered may yet queue records.
	<-shutDown
	return nil
}
