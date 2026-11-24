// SPDX-License-Identifier: AGPL-3.0-only
package engine

import (
	"context"
	"time"

	"softswitch/core/pkg/metrics"
)

// What the engine tells Prometheus. Counters are single atomic adds; the
// gauges are read when the metrics are collected, away from the call path.
var (
	mDecision      = metrics.NewHistogram("softswitch_engine_decision_seconds", "Time to answer an authorize request.", metrics.LatencyBounds)
	mAllowed       = metrics.NewCounter("softswitch_engine_authorize_total", "Calls asked about, by outcome.", "result=allowed")
	mRefused       = metrics.NewCounter("softswitch_engine_authorize_total", "Calls asked about, by outcome.", "result=refused")
	mEventErrors   = metrics.NewCounter("softswitch_engine_event_errors_total", "Call events that could not be applied (state store unavailable).")
	mReloadErrors  = metrics.NewCounter("softswitch_engine_reload_errors_total", "Configuration loads that failed; the previous configuration stays in use.")
	mSweepErrors   = metrics.NewCounter("softswitch_engine_sweep_errors_total", "Failed passes over calls whose reserved time is running out.")
	mExtended      = metrics.NewCounter("softswitch_engine_reservations_extended_total", "Reservations extended for calls in progress.")
	mCut           = metrics.NewCounter("softswitch_engine_calls_cut_total", "Calls ended by the engine because the funds ran out.")
	mHangupErrors  = metrics.NewCounter("softswitch_engine_hangup_errors_total", "Requests to end a call that the SIP core did not take.")
	mBlocked       = metrics.NewCounter("softswitch_engine_source_blocked_total", "Questions about an address the operator's firewall rules shut out.")
	mBans          = metrics.NewCounter("softswitch_engine_bans_reported_total", "Addresses the SIP core reported shutting out for flooding.")
	mPeerSkipped   = metrics.NewCounter("softswitch_engine_peer_skipped_total", "Routes left out of a call because the vendor's certificate was refused.")
	mHealthSkipped = metrics.NewCounter("softswitch_engine_health_skipped_total", "Routes left out of a call because the vendor does not answer the engine's OPTIONS.")
	mProbes        = metrics.NewCounter("softswitch_engine_probes_total", "Connection tests asked for by an operator.")
	mCDRErrors     = metrics.NewCounter("softswitch_engine_cdr_publish_errors_total", "Failed attempts to queue a call record; each is retried.")
	mUnauthorized  = metrics.NewCounter("softswitch_engine_unauthorized_total", "Requests refused because they did not carry the engine's token.")
	mEvents        = map[string]*metrics.Counter{}
	// The journal of call records (ADR-0029).
	mJournalReplayed = metrics.NewCounter("softswitch_engine_cdr_journal_replayed_total", "Call records found in the journal at start and sent to NATS again.")
	mJournalTorn     = metrics.NewCounter("softswitch_engine_cdr_journal_torn_total", "Segments found at start with a cut-off or damaged tail, which was dropped.")
	mJournalErrors   = map[string]*metrics.Counter{}
	// Codec translation (T-09): attempts sent with the media relay
	// translating, and routes passed over because TranscodeMax were in
	// progress.
	mTranscoded    = metrics.NewCounter("softswitch_engine_transcoded_total", "Attempts sent with the media relay translating between codecs.")
	mTranscodeFull = metrics.NewCounter("softswitch_engine_transcode_full_total", "Routes passed over because the most calls that may be transcoded at once were in progress.")
	mExpired       = metrics.NewCounter("softswitch_engine_cdr_expired_total", "Call records written for calls whose end was never reported before their state expired.")
)

func init() {
	// write: the disk did not take a batch; full: the journal is at its
	// size; read: a record could not be read back; remove: a segment whose
	// records NATS has could not be removed.
	for _, kind := range []string{"write", "full", "read", "remove"} {
		mJournalErrors[kind] = metrics.NewCounter("softswitch_engine_cdr_journal_errors_total", "Failures of the call record journal, by kind.", "kind="+kind)
	}
	for _, ev := range []string{"failed", "ringing", "answered", "ended"} {
		mEvents[ev] = metrics.NewCounter("softswitch_engine_events_total", "Call events reported by the SIP core.", "event="+ev)
	}
}

// registerGauges exposes the state of the engine that serves; a process
// runs one.
func (e *Engine) registerGauges(pending func() int, j *Journal) {
	metrics.NewGauge("softswitch_engine_cdr_journal_records", "Call records in the journal that NATS has not yet confirmed.", func() (float64, bool) {
		if j == nil {
			return 0, false
		}
		return float64(j.Depth()), true
	})
	metrics.NewGauge("softswitch_engine_cdr_journal_bytes", "Room the call record journal takes on disk.", func() (float64, bool) {
		if j == nil {
			return 0, false
		}
		return float64(j.Bytes()), true
	})
	metrics.NewGauge("softswitch_engine_cdr_journal_max_bytes", "The most room the call record journal may take.", func() (float64, bool) {
		if j == nil {
			return 0, false
		}
		return float64(j.MaxBytes()), true
	})
	metrics.NewGauge("softswitch_engine_snapshot_age_seconds", "Seconds since the configuration was last loaded.", func() (float64, bool) {
		at := e.loaded.Load()
		return time.Since(time.Unix(at, 0)).Seconds(), at > 0
	})
	metrics.NewGauge("softswitch_engine_snapshot_load_seconds", "How long the last configuration load took; only what changed is read (T-30), everything every RELOAD_FULL_SEC.", func() (float64, bool) {
		d := e.loadTook.Load()
		return time.Duration(d).Seconds(), d > 0
	})
	metrics.NewGauge("softswitch_engine_calls_active", "Calls in progress.", func() (float64, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		n, err := e.calls.Count(ctx, e.now(), []string{allCalls})
		if err != nil {
			return 0, false
		}
		return float64(n[0]), true
	})
	metrics.NewGauge("softswitch_engine_transcoding_calls", "Calls in progress whose audio the media relay translates.", func() (float64, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		n, err := e.calls.Count(ctx, e.now(), []string{transcodeCalls})
		if err != nil {
			return 0, false
		}
		return float64(n[0]), true
	})
	metrics.NewGauge("softswitch_engine_transcoding_max", "The most calls whose audio may be translated at once (TRANSCODE_MAX).", func() (float64, bool) {
		return float64(e.TranscodeMax), true
	})
	metrics.NewGauge("softswitch_engine_peers_refused", "Vendor connections given no calls because their certificate was refused.", func() (float64, bool) {
		st := e.peers.Load()
		if st == nil {
			return 0, false
		}
		return float64(len(st.bad)), true
	})
	metrics.NewGauge("softswitch_engine_vendors_unreachable", "Vendor connections given no calls because they do not answer the engine's OPTIONS.", func() (float64, bool) {
		st := e.health.Load()
		if st == nil {
			return 0, false
		}
		return float64(len(st.down)), true
	})
	for _, state := range []string{VendorRegRegistered, VendorRegPending, VendorRegFailing, VendorRegUnknown} {
		metrics.NewGauge("softswitch_engine_vendor_registrations", "Vendor connections the SIP core registers with, by how the registration stands.", func() (float64, bool) {
			st := e.vendorRegs.Load()
			if st == nil {
				return 0, false
			}
			return float64(st.count(state)), true
		}, "state="+state)
	}
	metrics.NewGauge("softswitch_sip_cert_expires_seconds", "Seconds until the SIP core's own certificate expires.", func() (float64, bool) {
		end := e.sipCertEnd.Load()
		return time.Until(time.Unix(end, 0)).Seconds(), end > 0
	})
	metrics.NewGauge("softswitch_engine_cdr_queue_pending", "Call records waiting in memory to be written to the journal (without one: to be published).", func() (float64, bool) {
		return float64(pending()), true
	})
}
