<!-- English version of CONTRIBUTING.tr.md; the Turkish document is the source and both are updated together. -->
# Contributing

Thank you for contributing. Please read this page before you send a change.

> English version of [`CONTRIBUTING.tr.md`](CONTRIBUTING.tr.md) (Turkish), which is the authoritative text; the two are updated together.

## Contributor licence agreement (CLA)

The code in this repository is published under AGPL-3.0-only, and the same code is also the basis of the commercially licensed Enterprise edition. For a contribution to be distributable under both licences, its author must grant that right explicitly; this is done with a Contributor License Agreement (CLA).

- The CLA document will be published separately by the maintainers. This page is not the CLA and does not replace it.
- **No contribution without a signed CLA is merged.** Until the CLA is published, pull requests can be discussed but not merged.
- Before contributing code that belongs to someone else (for example code your employer holds rights to), make sure you have the necessary permission.

## Before you start

- Read [`docs/en/ARCHITECTURE.md`](docs/en/ARCHITECTURE.md), [`docs/decisions/`](docs/decisions/README.md) (Turkish) and [`docs/TASKS.md`](docs/TASKS.md) (Turkish). Details: [`docs/08-cagri-arayuzu.md`](docs/08-cagri-arayuzu.md) (Turkish; the interface between the engine and the SIP core), [`docs/en/06-api.md`](docs/en/06-api.md), [`docs/07-panel.md`](docs/07-panel.md) (Turkish), [`docs/en/09-installation.md`](docs/en/09-installation.md), [`docs/en/10-monitoring.md`](docs/en/10-monitoring.md).
- **Decisions are binding.** If a change would go against a decision record (ADR), a rule in `docs/en/ARCHITECTURE.md` or the invariants below, open an issue and discuss it first. If a decision changes, a new ADR is written that marks the old one "Superseded", and the architecture document is updated in the same change.
- Work that depends on a point marked **OPEN QUESTION** (Turkish: **AÇIK SORU**) in the documents needs a decision first.
- Features of the Enterprise edition (reseller hierarchy, invoicing, DIDs, clustering, media pool, quality-based routing, fraud detection, licence verification) are not written in this repository; only the extension points and the single-account / single-node defaults are here ([`docs/en/04-open-core.md`](docs/en/04-open-core.md); the list of extension points is in `docs/en/ARCHITECTURE.md` §12). Enterprise features never appear in the panel in any form (no locked screens, greyed-out menus or labels).

## Invariants

- **No SQL queries on the call path.** route-engine decides from the in-memory snapshot; per call it goes only to Redis, with a single script (ADR-0003).
- **Decision time budget: p99 < 10 ms.** Run the benchmarks for every change that touches the call path (`go test -bench Authorize ./pkg/routing`, `make load CPS=200 CC=2000`).
- **Money is an integer.** Amounts are `int64` in the smallest unit (1/1,000,000); no floating point for money in Go, SQL, Lua or TypeScript (ADR-0004). Rate selection and amounts only through `pkg/rating` (ADR-0010).
- **CDRs are never lost.** Events are written to the queue at least once; consumers are safe to reprocess (ADR-0006, ADR-0029).
- **The interface between the SIP core and route-engine is versioned** and independent of the SIP server; no OpenSIPS-specific concept leaks into it (ADR-0002).
- **Every SIP feature comes with a SIPp scenario.** SIP behaviour without a scenario is not considered done.
- **The schema changes only through migration files;** every migration leaves existing rows with their old behaviour. A ClickHouse file is a single statement and can be run again.
- **`core` never imports `enterprise`.** OpenSIPS and rtpengine are GPL: we talk to them over the network only and neither link to nor copy their code (ADR-0009).

## Code standards

- **Go:** clean under `gofmt` and `go vet`; errors are never swallowed but wrapped with context and returned (`fmt.Errorf("…: %w", err)`); panics only for programmer errors (e.g. registering the same metric twice). A short comment saying "why" for every exported name and every non-obvious decision.
- **Call path** (`pkg/routing`, `pkg/rating`, the decision and event code in `pkg/engine`): I/O only through a single Redis script; no waiting under a lock; shared state is replaced with `atomic.Pointer`, never modified in place.
- **The OpenSIPS script stays thin:** it makes no decisions; `route(engine_token)` before every `rest_post`; no hard-coded addresses or realms (the script is a template, `docs/en/09-installation.md`). In this OpenSIPS version (3.6.10), no command that changes the request (`consume_credentials`, `fix_nated_contact`, `remove_hf`, `append_hf` …) is used before `async()`; change the request in the resume route instead (`docs/en/ARCHITECTURE.md` §4).
- **Tests:** pure logic with unit tests, the API with end-to-end tests (`make test-api`), SIP behaviour with SIPp scenarios (`make test-routing`). A time-dependent test using a fixed date must not fall into another test's "now" window.
- **API:** a new endpoint is registered with `s.Handle` or `s.HandlePublic`, with its summary and body types (the OpenAPI description is generated from them; `docs/en/06-api.md`, "Describing a new endpoint"). Every operation that writes records the audit log in the same transaction (`Server.Audit`; `AuditNothing` for a call that changes nothing).
- **TypeScript:** clean under `npx tsc --noEmit -p .`; shared components in `web/src/ui/kit.tsx`. Interface text comes only from the catalogues (Turkish and English); no `text-transform: uppercase`; amounts are formatted with `web/src/lib/format.ts`; the session key is kept in memory only (never written to browser storage); lists are paginated on the server. Screens and states that are not in the design are built from the shared components and added to the deviations table in `docs/07-panel.md` (Turkish).
- **Secrets** (passwords, tokens, keys) are never written to logs, error messages, the audit log, test output or the repository; lab values are read from the seed files.
- Every new source file carries the header `SPDX-License-Identifier: AGPL-3.0-only`.
- **Language:** code, comments and commit messages in English; `docs/` in Turkish (the English versions of the user-facing documents are in `docs/en/` and are updated in the same change as the Turkish ones; ADR-0050); the interface in Turkish and English (texts in the `web/src/i18n/` catalogues, ADR-0049; `npm run check-i18n` checks that the two catalogues match).
- A change is not done until the relevant documents are updated: the document of the area touched, `docs/03-yol-haritasi.md` and `docs/05-ozellik-katalogu.md`.

## Commands

| Command | What it does |
|---|---|
| `make lab-up` / `make lab-down` | Lab environment (OpenSIPS, rtpengine, SIPp, PostgreSQL 127.0.0.1:56432, Redis 127.0.0.1:56379, ClickHouse 127.0.0.1:58123, mail catcher 127.0.0.1:58025, NATS, route-engine 127.0.0.1:58090, rater, trace-collector); generates the lab certificates first. After changing Go code, `make lab-up` rebuilds |
| `make lab-seed` | Lab client, vendors and routes (`test/seed/lab-routing.sql`) |
| `make test` | Unit tests (`go test ./...`; no lab needed) |
| `make test-engine` | route-engine tests (wipes database 15 in the lab Redis on every run) |
| `make test-api` | API end-to-end test (wipes the `softswitch_test` database in the lab PostgreSQL on every run) |
| `make test-openapi` | Redocly lint of the API description (needs `npx`) |
| `make test-routing` | Routing scenarios with real calls (SIPp) |
| `make test-journal` | Scenario showing that no CDR is lost when the engine dies while NATS is down (run it on its own) |
| `make test-reports` | Fetches the summary report and the call list from ClickHouse and PostgreSQL and compares them (needs `make api-run`) |
| `make test-archive` | Archiving old CDRs and reading them back from ClickHouse |
| `make test-mail` | Password reset e-mail through the lab mail catcher (needs `make api-run` and `make seed-user`) |
| `make test-nft` | Checks the sample network-level rules in a throwaway container |
| `make test-call` | A single call through OpenSIPS, route-engine and rtpengine |
| `make load CPS=<n> CC=<n>` | Load test; report and PASS/FAIL (including the decision-time p99 budget) |
| `make bench-snapshot` | Load time and memory of a snapshot with 5 million rates |
| `BOOTSTRAP_API_KEY=... make api-run` | Runs the API on 127.0.0.1:58080 |
| `make seed-user` / `make seed` | Lab panel user / demo data for the panel (`test/seed/dev.env`) |
| `make panel-install` / `make panel-dev` / `make panel-build` | Runs the panel on 127.0.0.1:58081 (forwards API calls to 58080) / produces `web/dist` (served by the API with `WEB_DIR=web/dist`) |
| `make prod-smoke` | Tries the single-server install (`deploy/prod`) end to end on a private Docker network: install, a call, a crash, backup and restore, upgrade |

Notes: OpenSIPS has no arm64 package, so the image is built from source (the first install takes a few minutes). SIPp's built-in `uac`/`uas` scenarios do not honour Record-Route; use the ones under `test/sipp/`. Do not edit `test/sipp/run.sh` while it is running.

## Before sending a pull request

```sh
gofmt -l . && go vet ./... && go build ./... && go test ./...
(cd web && npx tsc --noEmit -p .)
```

If you touched SIP behaviour, bring up the lab (`make lab-up`) and run `make test-routing`; if you touched the API, `make test-api`; if you touched the call path, `make load CPS=200 CC=2000 DURATION=30`.

## Security

Do not report security vulnerabilities as public issues; follow the process in [`SECURITY.md`](SECURITY.md).
