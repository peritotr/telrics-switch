<!-- English version of docs/04-acik-cekirdek-modeli.md; the Turkish document is the source and both are updated together. -->
# The open-core model

> English version of [`docs/04-acik-cekirdek-modeli.md`](../04-acik-cekirdek-modeli.md) (Turkish), which is the authoritative text; the two are updated together.

The product comes in two editions: the free and open-source **Community** edition (this repository, single node) and the paid **Enterprise** edition (multiple nodes; target 20,000 concurrent calls).

## Principle: the editions differ by capability, not by numbers

A numeric limit cannot be enforced in open-source code: whoever deletes the line that sets it and rebuilds is rid of it. The two editions are therefore separated by **capability**:

- Community is **single-node** and **single-account**.
- The **clustering** code that makes high capacity and continuity possible, and multi-account capabilities such as the reseller hierarchy, are in Enterprise.

## The split

| Capability | Community | Enterprise |
|---|---|---|
| Routing (prefix, LCR, failover, number manipulation) | ✓ | ✓ |
| Rating and billing (tariffs, prepaid/postpaid, balance reservation) | ✓ | ✓ |
| Call and CPS limits, CDRs, basic reports | ✓ | ✓ |
| REST API and admin panel | ✓ | ✓ |
| Audit log, two-step verification | ✓ | ✓ |
| Single-server install | ✓ | ✓ |
| Multiple SIP nodes, dialog replication, seamless takeover | | ✓ |
| Media pool management (multiple rtpengine instances, region selection) | | ✓ |
| Database HA installation automation | | ✓ |
| Quality-based routing (ASR/ACD/PDD) | | ✓ |
| Fraud detection | | ✓ |
| Reseller hierarchy, sales and purchase invoices, customer portal | | ✓ |
| DID providers and DID pool | | ✓ |
| Transcoding pool management | | ✓ |
| SSO, fine-grained permissions | | ✓ |
| Support and SLA | | ✓ |

Principle: Community must be a genuinely usable product on its own. Taking billing out of Community would make the product useless, and no community would form around it.

## Licence

Community is published under **AGPL-3.0-only** (ADR-0007). Because the same code is also the basis of the Enterprise edition, outside contributions require a contributor licence agreement (CLA) ([`CONTRIBUTING.md`](../../CONTRIBUTING.md)).

### Relation to the GPL components

- OpenSIPS is licensed under GPLv2, rtpengine under GPLv3.
- Our own services (Go) talk to them **over the network** (HTTP, the ng protocol, MI); they are separate processes, are not linked to them, and copy none of their code (ADR-0009).
- A C module written for OpenSIPS, or a change made to rtpengine, becomes GPL and must be published. **No Enterprise-specific logic is ever put inside these components.**

## Code organisation

- **Two repositories:** the open `core` (all of Community, this repository) and the private `enterprise`.
- `enterprise` imports `core` as a Go module and plugs into the extension points `core` defines (`pkg/ext`: `ClusterCoordinator`, `MediaPoolSelector`, `LicenseProvider`; the `pkg/api` extension points; in the engine, `engine.Config.Adjust` to take part in route ordering and call admission, and `OnLeg` to listen to attempt results). List: [`docs/en/ARCHITECTURE.md`](ARCHITECTURE.md) §12.
- `core` contains the single-node / empty defaults of these interfaces; it knows nothing of `enterprise` and never imports it.
- The Enterprise binaries (`route-engine`, `api`, ...) are built in the `enterprise` repository: core plus extensions.
- The same approach for the user interface (web): Enterprise screens are written in a separate package and added to the panel with `createPanel(...extensions)`; no trace of Enterprise appears in the Community panel.

## The split as implemented (2026-11-21)

The reseller hierarchy, invoicing and DID code are in the `enterprise` repository.

- `core` is single-account: every record belongs to the root account, and an API key sees only its own account's records. Clients, vendors, tariffs, routing, CDR rating, payments, the account ledger and reports are in `core`.
- `enterprise` replaces this visibility rule with "its own subtree"; it adds the reseller, DID and invoice resources, endpoints and tables, and hooks into CDR rating for the reseller chain and DID pricing.
- The Enterprise schema is applied on top of the `core` schema with separate migration files (prefixed `ent_`). The Enterprise binary runs both in order.
- `core`'s Go packages live under `pkg/` rather than `internal/` so that Enterprise can import them. The exported surface of `pkg/api` is the contract between the two repositories.
