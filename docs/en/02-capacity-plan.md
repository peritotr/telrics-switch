<!-- English version of docs/02-mimari.md; the Turkish document is the source and both are updated together. -->
# Architecture and Capacity Plan — 20,000 Concurrent Calls

> English version of [`docs/02-mimari.md`](../02-mimari.md) (Turkish), which is the authoritative text; the two are updated together.

## Assumptions (awaiting approval)

- The product is **Class 4**: wholesale/retail SIP transit, client → routing → vendor. Subscriber registration, PBX features and IVR are out of scope.
- By default, media passes through the system (for topology hiding and billing accuracy).
- Transcoding is the exception; it runs in a separate pool and is licensed/charged separately.
- SIP only (no H.323).

## Load calculation

| Quantity | Calculation | Result |
|---|---|---|
| Answered CPS (wholesale, average duration 180 s) | 20,000 / 180 | ~110 |
| Attempt CPS (answer rate 40%) | 110 / 0.4 | ~280 |
| Attempt CPS (call-centre traffic: 30 s, 20%) | 20,000 / 30 / 0.2 | ~3,300 |
| **Design target** | | **1,000 CPS sustained, 3,000 CPS peak** |
| RTP packet rate (20 ms, 2 directions per call) | 20,000 × 100 | 2 M packets/s in + 2 M out |
| Bandwidth (G.711, ~80 kbit/s per direction) | 20,000 × 160 kbit/s | ~3.2 Gbit/s (on each side) |
| CDR volume (if 280 CPS were sustained) | 280 × 86,400 | ~24 M records/day upper bound |

The traffic profile (wholesale or call centre) changes the CPS target by a factor of 10; it is the first thing to settle.

## Components

```
 Clients ──SIP──▶ ┌──────────────┐        ┌───────────────┐ ──SIP──▶ Vendors
                  │   SIP core   │◀──────▶│ route-engine  │
                  │  (OpenSIPS)  │  HTTP  │  (Go)         │
                  └──────┬───────┘        └──┬─────────┬──┘
                         │ ng protocol       │         │
                  ┌──────▼───────┐       ┌───▼───┐ ┌───▼──────┐
 RTP ────────────▶│  rtpengine   │       │ Redis │ │PostgreSQL│
                  │  pool        │       └───────┘ └──────────┘
                  └──────────────┘
   CDR events ──▶ NATS JetStream ──▶ rater ──▶ ClickHouse (CDR) + PostgreSQL (balance)
   Management: api (Go, REST) ──▶ UI (decision pending)          SIP tracing: HEP ──▶ trace-collector ──▶ ClickHouse
```

| Component | Technology | Role | Count for 20,000 calls |
|---|---|---|---|
| SIP core | OpenSIPS (current LTS) | IP/Digest authentication, dialog tracking, topology hiding, failover to the next vendor, rtpengine selection | 3 active (N+1), each ~10,000 dialogs |
| Media | rtpengine, kernel forwarding | RTP relay, SRTP | 5 active + 1 spare, ~5,000 calls per node (to be verified by measurement), 10 GbE |
| Transcoding | rtpengine (separate pool) | G.729/Opus ↔ G.711 | As needed |
| route-engine | Go | Per-call decision: client identification, limits, number manipulation, LCR, balance reservation, max duration | 3 (stateless) |
| rater | Go | CDR rating, balance settlement | 2 |
| api (+ UI) | Go; UI technology not decided | Clients, vendors, tariffs, routing plans, reports | 2 |
| Redis | Redis (cluster) | Concurrent-call and CPS counters, balance reservation, rtpengine call state | 3 |
| PostgreSQL | HA with Patroni | Configuration, tariffs, balance ledger | 3 |
| ClickHouse | | CDRs and statistics | 2-3 |
| Event queue | NATS JetStream | CDR events, configuration change notifications | 3 |

In a small installation (≤2,000 calls) all components must be able to run on one server; that is how Sippy's advantage of easy installation is kept.

## Key decisions and their reasons

1. **No SIP stack will be written.** OpenSIPS + rtpengine is a proven data plane. Writing a B2BUA from scratch would be the riskiest and least distinctive work in the project. OpenSIPS was chosen because topology hiding is built into the dialog module and B2B modules and cluster (clusterer) support are ready. Kamailio is an equivalent alternative.
2. **All business logic in route-engine.** The OpenSIPS script is kept thin: for every INVITE it asks route-engine and applies the ordered list of vendors it returns. That way the SIP core can be replaced later (for example with a real B2BUA).
3. **No database query on the call path.** route-engine keeps tariffs and routing plans in memory (loads them from PostgreSQL and refreshes them on change notification). Per call it goes only to Redis. Target: decision time p99 < 10 ms.
   - **Size of the snapshot (T-30, ADR-0048; measured on a Mac):** 5 million rates take ~1.3 GiB in the engine's memory; a full load takes ~8 s and allocates 2.7 GiB, while the old snapshot is still in use the heap grows to 3.8 GiB, and under load, with garbage collection lagging, to 6.4 GiB. Because rates are read only where they changed, an ordinary refresh takes 10 ms, and a prefix change in a large tariff ~0.25 s (a copy of the map; a temporary +1.1 GiB). The engine server's memory is therefore chosen by the rate count of the largest tariff: ~0.3 GiB per million rates permanently, about 3 times that temporarily during a full load; the API process holds the same snapshot too when the simulator is used.
4. **Billing is reservation-based.** When a call is set up, time is reserved from the balance, the maximum call duration is given as the dialog timeout, and the charge is settled when the call ends. No floating point is used for money (integers, smallest unit 1/1,000,000).
5. **CDRs are separate from the call path.** OpenSIPS writes the event to the queue; even if ClickHouse or the rater slows down, calls are not affected.

## Open risks

- **rtpengine node capacity is uncertain.** Published reports range between 3,000 and 16,000. No hardware should be ordered before it is measured on real hardware in phase 0.
- **The kernel module does not run on macOS.** Development can be done on a Mac with Docker (in user-space mode), but load testing needs a physical Linux server.
- **Limits of a proxy-based core.** If needs such as independent session timers per leg or re-INVITE isolation come up, moving to the OpenSIPS B2B module or a separate B2BUA may be necessary.
- **Compatibility with real carriers.** Vendors' SIP differences only show up on a live connection; the lab cannot imitate that.
- **Regulation.** If operated as a carrier in Turkey, BTK obligations (lawful interception, record retention, emergency calls) must be handled separately.
