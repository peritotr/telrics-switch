<!-- English version of docs/05-ozellik-katalogu.md; the Turkish document is the source and both are updated together. -->
# Feature catalogue

> English version of [`docs/05-ozellik-katalogu.md`](../05-ozellik-katalogu.md) (Turkish), which is the authoritative text; the two are updated together.

The features considered standard in Sippy, PortaSwitch, VOS3000, Sansay, Kolmisoft M4 and Yeti-Switch, and their status in this project.

Edition: rows whose name starts with **(E)** exist only in the Enterprise edition; all others are in Community.

Status: **Available** = written and verified by tests · **Partial** = data model/API exists but is not wired into the call path, or is incomplete · **Not available** = not started yet (target phase in parentheses).

## Accounts and hierarchy

| Feature | Status | Note |
|---|---|---|
| Client accounts, prepaid/postpaid, credit limit | Available | |
| Vendor accounts and vendor connections | Available | Balance = what we owe the vendor |
| Connection and trunk settings, as "Basic" and "Advanced" | Available | On a vendor connection: sign-in to the vendor with username/password (Digest MD5, SHA-256, SHA-512-256; `qop=auth`; a vendor that requires only `auth-int` is not supported, T-15), number format (including `+`), connection-specific caller ID and identity header, ring timeout, maximum call duration, which failures move on to the next route, codec list and order. On a trunk: the client's number format (several rules, the longest match applies; T-17), maximum call duration, trunk limits, codec list, permission to transcode. Every setting has a scenario with real calls (`docs/08-cagri-arayuzu.md` (Turkish), "Bağlantı ve trunk ayarları") |
| Vendor health checks (SIP OPTIONS) and "Test connection" | Available | The engine sends the checks itself; no calls go to a connection that does not answer, and it is taken back once it answers. Status and a one-off test in the panel. Off by default |
| Registration (REGISTER) with a vendor | Available | Enabled on the connection (with a sign-in name and password, the requested expiry and an optional realm). The SIP core registers from its own address, answers 401/407 with Digest and renews before expiry; on failure it retries at a fixed interval (`REG_RETRY_SEC`; no back-off). Status in the panel ("Registration" column), in `GET /v1/peers`, in a metric and in an alert (`VendorRegistrationFailing`). UDP and TLS only (`docs/08-cagri-arayuzu.md` (Turkish), "Sağlayıcıya kayıt"; ADR-0035) |
| (E) Multi-level reseller hierarchy | Available | Each reseller sees only its own subtree; tested |
| (E) Wholesale tariff for resellers, per-call reseller rating | Available | Every reseller in the chain pays the one above it |
| (E) Calls of sub-clients refused when a reseller is suspended | Available | |
| API keys (admin / read-only) | Available | Only a hash is stored |
| API description (OpenAPI 3.0) | Available | `GET /v1/openapi.json` is generated from the running server's registrations; the generic resource endpoints and all of the core's custom operations, with request and response schemas (T-21); Redocly lint with `make test-openapi` |
| Client trunks: authentication by IP, tech prefix | Available | Several addresses and networks (CIDR) per trunk, IPv4 and IPv6; the narrowest network wins, and on a tie the longest tech prefix |
| IPv6: signalling, registration and audio | Available | With `SIP_IP6` the SIP core also listens on IPv6 (UDP, TLS); calls from a trunk with an IPv6 source, registration over IPv6 and calls to such a registration, IPv6 vendors and their health checks; the media relay bridges between IPv4 and IPv6. Off by default (IPv4 only). Tested on the lab's IPv6 network, not yet on a real server; to a vendor defined by name, audio is offered in the caller's address family (`docs/08-cagri-arayuzu.md` (Turkish), "IPv6"; ADR-0047) |
| Digest (username/password) authentication | Available | The API generates the password |
| Registration (REGISTER): a signed-in trunk reports where it is | Available | Registrations are kept in the engine (Redis); expiry limits, number of addresses per trunk, viewing and deleting in the panel. Short registrations instead of keep-alives; a call to a trunk registered over TLS goes through the registration's connection and SRTP is offered, and if that connection has closed the next registration is tried (T-07) (`docs/08-cagri-arayuzu.md` (Turkish), "Kayıt (REGISTER)") |
| Client behind NAT: signalling and audio | Available | Responses and in-dialog requests go to the address the packets came from; the media relay learns the real source of the audio. There is no router in the lab, so the mismatch was simulated; not yet tried with a real router |
| New offer within the call (re-INVITE): hold, resume, renegotiation | Available | Passes through the media relay in both directions, with the codec list kept in the dialog; reaches a party behind NAT at its real address; a rejected offer does not drop the call; duration, charge and the CDR do not change. An offerless INVITE (offer in the 200, answer in the ACK) also passes through the relay, both on the initial INVITE and within the call (ADR-0043; `docs/08-cagri-arayuzu.md` (Turkish), "Çağrı içi yeni teklif", "Teklifsiz INVITE") |
| Tearing down dead calls (media timeout); session timers (RFC 4028) | Available | When no audio arrives from either side for 60 seconds (`RTP_TIMEOUT`), the media relay has the SIP core close the call: a BYE to both sides, and a CDR with the real duration. For a call on hold the limit is one hour (`RTP_HOLD_TIMEOUT`). Session timer headers pass through unchanged; the switch does not request refreshes itself (`docs/08-cagri-arayuzu.md` (Turkish), "Ölü çağrı") |
| Fax (T.38) and keypad tones (DTMF) | Available | Switching to T.38 within the call and back to audio passes through the media relay unchanged in both directions; the codec list does not drop the fax; a call carrying fax packets is not considered dead. Tones pass end to end as `telephone-event` or SIP INFO, with no conversion between them; no T.38 gateway and no video settings (`docs/08-cagri-arayuzu.md` (Turkish), "Faks (T.38) ve tuşlar") |
| Refusing calls when the media relay is missing | Available | If the relay does not answer or runs out of ports, a new call is refused with 503, the next route is not tried, and the CDR shows 503 and is free of charge; the refusal is counted and raises an alert (`CallsRefusedNoMedia`, `MediaPortsLow`). When the relay comes back, calls get through again within ~10 seconds. The CDR shows the chosen connection, but the attempt that was never sent is not written to that connection's live counters and quality figures (`OnLeg`, `NotSent`) (`cause: "media"`, ADR-0039). A new offer on an established call passes through unchanged when the relay is missing (ADR-0030) |
| Early media (183), 100rel/PRACK; in-dialog transfer (REFER) | Available | A 183 with SDP passes through the media relay (the caller sees the relay's address); `Require: 100rel`, `RSeq`, `RAck` and PRACK pass through unchanged in both directions. A REFER within the call is rejected with 403, the call continues and the rejection is counted (`opensips_refer_refused`, alert `TransfersRefused`); the switch does not carry transfers. A call whose early media is silent and that rings for longer than 60 seconds is closed by the media relay, even if the connection's ring timeout is longer (ADR-0038; stated in the description of the setting in the panel) (`docs/08-cagri-arayuzu.md` (Turkish), "Erken medya, PRACK ve aktarma") |
| Delivering calls to a client's registered trunk | Partial | The mechanism exists (the engine turns the route into the registration, and the SIP core sends to the address the registration came from); which number goes to which client is the job of an extension. The CDR carries the destination trunk (`dst_trunk_id`; shown in the panel, in the list and in CSV; T-05). The destination trunk's own limits (`max_calls`, `max_cps`; when full, the next route or 503) and codec list are applied (T-06); the destination client's limits are applied too, and the limits of a trunk and of a client count the calls it makes and receives together (T-45, ADR-0042). There is an extension point that takes a call from a source that is not a client (a number provider) on behalf of a client (`engine.Config.Incoming`, T-04; ADR-0032): the client's limits, reservation and CDR apply |
| User accounts and roles | Available | Sign-in with e-mail and password, two roles (admin, read-only), account lockout, ending sessions |
| Password reset e-mail | Available | Single-use link, valid for 30 minutes |
| Audit log (who, what, when, from which address; old and new values of the changed fields) | Available | All write endpoints, including extensions' resources; secrets are masked; configurable retention; CSV export with filters (server-side streaming, T-23). No syslog/SIEM forwarding (T-47) ([`docs/en/06-api.md`](06-api.md), "Audit log") |
| Two-step verification for panel users (TOTP, recovery codes) | Available | An admin can reset it; the secret is stored encrypted (`SECRET_KEY`). Can be made mandatory for everyone with an account setting: a user who has not set it up reaches only the setup on sign-in; API keys are not affected (T-23, ADR-0045) |
| SSO (single sign-on) | Not available | On the roadmap (the rest of B7) |
| Client self-service portal | Available (Enterprise) | In the private repository; includes payment notices, number rental, two-step verification |

## Tariffs and rating

| Feature | Status | Note |
|---|---|---|
| Prefix-based sell and buy tariffs, longest match | Available | Tariffs with millions of rates: the engine re-reads rates only where they changed (with 5 million rates a routine refresh takes ~10 ms, a prefix change ~0.25 s; a full read ~8 s, every `RELOAD_FULL_SEC`; T-30, ADR-0048) |
| Initial / billing increment (e.g. 60/60, 1/1), connection fee, free period | Available | |
| Tariff versions with effective dates | Available | |
| CSV tariff upload | Available | |
| Destination blocking (blocked) | Available | |
| CDR rating (client, vendor) and resilience to redelivery | Available | Reseller chain rating (E) |
| Maximum call duration computed from the balance, call closed when it runs out | Available | Closed 1 s before the money runs out; the balance is never overdrawn |
| Real-time balance reservation (Redis) | Available | In 60 s steps; calls in progress share the balance (`docs/08-cagri-arayuzu.md` (Turkish)) |
| Rates by day and time of day | Available | Day and time window on the rate, time zone on the tariff (`docs/08-cagri-arayuzu.md` (Turkish)); window columns in CSV upload (T-19) |
| Public holiday calendar | Available | On a public holiday the rate and route windows treat the day as Sunday (or the chosen day); one calendar per account (T-19, ADR-0044) |
| Minute bundles | Available | Free seconds per month per client and prefix set; the engine reserves the seconds as part of the reservation, the rater deducts them (`docs/08-cagri-arayuzu.md` (Turkish), "Paket dakikalar"). No rollover minutes and no bundle fee |
| Commitments (minimum spend) | Not available | |
| Multiple currencies and exchange-rate conversion | Available | A party and its tariff use the same currency; exchange rates are entered by hand or optionally taken from the European Central Bank's daily reference rates (`FX_SOURCE=ecb`; once a day, converted into the account's currency; rates entered by hand are kept; T-20); routing and reports are in the account's currency (`docs/08-cagri-arayuzu.md` (Turkish)) |
| Low balance alert | Available | Threshold per client; open and closed alerts in the panel; an e-mail to the client when an alert opens. No webhook |
| Sending e-mail (SMTP or Resend) | Available | Queued, with retries; plain text, in Turkish |
| Automatic suspension | Not available (phase 5) | For a prepaid client, calls are already rejected with 402 when the balance runs out |

## Routing

| Feature | Status | Note |
|---|---|---|
| Routing plans, vendor list per prefix | Available | |
| LCR (cheapest first) and priority ordering | Available | |
| Loss protection (skip the route if buy > sell) | Available | Can be turned off per plan |
| Number manipulation (strip/add prefix) | Available | Per vendor connection (including a leading `+`); number format per client trunk (`00…`, `+…`, national) |
| Route simulation (test tool) | Available | `POST /v1/route/simulate` |
| (E) Client inherits the reseller's plan | Available | |
| Decision engine connected to OpenSIPS, failover to the next route | Available | `docs/08-cagri-arayuzu.md` (Turkish); decision p99 < 1 ms (lab) |
| CDRs from real calls (each attempt a separate leg), rating through a queue | Available | NATS JetStream → rater → PostgreSQL |
| CDRs kept safe during a NATS outage and an engine crash | Available | Journal on the engine's disk (`CDR_JOURNAL_DIR`), resent on start-up; a call whose end was never reported is recorded with no duration and free of charge; reconciled with the vendor's invoice by hand (ADR-0029, ADR-0040, T-33) |
| Weighted (percentage) traffic distribution | Available | Weighted draw per call between the routes of the same step; weight 0 = backup only (`docs/08-cagri-arayuzu.md` (Turkish)) |
| Enforcing concurrent call and CPS limits | Available | Per client, client trunk and vendor connection, with Redis counters |
| Quality-based routing (ASR/ACD/PDD) | Not available (Enterprise) | Extension points ready in the engine (`Adjust`, `OnLeg`) |
| Number portability (MNP/LRN) lookup | Not available (phase 5) | |
| Firewall (application level) | Available | Block / allow rules for addresses and networks, list of automatic blocks and lifting them; nothing at operating-system level |
| Network-level protection (operating system) | Guide | Sample nftables rules (closed ports, per-source and total rate limits, a "known addresses only" mode) and a script that syncs the set of known addresses from the panel; the administrator applies them by hand, the product does not manage them (T-10; [`docs/en/09-installation.md`](09-installation.md), "Network-level protection"; a firewall managed by the product is T-27) |
| Caller ID (CLI) rules, block/allow lists | Available | Block / allow / rewrite by prefix; per client or global; a rule looks at the number in From or in `P-Asserted-Identity`; rejecting callers without a number (433) is defined on its own; the identity of a caller asking for `Privacy: id` does not reach the vendor (T-16; `docs/08-cagri-arayuzu.md` (Turkish)). Connection-specific rewriting is in the connection settings |
| Time-based routing | Available | Day and time window on the route entry, time zone on the plan; follows public holidays |

## DID (E)

This whole section is in Enterprise.

| Feature | Status | Note |
|---|---|---|
| DID providers (setup, monthly and per-minute cost) | Available | The cost is booked to a vendor account |
| DID pool: bulk import, free/reserved/assigned/quarantine | Available | |
| Assignment to a client, setup fee, release | Available | |
| Monthly DID charges on sales and purchase invoices | Available | |
| Per-minute rating of DID CDRs | Available | |
| Routing inbound DID calls to the client (SIP) | Not available (phase 1–2) | The `forward_to` field is ready; the endpoints needed in the core are ready (`engine.Config.Incoming`, `routing.ToTrunk`, `AllowCDRField`) |
| Provider API integration (automatic ordering/return) | Partial (Enterprise) | Provider drivers written; not yet tried with a real account |
| Partial-month (pro rata) charging | Not available | |

## Invoicing and accounting

| Feature | Status | Note |
|---|---|---|
| (E) Sales invoice (to the client): usage + DID + tax | Available | |
| (E) Purchase invoice (from the vendor): usage + DID cost | Available | |
| (E) Invoice life cycle: draft → issued → paid / cancelled | Available | Cancelling reverses the effects on the balance |
| Payment, adjustment and refund records; account ledger | Available | |
| (E) Duplicate invoices for the same period prevented | Available | |
| Invoice printing and e-mail notice | Partial (Enterprise) | Print/PDF from the browser and a "new invoice" e-mail exist; no PDF generated on the server and no attachment |
| Automatic periodic invoicing | Not available (phase 6) | The period field exists, the scheduler does not |
| e-Invoice / e-Archive (GİB, Turkish Revenue Administration) integration | Not available | May be required for sales in Turkey |
| Vendor invoice reconciliation (dispute) | Not available (phase 6) | |
| Payment gateway integration | Not available (phase 6) | |

## Reporting and monitoring

| Feature | Status | Note |
|---|---|---|
| CDR search | Available | In PostgreSQL for now |
| Summary report: calls, ASR, ACD, revenue, cost, margin | Available | Breakdown by client, vendor, prefix, day |
| CDRs streamed to ClickHouse | Available | Through the export queue, at least once; the summary report is read from ClickHouse, the call list from PostgreSQL (optionally from ClickHouse); records archived out of PostgreSQL are served from ClickHouse in the single-record view, the list and CSV, in the same format (T-24) |
| Live call list, hanging up a call | Available | Panel screen and API (`/v1/live/calls`) |
| Live monitor figures (concurrent calls, CPS, ASR, ACD, PDD, NER, breakdown by country/vendor/client) | Available | Panel screen and API (`/v1/live`); counters in the engine's memory, last 60 seconds |
| CDR export (CSV) | Available | All records matching the filter, at most 200,000 per request |
| Scheduled reports | Available | Daily, weekly or monthly summary, plain-text e-mail; no attachment |
| Call trace: storing SIP messages, SIP flow in the call detail, pcap download | Available | The install's own collector (HEP → ClickHouse, 7 days by default); ladder diagram and search by Call-ID in the panel; from a row in live calls to the trace of a call in progress, "Refresh" for the messages so far (T-25); the pcap is generated from the stored messages (not a wire capture); password digests are not stored. Signalling only: no audio recording. A client's rejected calls are traced too (INVITE, rejection, ACK; T-26); requests from unknown sources are not traced ([`docs/en/10-monitoring.md`](10-monitoring.md)) |
| Sending to an external trace collector (HEP/Homer), Prometheus metrics, alerts | Available | `HEP_TARGET`; metrics and rules in [`docs/en/10-monitoring.md`](10-monitoring.md). No ready-made dashboard (Grafana) |
| Fraud detection | Not available (Enterprise) | Extension points ready in the engine (`Adjust`, `OnLeg`) |
| Language of the admin panel: Turkish and English | Available | Turkish by default; switched in the top bar, on the sign-in page and in Settings, opened with `?lang=en`; number and date formats follow the language. API error messages are in English. Enterprise screens and the customer portal are in Turkish for now (ADR-0049, `docs/07-panel.md` (Turkish), "Dil") |
| Releases, upgrades from GitHub, CI | Available | Semantic versions (`v0.1.0` …) as GitHub Releases, notes from `CHANGELOG.md`; the version in the panel, in `GET /v1/session` and in the `softswitch_build_info` metric. `upgrade.sh <version>` fetches the tag and moves to it after a backup; going back uses that backup (`--restore`; the schema only goes forward). CI runs the checks that need no lab on every change; the SIPp, load and install tests run in the lab (ADR-0051, [`docs/en/09-installation.md`](09-installation.md)) |

## Signalling and media

| Feature | Status | Note |
|---|---|---|
| SIP call relay, dialog tracking | Available | OpenSIPS 3.6; lab tests pass |
| Media relayed through the system | Available | rtpengine, user-space mode |
| Topology hiding | Available | Via, Record-Route and Contact are hidden; the Call-ID stays the same |
| Flood and scan protection against unknown sources | Available | Per-address rate limit and automatic blocking; non-call requests from unknown sources get no answer. Not against floods with spoofed source addresses |
| TLS (signalling) | Available | Callers can connect to 5061 over TLS; a vendor whose transport is `tls` is reached over TLS and its certificate is verified. Per-trunk "TLS required" or "TLS and client certificate" rule. The certificate is obtained automatically and loaded without a restart; a chain with intermediate certificates (up to 512 KiB per file) is loaded and presented in full (T-50; renewal with Let's Encrypt not yet observed). The engine checks the name in the vendor's certificate; no calls are sent to a connection whose name does not match |
| SRTP (audio) | Available | With SDES keys. The vendor leg is encrypted if its connection is TLS; the leg to a registered trunk, if the trunk registered over TLS (T-07); the caller leg is as the caller offered it; rtpengine translates between them. A caller may offer DTLS-SRTP (only the negotiation has been tested); it is not offered to vendors |
| Transcoding | Available (single node) | Enabled on the connection or the trunk (off by default): when the codec lists of the trunk and the connection do not overlap, the call is still sent and the audio is transcoded by rtpengine on the same server (both directions, including new offers within the call); the number of calls transcoded at once is limited by `TRANSCODE_MAX` (200), and when it is reached the route is skipped; metric and alert available. The decision is based on the lists, not on the offer (a trunk without a list is not transcoded). The image has G.711, G.722, G.729 (bcg729), Opus, Speex, GSM; AMR/iLBC are decode-only. Only PCMA ↔ PCMU has been tested in the lab (T-09, ADR-0041). A separate transcoding pool is Enterprise |
| Kernel-mode media forwarding | Not available (phase 4) | Requires a Linux server |
| Clustering, high availability | Not available (phase 4) | |
| STIR/SHAKEN | Not available | Required if US traffic is to be carried |
| H.323 | Not available | Assumed out of scope |
