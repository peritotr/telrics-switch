<!-- English version of docs/10-izleme.md; the Turkish document is the source and both are updated together. -->
# Monitoring: metrics, alerts and call trace

> English version of [`docs/10-izleme.md`](../10-izleme.md) (Turkish), which is the authoritative text; the two are updated together.

Three parts: the metrics the services expose to Prometheus, the alert rules that run on those metrics, and the storage of the calls' SIP signalling (call trace).

> **Status.** Metrics, rules and the alert reaching Alertmanager are tried in the smoke test (`make prod-smoke`): five services are scraped, and a killed SIP process and a stopped service raise alerts. Call trace is tried with real calls (`make test-routing`), in the smoke test and under load (`make load`). The load test report also gives the highest number of ports the relay used, the number of "Ran out of ports", the 180/183s sent to the caller after the 200 (from the trace) and the calls whose end the engine never learned. The 180/183 count is for information; a port error and calls left in the engine fail the test (T-29). Not tried: an alert reaching a human (the receiver is yours to define), server metrics (`node-exporter`, production layout only), sending to a real Homer install, the size of the trace store under real traffic. The thresholds are starting values; they have not been tuned against real traffic. There is no ready-made dashboard (Grafana).

## Metrics

None of them has authentication, and they reveal traffic volume: they are open only on the private network or on 127.0.0.1.

| Service | Address (production layout) | How it is turned on |
|---|---|---|
| route-engine | `127.0.0.1:58090/metrics` | always, on its own listener; unlike the engine's other endpoints it does not require the secret (`ENGINE_TOKEN`) |
| api | `127.0.0.1:58091/metrics` | `METRICS_LISTEN` (separate from the panel's listener, which faces outwards) |
| rater | `127.0.0.1:58092/metrics` | `METRICS_LISTEN` |
| trace-collector | `127.0.0.1:58093/metrics` | `METRICS_LISTEN` |
| opensips | `127.0.0.1:8888/metrics` | always (`SIP_METRICS`) |
| rtpengine | `127.0.0.1:2225/metrics` | always (`RTP_METRICS`) |
| server | `127.0.0.1:59100/metrics` | `monitoring` profile (node-exporter) |

Metrics of our own services (`pkg/metrics`; counters are a single atomic add on the call path, and values that ask the database or Redis are computed only when Prometheus scrapes):

| Metric | Meaning |
|---|---|
| `softswitch_build_info{version, goversion}` | The release the process was built from (always 1; ADR-0051). Given by the engine, the API, the rater and the trace collector; after an upgrade, `count by (version) (softswitch_build_info)` shows whether every service runs the new release. A development build reports `dev`, `dev-<commit>` or a version ending in `-dirty` |
| `softswitch_engine_decision_seconds` | Time taken by the call decision (histogram). Budget: p99 < 10 ms |
| `softswitch_engine_authorize_total{result}` | Calls asked about: `allowed` / `refused` |
| `softswitch_engine_events_total{event}` | Events reported by the SIP core |
| `softswitch_engine_calls_active` | Number of calls in progress |
| `softswitch_engine_snapshot_age_seconds` | Time since the configuration was last loaded |
| `softswitch_engine_snapshot_load_seconds` | Duration of the last load; only the rates that changed are read (ADR-0048), so an ordinary refresh takes milliseconds and the full load at `RELOAD_FULL_SEC` takes seconds with a large tariff |
| `softswitch_engine_reload_errors_total`, `…_event_errors_total`, `…_sweep_errors_total` | Configuration load errors and state store (Redis) errors |
| `softswitch_engine_reservations_extended_total`, `…_calls_cut_total`, `…_hangup_errors_total` | Balance reservation: calls extended, calls hung up because the money ran out, and calls that could not be hung up |
| `softswitch_engine_source_blocked_total`, `…_bans_reported_total` | Firewall: questions arriving for an address blocked by a rule; blocks the SIP core reported because of flooding |
| `softswitch_engine_peers_refused`, `…_peer_skipped_total` | TLS vendor connections given no calls because their certificate was refused, and the routes skipped because of it (alert: `VendorCertificateRefused`) |
| `softswitch_engine_vendors_unreachable`, `…_health_skipped_total` | Vendor connections pinged by the engine (with `options_interval_sec` set) that are given no calls because they do not answer, and the routes skipped because of it (alert: `VendorUnreachable`, 5 minutes; warning level) |
| `softswitch_engine_vendor_registrations{state}` | Vendor connections the switch registers with (T-14), by registration state: `registered`, `pending`, `failing` (the last attempt failed: no answer, password rejected, vendor refused, or it could not be sent), `unknown` (the SIP core could not be asked) (alert: `VendorRegistrationFailing`, 10 minutes; warning level) |
| `softswitch_engine_probes_total` | Connection tests requested from the panel ("Test") |
| `softswitch_sip_cert_expires_seconds` | Time left until the SIP core's own certificate expires; the engine reads it by connecting to the `SIP_TLS_ADDR` address (alert: `SIPCertificateExpiring`, 14 days) |
| `softswitch_engine_cdr_queue_pending`, `…_cdr_publish_errors_total` | CDRs waiting in the engine's memory to be written to the journal (to NATS if there is no journal); failed attempts to send to NATS |
| `softswitch_engine_cdr_journal_records`, `…_cdr_journal_bytes`, `…_cdr_journal_max_bytes` | The CDR journal on the engine's disk (ADR-0029): records NATS has not yet acknowledged; the space the journal takes on disk and the most it can take (`CDR_JOURNAL_MAX_MB`). While NATS is down the record count grows, and when NATS is back it drops to zero (alerts: `CallRecordJournalBacklog`, `CallRecordJournalFilling`) |
| `softswitch_engine_cdr_journal_replayed_total`, `…_cdr_journal_torn_total` | Records found in the journal at start-up and sent to NATS again (including those of the last minute that had already been sent; repeats are harmless); segments whose tail was cut because their last record was incomplete or corrupt |
| `softswitch_engine_cdr_journal_errors_total{kind}` | Journal errors: `write` (the disk did not take the batch write; records wait in memory), `full` (journal full), `read` (a record could not be read back), `remove` (a segment that reached NATS could not be deleted; it is sent again after a restart) (alerts: `CallRecordJournalFailing`, `CallRecordJournalFilling`) |
| `softswitch_engine_cdr_expired_total` | Records the engine wrote for calls whose state lapsed without their end ever being reported (talk time and charge 0; `docs/08` (Turkish), "Bitişi bildirilmeyen çağrı", call whose end is not reported; alert: `CallsExpiredUnreported`) |
| `softswitch_engine_register_total{result}`, `…_register_errors_total`, `…_trunk_unreachable_total`, `…_trunk_codec_mismatch_total` | Registration requests: `stored` / `removed` / `queried` / `refused`; registrations that could not be stored or read (state store; alert: `EngineStateErrors`); trunk routes dropped from a call because the trunk is not registered; trunk routes dropped because they have no codec in common with the caller's trunk (a result of settings, no alert) |
| `softswitch_engine_transcoded_total`, `…_transcode_full_total`, `…_transcoding_calls`, `…_transcoding_max` | Transcoding (T-09; `docs/08` (Turkish), "Kodek çevirisi", transcoding): attempts sent for the media relay to transcode (per attempt for a call that moves to the next route); routes skipped while the number of calls being transcoded at once is at the limit (alert: `TranscodingAtLimit`); calls being transcoded right now (`cc:x` in Redis, counted when Prometheus scrapes); the limit (`TRANSCODE_MAX`) |
| `softswitch_engine_unauthorized_total` | Requests the engine rejected with 401 because they did not carry its secret (`ENGINE_TOKEN`) or carried a wrong one (alert: `EngineRefusingCallers`) |
| `softswitch_rater_records_total{result}` | Records taken from the queue: `stored` / `dropped` / `retried` |
| `softswitch_rater_queue_pending` | Records waiting in the queue (not yet stored) |
| `softswitch_export_queue_pending`, `softswitch_export_records_total`, `softswitch_export_errors_total` | Copying to ClickHouse |
| `softswitch_archive_records_total`, `…_archive_missing_total`, `…_archive_errors_total` | Archiving (`CDR_KEEP_DAYS`): old records deleted from PostgreSQL; records not deleted because they were not found in ClickHouse and queued for export again; failed passes (alert: `ArchivedRecordsMissing`) |
| `softswitch_trace_received_total`, `softswitch_trace_stored_total`, `softswitch_trace_queue_pending` | Call trace: HEP packets reaching the collector, messages written to ClickHouse, messages waiting to be written |
| `softswitch_trace_dropped_total{reason}`, `softswitch_trace_store_errors_total` | Messages not stored: `unreadable` (a packet that cannot be read as HEP or SIP), `queue_full` (did not fit in the waiting queue because ClickHouse could not keep up), `store_failed` (a batch that could not be written in three attempts); failed write attempts (alert: `CallTracesDropped`) |
| `softswitch_api_requests_total{class}`, `softswitch_api_request_seconds` | Management API requests |
| `softswitch_mail_outbox_pending`, `softswitch_mail_outbox_oldest_seconds` | E-mails waiting to be sent |
| `softswitch_api_db_connections` | Database connections used by the API |
| `softswitch_fx_fetch_failures_total`, `softswitch_fx_fetch_age_seconds` | Exchange rates from an external source (`FX_SOURCE=ecb`, T-20): failed fetch attempts (unreachable, HTTP error, unreadable file; the last rates remain); time since the last successful fetch (visible only while the source is on and after one fetch) (alert: `ExchangeRatesNotFetched`) |

None of the metrics endpoints requires authentication; the engine's is deliberately left open too (it only gives counters; the reasoning is in `docs/08-cagri-arayuzu.md` (Turkish), "Kimlik doğrulama", authentication). All of them should listen only on 127.0.0.1 or on the private network.

Because rater and route-engine contain the API package, they also show the `softswitch_api_*` metrics as zero; the meaningful ones are those labelled `service="api"`.

OpenSIPS (`opensips_*`: request and reply counters, dialogs in progress, shared memory, process load) and rtpengine (`rtpengine_*`: sessions, packet and error counters) expose their own metrics. The SIP core's script also keeps its own counters (the `statistics` module, `update_stat`; they appear in Prometheus on the same endpoint under the name given in the `modparam("prometheus", "statistics", …)` list). These are used in addition:

| Metric | Meaning |
|---|---|
| `opensips_refer_refused` | Transfers requested within a call and refused with 403 (REFER, in either direction; ADR-0028). The call continues. The counter is reset when the SIP core restarts (alert: `TransfersRefused`) |
| `opensips_media_refused` | New calls refused with 503 because the media relay did not take the offer (no answer, no ports left, or an error) (ADR-0030); also calls hung up right after the answer in an offerless INVITE because the relay did not take the offer in the vendor's 200 (ADR-0043; one per call). The next route is not tried; the engine records the call as `ended 503` (`cause: "media"`: not written to the engine counters of the chosen connection, ADR-0039). The counter is reset when the SIP core restarts (alert: `CallsRefusedNoMedia`) |
| `rtpengine_ports_free`, `rtpengine_ports_used` | The relay's media ports (range: `RTP_PORT_MIN`–`RTP_PORT_MAX`; four per call). The relay counts only those it uses itself and does not see ports held by another process (alert: `MediaPortsLow`) |
| `rtpengine_transcoded_media`, `rtpengine_transcode_packets_total{chain}` | Streams the relay is transcoding at that moment; packets transcoded from one codec to another, by direction (`chain="PCMA/8000 -> PCMU/8000"`). The engine's counter counts calls by the lists; these show the audio actually transcoded (`make test-routing`, "transcode", checks both directions with them) |
| `rtpengine_closed_sessions_total{reason="timeout"}` | Calls the relay closed because their audio stopped for `RTP_TIMEOUT` (60) seconds (ADR-0027; `reason="silent_timeout"`: `RTP_HOLD_TIMEOUT` ran out while on hold). The relay has the SIP core tear each of them down with `teardown`; its ratio to `rtpengine_sessions_total` is what the alert is about (alert: `CallsLosingMedia`) |

## Prometheus and alerts

```bash
./deploy/prod/install.sh --monitoring
```

Turns on the `monitoring` profile: Prometheus (`127.0.0.1:59090`), Alertmanager (`127.0.0.1:59093`) and, in the production layout, node-exporter. Both interfaces can be reached only from the server itself; use an SSH tunnel to look at them remotely. Metrics are kept for 30 days (`METRICS_KEEP`).

The rules are in `deploy/prod/monitoring/alerts.yml`:

| Alert | When | Severity |
|---|---|---|
| `ServiceDown` | A service has not been scrapable for a minute | critical |
| `SIPCoreRestarted` | The SIP core's counters started from zero (the process died and was restarted) | critical |
| `SIPCoreMemoryLow` | Less than 10% of the SIP core's shared memory is free | warning |
| `DecisionSlow` | Decision time p99 > 10 ms (5 min) | warning |
| `ConfigurationStale` | The engine has not been able to load the configuration for 5 minutes | critical |
| `EngineStateErrors` | The engine cannot reach the call state (Redis) | critical |
| `HangupFailing` | A call whose money ran out cannot be hung up | critical |
| `EngineRefusingCallers` | The engine rejected more than 20 requests in five minutes for having no secret or a wrong one: the `ENGINE_TOKEN` of the SIP core or of the API differs from the engine's (calls get 503), or someone is probing the engine's port | critical |
| `MostCallsRefused` | More than half of the calls are refused (10 min, while there is traffic) | warning |
| `CallsRefusedNoMedia` | In the last 5 minutes at least one call was refused with 503 because the media relay did not take the offer: the relay has stopped or cannot be reached (`ServiceDown` also shows rtpengine) or it has run out of ports ("Ran out of ports" in its log) | critical |
| `MediaPortsLow` | Less than 10% of the relay's media ports are free (5 min): when the ports run out, new calls get 503 | warning |
| `TranscodingAtLimit` | In the last 10 minutes at least one route was skipped because the calls being transcoded at once reached `TRANSCODE_MAX`: the call went to the next route, or was refused if there was no other route. If the CPU has room, raise the limit, or give the trunk and connection concerned a codec in common | warning |
| `CallsLosingMedia` | More than 5% of calls are hung up because their audio stopped (10 min, while there is traffic): the audio may not be arriving at all (wrong `RTP_INTERFACE`, closed media ports, NAT) | warning |
| `TransfersRefused` | More than 20 transfers (REFER) refused in 15 minutes: a client's device is trying to transfer through the switch (its users see the transfer fail), or someone is trying to set up calls by transfer (ADR-0028) | warning |
| `CallRecordsWaiting` | CDRs are piling up without being stored | warning |
| `CallRecordJournalFailing` | The engine cannot write to or read from the CDR journal (a `write`/`read` error in the last 5 minutes); records are waiting in memory | critical |
| `CallRecordJournalBacklog` | For 10 minutes more than 1,000 records have been waiting for NATS on the engine's disk | warning |
| `CallRecordJournalFilling` | The journal passed 80% of its size limit, or is full | critical |
| `CallsExpiredUnreported` | In the last hour a call whose end was not reported was recorded free of charge (the SIP core restarted or could not reach the engine) | warning |
| `CallRecordsDropped` | The rater dropped a record | critical |
| `ReportsFallingBehind` | Records are not reaching ClickHouse | warning |
| `CallTracesDropped` | Call traces cannot be stored: the collector is dropping messages or cannot write to ClickHouse. Calls and CDRs are not affected | warning |
| `ExchangeRatesNotFetched` | If exchange rates come from an external source (`FX_SOURCE=ecb`): a fetch attempt failed in the last day, or there has been no successful fetch for two days. The last rates continue to be used; calls are not affected. The reason is on the exchange rate card in the panel and in the API log | warning |
| `MailNotGoingOut` | An e-mail has been waiting for 15 minutes | warning |
| `APIErrors` | More than 5% of API requests fail | warning |
| `DiskAlmostFull` | Less than 15% of a disk is free | critical |

**Where the alerts go** is set in `deploy/prod/monitoring/alertmanager.yml` (the install copies it from the example; an upgrade does not overwrite it). As shipped, alerts are collected but **nobody is notified**: define a receiver (e-mail, webhook) in the file, point `route.receiver` at it, then `docker compose restart alertmanager`.

Monitoring itself runs on the same server: if the server goes, the alert goes with it. Probing the server from outside (e.g. OPTIONS to 5060, HTTPS to the panel) has to be set up separately.

## Call trace

The SIP core copies the signalling of every call it carries (from the caller's INVITE to the end, both sides and all route attempts) over HEP3 to the install's own collector (`trace-collector`). The collector writes the messages to the `sip_messages` table in ClickHouse; the panel and the API read a call's trace from there.

```
OpenSIPS ──HEP/UDP──▶ trace-collector ──▶ ClickHouse (sip_messages) ──▶ API ──▶ panel, pcap
        └─HEP/UDP──▶ HEP_TARGET (optional: your own Homer)
```

**What is stored.** Per message: time (microseconds), Call-ID, direction (`in`: received by the core, `out`: sent by it), transport (`udp`, `tcp`, `tls`), source and destination address and port, and the raw text of the message.

- **What is traced (ADR-0046).** Every call the engine counts as belonging to a client: those accepted and sent to a vendor (from the caller's INVITE to the end) and those the engine refuses (balance, limit, tariff, CLI rule, no route; also a call the caller cancels while the engine is deciding). The trace of a refused call has three messages: the incoming INVITE, the outgoing rejection and the caller's ACK (`100 Trying`, and a CANCEL arriving while the decision is awaited, are absent because they are answered before the trace starts). The engine says so with the `trace` field in the `authorize` response; the script does not decide for itself. Every traced rejection also has a CDR.
- The trace of a trunk identified by password starts at the INVITE that carries the credentials (CSeq 2): the first INVITE and the 401 given to it are not traced, because they arrive before it is known who the caller is.
- **What is not traced.** No request from a source the engine does not recognise: the call from an unknown address and the 401 challenge sent to it, a wrong password (even if the username exists), a call the engine rejects as an "unknown source", an address blocked by the firewall (even if it was recognised before), flooding and `ban:` blocks, the 503 given when the engine cannot be reached. This way strangers' traffic cannot fill the trace store. To see the 401 on the client side you need `tcpdump` on the server; the 401/407 a vendor challenges us with is in the trace of the accepted call.
- Messages carried over TLS are stored as the core decrypted them, in clear text.
- **No password digest is stored.** The digest response (`response`) in the `Authorization` and `Proxy-Authorization` headers is replaced with `***` before it is written; for authentication schemes other than Digest the whole header value is masked. The username, realm and nonce remain. `WWW-Authenticate` and `Proxy-Authenticate` (the challenge) carry no secret and are stored as they are.
- Audio (RTP) and audio quality reports (RTCP) are not stored. Decision (2026-11-22): audio recording is out of scope.
- Messages contain the calling and called numbers. The collector's port and ClickHouse are open only on the private network or on 127.0.0.1.

**Retention.** 7 days by default; changed with `TRACE_KEEP_DAYS` in `.env` (the collector sets the table's TTL accordingly when it starts; ClickHouse deletes expired days in the background). The table is partitioned by day and an expired day is dropped as a whole.

**Disk.** In the lab load test a call held 12 messages and about 4.8 KB of raw text. The size on disk depends on compression: the load test's identical messages come down to 0.3 KB per call; with real traffic several times that should be expected, but it has **not been measured**. Rough estimate: 1,000 calls per second makes 86 million calls and 400 GB of raw text a day; for 7 days set aside hundreds of GB depending on the compression ratio, and in the first days look at the real size through `system.parts` and adjust the period. If there is no room, shorten the period or turn storage off.

**Effect on calls.** The collector is not on the call path. The copy is sent over UDP without waiting for a reply: while the collector or ClickHouse is down, calls continue and only the traces of that period are missing. When ClickHouse slows down, the collector holds at most 100,000 messages in memory and drops the rest, counting them (`softswitch_trace_dropped_total`, alert `CallTracesDropped`). In the lab at 200 CPS / 2,000 concurrent calls (~2,400 messages per second) the collector wrote all 71,953 messages it received and the trace of all 6,000 calls was complete; decision time p99 was 3.1 ms (4.9 ms the same day without the collector: the difference is within measurement noise). After refused calls started being traced too (T-26), at the same load the collector wrote all of 71,861 messages and dropped none (the load test's calls are always accepted; each rejection adds 3 messages). It has not been tried at the target load (1,000 CPS).

**Turning it off.** Write `TRACE_TARGET=` (empty) in `.env` and run `docker compose up -d opensips`: the core sends no copy. The `trace-collector` service keeps running but stays idle.

**Sending to your own Homer.** If `HEP_TARGET=<address>:<port>` is given, the same copy also goes to that address in addition to our own collector (such as [Homer](https://sipcapture.org); not part of this install). This copy is not masked: it leaves the SIP core as it is and contains the digest response. In the lab, sending is tried with a simple receiver; it has not been tried with a real Homer install. The copies leave from a separate socket of the SIP core (`HEP_IP:HEP_PORT`, 9061); when all collectors are on the server this socket is on 127.0.0.1, and when there is an external `HEP_TARGET` it is on `SIP_IP`: give the internal address that reaches that collector with `HEP_IP`. HEP packets arriving at the socket are not processed and are not counted as calls (T-51; [`docs/en/09-installation.md`](09-installation.md), "Ports that must not be open to the outside").

### Reading a trace

- Panel: the "SIP flow" section in the detail that opens when you click a call in the CDRs; search by Call-ID on the "Tools → Call trace" page. Endpoints are drawn as columns and messages as arrows in time order; clicking a message shows its raw text.
- API: `GET /v1/trace?call_id=…` and `GET /v1/trace/pcap?call_id=…` (`docs/en/06-api.md`).
- Access follows the same rule as CDRs: an account sees the trace of a call only if the call has a record belonging to one of its own clients; anyone else's returns "not found". The trace of a call that has no record yet (in progress) can be seen only by an account that sees all clients.

### The pcap file

The "Download pcap" button and `/v1/trace/pcap` generate, from the stored messages, a capture file that Wireshark and tcpdump open (classic pcap, Ethernet). In Wireshark, "Telephony → VoIP Calls → Flow Sequence" draws the flow.

**This file is not real packets captured from the wire.** For each message an Ethernet, IP and UDP or TCP header is generated from what is known (addresses, ports, time):

- MAC addresses are made up; the IP ID, TTL and window values are fixed.
- Messages carried over TCP and TLS are written as plain TCP: there is no handshake, sequence numbers start at 1, and long messages are split into 1,400-byte segments. TLS messages appear **decrypted**; there are no TLS records or certificates in the file (Wireshark decodes this plain text on port 5061 as SIP).
- Retransmissions are present only as far as the SIP core handled and traced them; losses, fragmentation and delays at network level are not visible. The time is the moment the core processed the message.
- To troubleshoot a network problem (packet loss, MTU, TLS handshake) you need `tcpdump` on the server; this file answers the question "who said what to whom".

## System page in the panel

The "Tools → System resources" page in the panel works even without Prometheus: the API process samples the machine itself every five seconds and keeps the last ten minutes in memory (`pkg/sysinfo`, `GET /v1/system`).

- **Machine:** CPU usage, one-minute load, memory, incoming and outgoing traffic on the server's own network interfaces, disks. The figures are read from the kernel's counters under `/proc`; even when read from inside a container they describe the whole machine. The network figure shows the server's interfaces when the API uses the host network (production layout), and only the container's on a private network (smoke test).
- **Services:** the API knows PostgreSQL, the engine and ClickHouse itself; the others are given with `SYSTEM_SERVICES` (`Name=http://address/path, …`; the install defines the SIP core, media and rating). A service counts as running if its address answers with an HTTP code below 500 within two seconds. The addresses are not sent to the panel.
- `SYSTEM_DISKS`: the paths whose disks are shown (comma-separated; default `/`, i.e. the disk holding the container and the Docker data).
- Only the account that operates the system (root) sees the page.

This page is for looking at the current state; for history, thresholds and alerts use Prometheus and the rules above. Redis and NATS are not probed separately: if they cannot be reached, the metrics and alerts of the engine and of rating show it.
