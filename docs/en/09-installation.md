<!-- English version of docs/09-kurulum.md; the Turkish document is the source and both are updated together. -->
# Installation, upgrades and backups (single server)

> English version of [`docs/09-kurulum.md`](../09-kurulum.md) (Turkish), which is the authoritative text; the two are updated together.

The platform is installed on a single Linux server with Docker Compose. The files are in `deploy/prod/`; the images are built on the server from this repository (there is no registry of ready-made images).

> **Status.** The scripts and images have been tried end to end on a private Docker network (`make prod-smoke`). The production layout (the server's own network, `docker-compose.host.yml`) has **not yet been run** on a real Linux server; the first installation will also be the first trial of this layout. High availability (a second server) is not covered by this guide (roadmap A2).

## What gets installed

| Service | Job | Network |
|---|---|---|
| `opensips` | SIP core (5060 UDP, 5061 TLS) | the server's network |
| `rtpengine` | Media relay (default 30000–39999 UDP) | the server's network |
| `route-engine` | Call decisions | the server's network, 127.0.0.1 only |
| `api` | Management API and panel; updates the schema at start-up | the server's network, 127.0.0.1:58080 only |
| `rater` | Rates call records, copies them to ClickHouse | Docker network |
| `trace-collector` | Writes the signalling copied by the SIP core (HEP) to ClickHouse; the call trace is read from here | the server's network, 127.0.0.1:59060/UDP only |
| `postgres`, `redis`, `nats`, `clickhouse` | Data | Docker network; exposed on 127.0.0.1 only |
| `caddy` (optional) | HTTPS for the panel (80, 443) | the server's network |

The three services on the call path do not go through Docker's address translation: media ports are not published one by one, and the services talk to each other over 127.0.0.1. The engine's call interface is protected by a secret that the installation generates (`ENGINE_TOKEN`): the SIP core and the API send this secret, and a request without it gets 401. The secret travels unencrypted on the network; the SIP core's management socket (and the XML-RPC gate on its metrics listener, `/RPC2`) and rtpengine's control socket have no authentication at all. That is why all three listen on 127.0.0.1 only (`docs/08-cagri-arayuzu.md` (Turkish), "Kimlik doğrulama").

All services run with `restart: unless-stopped`: a service that crashes, and all of them after the server reboots, start again by themselves. If a worker process in the SIP core dies, the core shuts down and Docker restarts it; calls in progress are dropped, and new ones are accepted again within a few seconds (tried in the smoke test).

## Requirements

- Linux (x86-64 or arm64), Docker Engine and the `docker compose` plugin, `git`, `openssl`, `curl`.
- Ports that must be open to the outside in the firewall: 5060/UDP, 5061/TCP, the media port range/UDP; 80 and 443/TCP if the panel is to be served over HTTPS. No other port should be open to the outside. Sample rules and a rate limit against floods: "Network-level protection" below.
- If clients connect from behind address translation (NAT), the firewall in front of the server must allow packets **from every source address and every source port** to 5060/UDP (5061/TCP for TLS) and to the media port range: the outside address of such a client is not known in advance, and its source port will not be 5060. A stateful firewall is enough for the return traffic; with a stateless firewall, UDP from the server to every address and port must also be allowed. The firewall must not drop the UDP mapping between the server's 5060 and the client sooner than the registration interval (`REG_NAT_EXPIRES`, 60 s). SIP ALG must be turned off on the client's router: when it is on, it rewrites messages in its own way and the result differs from device to device.
- Kernel settings for heavy traffic (the installation script does not touch system settings; the server's administrator does this): `net.core.rmem_max` and `net.core.wmem_max` (e.g. 16777216), `net.core.netdev_max_backlog`, and, if `nf_conntrack` is in use, turning off tracking for the SIP and media ports (the sample rules do this: "Network-level protection"). These values will be settled by measurement in the Linux load test (A1).

## First installation

Releases are tags of the public repository (`v0.1.0` …; list and notes: https://github.com/peritotr/telrics-switch/releases). Clone the repository at that release:

```bash
git clone --branch v0.1.0 https://github.com/peritotr/telrics-switch.git /opt/softswitch && cd /opt/softswitch
```

The installation directory must stay a git clone: later upgrades (`upgrade.sh <version>`) fetch the tag into it. In an existing clone, `install.sh --version <version> …` first fetches that release and checks it out, then installs.

```bash
./deploy/prod/install.sh --ip <server address>
```

Options:

- `--ip` — the address SIP is served on. If the server's outside address is directly on the server, that address.
- `--advertised-ip` — if the server is behind one-to-one address translation (most cloud providers): `--ip` is the private address and `--advertised-ip` the outside address. **This layout has not been tried.**
- `--sip-domain` — the name clients and vendors connect to over TLS. If given, a certificate for this name is obtained and renewed automatically (see "SIP certificate" below).
- `--panel-domain` — the panel is served over HTTPS under this domain name; Caddy obtains and renews the certificate itself (the domain must point to the server, and 80 and 443 must be open). If not given, the panel is only on `127.0.0.1:58080` and you need to put your own HTTPS proxy in front of it. Do not expose the panel unencrypted to the outside.

The script creates `deploy/prod/.env` (readable by its owner only), generates the database passwords, the engine's secret (`ENGINE_TOKEN`) and the first API key and writes them there (it prints none of them), builds the images (the first build takes a few minutes because it compiles the SIP core from source) and starts the services. Running it again is safe: existing settings and secret values are kept.

The first API key is `BOOTSTRAP_API_KEY` in `.env`. Use it to create the first panel user ([`docs/en/06-api.md`](06-api.md), `POST /v1/users`), then sign in as that user for daily work.

### Settings

`deploy/prod/.env` (template: `env.example`). After changing it, `docker compose up -d` (in the same directory) restarts the services that changed.

| Setting | Meaning |
|---|---|
| `SIP_IP`, `SIP_ADVERTISED_IP`, `RTP_INTERFACE` | Addresses (above) |
| `SIP_IP6`, `SIP_ADVERTISED_IP6` | IPv6 (optional; T-28). When `SIP_IP6` is given, the SIP core also listens on this IPv6 address on the same ports (5060/UDP, 5061/TLS); `SIP_ADVERTISED_IP6` only if the IPv6 address is translated too. Written without square brackets (`2001:db8::10`). For media, the same address is added to `RTP_INTERFACE` with a semicolon: `203.0.113.10;2001:db8::10` (behind translation `10.0.0.5!203.0.113.10;2001:db8::10`); the relay can then connect a caller in one family to a vendor in the other. If both are empty (the default), the installation is IPv4 only, as before. For the connection of a vendor that is on IPv6 only, write an address, not a name: media towards a vendor defined by name is offered in the caller's family (`docs/08-cagri-arayuzu.md` (Turkish), "IPv6"; ADR-0047). After changing it, `docker compose up -d`: the SIP core and the relay restart, **calls in progress are dropped**. **Not tried on a real server** (only on the lab's IPv6 network) |
| `RTP_PORT_MIN`, `RTP_PORT_MAX` | Media port range. Every call uses four ports; for 20,000 concurrent calls the range must be at least 80,000 ports, so one address is not enough (media pool, Enterprise). The ports of a call that has ended are released at once (`delete-delay = 0`; the relay's default holds them for 30 s, and at 200 CPS that alone would keep 24,000 ports). If no port is left, a new call is rejected with 503 (ADR-0030) |
| `SIP_REALM` | Digest realm. If it changes, every trunk password issued so far becomes invalid |
| `ENGINE_TOKEN` | The secret the engine expects from its callers (the SIP core, the API, your scripts); the installation generates a random value of 48 hex digits. Letters, digits and `- . _ ~ + / =` only. When you query the engine yourself: `curl -H "Authorization: Bearer $ENGINE_TOKEN" http://127.0.0.1:58090/v1/stats`. To change it, write the new value and run `docker compose up -d`: the engine, the SIP core and the API restart, **calls in progress are dropped**. If left empty, the engine answers everyone and writes a warning at start-up; do not leave it empty |
| `UDP_WORKERS`, `OPENSIPS_SHM_MB`, `RTP_THREADS` | Capacity settings; to be determined by load testing |
| `RTP_TIMEOUT`, `RTP_HOLD_TIMEOUT` | Tearing down a call whose media has stopped (ADR-0027): if no media at all arrives from either side for this many seconds, the media relay has the SIP core end the call, a BYE goes to both sides and the record is written with the real duration. Default 60; for a call on hold (one side `sendonly`/`recvonly`/`inactive`) 3600. At least 1, whole seconds. Also applies to a call ringing with silent early media (183 with SDP, no audio at all): even if the connection's ring timeout is longer, the call is released after this many seconds (ADR-0038); raising it also delays the teardown of dead calls. After changing it, `docker compose up -d rtpengine`: the relay restarts, **the media of calls in progress is cut** (`docs/08-cagri-arayuzu.md` (Turkish), "Ölü çağrı"). The relay reaches the SIP core through the XML-RPC gate on the metrics listener (`/RPC2`); its address is written in the compose file (`RTP_TEARDOWN_URL`) |
| `RTP_KERNEL_TABLE` | `-1`: media is relayed in user space. For in-kernel forwarding the `xt_RTPENGINE` module must be installed on the server and the container must run privileged; **this is not ready in this installation and has not been tried** (together with A1) |
| `TLS_CA_LIST` | The root certificate file used to verify vendor certificates. If empty, the public roots the system trusts. The engine checks the name in the certificate with the same roots; `PEER_VERIFY=off` turns the check off (`docs/08-cagri-arayuzu.md` (Turkish), "Şifreleme") |
| `REG_TIMER_SEC`, `REG_RETRY_SEC` | The switch's registration with vendors (if "Registration" is on for the connection; ADR-0035), seconds: how often the SIP core looks at the registrations (30; at least 10) — a registration is renewed this long before it expires; and the time until a failed registration is retried (60; fixed interval). Both are given to the SIP core. The registration list is fetched from the engine every `REG_SYNC_SEC` (10) seconds; the engine reads the status every `REG_STATUS_SEC` (10) seconds. Because the list carries vendor passwords, the SIP core's `/run/opensips` directory is kept in memory (tmpfs) in the compose file (`docs/08-cagri-arayuzu.md` (Turkish), "Sağlayıcıya kayıt") |
| `REG_MIN_EXPIRES`, `REG_MAX_EXPIRES`, `REG_NAT_EXPIRES` | Duration of trunk registrations (REGISTER), seconds: the shortest (60) and longest (3600) duration a trunk may ask for; the longest duration given to a trunk behind address translation (NAT) or connected over TCP/TLS (60). Such a trunk registers again before this expires, and that keeps the path to it open; if calls do not reach trunks behind NAT, lower `REG_NAT_EXPIRES` (`docs/08-cagri-arayuzu.md` (Turkish), "Kayıt (REGISTER)") |
| `TRANSCODE_MAX` | The number of calls the media relay may transcode at the same time (default 200; 0 = no call is transcoded). Only meaningful for connections and trunks that allow transcoding; when it is full, a route that would need transcoding is skipped and the `TranscodingAtLimit` alert fires. After changing it, `docker compose up -d route-engine`. See "Transcoding" below |
| `TLS_CLIENT_CA` | The root certificate file used to verify certificates presented by callers; needed for trunks that require a client certificate. If empty, no certificate is requested from callers |
| `SIP_DOMAIN` | The name SIP is served under over TLS; its certificate is obtained automatically (needs the `https` profile) |
| `CERTS_DIR` | Certificate directory (default `certs`) |
| `PANEL_DOMAIN`, `PANEL_URL`, `COMPOSE_PROFILES=https` | Serving the panel over HTTPS |
| `CDR_KEEP_DAYS` | How many days PostgreSQL keeps call records (0 = forever). An older record is deleted after ClickHouse has confirmed that it holds it; a record that cannot be confirmed is not deleted but queued for export again. Give the API `CLICKHOUSE_URL`: single records, the list and CSV then read archived records from ClickHouse (archive horizon `cdr_archive`; T-24). In a large installation, `CDR_LIST_SOURCE=clickhouse` reads the whole list from ClickHouse. Do not use it with the Enterprise edition (invoicing and the customer portal read from PostgreSQL) |
| `RELOAD_FULL_SEC` | The engine re-reads rates only where they changed (from the notes of a trigger in the database, ADR-0048); at this interval, in seconds, it reads all rates from scratch (default 300, at most 1,800 is used; `0`: all rates on every load, the behaviour before T-30). With a tariff of 5 million rates, a full read takes ~8 seconds and temporarily needs ~2.7 GiB of memory; size the engine's memory for the largest tariff ([`docs/en/02-capacity-plan.md`](02-capacity-plan.md)). If you wrote to the `rates` table with triggers disabled (e.g. a load with `session_replication_role = replica`), the change becomes visible within this interval; restart the engine if you want it to be visible immediately |
| `CDR_JOURNAL_MAX_MB` | Maximum size, in megabytes, of the on-disk journal where the engine keeps call records that have not yet reached NATS (default 1024; at full load, 1,000 calls per second, about one hour of NATS outage). The journal lives in the `engine-journal` volume (in the container `CDR_JOURNAL_DIR=/var/lib/softswitch/cdr-journal`, written in the compose file); even if the engine restarts or crashes, the records stay there and are sent to NATS again at start-up (ADR-0029). If it fills up, new records wait in memory (lost if the engine stops meanwhile) and the `CallRecordJournalFilling` alert fires. The volume is not backed up: its contents are deleted once they reach NATS. Do not delete the volume (`docker compose down -v` deletes it): any records in it that have not reached NATS would be lost |
| `CDR_LIST_SOURCE` | `clickhouse`: the CDR list is read from ClickHouse (same filters, same order, same fields); on error it falls back to PostgreSQL |
| `COMPOSE_PROFILES=monitoring`, `METRICS_KEEP` | Monitoring ([`docs/en/10-monitoring.md`](10-monitoring.md)) |
| `TRACE_KEEP_DAYS` | How many days the SIP messages of calls are kept (default 7). At full load this is the largest item on disk; for the size estimate see [`docs/en/10-monitoring.md`](10-monitoring.md), "Call trace" |
| `TRACE_TARGET` | If not written, the SIP core copies the signalling to the installation's own collector (call trace on). If left empty (`TRACE_TARGET=`), no copy is sent and no trace is stored |
| `HEP_TARGET` | An external HEP collector (Homer) to which the same copy is also sent, `address:port`. In earlier versions this was the only setting that turned tracing on; now it works in addition to our own collector |
| `HEP_IP` | Address of the socket the SIP core sends its HEP copies from. If empty: 127.0.0.1 when all collectors are on this server (127.0.0.1), otherwise `SIP_IP`. With an external `HEP_TARGET`, give your internal address that reaches that collector; the socket does not process incoming packets but does listen (T-51) |
| `MAIL_FROM`, `SMTP_URL` or `RESEND_API_KEY` | E-mail (optional; `PANEL_URL` is needed too) |
| `SECRET_KEY` | The key that encrypts, in the database, panel users' two-step verification secrets and the sign-in passwords for vendors ("Sign in to vendor" in the connection settings); it is given to the API and to the engine (`route-engine`) with the same value, because the engine reads the password during the call. 64 hex digits (`openssl rand -hex 32`). The installation generates it; on an older installation `upgrade.sh` adds it (secrets stored in clear before keep being readable). If empty, secrets are stored in clear. **Do not change the key, and back it up together with `.env`:** without the key the encrypted secrets cannot be read, those users cannot sign in with a code, and an admin (or you, with the API key) has to reset their two-step verification ([`docs/en/06-api.md`](06-api.md), "Two-step verification"); vendor passwords have to be entered again in the panel as well (until then the authentication challenges of those vendors go unanswered and calls fail over to the next route) |
| `AUDIT_RETENTION_DAYS` | How many days the audit log is kept (default 365; `0` = keep forever). The API process deletes expired entries once an hour ([`docs/en/06-api.md`](06-api.md), "Audit log") |
| `FX_SOURCE`, `FX_URL` | Fetching exchange rates from an external source (T-20, ADR-0036). Empty (the default): all exchange rates are entered by hand. `ecb`: exchange rates whose source is set to "ECB" in the panel are updated from the European Central Bank's daily reference rates, converted to the account's currency; rates entered by hand are not touched. The API fetches them at start-up and once a day after 15:30 UTC; the API must be able to reach `www.ecb.europa.eu` over HTTPS. If they cannot be fetched, the last rates stay, and a warning in the panel and the `ExchangeRatesNotFetched` alert appear. `FX_URL` is another address for the same file (a mirror, a test); only with `FX_SOURCE=ecb` (`docs/08-cagri-arayuzu.md` (Turkish), "Kurların dış kaynaktan alınması") |
| `TRUST_PROXY` | `1`: there is a reverse proxy in front of the API that writes `X-Forwarded-For`; the audit log records the caller's address rather than the proxy's. The `https` profile is such a proxy: it becomes `1` automatically when installed with `install.sh --panel-domain`; if you turned the profile on later or use your own proxy, write it by hand. If the API is reached directly, leave it empty: the header would then be whatever the caller wrote |

### Transcoding

Between a client and a vendor that have no codec in common, the media is transcoded in the media relay on the same server (rtpengine, in user space); it is turned on in the panel in the connection or trunk form under "Advanced settings → Audio codecs → Transcoding" (`docs/08-cagri-arayuzu.md` (Turkish), "Kodek çevirisi"). What the image contains (`deploy/images/rtpengine`, the Debian 12 package `rtpengine-daemon` 10.5 and its dependencies; shown by `rtpengine --codecs`):

| Codec | Status |
|---|---|
| PCMA, PCMU (G.711), G.722, G.723, GSM, Speex, Opus | Transcoded in both directions (FFmpeg `libavcodec`, `libopus`) |
| G.729 / G.729a | Transcoded in both directions: Debian's `libbcg729` package (Belledonne Communications' open-source codec, GPL-3) enters the image as a dependency of the relay. The G.729 patent pool expired in 2017; assess the legal position of using and distributing it in your country yourself. If you do not want it, do not select G.729 on connections and trunks |
| iLBC, AMR, AMR-WB, EVRC, QCELP | Decode only: cannot be transcoded into this codec (the AMR encoders are licensed and not in the image). A call with transcoding cannot be set up to a vendor that asks for these codecs |
| `telephone-event` (RFC 4733), CN | Relayed; DTMF events pass in a transcoded call too, and are not converted to SIP INFO (ADR-0031) |

CPU: transcoding runs in the relay's user space (on Linux, in-kernel forwarding does not carry a transcoded stream). G.711 ↔ G.711 is cheap; G.729 and Opus encoding are several times more expensive. The default of `TRANSCODE_MAX` (200) is a starting value: tune it on the server with a load test with transcoding on (the transcoding scenario of `make load`; measurement in `docs/TASKS.md` (Turkish), T-09), watching the relay's CPU usage. Separate transcoding servers (a pool) are in Enterprise.

### SIP certificate

The SIP core does not start without a certificate. The installation writes a **self-signed** certificate into `deploy/prod/certs/`; it is used until a real certificate arrives. There are two ways to get a real certificate:

- **Automatically (`--sip-domain sip.example.com`):** the panel's HTTPS proxy (Caddy) obtains a certificate for this name too and renews it before it expires; the `sip-cert` service compares the certificate in Caddy with `certs/` every five minutes and copies it if it changed. The name must point to this server, and 80 and 443 must be open to the outside (the certificate authority verifies the server through these ports). The panel is also served under this name.
- **By hand:** put the certificate (with its intermediate certificates) and the key in place as `certs/opensips.crt` and `certs/opensips.key`. Your own renewal tool (e.g. certbot) can update the same files.

Either way the SIP core is **not restarted**: the files are checked every 30 seconds, a changed certificate is loaded, and calls in progress are not dropped. A certificate that does not match its key or cannot be read is not loaded, and the old one stays in use; what happened is shown by `docker compose logs opensips | grep tls:`. Every file (the chain with its intermediate certificates, the key, the CA lists) must be smaller than 512 KiB; a larger one is rejected in the same way (T-50; in earlier versions the limit was 4 KiB and a Let's Encrypt chain could not be loaded, so the SIP core did not start at all with it). The `TLS_CA_LIST` and `TLS_CLIENT_CA` files are watched in the same way.

When less than 14 days are left before the certificate expires, the `SIPCertificateExpiring` alert fires ([`docs/en/10-monitoring.md`](10-monitoring.md)): the engine connects to the SIP core's TLS port and reads the expiry date.

**Upgrade order:** `upgrade.sh` renews the API and the rater first, then the others: the rater must know every field of the call record the engine will send before the engine does (it rejects a record carrying a field it does not know). Keep to this order when upgrading by hand too.

**Upgrade note (the engine's secret):** on a server installed before this version, `.env` has no `ENGINE_TOKEN`. If it is missing, `upgrade.sh` generates one and adds it to `.env` (without showing the value), and the engine, the SIP core and the API restart with the new value; because the SIP core's image also changed in this version, calls in progress are dropped in any case. During the restart, new calls may get 503 for a few seconds (while the engine has started to require the secret and the SIP core has not been renewed yet). If you upgrade by hand: first add `ENGINE_TOKEN=$(openssl rand -hex 24)` to `.env`, then `docker compose up -d`. In an upgrade done without adding the secret, the services keep running as before, without the secret (no calls are cut), and the engine writes a warning at every start. If you have your own scripts that query the engine directly (e.g. `/v1/stats`), they need to send the secret; `/metrics` stays open, and the Prometheus configuration does not change.

**Upgrade note:** the name in the certificate of vendors reached over TLS is now checked. A connection whose host field holds an IP address and whose certificate is issued to a name gets no calls after the upgrade; fill in the "Name in certificate" field on the connection (or, temporarily, `PEER_VERIFY=off`).

## Releases and upgrades

Releases follow semantic versioning: `vMAJOR.MINOR.PATCH` (the first public release is `v0.1.0`; ADR-0051). A patch release only fixes bugs; until 1.0.0 a minor release may also change settings or the API, and such changes are listed in the release notes under "Upgrade notes". The notes of every release are in the repository's `CHANGELOG.md` and are also published on the release page on GitHub.

**Installed version:** at the foot of the panel's menu; in the API, `version` in the `GET /v1/session` response; in every service's metrics, `softswitch_build_info{version=…}` ([`10-monitoring.md`](10-monitoring.md)); on the server, `git -C /opt/softswitch describe --tags`. The install and upgrade scripts build the images with the clone's version (`git describe --tags --always --dirty`; if a tracked file was changed by hand, the version ends in `-dirty`).

**Upgrading** (in `deploy/prod/`):

```bash
./upgrade.sh v0.2.0
```

The script takes a backup (`backup.sh`), fetches the release tag from the repository and checks that the repository really has it (it stops if it does not, if there is no network, or if a local tag of the same name differs from the repository's; nothing has changed up to that point), checks the clone out at that tag, then runs **the new release's own** `upgrade.sh`: it builds the images, starts the API first, then restarts the other services that changed. Every move is written to `upgrades.log` together with the backup taken before it; at the end the script also prints the command to go back. If tracked files were changed by hand, the script stops before it starts (`git status` shows them); `.env`, `certs/`, `backups/` and `monitoring/alertmanager.yml` are not tracked and stay as they are. There is no automatic upgrade: nothing is fetched unless the script is run.

`./upgrade.sh` without a tag upgrades, as before, to whatever is checked out in the clone at the moment (for those who run `git checkout` by hand).

The script first takes a backup, builds the images (a service that has not changed is not restarted), starts the API first (the API applies the new schema steps; two services starting at the same time wait for each other in turn), then restarts the other services that changed. If the SIP core's image changed, calls in progress are dropped; upgrade at a time of low traffic. Upgrades without interruption are possible with a second server (A2).

**Upgrade note (the call-record journal, T-33):** a new volume for the engine (`engine-journal`) and the settings `CDR_JOURNAL_DIR`, `CDR_JOURNAL_MAX_MB` were added to the compose file; `upgrade.sh` creates the volume by itself, and nothing needs to be added to `.env` (`CDR_JOURNAL_MAX_MB` defaults to 1024 if not written). If records that had not reached NATS were left in the old engine's in-memory queue when it shut down, they are lost as before; after the new engine, none are. At every start the new engine also resends the records of the last minute in the journal; the rater recognises them and does not charge them a second time. The engine now also starts and takes calls without being able to reach NATS.

**Upgrade note (attempt not sent, ADR-0039):** when the media relay does not accept the offer, the SIP core adds the optional `cause` field to the event it sends to the engine. Because the engine ignores unknown fields, the order does not matter: the old engine works with the new SIP core, and the new engine with the old SIP core, as before (the unsent attempt is written to the connection's counters); once both are upgraded, it is not. Nothing needs to be added to `.env`.

**Upgrade note (limits count both directions together, ADR-0042):** a trunk's `max_calls`/`max_cps` limit now counts the calls the trunk makes and the calls delivered to it together; a client's limit likewise counts the calls the client makes and the calls delivered to its trunks. A trunk or client that relied on separate capacity for each direction is limited sooner after the upgrade (e.g. a trunk with `max_calls = 10` that makes 10 calls and receives 10 at the same time stays at 10 in total): review these limits before upgrading. In an installation without delivery (an extension's `routing.ToTrunk` route), behaviour does not change. The counters in Redis move over by themselves: when the new engine starts, it adds the calls in progress in the old `cc:s:<trunk>` sets to `cc:t:<trunk>` and deletes the old set (in the log "moved the counts of N trunks"); the per-second counters drop off within two seconds. Calls that are in progress at the moment of the upgrade and were delivered to a trunk do not appear in the target client's limit until they end. The schema and `.env` do not change; the upgrade order does not matter.

### Going back

**Schema steps only go forward; there are no scripts to undo them.** As it starts, the new release's API applies the new steps to the database; the old release's code may not work with the new schema (it does not know a new column or trigger; it looks for something that was removed). Going back to an earlier release therefore means taking the database back to the backup made just before the upgrade too:

```bash
./upgrade.sh v0.1.0 --restore backups/softswitch-<time>.dump
```

The script first takes a backup of the current state as well, checks out the old tag, builds the old release's images, then takes the database back to the given backup with `restore.sh` and starts the services on the old release. The right backup is named in the command the script prints when the upgrade finishes and in `upgrades.log`. Limits:

- Everything written after the upgrade is lost: call records, balance movements, changes made in the panel, the audit log. Calls in progress are dropped and Redis is emptied. The lost records are in the backup taken before going back; but that backup is in the new schema and cannot be loaded into the old release as it is, only picked apart by hand.
- You go back one release, with the backup taken when leaving it. Going back without a backup (`./upgrade.sh <old version>` alone) works if the schema did not change; if it did, the old API may not start or may misbehave. Do not do it unless the release notes say "schema unchanged".
- Settings the new release added to `.env` (e.g. a secret) stay; the old release ignores them.
- Releases older than `v0.1.0` cannot be reached this way (there is no tag).

### Repository address

Releases are fetched from the address in `SOFTSWITCH_REPO` in `.env`; if it is empty, from the public repository (https://github.com/peritotr/telrics-switch.git). To use a mirror or a fork of your own, put its address there; while there is no `.env` yet (a first installation), give the same name as an environment variable: `SOFTSWITCH_REPO=<address> ./install.sh --version <version> …`. Tags are not signed; to check what was fetched, compare the commit the tag points to (`git -C /opt/softswitch rev-parse v0.2.0^{commit}`) with the one on the release page on GitHub.

### Moving an installation without git

If the installation directory is not a git clone (copied with `git archive`, for example), `upgrade.sh <version>` stops with "is not a git checkout". Such an installation is moved to a clone once, with the steps below. The chosen way leaves the old directory as it is and sets up a clean clone beside it: running `git init` in place would also keep files left in the old tree that the repository no longer has; and the old directory stays available for going back after the move. The data is in Docker volumes; the volumes' names start with the compose project name, and the project name (unless `.env` has `COMPOSE_PROJECT_NAME`) is the directory's name, `prod`: it is the same for both copies, so the data stays where it is. Calls in progress are dropped; do it at a time of low traffic.

Below, the installation is in `/opt/softswitch` and the new release is `v0.1.0`; use your own path and release. Run the commands as the user who installed it.

1. Take a backup and note the project name:
   ```bash
   cd /opt/softswitch/deploy/prod && ./backup.sh
   docker compose ls          # NAME column: prod (or COMPOSE_PROJECT_NAME from .env)
   ```
   Copy the backup file, `.env` and `certs/` off the server as well.
2. Set up the new clone beside the old one:
   ```bash
   git clone --branch v0.1.0 https://github.com/peritotr/telrics-switch.git /opt/softswitch.new
   ```
3. Carry over the settings, certificates and backups (permissions are kept; `.env` must stay readable by its owner only):
   ```bash
   cd /opt/softswitch/deploy/prod
   cp -a .env certs /opt/softswitch.new/deploy/prod/
   [ -d backups ] && cp -a backups /opt/softswitch.new/deploy/prod/
   [ -f monitoring/alertmanager.yml ] && cp -a monitoring/alertmanager.yml /opt/softswitch.new/deploy/prod/monitoring/
   ```
   If `CERTS_DIR` or `BACKUP_DIR` in `.env` points elsewhere, move those too or leave them as absolute paths.
4. Stop the services and swap the directories. `down` removes the containers and does not touch the volumes (the data). **Do not add `-v`: it deletes all data.**
   ```bash
   cd /opt/softswitch/deploy/prod && docker compose down
   mv /opt/softswitch /opt/softswitch.old && mv /opt/softswitch.new /opt/softswitch
   ```
   (The containers keep the old directory's certificate and settings files mounted; an upgrade started from the new directory without `down` would leave the containers that did not change mounted on the old directory.)
5. Start the database and upgrade with the new clone (it takes a backup, builds the images and starts the API first):
   ```bash
   cd /opt/softswitch/deploy/prod && docker compose up -d postgres && ./upgrade.sh
   ```
6. Check: `docker compose ls` shows the same project name and `docker compose ps` every service running; the panel opens and shows `v0.1.0` at the foot of the menu; a test call goes through. The backup line in the scheduler (`/opt/softswitch/deploy/prod/backup.sh`) points to the same path, so it does not change.
7. If all is well, delete `/opt/softswitch.old` a few days later. Until then you can go back; data written after the move is lost:
   ```bash
   cd /opt/softswitch/deploy/prod && docker compose down
   mv /opt/softswitch /opt/softswitch.new && mv /opt/softswitch.old /opt/softswitch
   cd /opt/softswitch/deploy/prod && docker compose build && docker compose up -d postgres
   ./restore.sh backups/softswitch-<time from step 1>.dump --yes
   ```
   Rebuilding the images is required: image names come from the project name, and the ones the new clone built have replaced the old ones. The database goes back to the backup from step 1 because the new release may have moved the schema forward.

From then on, upgrades are done with `./upgrade.sh <version>`.

### Publishing a release (maintainers)

The public repository is produced from the maintainers' source tree; the script that prepares a release (`tools/release.sh`) lives in that tree and is not in the public repository. Steps:

1. In `CHANGELOG.md`, the entries under "Unreleased" move under the new release's heading (`## [0.2.0] - YYYY-MM-DD`, with the link line at the end), the version in `web/package.json` is set to the same (`npm --prefix web version 0.2.0 --no-git-tag-version`), and the change goes into `main`.
2. `tools/release.sh v0.2.0` (`--check` for the checks only): it checks that it runs on a clean `main`, that the version is new and higher than the last one, the changelog section, the panel's version and that the English documents are up to date; it exports the public tree (with its own checks), creates the "Release v0.2.0" commit and the annotated tag `v0.2.0` in the local copy of the public repository and a tag of the same name in the source tree, and writes the release notes (that section of the changelog) to a file. **It sends nothing** and creates no release on GitHub.
3. Once the owner approves, the commands the script printed are run: `git push origin main`, `git push origin v0.2.0` and `gh release create v0.2.0 --repo peritotr/telrics-switch --verify-tag --title v0.2.0 --notes-file <notes>`.
4. CI runs for the tag too (`.github/workflows/ci.yml`); if it is red, the release is not announced and the fix comes out as a new patch release (a published tag is never moved).

CI runs the checks that need no lab on every push and pull request: `gofmt`, `go vet`, `go build`, `go test ./...` (tests that need PostgreSQL, Redis or ClickHouse skip themselves when their addresses are not given), and for the panel `npm ci`, the type check, the message catalogues and the build. The same locally: `make ci`. The SIPp scenarios (`make test-routing`), the API's end-to-end test with the database (`make test-api`), the load test and the install smoke test (`make prod-smoke`) need the lab; they are not in CI and the maintainer runs them before a release.

## Backup and restore

```bash
./deploy/prod/backup.sh
```

Writes a PostgreSQL dump into `deploy/prod/backups/`, checks that it can be read, and deletes those older than 14 days (`KEEP_DAYS`, `BACKUP_DIR`). Add it to the server's scheduler to run every night, for example: `15 3 * * * /opt/softswitch/deploy/prod/backup.sh`.

- Everything that cannot be regenerated is in PostgreSQL: configuration, balances, call records. The copy in ClickHouse is rebuilt from these records on restore; the state of calls in progress in Redis is not backed up.
- **Call traces (SIP messages) are not backed up.** They are only in ClickHouse and are kept for a few days anyway; if ClickHouse's disk is lost, the traces of those days are lost too. A restore does not touch traces.
- **`.env` and `certs/` are not in the backup.** Keep them separately, in a safe place: without `.env` the passwords and the key of the two-step verification secrets (`SECRET_KEY`) are lost, and without the certificate key the TLS identity is lost.
- The audit log is in PostgreSQL and is included in the backup.
- Backups are on the server's own disk. Copying them elsewhere against the loss of the disk or the server is outside this installation and your responsibility.
- Data written after the last backup is lost on restore (there is no point-in-time recovery, i.e. no continuous archiving).

```bash
./deploy/prod/restore.sh deploy/prod/backups/softswitch-<time>.dump --yes
```

Replaces the whole database with the one in the backup: stops the services, drops calls in progress, empties Redis, rebuilds the ClickHouse copy and starts the services. To move to a new server: install on the new server, put the old `.env` and `certs/` files in place and run `install.sh` again, then restore the backup.

## Daily operations

Commands are run in the `deploy/prod/` directory.

- Status: `docker compose ps`
- Logs: `docker compose logs -f opensips` (by service name). Logs are limited to 5 × 50 MB per service.
- Restarting one service: `docker compose restart rater`
- Stopping: `docker compose stop` (data stays). `docker compose down -v` **deletes all data.**

## Trial installation

```bash
make prod-smoke
```

Installs the same images on a private Docker network (`docker-compose.bridge.yml`), under a separate project name and with its own certificate directory (`certs-smoke`), and tries the following: the installation; serving the panel over HTTP and HTTPS; the certificate obtained for the SIP name (here from Caddy's own authority) reaching the SIP core together with its chain and without a restart; generating the engine's secret and rejecting requests with no secret or a wrong one; a real call, its record and its stored trace (messages and the pcap file); recovery after a process dies in the SIP core, and its alert; taking a backup and restoring it (including rebuilding ClickHouse); an upgrade; an installation without a secret getting one on upgrade while calls go on; the clone's version showing in the API and in the engine's metrics. When it finishes it deletes everything it installed. It runs on machines that are not production servers (Docker Desktop included); it is not meant to carry traffic.

## Network-level protection

The installation **does not touch** the server's firewall. This section describes the operating-system layer that the server's administrator applies themselves; sample rules are in `deploy/prod/nftables/` (`softswitch.nft`, `known-sources.sh`). The aim is to drop spoofed-source and high-volume floods (roadmap B5) cheaply in the kernel before they reach the SIP core, and to close a second time the ports that must not be open to the outside. It does not replace the protection at the application level (`pike`, `ban:`, `firewall_rules`; ADR-0017); it is added in front of it (see "Relation to the application level" below). Reflecting the panel's rules into the server's firewall by the product is a separate task (T-27) and has not been done.

> **Status.** The rules are syntax-checked, loaded and tested together with the sync script by `make test-nft`, in a Docker container's own network namespace (requirements: Docker and access from the container to the Debian mirrors; `nftables` is installed inside the container). They have **not been tried** on a real server, with real traffic and a real flood; the rates are not measured but starting values.

### Ports to open

| Port | Protocol | What | Setting |
|---|---|---|---|
| 5060 | UDP | SIP | `SIP_PORT` (in the SIP core's container; default 5060) |
| 5061 | TCP | SIP over TLS | `SIP_TLS_PORT` (default 5061) |
| 30000–39999 | UDP | Media (RTP, SRTP; UDPTL for fax) | `RTP_PORT_MIN`, `RTP_PORT_MAX` (`.env`) |
| 80, 443 | TCP | Panel over HTTPS and obtaining the SIP certificate (Caddy) | only with `COMPOSE_PROFILES=https` |
| 22 | TCP | SSH | the server's own setting |

The SIP core opens only these two sockets (`sip/opensips/opensips.cfg`: `udp:` and `tls:`); there is no plain TCP or WebSocket listener, so do not open ports for them. The SIP and media ports are on the `SIP_IP` and `RTP_INTERFACE` address (if IPv6 is on, also on `SIP_IP6` and the IPv6 address in `RTP_INTERFACE`; the sample rules cover both families); if the server has several addresses, you can narrow the rule to that address too (`ip daddr`, `ip6 daddr`).

### Ports that must not be open to the outside

In the layout on the server's network (`docker-compose.host.yml`), all of these listen on 127.0.0.1 only; the firewall ensures it a second time. Those without authentication are in bold.

| Port | What |
|---|---|
| 58080/TCP | Management API and panel (`API_PORT`); to the outside only through Caddy (443) |
| 58090/TCP | Engine (requires `ENGINE_TOKEN`, but the secret and password digests travel unencrypted on the network) |
| 58091, 58092, 58093/TCP | Metrics of the API, the rater and the call-trace collector |
| **8888/TCP** | The SIP core's metrics and **`/RPC2` (XML-RPC management gate: ends calls)** |
| **8899/UDP** | **The SIP core's management socket (`MI_SOCKET`)** |
| **2223/UDP** | **rtpengine's control socket** |
| 2225/TCP | rtpengine's metrics |
| **59060/UDP** | **HEP input of the call-trace collector** |
| 9061/UDP | The socket the SIP core sends its HEP copies from (`HEP_PORT`). Incoming HEP is not processed (T-51); even so it is opened on 127.0.0.1 only. When a `HEP_TARGET` outside the server is given, it is opened on `SIP_IP`: move it with `HEP_IP` to the internal address facing that collector, or keep it closed in the firewall |
| 56432, **56379**, **54222**, 58123/TCP | PostgreSQL, **Redis**, **NATS**, ClickHouse |
| 59090, 59093, 59100/TCP | Prometheus, Alertmanager, node-exporter (`--monitoring`) |

### Sample rules

`deploy/prod/nftables/softswitch.nft` sets up its own table (`inet softswitch`) and does not touch other rules on the server (Docker's included). The values to change are at the top of the file (`define`): SIP ports, media range, SSH port and the addresses allowed into SSH (`ADMIN_V4`, `ADMIN_V6`; everyone by default, narrow them to your own networks), panel ports. The rates are inside the rules (nftables takes no variables there) and are marked with `RATE:`.

In order:

1. **SIP and media without connection tracking.** Packets arriving at and leaving 5060/UDP and the media range are not written to the connection table (`nf_conntrack`) (`notrack`). A spoofed-source flood uses a new source on every packet; if it were tracked, it would fill the table and drop all connections, SSH included. That is why the SIP and media rules look at the port, not at the state.
2. **The input chain** drops by default (`policy drop`); `lo`, the installation's Docker networks (`docker0`, `br-*`), the return traffic of established connections, necessary ICMP and IPv6 neighbour discovery, limited ping, SSH (10 new connections a minute), 80/443 and the SIP chains below pass. The media range is open to everyone: a caller's media often comes from a different address than its signalling, and the relay ignores packets arriving at a port that no call holds. Everything else (the management ports above included) is dropped; dropped packets show in the counter at the end of the chain.
3. **SIP (UDP).** A source blocked for flooding is dropped; a known address (`known_v4`, `known_v6`; below) passes; 100 packets a second per unknown source (burst of 200), and a source that exceeds it is dropped for two minutes (`sip_banned_v4`/`_v6`); 2,000 packets a second for **all** unknown sources together. The last limit is for spoofed-source floods: each packet gets past the per-source limit with a new source, but all of them together hit this ceiling. Known addresses are not affected by the ceiling because they passed earlier; during a flood, the traffic of clients and vendors keeps getting through.
4. **SIP (TLS).** Only new connections are counted: at most 50 connections at a time per unknown source (the rest are refused) and 10 new connections a second. TCP cannot be set up from a spoofed address; against half-open connection floods `net.ipv4.tcp_syncookies = 1` (the default in most distributions) is enough.

The rates are deliberately looser than the SIP core's own limit (30 requests in 2 seconds for an unknown source, `pike`): the exact limit stays at the application level (where blocks are visible in the panel and can be lifted), and the network level only cuts floods. 100 packets per source may be too tight for a client that signs in with a password with many users from a single address (e.g. a NAT with hundreds of phones behind it): add such an address to the known addresses (`KNOWN_EXTRA`, below) or raise the rate. The ceiling of 2,000 is the upper limit for all new call and registration traffic from unknown sources; if you have many trunks that sign in with a password, adjust it accordingly.

To apply (as root, after changing the values):

```bash
install -d -m 755 /etc/softswitch
cp deploy/prod/nftables/softswitch.nft deploy/prod/nftables/known-sources.sh /etc/softswitch/
nft -c -f /etc/softswitch/softswitch.nft          # check only, first
# Timed rollback: the table is deleted unless this is cancelled within two minutes.
systemd-run --on-active=120 --unit=softswitch-nft-rollback /usr/sbin/nft delete table inet softswitch
nft -f /etc/softswitch/softswitch.nft && /etc/softswitch/known-sources.sh --apply
# Open a new SSH session, try a call; if all is well:
systemctl stop softswitch-nft-rollback.timer
```

To make it permanent, load them in the same order at boot (e.g. `include "/etc/softswitch/softswitch.nft"` and `include "/etc/softswitch/known-sources.nft"` in `/etc/nftables.conf`, `systemctl enable nftables`). **Caution:** the distributions' sample `/etc/nftables.conf` starts with `flush ruleset`; this deletes Docker's own rules too (the containers lose their network until Docker is restarted). Remove that line. The rules file deletes and rebuilds its own table on every load: the known-address sets and the flood blocks are emptied, so `known-sources.nft` must be loaded right after it (`--apply` does this). In "known addresses only" mode, an empty set cuts all SIP.

Also:

- **Ports published by Docker** go through the forward chain, not the input chain; this file does not protect them. In the production layout all published ports are bound to 127.0.0.1; do not publish a port to the outside. These rules do not apply to the trial layout on a private Docker network (`docker-compose.bridge.yml`).
- **In-kernel media forwarding** (`RTP_KERNEL_TABLE` ≥ 0) adds its own rule to iptables' input chain; because this file accepts the media range, the two work together.
- **Ephemeral ports:** the port range Linux picks for outgoing connections (`net.ipv4.ip_local_port_range`, usually 32768–60999) overlaps the media range; a port taken by a program is not available to the relay. Reserve it with `net.ipv4.ip_local_reserved_ports = 30000-39999` (your media range).
- To see the flood blocks and what was dropped: `nft list set inet softswitch sip_banned_v4`, `nft list chain inet softswitch input` (counters). This layer's blocks are not visible in the panel (the SIP core never sees those packets).

### "Known addresses only" mode

When the leading `#known-only ` is removed from the two lines marked `#known-only drop` in the `sip` and `sip_tls` chains, no SIP packet from an address that is not in the known sets passes. This is the strongest rule against floods: random spoofed sources are dropped in the kernel without touching any counter or any process. The cost:

- Trunks that sign in with a password and whose address changes (PBXs and phones behind address translation; those that register included) cannot connect. Add the address of a trunk that signs in with a password from a fixed address with `KNOWN_EXTRA`.
- The vendor's in-dialog requests (BYE, re-INVITE) and responses also go through this set: anything the vendor sends from an address other than the one written on the connection is dropped. A vendor defined by name (when `host` is a name) enters the set with the addresses resolved at sync time; a vendor whose name points to other addresses is dropped until the next sync. The addresses of vendors that spread traffic over other servers with an SRV record are not resolved: add them with `KNOWN_EXTRA`.
- The address of a trunk added in the panel is dropped until the next sync (at the application level it is recognised at once).
- The media range is open to everyone in this mode too (above); you can use the same set for media if you like, but clients whose media comes from another address will then have no audio.

Even if you do not use the mode, the sets are useful: known addresses pass ahead of the rate limit and the ceiling.

**Syncing the sets.** `known-sources.sh` writes the `known-sources.nft` file from the data in the panel: the addresses and networks of all enabled client trunks identified by address (`GET /v1/client-trunks`, `auth_type: "ip"`, `enabled`), the addresses of all enabled vendor connections (`GET /v1/vendor-connections`; resolved if a name), and whatever is in the optional `KNOWN_EXTRA` file (one address or network per line, `#` for comments). The `allow` rules of the panel's firewall do **not** enter the set (they may be time-limited; write them into `KNOWN_EXTRA` if needed); `deny` rules are not reflected in this layer either (T-27).

```bash
# A read-only API key (POST /v1/api-keys, "role": "readonly"),
# in a file only root can read:
install -m 600 /dev/null /etc/softswitch/nft-sync.key   # then write the key into it
# Every minute (cron):
* * * * * root /etc/softswitch/known-sources.sh --apply >/dev/null
```

Settings are given as environment variables: `SOFTSWITCH_API` (default `http://127.0.0.1:58080`), `SOFTSWITCH_API_KEY_FILE` (default `/etc/softswitch/nft-sync.key`), `KNOWN_EXTRA`; the output file is the first argument (default `/etc/softswitch/known-sources.nft`). Requirements: `curl`, `jq`, `getent`, `nft`. The script:

- Writes the key nowhere: it passes it to `curl` on standard input (so it does not appear in the process list either).
- Renews the file only when its content changes (with the same content it does not touch it); `--apply` loads the file in a single transaction (the sets are emptied and filled at the same moment, never left empty in between). Running it repeatedly is safe.
- If anything goes wrong, it leaves the old file and the loaded sets as they are and exits with a non-zero code: if the API cannot be read or the key is wrong, if a vendor's name does not resolve (it says which connection), if a value comes back that does not look like an address, if the list is empty (an empty set cuts all trunks in "known addresses only" mode; if you really want it, `--allow-empty`).
- Resolves names for the address families the server has (a server without an IPv6 address does not get a name's IPv6 addresses); an IPv6 address written as the address on a connection is taken as it is.

### Spoofed source addresses and BCP 38

UDP has no handshake: an attacker can write any address as the packet's source. Every per-source limit (our `pike`, the rate limit above) therefore does not stop a spoofed-source flood, only a single-source one. With a spoofed address, someone else's address can also be pushed into the automatic block (at the application level as well as here; known addresses cannot, because they pass before reaching the block rule).

- **BCP 38 (RFC 2827)** is source address filtering and is applied in the network the packet **leaves**: a network does not let out packets from its own customers with source addresses that do not belong to it. A single server cannot enforce this in other people's networks. What you can do: ask whether your own network (data centre, cloud provider) applies it and filters spoofed-source packets at its edge for you (uRPF).
- **`rp_filter`** (`net.ipv4.conf.all.rp_filter = 1`, strict) checks whether the return path to an incoming packet's source goes out of the same interface. On a server with one interface and a default route, every address on the internet returns through the same interface; so it does **not stop** spoofed sources from the internet. Only on a server with several interfaces does it stop, e.g., a packet carrying an inside network's address but arriving on the outside interface. With asymmetric routing (out and back through different interfaces) loose mode (`2`) is needed. The `fib saddr . iif oif missing drop` line in the rules file is the nftables form of the same check, and is therefore disabled by default.
- **If the bandwidth fills up,** no rule on the server helps: by the time the packet reaches the rule, the line is already full. The protection against this is in front of the server: your provider's flood protection (scrubbing), an access list on the upstream router (only known addresses to 5060: the same set, generated by the same script), temporarily not routing the address under attack (blackhole). Discuss these with your provider in advance.
- What the server itself can do: let known addresses pass ahead of the limits (client traffic continues during a flood), put a total ceiling on unknown ones (the load reaching the SIP core stays limited), keep SIP and media out of the connection table (so the table does not fill up and make the server unreachable) and, if necessary, accept known addresses only. A spoofed packet imitating a known address still passes; the application level (trunk matching, password, limits) deals with it.

### Relation to the application level

The layers do not replace one another; from the outside in:

| Layer | Where | What it does | Visible? |
|---|---|---|---|
| The provider's network | In front of the server | Filtering against the line filling up, BCP 38 | At the provider |
| nftables (this section) | The server's kernel | Closed ports; per-source and total rate limits; optionally known addresses only | `nft list …`; not in the panel |
| `pike` and `ban:` | SIP core | 30 requests in 2 seconds for an unknown source, unanswered for two minutes when exceeded; no answer to out-of-dialog requests | In the panel as "automatic blocks", can be lifted (`docs/08-cagri-arayuzu.md` (Turkish), "Yabancı kaynaklar") |
| `firewall_rules` | Engine (with the snapshot) | The operator's `deny`/`allow` rules; within a minute, without logging into the server | In the panel, in the audit log |
| Trunk matching, password, limits | Engine | Who may make calls | In the CDRs |

To block or allow a single address, use the panel's firewall (it needs no root access, it is logged, and it can be given a duration). Turn to nftables only against volume and to close ports; keep its rates looser than `pike`'s so that the exact decision stays at the application level. Reflecting the panel's rules into the server's firewall (management ports always exempt, changes confirmed within a time limit) is the job of T-27; until that comes, these files are samples and are applied by hand.

## Known limits

- The production layout (the server's network) and an installation behind address translation have not been tried on a real server.
- Obtaining and renewing the SIP certificate from a real certificate authority (Let's Encrypt) has not been tried; what has been tried is the same flow with Caddy's own authority. There is no certificate expiry alert.
- In-kernel media forwarding is not ready; media is relayed in user space, and that limits capacity.
- The SIP core is built with the debugging allocator that catches memory errors; switching to the fast allocator for production will be assessed together with the load test.
- Monitoring is optional (`--monitoring`, [`docs/en/10-monitoring.md`](10-monitoring.md)), and where alerts go is empty until you define it.
- The disk space the call trace will take has not been measured with real traffic; running the collector next to the SIP core (on the server's network) in the production layout has not been tried on a real server.
- The engine's secret is single and shared, and travels unencrypted on the network; the SIP core's management socket, rtpengine's control socket, Redis and NATS have no authentication. All of them must stay open on 127.0.0.1 (or the Docker network) only.
- Single server: if the server goes, the service stops. The engine's call-record journal (`engine-journal`) is on that server's disk: if the disk goes, records that have not reached NATS go too.
- The network-level rules (`deploy/prod/nftables/`) are samples only, and the installation does not apply them; they have not been tried on a real server or with a real flood, and their rates have not been measured. The panel's rules are not reflected in the server's firewall (T-27).
- The Enterprise edition is not installed with these files; it will be prepared separately in the private repository.
