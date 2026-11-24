# Changelog

All notable changes to Telrics Switch Community are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html): `vMAJOR.MINOR.PATCH`. Until 1.0.0 a minor version may change settings or the API in ways that need attention when upgrading; such changes are listed under "Upgrade notes".

Each release is a tag of this repository and a [GitHub Release](https://github.com/peritotr/telrics-switch/releases) whose notes are the section below. Upgrading an installation: `./deploy/prod/upgrade.sh <tag>` ([`docs/en/09-installation.md`](docs/en/09-installation.md#releases-and-upgrades)).

## [Unreleased]

## [0.1.0] - 2026-11-24

The first public release: a Class 4 SIP softswitch that runs on one server with one account.

### Added

- **Routing:** longest-prefix match, LCR and priority ordering with loss protection, weighted distribution, failover by SIP response code, routes and rates by day, time and public holiday, number manipulation per vendor connection and client trunk, caller ID rules (`P-Asserted-Identity`, `Privacy`), route simulation.
- **Rating and balance:** sell and buy tariffs (increments, connection fee, free seconds, effective dates, CSV upload, millions of rates reloaded incrementally), real-time balance reservation in 60-second steps, bundled minutes, multiple currencies with exchange rates (optionally from the European Central Bank), low-balance warnings; money is an integer everywhere.
- **SIP and media:** OpenSIPS and rtpengine driven over the network by the Go route-engine; trunks identified by address or Digest, registration and NAT traversal, concurrent-call and CPS limits, topology hiding, TLS and SRTP with certificate name checks, registration towards vendors, OPTIONS health checks, re-INVITE, offerless INVITE, early media, PRACK, T.38 and SIP INFO pass-through, dead-call teardown, IPv6, transcoding on a single server.
- **Call records and monitoring:** a CDR per attempt over NATS JetStream with an on-disk journal, PostgreSQL as the record and ClickHouse as a copy, ASR/ACD/PDD/NER and margin reports, CSV export, scheduled report e-mails, live monitor and live calls, call trace with ladder diagram and pcap, Prometheus metrics and alert rules, application-level firewall.
- **Management:** REST API with a generated OpenAPI description, admin panel in English and Turkish, API keys and panel users with two roles, two-step sign-in, audit log.
- **Installation:** single-server install with Docker Compose (`deploy/prod/`): install, upgrade, backup and restore scripts, optional HTTPS front with automatic certificates for the panel and SIP, optional Prometheus and Alertmanager, sample nftables rules.
- **Releases:** the version is built into the services and the panel; the API reports it in `GET /v1/session`, every service as the `softswitch_build_info` metric, and the panel at the foot of its menu. `upgrade.sh <tag>` and `install.sh --version <tag>` fetch a release from this repository. Continuous integration checks formatting, vet, build, unit tests and the panel build on every push and pull request.

### Upgrade notes

- An installation copied to the server without `.git` moves once to a clone of this repository before `upgrade.sh <tag>` can be used; the steps are in the installation guide ("Moving an installation without git").

[Unreleased]: https://github.com/peritotr/telrics-switch/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/peritotr/telrics-switch/releases/tag/v0.1.0
