<!-- English version of SECURITY.tr.md; the Turkish document is the source and both are updated together. -->
# Security policy

> English version of [`SECURITY.tr.md`](SECURITY.tr.md) (Turkish), which is the authoritative text; the two are updated together.

## Reporting a vulnerability

**Do not report security vulnerabilities as public issues, pull requests or discussions.** Report them privately instead:

- GitHub's private vulnerability reporting: the **Report a vulnerability** button on the repository's **Security** tab. The report is visible only to the project maintainers.

If possible, your report should include:

- the affected component and version (commit),
- the type of vulnerability and its impact,
- steps to reproduce it (configuration, SIP messages, a SIPp scenario, etc.),
- any workaround you know of.

Do not send secrets, passwords, customer data or CDRs taken from real systems; reproduce the problem with lab data.

## Process

1. We acknowledge that we have received the report.
2. We verify the vulnerability and assess its impact.
3. We prepare a fix and agree a disclosure date with you.
4. Once the fix is released, we announce the vulnerability, with your name if you wish.

## Scope

This repository (the Community edition). OpenSIPS and rtpengine are separate projects; report vulnerabilities in them to those projects. For the security of the installation itself, see [`docs/en/09-installation.md`](docs/en/09-installation.md) ("Network-level protection", "Known limits") and [`docs/08-cagri-arayuzu.md`](docs/08-cagri-arayuzu.md) (Turkish, "Kimlik doğrulama", authentication): components such as the SIP core's management socket, Redis and NATS have no authentication and rely on the assumption of a private network.
