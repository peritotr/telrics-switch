<!-- English version of docs/ARCHITECTURE.md; the Turkish document is the source and both are updated together. -->
# Architecture

> English version of [`docs/ARCHITECTURE.md`](../ARCHITECTURE.md) (Turkish), which is the authoritative text; the two are updated together.

This document describes the system as it is today: the components, the interfaces between them, the call flows, the concurrency model, and the error and timeout strategy. The aim is that someone who reads only this document, the decision records under `docs/decisions/` (Turkish) and `docs/TASKS.md` (Turkish) can write the right code in the right place.

- Capacity calculation and hardware plan: [`docs/en/02-capacity-plan.md`](02-capacity-plan.md).
- Field-by-field definition of the interfaces: `docs/08-cagri-arayuzu.md` (Turkish; engine ↔ SIP core), [`docs/en/06-api.md`](06-api.md) (management API).
- If this document and the code disagree, the code is right; report the disagreement and fix the document.
- Points that have not been decided are marked **OPEN QUESTION**; all of them are collected in the list at the end. Do not finish work that touches an OPEN QUESTION on your own assumption; ask.

## 1. Scope and basic principle

The product is a **Class 4** SIP switch: it identifies a call arriving from a client trunk, authorises it, prices it, routes it to a vendor and charges for it. The target is 20,000 concurrent calls and 1,000 CPS (so far only 200 CPS / 2,000 calls have been measured, in Docker on a Mac).

**We do not write the SIP stack** (ADR-0001). SIP parsing and the transaction and dialog layer are OpenSIPS; media relay is rtpengine. Our code is the Go services around them. So what is described under the headings "SIP stack" and "transaction/dialog layer" is an OpenSIPS **script** (`sip/opensips/opensips.cfg`), not Go code. The only place on the Go side that produces a SIP message is the `OPTIONS` request the engine sends to ping vendors (`pkg/engine/health.go`); registration (REGISTER) towards vendors goes from the SIP core, with OpenSIPS's `uac_registrant` module (ADR-0035).

The second basic principle: **business logic does not live in the SIP script** (ADR-0002). The script asks, applies the answer and reports what happened. Who may call, where the call goes, how long it may last and whether a registration is accepted are the engine's decisions.

## 2. Components

```
 Client trunks ──SIP/UDP,TCP,TLS──▶ ┌──────────────┐ ──SIP──▶ Vendors
                                    │   SIP core   │
                ┌──── HTTP+JSON ───▶│  (OpenSIPS)  │◀── MI (UDP, JSON-RPC) ──┐
                │    (v1, Bearer)   └──┬────────┬──┘                         │
                │                      │ng      │HEP                         │
         ┌──────┴───────┐       ┌──────▼────┐ ┌─▼───────────────┐            │
         │ route-engine │       │ rtpengine │ │ trace-collector │            │
         │    (Go)      │───────┼───────────┼─┼─────────────────┼────────────┘
         └─┬────┬─────┬─┘       └───────────┘ └──────┬──────────┘
   Redis ◀─┘    │     └─▶ NATS JetStream ──▶ rater ──┼─▶ ClickHouse
 (limits, reservations,  (CDR events)       (Go)     │   (CDR copy, sip_messages)
  call state, registrations)                  │      │
                │ snapshot (LISTEN/NOTIFY)    ▼      │
                └────────────────────────▶ PostgreSQL ◀── api (Go, REST) ◀── panel (React)
                                          (configuration, balances, CDR system of record)
```

| Component | Code | Job | Holds state? |
|---|---|---|---|
| **SIP core** | `sip/opensips/opensips.cfg`, `deploy/images/opensips/` | SIP parsing, transactions and dialogs, topology hiding, Digest challenge, route attempts and moving to the next one, NAT, TLS, offer/answer to rtpengine, edge protection, registration towards vendors (`uac_registrant`; `registrants.sh` fetches the list from the engine) | Transactions and dialogs (in memory); local cache (`known:`, `ban:`); vendor registration table (`/run/opensips/regdb`, tmpfs, with passwords) |
| **route-engine** (the engine) | `cmd/route-engine`, `pkg/engine`, `pkg/routing`, `pkg/rating` | Call decision, limits, balance and bundle reservation, registrar, vendor health and certificate checks, vendor registration list and status, live counters, CDR generation | In memory only the snapshot and counters; persistent state in Redis |
| **rtpengine** | `media/rtpengine/rtpengine.conf` | RTP/SRTP relay, SDP rewriting, codec filtering, source learning behind NAT | Sessions (in memory) |
| **rater** | `cmd/rater`, `pkg/rater`, `pkg/api` (`IngestCDRs`) | Rates the CDR, charges the balance and the bundle, writes to PostgreSQL, copies to ClickHouse, archives old records | No (PostgreSQL) |
| **api** | `cmd/api`, `pkg/api`, `pkg/store` | REST management API, identity and roles, audit log, reports, e-mail queue, scheduled jobs | No (PostgreSQL) |
| **panel** | `web/` | Admin interface (React + TypeScript + Vite) | Session token in memory only |
| **trace-collector** | `cmd/trace-collector`, `pkg/trace` | Writes the copy of the signalling received over HEP to ClickHouse | No |
| PostgreSQL | `db/migrations/postgres/` | Configuration, balance ledger, CDR system of record, audit log | — |
| Redis | — | Concurrent-call and CPS counters, reservation ledgers, state of calls in progress, registrations (`reg:<trunk>`) | — |
| NATS JetStream | — | `CDR` stream, `cdr.v1` subject | — |
| ClickHouse | `db/migrations/clickhouse/` | CDR copy (search, reports), `sip_messages` | — |

Go packages and their responsibilities:

| Package | Responsibility | On the call path? |
|---|---|---|
| `pkg/routing` | Snapshot (`Snapshot`) data structures and the pure decision: trunk matching, prefix matching, LCR/priority/weight, CLI rules, firewall verdict. Does no I/O | Yes |
| `pkg/rating` | Rate selection (`Tidy`, `Select`), amount (`Rate.AmountAfter`), currency conversion (`Convert`), period (`Period`). The **single** code for the engine and the rater | Yes |
| `pkg/engine` | HTTP endpoints, Redis scripts, reservation timer, registration, health/certificate checks, live counters, writing CDRs to NATS | Yes |
| `pkg/fx` | Reading published reference exchange rates (the European Central Bank file) and crossing them into the account's currency with integer arithmetic (T-20, ADR-0036). The API process writes the rates (`pkg/api/fx.go`) | No |
| `pkg/sipctl` | Driver for the commands the engine gives the SIP core (ending a call, lifting a block, vendor registration status `reg_list`). The only OpenSIPS-specific Go code | No (background) |
| `pkg/api` | Resources (generic CRUD), special operations, loading the snapshot from PostgreSQL (`LoadSnapshot`), storing CDRs (`IngestCDRs`), extension points | No |
| `pkg/rater` | NATS consumer, ClickHouse export, archive | No |
| `pkg/store` | Connection pool, schema steps (`MigrateFS`), `NotifyConfig` | No |
| `pkg/metrics`, `pkg/trace`, `pkg/sysinfo`, `pkg/mail`, `pkg/totp`, `pkg/numplan`, `pkg/clickhouse` | Helpers | `metrics` yes (a single atomic add) |

## 3. Interfaces

| # | From → to | Transport | Contract | Identity |
|---|---|---|---|---|
| A1 | SIP core → engine | HTTP + JSON, `"v": 1` | `docs/08-cagri-arayuzu.md` (Turkish): `/v1/authorize`, `/v1/events`, `/v1/source`, `/v1/credentials`, `/v1/register`, `/v1/ban`; also `GET /v1/vendor-registrations` for `registrants.sh` next to the SIP core (the list of registrations towards vendors, with passwords) | `Authorization: Bearer <ENGINE_TOKEN>` |
| A2 | Engine → SIP core | OpenSIPS MI (`mi_datagram`, UDP, JSON-RPC), through `pkg/sipctl` | Only two commands and one read: end the call (`dlg_end_dlg`), forget the block (`pike_rm` + `cache_remove`); read the status of registrations towards vendors (`reg_list`, ADR-0035) | None (private network assumed) |
| A3 | SIP core → rtpengine | ng protocol (UDP) | `rtpengine_offer` / `_answer` / `_delete` flags (for transcoding `codec-mask-all`, `codec-transcode-…`; §9) | None |
| A4 | Engine → Redis | Redis protocol, Lua scripts | Key layout §6.3 | None |
| A5 | Engine → NATS | JetStream publish, `cdr.v1` | `engine.CDR` JSON; `api.CDR` rejects unknown fields | None |
| A6 | Engine ← PostgreSQL | `LoadSnapshot` + `LISTEN softswitch_config` | The snapshot is loaded; for rates only what changed (`rate_changes`, ADR-0048) | Password |
| A7 | api → engine | HTTP + JSON (the same listener as A1): `/v1/live`, `/v1/calls`, `/v1/hangup`, `/v1/registrations`, `/v1/unregister`, `/v1/bans`, `/v1/unban`, `/v1/peers`, `/v1/probe`, `/v1/stats`, `/v1/reload` | `docs/08-cagri-arayuzu.md` (Turkish) | Bearer |
| A8 | Panel / external client → api | REST + JSON, `GET /v1/openapi.json` | [`docs/en/06-api.md`](06-api.md) | API key or `tss_` session token |
| A9 | SIP core → trace-collector | HEP v3 (UDP) | `pkg/trace` | None |
| A10 | Enterprise → core | Go import | `pkg/api` extension points, `engine.Config` (ADR-0008) | — |
| A11 | rtpengine → SIP core | XML-RPC (HTTP), `/RPC2` on the `SIP_METRICS` listener (`mi_xmlrpc_ng`) | Only `teardown <Call-ID>` (a command of the rtpengine module, a wrapper around `dlg_end_dlg`): end a call whose media has stopped (ADR-0027) | None (private network assumed; every MI command is open on the same port, S-14) |

Interface rules (all binding):

1. **A1 is versioned and independent of the SIP server.** No OpenSIPS-specific concept (AVP name, flag, module name, a variable such as `$du`) enters the body. A new need is met with an optional field; a change that alters the meaning of an existing field or adds a mandatory field increments `v`. The engine does not reject an unknown field, it ignores it: adding an optional field therefore does not constrain the upgrade order. Examples: `cause` in `events` (ADR-0039; stayed in v1), `transcode` in `route` (ADR-0041).
2. On A1 the response is always HTTP 200; the decision is in the body. A non-200 response means "the engine could not answer" (§7).
3. The engine stamps times with its own clock; no time is carried over the interface.
4. Every event may arrive more than once; the engine must handle a repeat harmlessly.
5. In the script, `route(engine_token)` is called before every `rest_post` (`rest_append_hf` only applies to the next request).
6. A2 is kept one-way and narrow: the engine says "end this call" (and asks for the status of vendor registrations); how that is said and how the answer is read is the job of the `pkg/sipctl` driver.
7. In the management API a new endpoint is registered with `s.Handle` or `s.HandlePublic` (OpenAPI is generated from there); `s.mux` is not used directly.

## 4. SIP core: transaction and dialog layer

Structure of the script (`sip/opensips/opensips.cfg`):

| Block | When it runs | What it does |
|---|---|---|
| `route` | Every request | Max-Forwards; NAT test and `force_rport`; separating in-dialog requests (403 for in-dialog REFER, §5.6); edge protection; branching by method |
| `route[signin]` | INVITE/REGISTER carrying `Authorization` | Fetches the digest from the engine (`/v1/credentials`), checks the password |
| `route[register]` | REGISTER | Asks the engine (`/v1/register`), writes the response |
| `route[authorized]` | When the `authorize` response arrives (asynchronous resume) | Applies the decision: rejection or dialog set-up, caller ID, first route |
| `route[take]` | For each route | Takes the route the engine gave into transaction variables (destination, identity, codecs, timeouts) |
| `route[attempt]` | Each attempt | Sends the request; if it cannot be sent (503) or there is no codec (488), reports `failed` and takes the next route; at most 16 rounds |
| `branch_route[out]` | In each attempt's own request | Only the changes that belong to that attempt: SDP offer, caller ID, identity header |
| `onreply_route[media]` | Vendor response | SDP answer; `ringing` event on the first 180/183, `answered` event and ACK wait on 200 (dialog timer 32 s) |
| `failure_route[next]` | When an attempt gives a final error | One Digest response to 401/407; `failed` event; a new attempt if the engine gave a route, otherwise deletes the media session |
| `route[hop]`, `local_route` | In-dialog requests; the SIP core's own BYE (engine, max duration, ACK wait, the relay's `teardown`) | Reaches a party behind NAT at its real address; on its own BYE deletes the relay session and sends the `ended` event |

Modules used and their roles: `tm` (transaction state, retransmission, timers), `dialog` (call state, max duration, dialog values), `topology_hiding` (hiding Via/Record-Route/Contact; `rr` is loaded only because `uac` requires it, no Record-Route is added), `uac` (From replacement, Digest towards vendors), `auth` (Digest challenge to callers), `pike` (flood), `cachedb_local` (source cache), `rest_client` (the engine), `rtpengine`, `tls_mgm` + `db_text` (certificates), `nathelper` (only `nat_uac_test`), `mi_datagram` (A2), `mi_xmlrpc_ng` (A11: the relay's `teardown`), `prometheus`, `statistics` (the script's own counters: `refer_refused`, §5.6; `media_refused`, §9); the lines needed for the HEP trace copy (A9) are added by `deploy/images/opensips/run.sh` when the container starts (`TRACE_TARGET`, `HEP_TARGET`; the sending socket is on 127.0.0.1 or on `SIP_IP`, `HEP_IP`; incoming HEP is not processed and `sipcapture` is not loaded: if it were, a packet would enter the script with the source address it carries, T-51).

**Forbidden in this OpenSIPS version (3.6.10):** any command that changes the request before `async()` (`consume_credentials()`, `fix_nated_contact()`, `remove_hf()`, `append_hf()`, `subst*()` …). If the request has to be changed, do it in the resume route (`route[authorized]`). The script does not use `proxy_challenge()` or `fix_nated_contact()` (ADR-0014).

Values stored in the dialog (`$dlg_val`): `caller_hop`, `callee_hop` (the real address of a party behind NAT), `codecs` (codec flags to apply to new in-dialog offers), `limit` and `ack_wait` (the call's max duration while the ACK after the answer is awaited; §5.4), `media_gone` (the relay session was deleted on the SIP core's own BYEs; §5.3), `offerless` (the initial INVITE had no offer; the profile to offer to the caller), `offerless_af` (that offer's address family: the family the INVITE arrived on; ADR-0047), `late` (the answer to the offer made in the 200 is awaited in the ACK), `late200` and `gone` (the 200 of an offerless INVITE is being processed in the relay, or the relay did not take it), `reoffer` (whether the last in-dialog INVITE carried an offer, and if not, which direction the offer in the 200 goes; §5.2, ADR-0043), `rung` (the branch index of the last attempt reported to the engine as `ringing`; only the first 18x of each attempt is reported, T-46). Information the reply route needs is kept in the dialog, not in AVPs: the reply route does not see the transaction's AVPs (the `tm` default `onreply_avp_mode` is 0). There is no other persistent place for call state; the script uses no database.

## 5. Call flows

In the diagrams `M` = engine, `R` = rtpengine. For the interface fields see `docs/08-cagri-arayuzu.md` (Turkish).

### 5.1 INVITE (call set-up)

```
Caller                 SIP core                      M            R        Vendor
  │ INVITE ───────────────▶│                         │            │           │
  │                        │ edge: known:/ban: cache, pike                     │
  │                        │ (if not known) POST /v1/source ─▶│                │
  │ ◀── 401 (unknown source; Digest challenge)       │                        │
  │ INVITE + Authorization ▶│ POST /v1/credentials ──▶│                        │
  │                        │ async POST /v1/authorize ▶│ decision from snapshot│
  │                        │                         │ + Redis: limits, reservation
  │                        │◀── allowed, route, max_duration_sec               │
  │ ◀── 4xx/5xx (reject)   │ or: set up dialog, hide topology                  │
  │                        │ offer ───────────────────────────▶│               │
  │                        │ INVITE ──────────────────────────────────────────▶│
  │ ◀── 180/183 ───────────│◀──────────────────────────────────────── 180/183 │
  │                        │ POST /v1/events ringing ─▶│ (PDD measurement)     │
  │                        │◀──────────────────────────────────────── 200 OK  │
  │                        │ answer ──────────────────────────▶│               │
  │                        │ POST /v1/events answered ▶│                       │
  │ ◀── 200 OK ────────────│                         │                         │
  │ ACK ──────────────────▶│ ACK ─────────────────────────────────────────────▶│
```

The engine's decision order (`pkg/engine/engine.go`, `pkg/routing/engine.go`):

1. Firewall verdict (`deny` → `source_blocked`).
2. Trunk matching: by name if `auth_user` is set, otherwise by source address (narrowest network; on a tie, longest tech prefix). If no trunk is found, `unknown source`; but if `auth_user` is empty and `Config.Incoming` is set, the hook is asked, and if it takes the call on behalf of a client the decision continues as that client's trunkless call (ADR-0032; steps 3–6 with the client's rules, without a trunk rule; `source_known` stays false).
3. Trunk rule (`security`: `any`/`tls`/`cert`), account status.
4. The trunk's number format; CLI rules (`cli_rules`): their own rules are applied separately to the number in From and (field `asserted`) to the number in `P-Asserted-Identity`; a caller without a number gets 433 under a "reject callers without a number" rule. If `privacy` contains `id`, the identity header is removed on every route of the call (`identity: "remove"`, which takes precedence over the connection's `pai_mode` setting).
5. Sell rate (`rating.Select` for the moment the call starts), bundle.
6. Routing plan → routes: steps (priority; in LCR, price), within a step a weighted order with `PickFor(call_id)`; routes outside their time window, whose certificate was rejected, that do not answer pings, or that share no codec are removed.
7. `Config.Adjust` (if set; Enterprise).
8. Admission in Redis with a single script: client/trunk/vendor limits, CPS, balance and bundle reservation, writing the call state.
9. Response: the first route and `max_duration_sec`.

**Failover to the next route.** If an attempt gives a final error or times out, the script sends the `failed` event; the engine returns either the next route or an empty response. The engine decides whether to fail over (default: 404, 480, 484, 486, 487 and 6xx end the call; the connection's `failover_mode` setting changes this). Every attempt is a separate CDR leg (`leg` 1, 2, 3…).

**Offerless INVITE** (without SDP; ADR-0043): the offer is in the vendor's 200 and passes through the relay in the response; the caller's answer is in the ACK (§5.2).

**If the relay does not take the offer** (the `offer` step; no response or no ports left) the attempt is not sent to the vendor: the script reports the `ended` event to the engine with 503 and `cause: "media"`, and gives the caller 503; since `failed` is not sent, the next route is not tried (ADR-0030). The engine writes the record with the selected connection, but does not count the attempt in that connection's counters or quality (`OnLeg`: `NotSent`; ADR-0039, §8.2, §9).

### 5.2 re-INVITE (new offer within the call)

```
Party A            SIP core                          R            Party B
  │ INVITE (to-tag) ─▶│ topology_hiding_match, route(hop)          │
  │                   │ offer (with the dialog's codec flags) ──▶│  │
  │                   │ INVITE ────────────────────────────────────▶│
  │                   │◀─────────────────────────────────── 200 OK │
  │                   │ answer ─────────────────────────────────▶│  │
  │ ◀── 200 OK ───────│                                            │
```

- **The engine is not told.** The call's duration, reservation and record are not affected by a re-INVITE; the dialog timer (max duration) is not reset either: the duration keeps counting from the answer.
- The same path in both directions: `route[hop]` (a party behind NAT at its real address), `rtpengine_offer` (with the dialog's codec flags; `SIP-source-address` for an offerer behind NAT), in the response `onreply_route[reinvite]` → `route(answer)`. No flag is given for transport (RTP/SAVP) or ICE: rtpengine keeps what was agreed in each leg's first negotiation (ICE lines are removed from the new offer too; an SRTP leg stays SRTP, verified directly against the relay).
- Because the codec list is stored in the dialog, it is applied to the new offer whichever side it comes from.
- There is no special handling for hold (`sendonly`/`inactive`), T.38 or session refresh; direction lines pass unchanged. An offer switching to T.38 (`m=image … udptl t38`) passes through the relay with its format and attributes; codec-list flags are not applied to a non-RTP stream (ADR-0031). In-dialog SIP INFO (DTMF) is forwarded unchanged. Scenarios: `make test-routing`, "fax" (TASKS: T-40).
- A rejected new offer (e.g. 488) does not drop the call (RFC 3261 §14.1); the ACK for the rejection is hop by hop and is absorbed by `tm`.
- **Offerless INVITE** (offer in the 200, answer in the ACK; RFC 3261 §13.2.1, §14.1, RFC 3264 §5–6; ADR-0043): on the initial INVITE the offer in the 200 (and in an SDP-carrying provisional response) passes through the relay with `rtpengine_offer` in `onreply_route[media]`, and within the call in `onreply_route[reinvite]` (codec flags according to the direction the offer goes, the NAT flag according to the response's `Contact`); the answer in the ACK with `rtpengine_answer` in `route[ack]`. On the initial offer, the profile sent to the caller is `RTP/SAVP` if the caller's signalling is TLS, otherwise `RTP/AVP` (the rule for the side the switch makes the offer to, ADR-0015); the vendor leg follows its own offer. On an ACK that carries no answer the call is closed with a BYE to both sides; if the relay does not take the offer in the 200, the 200 is forwarded without SDP, the call is closed and `ended 503` (`cause: "media"`) goes to the engine (§9). Scenarios: `make test-routing`, "late" (TASKS: T-43).
- Scenarios: `make test-routing`, "reinvite" (TASKS: T-02; `docs/08` (Turkish), "Çağrı içi yeni teklif", new offer within the call).
- **Session timers (ADR-0027, formerly S-1):** RFC 4028 headers (`Supported`/`Require: timer`, `Session-Expires`, `Min-SE`) pass end to end unchanged; the SIP core does not request refreshes itself and applies no per-leg session timer. A dead call is detected by the media relay (§5.3, §9): if no media at all arrives for 60 seconds, the call is closed towards both sides. Scenarios: `make test-routing`, "deadcall" (TASKS: T-38).

### 5.3 BYE

There are four sources; in all of them the engine receives the same `ended` event, and the event may arrive more than once.

| Source | Flow |
|---|---|
| One of the parties | BYE → `topology_hiding_match` → `route(hop)` → `rtpengine_delete` → `POST /v1/events ended` → forwarded to the other party |
| Engine (reservation ran out, `POST /v1/hangup`) | Engine → `pkg/sipctl` → MI `dlg_end_dlg` → the SIP core generates a BYE to both parties → `local_route`: `route(hop)` + `ended` |
| SIP core (`max_duration_sec` reached, or no ACK within 32 s of the answer) | Dialog timeout → BYE to both parties → `local_route` |
| Media relay (no media at all for `RTP_TIMEOUT`; ADR-0027) | rtpengine → XML-RPC `teardown <Call-ID>` (A11) → SIP core `dlg_end_dlg` → BYE to both parties → `local_route` |

On the SIP core's own BYEs (`local_route`) the relay's session is also deleted, once (`$dlg_val(media_gone)`); otherwise the session would be left to the relay's timeout and the relay would send a `teardown` a minute later for a call that has already ended (harmless, but noise).

On `ended` the engine removes the call from Redis, lowers the reserved amount to the call's real charge (it does not release it; §6.4) and writes the CDR leg to NATS.

### 5.4 CANCEL

```
Caller             SIP core                         M             Vendor
  │ CANCEL ──────────▶│ t_check_trans → t_relay                       │
  │ ◀── 200 (CANCEL)  │ CANCEL ──────────────────────────────────────▶│
  │                   │◀────────────────────────────────────── 487    │
  │                   │ failure_route: t_was_cancelled → code 487     │
  │                   │ POST /v1/events failed 487 ─▶│ call ends      │
  │ ◀── 487 ──────────│ rtpengine_delete                              │
```

- 487 never fails over to the next route under any rule (even if the connection is `always`).
- A CANCEL with no matching transaction is left unanswered.
- CANCEL arriving while the `authorize` response is awaited: `tm` answers the CANCEL with 200. When the engine's decision arrives, `route[authorized]` sees that the call was cancelled (`t_was_cancelled`), reports `failed 487` for the route the engine gave (the engine has accepted the call and is holding the counter and the reservation), gives the caller 487 and sends nothing to the vendor. The same check is in `route[attempt]`: an attempt that could not be sent counts as 487, not 503, if the caller gave up in the meantime. (Before this fix the attempt was reported with 503: the record was written with the wrong code, counted as a network error, and on a call with a backup route the engine returned the next route.)
- A CANCEL arriving after the answer (200) has no effect (RFC 3261 §9.1): the call continues and the caller has to send ACK and BYE.
- Scenarios: `make test-routing`, "cancel" (TASKS: T-01).
- **Answer that gets no ACK (ADR-0026, formerly S-17):** the SIP core closes the call of a caller that does not ACK an answered call (for example a device whose CANCEL raced the 200 and believes it gave up) after 32 seconds (64×T1) with a BYE to both sides; the engine receives `ended`, and the record is written with 200 and the real duration. Implemented with the dialog timer: `onreply_route[media]` lowers the timer to 32 seconds on the 200 and keeps the max duration in `$dlg_val(limit)`; when the ACK arrives, the timer is set back to the remainder of the max duration. A call whose max duration is 32 seconds or less is not touched. Scenarios: `make test-routing`, "ackwait" (TASKS: T-41; `docs/08` (Turkish), "Cevabı onaylamayan arayan", caller that does not acknowledge the answer).

```
Caller             SIP core                         M             Vendor
  │                   │◀──────────────────────────────────── 200 OK   │
  │ ◀── 200 OK ───────│ timer: 32 s; answered ────────▶│              │
  │   (no ACK)        │ … 32 s …                                      │
  │ ◀── BYE ──────────│ dialog timeout ("B") ─────────────── BYE ────▶│
  │                   │ local_route: ended ───────────▶│ call ends    │
```

### 5.5 REGISTER

```
Trunk               SIP core                          M (Redis: reg:<trunk>)
  │ REGISTER ────────▶│ edge protection (a registered address does not become "known")
  │ ◀── 401 ──────────│
  │ REGISTER + Auth ─▶│ POST /v1/credentials ──────▶│
  │                   │ POST /v1/register ─────────▶│ trunk rule, firewall,
  │                   │   (Contact and Expires raw) │ expiry limits, max_contacts
  │ ◀── 200 + Contact │◀── sip_code, contacts, min_expires
```

- The registrar is **in the engine** and lives in Redis; `usrloc`/`registrar` are not used (ADR-0013).
- The engine parses the `Contact` header; the script carries the raw text.
- No keep-alives are sent: a trunk behind NAT or connected over TCP/TLS is given at most `REG_NAT_EXPIRES` (60 s).
- A call to a registered trunk is made only if an extension puts `routing.ToTrunk` in `Adjust` (or among the routes of the `Incoming` response); the engine turns the route into the registration (`next_hop`, `media`), 480 if there is no registration. The target trunk's `max_calls`/`max_cps` values and those of the trunk's client are put on the route for each registration and checked in the admission script like vendor routes (trunk: `cc:t:<trunk>`, `cps:t:<trunk>:<second>`, shared with the calls the trunk makes itself; client: `cc:c:<client>`, `cps:c:<client>:<second>`, shared with the calls the client makes; if one is full, the next route, otherwise 503; ADR-0042, T-45); the codec list goes into the route's `codecs` field, and if there is no codec in common with the caller's trunk the trunk does not count as a route (T-06). A call to a trunk registered over TLS goes only over the connection the registration came from (`next_hop` `;transport=tls`; the SIP core opens no new connection: the `NO_NEW_CONN` branch flag); if the connection has closed, the attempt fails immediately with 503 and the next registration is tried (T-07). A call is counted once in each counter: moving to another registration of the same trunk, or to another trunk of the same client, is not a new call; a call the client makes to its own trunk is checked only on the calling side. **DECISION (ADR-0042)**.

The reverse direction, the switch's registration with a vendor (T-14, ADR-0035):

```
M                      registrants.sh (SIP core container)      SIP core (uac_registrant)           Vendor
│◀── GET /v1/vendor-registrations?version= ──│                          │                              │
│── list (if changed, with passwords) ──────▶│ writes the table ───────▶│ reg_reload                   │
│                                            │                          │── REGISTER ─────────────────▶│
│                                            │                          │◀── 401 ──────────────────────│
│                                            │                          │── REGISTER + Authorization ─▶│
│                                            │                          │◀── 200 (Contact;expires) ────│
│── reg_list (MI, REG_STATUS_SEC) ──────────────────────────────────────▶│ status → GET /v1/peers, metric │
```

- The registration goes from the SIP core's address; the engine sends no SIP message. The script (the OpenSIPS script) does not touch registration at all.
- Refreshed `REG_TIMER_SEC` before expiry; on failure retried `REG_RETRY_SEC` later (fixed interval).

### 5.6 Other methods

| Method | Behaviour |
|---|---|
| OPTIONS (from a known or registered address) | 200 |
| OPTIONS and others (from a stranger) | No response |
| ACK | Forwarded with the dialog if in-dialog, otherwise with the matching transaction |
| In-dialog PRACK, UPDATE, INFO, NOTIFY etc. | Forwarded unchanged if it matches the dialog (PRACK in an early dialog too) |
| In-dialog REFER (from either side) | 403; not forwarded to the other side, the call continues; counted (`opensips_refer_refused`) |
| Everything else outside a dialog (SUBSCRIBE, MESSAGE, REFER…) | 405 |

**DECISION (ADR-0028, formerly S-3), implemented (T-42):** early media (183 with SDP) and 100rel/PRACK pass through unchanged: the SDP in a 183 passes through the relay in `onreply_route[media]` like any response (the caller sees the relay's address), `Require: 100rel`, `RSeq`, `RAck` are unchanged, and PRACK is matched in the early dialog with `topology_hiding_match` and forwarded. An in-dialog REFER is rejected by a fixed rule (it is not a decision; the engine is not asked). SDP in PRACK/UPDATE does not pass through the relay (not tested). A call with silent early media is closed at the media timeout (60 s), even if the connection's ring timeout is longer: **DECISION (ADR-0038)**, §8.1. Details and scenarios: `docs/08` (Turkish), "Erken medya, PRACK ve aktarma" (early media, PRACK and transfer).

## 6. The engine

### 6.1 Snapshot

- The whole configuration (clients, trunks, tariffs, plans, rules, exchange rates, bundles, firewall) is loaded from PostgreSQL in one go and kept as an immutable structure: `atomic.Pointer[routing.Snapshot]`.
- Refresh: immediately when `NOTIFY softswitch_config` arrives (notifications in quick succession are merged), also at the `RELOAD_SEC` (10 s) interval, also via `POST /v1/reload`.
- Rates (`rates`) are read only for the part that changed (**DECISION ADR-0048**, T-30): a trigger in the database notes the tariff and prefix of every rate written, with the transaction ID, in `rate_changes`; the loader reads the notes that the previous load's database snapshot did not see, re-reads those prefixes into a copy of the tariff's map, and carries the maps of unchanged tariffs over into the new snapshot by sharing them. The tariff of a rate whose validity starts or ends, and the tariff of a statement larger than 10,000 rows, is read in full. The rest of the snapshot is complete on every load; every `RELOAD_FULL_SEC` (300 s) the rates are also read from scratch. With 5 million rates a full load takes ~8 s and allocates 2.7 GiB; with nothing changed 10 ms, on a prefix change ~0.25 s.
- Do not change the snapshot in place; build a new one and swap the pointer. In tests, do not copy the snapshot and change a field (the copy carries the old indexes); build it from scratch.
- Any code that changes data outside the API calls `store.NotifyConfig`.

### 6.2 Call-path budget

- Per call: a decision from memory + a **single** Redis script call. No SQL, no files, no other network calls. Exception: on a call with a trunk route, one more Redis read (registrations).
- Decision time p99 < 10 ms. After every change that touches the call path, `make load CPS=200 CC=2000` and `go test -bench Authorize ./pkg/routing`.
- `Adjust` and `Incoming` are on the call path: memory only, no waiting; `Incoming` decides from the snapshot it is given and does not change it.

### 6.3 State in Redis

| Key family | Content |
|---|---|
| Call state | Per `call_id`: client, trunk, remaining routes, attempt count, answer time, reserved amount; kept for 24 hours after the time the call should have ended |
| Calls about to expire | `expiry`: scored by the time the call should end; the engine writes the record of a call past that time (ADR-0029) |
| Concurrent-call sets | Client (`cc:c:<client>`: calls the client makes and those delivered to its trunks), trunk (`cc:t:<trunk>`: calls the trunk makes, if it has a limit, and those delivered to it, always; both directions in one counter, the per-second counter `cps:t:<trunk>:<second>` is shared too; **DECISION (ADR-0042)**; the engine merges old `cc:s:` sets into `cc:t:` at start-up), per connection; transcoded calls (`cc:x`, limit `TRANSCODE_MAX`; ADR-0041); members are scored by the time they should end (a call whose end is never reported drops out by itself) |
| CPS counters | `…:<second>` |
| Reservation ledgers | `hold:` / `held:` / `heldsum:` + ledger name; the ledger is a client ID or `engine.BundleLedger(bundle, period)`; the `holders` set |
| Registrations | `reg:<trunk>` |

Admission is a single script per call (`admitScript`): system, country, client and trunk counters, reservation ledgers and candidate routes (vendor connections and trunk registrations to deliver to; each with a set, a CPS counter, a transcoding set and three limits — the transcoding limit of a route that is not transcoded is -1) in the same call; keys are positional, arguments are counted from the end (layout in `pkg/engine/calls.go`, in the comment above the script). Redis scripts do not calculate prices; Go calculates, the script applies atomically. The keys assume a single Redis node (TASKS: T-32).

### 6.4 Reservation

Details: `docs/08-cagri-arayuzu.md` (Turkish), "Bakiye rezervasyonu" (balance reservation). In short: on admission, an amount for `RESERVE_SEC` (60) seconds is reserved; 10 seconds before it runs out, one more step; if the money is not enough, the engine ends the call 1 second before the last second it can pay for; when the call ends, the reserved amount is lowered to the real charge and stays reserved until the rater reports the charge (at most 24 hours). The final amount is always set by the rater.

## 7. Concurrency model

| Component | Model | Shared state and its protection |
|---|---|---|
| SIP core | Multi-process (worker processes), shared memory; OpenSIPS manages transaction and dialog locks | The `authorize` question is asked with `async()`: the worker does not wait, `route[authorized]` runs when the answer arrives. The other questions (`source`, `credentials`, `register`, `events`, `ban`) are synchronous and hold the worker for at most `curl_timeout` |
| Engine | A goroutine per request (`net/http`) | Snapshot, certificate and health verdicts: `atomic.Pointer` (lock-free reads). Limits/reservations/call state: Redis Lua script (atomic). Live counters, block list, registered-address index: their own locks (`sync.Mutex`/`RWMutex`); metrics: a single atomic add |
| The engine's background jobs | Separate goroutines: snapshot refresh, `LISTEN`, reservation extension/closing and the timer for expired calls, certificate checks, vendor pings, reading vendor registration status, the CDR journal's writer, reader and NATS senders | They never make the call path wait; they publish their results through an atomic pointer. The call path only puts a CDR into the in-memory queue; a single writer writes to disk, and no file operation happens under the journal's lock |
| rater | A single durable consumer (`rater`), batch pulls, explicit acknowledgement (`AckWait` 30 s) | Uniqueness in the database: `(call_id, leg)`; bundle counter with a row lock |
| api | A goroutine per request; every write in a single transaction (record + audit log + e-mail queue if any) | Scheduled jobs (alert scan, report scheduler) are a single goroutine in the process |
| trace-collector | Receiver + bounded queue + batch writer | If the queue is full it **drops and counts**; it never makes the sender wait |

Rules:

- No I/O under a lock on the call path.
- Events for the same call may come from different workers, out of order and repeated; correctness relies on the atomicity of the Redis script and on events being safe to process again, not on ordering in the engine's memory.
- Several engine nodes can share the same Redis, but each keeps its own snapshot, live counters and health verdicts (TASKS: T-31).
- With `metrics`, a series is registered once per process (a second registration panics).

## 8. Error and timeout strategy

Principle: **every dependency on the call path has a defined timeout and a defined "unreachable" behaviour; no call waits indefinitely, and no error silently means "allow"** (the single deliberate exception: the engine answers everyone when `ENGINE_TOKEN` is empty).

### 8.1 Timers

| Timer | Value | Where | On expiry |
|---|---|---|---|
| First response from the vendor (`fr_timeout`) | 5 s | `tm` | Attempt fails with 408; next route |
| Final response (`fr_inv_timeout`) | 30 s; the connection's `ring_timeout_sec` if set | `tm` | CANCEL to the vendor, attempt 408 |
| Max duration of the call | `max_duration_sec` (at most 7200 s) | `dialog` | BYE to both parties |
| ACK wait after the answer | 32 s (64×T1; fixed in the script), or the max duration if shorter | `dialog` (set on the 200, returns to the remainder of the max duration on the ACK) | BYE to both parties; record 200 with the real duration (ADR-0026) |
| Silent call (media timeout) | `RTP_TIMEOUT`, 60 s; on hold (one side `sendonly`/`recvonly`/`inactive`) `RTP_HOLD_TIMEOUT`, 3600 s | rtpengine (`timeout`, `silent-timeout`) | The relay deletes the session, `teardown` to the SIP core; BYE to both parties; record 200 with the real duration (ADR-0027). Also applies to a ringing call with early media (183 with SDP): CANCEL to the vendor, 487 to the caller, record 487; even if the connection's `ring_timeout_sec` is greater than 60 (**DECISION, ADR-0038**; stated in the setting's description in the panel) |
| Session timer (RFC 4028) | Not applied; headers pass end to end | — | The parties close it themselves (ADR-0027) |
| Connecting to the engine / total | 1 s / 2 s | `rest_client` | §8.2 |
| Media relay response | 1 s, 5 attempts (the module's default) | `rtpengine` module | The relay is set aside, the call gets 503 (§8.2); it is pinged again in the background every 10 s (`rtpengine_disable_tout`) |
| Reservation step / extension margin | 60 s / 10 s before the end | Engine | If there is no money the call is ended |
| Reservation whose charge is never reported | 24 hours | Redis | The amount is released |
| Known-source cache | 300 s | SIP core | Asked again |
| Block cache (flood / firewall) | 120 s / 60 s | SIP core | The block is lifted |
| Flood threshold | 30 requests in 2 s (unknown source) | `pike` | Block |
| Snapshot refresh | 10 s (+ notification); all rates every `RELOAD_FULL_SEC` (300 s) | Engine | — |
| Vendor ping | 10–3600 s per connection; a ping takes at most 3 s; two lost → "not answering" | Engine | Routes are removed from the decision |
| Registration with a vendor | Refreshed `REG_TIMER_SEC` (30) before expiry; on failure retried `REG_RETRY_SEC` (60) later (fixed interval) | SIP core (`uac_registrant`) | Status "failing", metric and alert; calls are not affected |
| Certificate check | 60 s | Engine | Routes of a rejected connection are removed |
| Registration expiry | 60–3600 s; at most 60 s over NAT/TCP/TLS | Engine | The registration drops |
| Session token / sign-in lock | 12 hours / 15 min after 5 failures | api | — |

### 8.2 When a dependency is unreachable

| Situation | Behaviour |
|---|---|
| The engine does not answer or answers non-200 (`authorize`) | The call is rejected with 503 (fail closed) |
| The engine does not answer (`source`) | The request is left unanswered |
| The engine does not answer (`events failed`) | Treated as "no route"; the call ends |
| The engine does not answer (`events answered`/`ended`), or the SIP core restarted | The call continues/ends; the engine's state drops out by its end score in the set. When it drops, the engine writes the record itself: if answered, 200 and end = answer time (duration and charge 0), otherwise 408; the reserved amount is released (ADR-0029, content ADR-0040: charging by the reserved time could charge a 30-second call for up to two hours; it is reconciled by hand with the vendor's invoice; `docs/08` (Turkish), "Bitişi bildirilmeyen çağrı", call whose end is never reported). Until then (max duration + 300 s) the call stays counted and its money reserved |
| The secret does not match (401) | Same as above; metric and alert: `EngineRefusingCallers` |
| Redis unreachable | The engine returns 503 → the call is rejected; calls in progress cannot be extended or ended and may last until `max_duration_sec` (roadmap D3; left outside the scope of ADR-0029 and T-33) |
| PostgreSQL unreachable | The engine keeps working with the last snapshot; api and rater stop; CDRs wait in NATS |
| NATS unreachable | The engine writes records to the journal on its own disk (`CDR_JOURNAL_DIR`, at most `CDR_JOURNAL_MAX_MB`) and retries with an increasing interval (50 ms → 5 s) until NATS accepts them; if the engine restarts or crashes while NATS is down, it resends the records in the journal at start-up (ADR-0029; `pkg/engine/journal.go`, `sink.go`; `docs/08` (Turkish), "Çağrı kayıtlarının günlüğü", the CDR journal). The call path does not write to disk: it puts the record in the in-memory queue (200,000), and the background writer writes in batches and calls `fsync`. The engine starts even without NATS. If the journal is full or the disk does not write, records wait in memory (lost if the engine stops in the meantime; if the queue fills as well, only end-of-call events wait); it raises an alert |
| rater slow or down | Calls are not affected; because reserved amounts are not released, the available balance looks lower than it is (the safe side) |
| ClickHouse unreachable | CDRs wait in `cdr_export_queue`; reports are served from PostgreSQL; trace messages are dropped and counted |
| rtpengine does not answer or has no ports left | New call: the attempt is not sent, 503 to the caller, `ended 503` with `cause: "media"` to the engine (the next route is not tried; record 503 with the selected connection, no charge; not written to the connection's live counters or, through `OnLeg`, to its quality, ADR-0039); counted (`opensips_media_refused`) and alerted (`CallsRefusedNoMedia`). An unresponsive relay is waited for ~5 s when first noticed (1 s × 5), then set aside and pinged in the background every 10 s; in the meantime calls are rejected without waiting (ADR-0030, formerly S-5; `docs/08` (Turkish), "Medya aktarıcısı teklifi almazsa", if the media relay does not take the offer). The offer in an offerless INVITE's 200 (ADR-0043): the 200 is forwarded without SDP, BYE to both parties a second later, `ended 503` with `cause: "media"` to the engine (not `answered`; no charge), counted. A new offer or answer in an established call (including the 200 of an offerless in-dialog INVITE): the SDP is forwarded unchanged (a limit; not tested) |
| The vendor cannot be reached at all (connection refused, TLS error) | `route[attempt]` reports `failed` with 503, the next route is tried immediately |
| No MI socket (`OPENSIPS_MI` empty) | The engine cannot end calls; only `max_duration_sec` remains |

### 8.3 Safe reprocessing

- `events`: a repeated `answered` does not change the answer time; an event for an ended or unknown call is ignored.
- CDR: NATS message ID + `(call_id, leg)` uniqueness; the record, the balance charge, the bundle charge and the `cdr_export_queue` row in a single transaction.
- ClickHouse: `ReplacingMergeTree`; a query that needs exact counts uses `FINAL`.
- REGISTER: a smaller `CSeq` with the same `Call-ID` changes nothing.
- Scheduled report and low-balance e-mail: the message is queued in the same transaction as the state change that causes it.

### 8.4 Error codes

The table of codes returned to the caller is in `docs/08-cagri-arayuzu.md` (Turkish) (403, 402, 480, 503, 423…). When adding a new rejection reason: use an existing code and a new `reason` text; do not change the meaning of a code; add the reason to that table and to a scenario.

## 9. Media

- Media passes through rtpengine on every call (for topology hiding and NAT). Kernel forwarding only on Linux; user space on a Mac.
- Offer flags are set in the script: always `replace-origin replace-session-connection ICE=remove`; if there is a codec list, `codec-strip-all` + `codec-offer-…`; for a party behind NAT, `SIP-source-address`.
- SRTP: the vendor leg is offered with SRTP (SDES) only if the signalling is TLS; the caller leg follows the caller's offer (RTP, SDES-SRTP, DTLS-SRTP). The same rule for a registered trunk: SRTP to one registered over TLS (the request goes over that connection), plain RTP to one registered over UDP (the engine says `media: "rtp"`; T-07). The script decides whether the signalling is TLS from where the request actually goes (`$du`, otherwise `$ru`).
- **Transcoding (ADR-0041, T-09):** off by default. If the codec lists of the caller's trunk and of the connection (or the target trunk) are both set and share no codec, the route is skipped; if either of them allows it with `transcode`, the route stays, the engine reports it with `route.transcode` (the caller side's list) and rtpengine transcodes (`codec-mask-all` + `codec-transcode-…`; the reverse direction for a new offer from the far end). The decision is based on the lists, not on the SDP. Calls transcoded at the same time are limited by `TRANSCODE_MAX` (200); at the limit the route is skipped (in the admission script, §6.3). Single node: transcoding is done in the same relay; a separate transcoding pool is in Enterprise. `telephone-event` stays on both sides, T.38 is not touched (ADR-0031). Codecs in the image: [`docs/en/09-installation.md`](09-installation.md).
- **IPv6 (ADR-0047, T-28):** with `SIP_IP6` the SIP core also listens on IPv6, and the relay has an address in both families. The script chooses the family of the relay address in the offer from how the request's destination is written (an address in square brackets is IPv6, dotted is IPv4, a name: left to the relay); this is not a decision but a mechanical mapping, like TLS → SRTP. The engine converts an IPv6 source address to its short form on input (OpenSIPS writes it in full and in upper case); the interface did not change.
- **No relay, no call (ADR-0030):** if the relay does not take a new call's offer (no response, no ports left or another error), the attempt is not sent and the call is rejected with 503; the next route is not tried, because the problem is in the switch (`branch_route[out]` → `route[media_refused]`; §8.2). Ports of an ended call are returned immediately (`rtpengine.conf`: `delete-delay = 0`; the default is 30 s, which exhausted the range at 200 CPS in the load test). Media is never set up directly between the parties without the relay; this applies only to new calls: a new offer in an established call passes unchanged if there is no relay. In an offerless INVITE the offer is in the vendor's 200: if the relay does not take it, the 200 is forwarded without SDP and the call is closed immediately (`route[late_refused]`, ADR-0043).
- There is no audio recording and there will not be (ADR-0018).
- **Dead call (ADR-0027):** if the relay sees no packet on any of a call's streams for `RTP_TIMEOUT` (60) seconds, it deletes the session and sends `teardown` to the SIP core (A11, `b2b-url`, `xmlrpc-format = 1`); the SIP core sends BYE to both parties and the engine receives `ended`. If one side has announced that it will not send (hold), the time is `RTP_HOLD_TIMEOUT` (3600) seconds; media flowing in one direction keeps the call alive. The values are template placeholders (`media/rtpengine/rtpengine.conf`, `deploy/images/rtpengine/run.sh`). Closed sessions show in the relay's metric: `rtpengine_closed_sessions_total{reason="timeout"}`.
- **Early media (ADR-0028):** a 183 with SDP passes through the relay like any response (`rtpengine_answer`); from then on the relay's session knows both sides and the timeout (above) also applies while ringing: a call with early media that gets no packets from either side is closed at 60 seconds, even if the connection's ring timeout is longer (**DECISION, ADR-0038**; scenario: `make test-routing`, "deadcall"). On a 180 without SDP the relay knows only the offer and does not touch the ringing call.
- There is no setting for fax (T.38), video or DTMF conversion (RFC 2833 ↔ SIP INFO). **DECISION (ADR-0031, formerly S-6).** A T.38 stream is only relayed (rtpengine's T.38 gateway is not used); UDPTL packets count as media, so a fax call is not closed as a dead call while packets flow. `telephone-event` stays in the offer and SIP INFO passes end to end; there is no conversion between the two (`docs/08` (Turkish), "Faks (T.38) ve tuşlar", fax and DTMF; TASKS: T-40).

## 10. CDR and rating pipeline

```
Engine ──cdr.v1──▶ NATS JetStream ──▶ rater ──one transaction──▶ PostgreSQL: cdrs + balance + bundle_usage + cdr_export_queue
                                                  └──batch──▶ ClickHouse: cdrs (ReplacingMergeTree)
                                        rater ──notification──▶ Redis: charge done (reservation released)
```

- Every attempt is a separate leg. A rejected call from a known client is recorded as a single leg without a vendor; a call from an unknown source is not recorded. The record of a call whose end is never reported is written by the engine when its state drops (free of charge; ADR-0029, ADR-0040).
- The engine writes the record to the journal on its own disk before sending it to NATS and deletes it once NATS accepts it; at start-up it resends whatever is left in the journal. A repeat is harmless (message ID `call_id:leg`, `(call_id, leg)` uniqueness in the rater). The only remaining window: the few milliseconds between the event and the `fsync` of the batch write.
- PostgreSQL is the system of record. ClickHouse is a copy; old records are deleted from PostgreSQL only after they have been verified by ID in ClickHouse (`CDR_KEEP_DAYS`). Every deleting pass moves the archive horizon (`cdr_archive.horizon`) forward; the API reads records older than the horizon (single record, list, CSV) from ClickHouse (T-24).
- Money is `int64` micro-units (1/1,000,000); there is no floating point anywhere (ADR-0004). Currency conversion uses a 128-bit intermediate product, rounding half up.
- Both the engine and the rater select the rate with `rating.Tidy` + `rating.Select`; `rating.Rate.AmountAfter` calculates the amount. Do not copy this logic into SQL or anywhere else.
- Adding a field to the CDR changes four places together: `engine.CDR`, `api.CDR`, the `cdrs` table (migration), the ClickHouse schema; the list's ClickHouse row (`pkg/api/cdrlist.go`, `cdrCols`) must give the same field as PostgreSQL's (`make test-reports`). On upgrade the rater is upgraded before the engine. A field that only makes sense on some legs is written with `omitempty` in the engine (e.g. `dst_trunk_id`, T-05): that way an old rater that does not know the field still accepts ordinary records during the upgrade.
- Report queries are written separately for PostgreSQL and ClickHouse and must give the same fields (`make test-reports`).

## 11. Management API and panel

- Most resources are defined with the generic CRUD (`pkg/api/crud.go`: `Resource`); special operations are in separate files.
- Every write: one transaction → the record + an `audit_log` row (`Server.Audit`) → `NOTIFY softswitch_config`. A call that changes nothing uses `AuditNothing`. Secrets are masked in the audit log by field name.
- Identity: an API key, or a 12-hour session obtained with e-mail + password (bcrypt); optional two-step verification (TOTP); the account can make it mandatory for everyone, and the session of a user who has not set it up only reaches the set-up (ADR-0045). Roles: `admin`, `readonly`.
- Visibility: Community has a single account; every record belongs to the root account. Enterprise sets the "its own subtree" rule with `SetScope`.
- Lists are paginated on the server.
- Panel: `web/src/panel.tsx` (menu and `groups`), `web/src/screens/`, shared components `web/src/ui/kit.tsx`. Enterprise screens are written in the private repository and added with `createPanel(...extensions)`; no trace of Enterprise shows in the Community panel.

## 12. Open-core boundary

- This repository is Community (AGPL-3.0-only). Enterprise is in a separate, private repository and imports this one; never the other way round.
- Enterprise features (reseller hierarchy, invoicing, DIDs, clustering, media pool, quality-based routing, fraud detection, licensing) are not written here; only the extension point and its single-account/single-node default are added.
- The extension points (ADR-0008) are the contract between the two repositories; after every change to their signatures, the Enterprise repository is also built and tested:
  - **API** (`pkg/api`): `Extension`, `AddResource`, `Handle`, `HandlePublic` (endpoints where the extension does the authentication), `HashPassword`/`CheckPassword`, `WriteErr`, `QueueMail`/`MailEnabled`/`NewResetToken`/`Amount` (e-mail), `OnAlertMail` (whether the low-balance e-mail is sent), `Audit`/`AuditEntry`/`AuditNothing`/`AuditSnapshot`/`AuditDiff` (audit log), `ClientIP`, `SetScope`, `AddParty`, `OnCDRPrice`, `OnCDRStored`, `AllowCDRField`, `SetSnapshotLoader`, `SetEdition`. route-engine and rater also run on the same `api.Server` (`LoadSnapshot`, `IngestCDRs`, `RootCaller`); the extension's rules thus apply in all three services.
  - **Engine** (`engine.Config`): `Adjust` (orders or removes the routes of a call about to be admitted, or rejects the call; on the call path, memory only; with `routing.ToTrunk` the call can be given to a client's registered trunk), `OnLeg` (the outcome of every attempt and rejection; `Leg.NotSent`: an attempt the SIP core could not send, not counted against the vendor), `Incoming` (takes a call whose source matches no trunk on behalf of a client; ADR-0032), `Peers` (`engine.PeerCheckFromEnv`), `MaxCalls`.
  - **Panel:** `createPanel(...extensions)` (`web/src/lib/session.tsx`, `PanelExtension`).
- OpenSIPS and rtpengine are GPL: they are spoken to over the network only; their code is not linked or copied, and no modules are written for them.

## 13. Deployment

- Lab: `deploy/compose/` (`make lab-up`). Production: `deploy/prod/`, a single server with Docker Compose ([`docs/en/09-installation.md`](09-installation.md)).
- `opensips.cfg` and `rtpengine.conf` are templates: names between double underscores are filled in from environment variables when the container starts. No address or domain is hard-coded.
- Schema steps are applied in order under an advisory lock (`store.MigrateFS`); ClickHouse files contain a single statement and must be safe to run again.
- A multi-node installation (active/standby SIP, dialog replication, Redis/PostgreSQL/NATS clusters) has **not been designed**; it is Enterprise work and awaits real servers. **OPEN QUESTION (S-7).**

## 14. OPEN QUESTION list

Struck-through questions were decided on 2026-11-23; the decision record is next to each. The old "OPEN QUESTION" markers in the text have changed to "DECISION"; until a decision is implemented, the "today" description in that section applies.

| No | Question | Where |
|---|---|---|
| ~~S-1~~ | ~~Session timers (RFC 4028): will the core apply them per leg?~~ — decision: ADR-0027 | §5.2 |
| ~~S-2~~ | ~~Closing the engine's state on a CANCEL that arrives while `authorize` is awaited~~ — closed (T-01): the state was being cleaned up, but the attempt was reported with 503; the script was fixed | §5.4 |
| ~~S-3~~ | ~~REFER, PRACK/100rel and early media policy~~ — decision: ADR-0028 | §5.6 |
| ~~S-4~~ | ~~While NATS is unreachable, the engine's in-memory CDR queue is lost if the engine crashes: will this be closed, and how?~~ — decision: ADR-0029; implemented (T-33) | §8.2, §10 |
| ~~S-5~~ | ~~Should the call be rejected if rtpengine does not answer?~~ — decision: ADR-0030; implemented (T-39) | §8.2 |
| ~~S-6~~ | ~~Fax (T.38), DTMF conversion, video~~ — decision: ADR-0031 | §9 |
| S-7 | High-availability design: dialog replication, virtual IP, key layout in a Redis cluster, balance view across nodes | §13 |
| ~~S-8~~ | ~~Marketplace module model: compiled in and unlocked by licence, or installed afterwards?~~ — decision: ADR-0037 | ADR-0022 |
| ~~S-9~~ | ~~Should the `/8` (IPv4) and `/16` (IPv6) width limit on trunk networks stay?~~ — decision: ADR-0034 | ADR-0020 |
| S-10 | Do the target markets include the USA/Canada (STIR/SHAKEN required)? | TASKS: T-13 |
| S-11 | Number portability for which markets, and with which lookup provider? | TASKS: T-12 |
| ~~S-12~~ | ~~The shape of the extension point for incoming calls (from a source that is not a client)~~ — decision: ADR-0032 | TASKS: T-04 |
| ~~S-13~~ | ~~Will keep-alives (OPTIONS) be sent to registered trunks, or is a short registration expiry the permanent solution?~~ — decision: ADR-0033 | ADR-0013 |
| S-14 | TLS on the engine interface and a separate secret per caller; protecting the MI socket and MI's XML-RPC port (A11) | ADR-0016 |
| S-15 | Identity provider type for SSO (OIDC, SAML or both) | TASKS: T-22 |
| S-16 | Text of the contributor licence agreement (CLA) | ADR-0007 |
| ~~S-17~~ | ~~A caller that does not ACK an answered call: when should the call be closed?~~ — decision: ADR-0026 | §5.4 |
| ~~S-18~~ | ~~A call whose early media is silent (183 with SDP, no packets at all) and that rings for longer than 60 seconds: does the media relay's timeout or the connection's `ring_timeout_sec` win?~~ — decision: ADR-0038 (the timeout wins) | §8.1, §9 |
| ~~S-19~~ | ~~Should a trunk's concurrent-call limit (`max_calls`) be counted separately in each direction, or as a total?~~ — decision: ADR-0042 (total, a single counter); implemented (T-45) | §6.3; `docs/08` (Turkish), "Müşterinin trunk'ına çağrı" (call to a client's trunk) |
| ~~S-20~~ | ~~Should the limits of the target trunk's **client** (`clients.max_calls`/`max_cps`) apply to a call delivered to a trunk?~~ — decision: ADR-0042 (they apply; the client's limit also counts both directions together); implemented (T-45) | §5.5 |
