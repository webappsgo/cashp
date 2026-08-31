# Integrations

CasHp talks to a number of external systems: identity providers, certificate
authorities, domain registrars, payment processors, container registries,
metrics collectors, and the well-known protocol endpoints that other software
on the internet expects a host to answer.

Every one of them is optional, and every one has a defined trust boundary. See
[Security](security.md#trust-boundaries) for what each is trusted for and how
each fails.

## Well-known endpoints

CasHp serves the `/.well-known/` namespace itself. Entries are generated from
configuration, not hand-maintained files.

### Enabled by default

| Path | Standard | Source |
|---|---|---|
| `/.well-known/security.txt` | RFC 9116 | `server.contact.security` |
| `/.well-known/llms.txt` | llmstxt.org | Site structure and branding |
| `/.well-known/pgp-key.asc` | — | The security PGP public key, when one exists |
| `/.well-known/acme-challenge/{token}` | RFC 8555 | ACME HTTP-01 validation |
| `/.well-known/change-password` | RFC 8615 | Password-change discovery |

`change-password` is feature-gated: it is served only when user accounts are
enabled.

### Disabled by default

These exist but stay off until the corresponding integration is configured:

| Path | Purpose |
|---|---|
| `/.well-known/webfinger` | Fediverse and account discovery |
| `/.well-known/openid-configuration` | OIDC provider metadata |
| `/.well-known/assetlinks.json` | Android app links |
| `/.well-known/apple-app-site-association` | iOS universal links |
| `/.well-known/mta-sts.txt` | SMTP MTA-STS policy |

### Contract

- **`GET` and `HEAD` only.** Any other method returns `405`.
- **Unknown names return `404`**, never a directory listing and never an empty
  `200`.
- **Root-level shortcuts are not served.** `/security.txt` is a `404`; only the
  `/.well-known/` path is canonical.
- **Each entry has a fixed content type** appropriate to its standard —
  `text/plain` for `security.txt` and `mta-sts.txt`, `application/json` for the
  app-association files.

### Override order

For each entry, the first source that yields content wins:

1. A file placed in the data directory's well-known override path
2. Content stored in configuration
3. The generated default

An override lets you publish, for example, a hand-signed `security.txt`
without giving up generation for every other entry.

Administration is at `config/web/well-known` in the admin panel, with a preview
endpoint per entry. See [Admin Panel](admin.md#admin-api).

## Identity providers

External identity is optional; local accounts always work. Three protocols are
supported, each configurable with multiple providers.

| Protocol | Admin page |
|---|---|
| OIDC | `config/security/auth/oidc` |
| LDAP | `config/security/auth/ldap` |
| SAML | `config/security/auth/saml` |

External authentication flows live under the `/server/auth/*` route scope,
separate from the local login form. A successful external authentication
produces the same session type as a local login, with the same lifetime, idle
timeout, and cookie flags described in [Security](security.md#sessions).

!!! note "External identity does not bypass 2FA"
    If an account requires a second factor, it is still required after the
    external provider returns. The provider authenticates *who you are*; it does
    not decide the platform's authentication policy.

## Certificates (ACME)

Let's Encrypt is the built-in certificate authority, using the HTTP-01
challenge served from `/.well-known/acme-challenge/`.

Configuration lives under `server.ssl` — see
[Configuration](configuration.md#serverssl). Minimum protocol version is TLS
1.2; HSTS is available with a one-year `max-age`.

Let's Encrypt is trusted **only** to issue domain-validated certificates for
domains you already control. If issuance fails, the site keeps its existing
certificate, or falls back to HTTP with a warning, and issuance is retried on
schedule. Certificate renewal is one of the built-in scheduled tasks.

## Domain registrars

Registrar APIs are used for two things: setting nameservers and reading or
writing DNS records for domains CasHp manages.

CasHp holds registrar credentials scoped to exactly that and no more — never
account-wide or billing-capable credentials. This is a deliberate design
decision: minimal registrar scope limits the blast radius if the credential
leaks.

When a registrar API is unreachable, DNS changes queue and retry and the
operator is alerted. There is no fallback to an unauthenticated path.

## Payment providers

Payment processing and subscription billing run through external providers.

- **CasHp never stores raw card or bank data.** PCI scope stays with the
  provider.
- **Webhooks must be signature-verified.** Billing state changes only on a
  verified provider event — an unsigned or badly-signed webhook is rejected,
  which is what closes off fake-webhook and replay fraud.
- **A transient billing failure never suspends service.** Events queue for
  retry rather than triggering a suspension the tenant did not earn.

Billing sells quotas, not features: every tenant runs the same code with the
same security stack, differing only in resource limits.

## Container registries

Docker Hub and any custom or private OCI registry can be used as an image
source for tenant workloads.

Custom registries are **untrusted by default**. A tenant pointing at their own
registry is extending trust on their own behalf, for their own workloads — and
the pulled image runs inside the same per-tenant isolation as any other
workload. CasHp does not vet image contents.

## Third-party OS package repositories

Several managed services need packages that are not in a distribution's base
repositories — multi-version PHP, current Docker, current Incus. CasHp adds
those repositories itself.

!!! warning "CasHp never runs a vendor install script"
    No `curl | bash`, ever. CasHp writes the repository definition and the GPG
    key itself, with the expected key fingerprint compiled into the binary.

Signature verification is never disabled to work around a key problem. If a
repository's key does not match, that feature's setup step fails with a clear
message — and unrelated install steps continue. See
[Installation](installation.md#third-party-repositories-cashp-adds) for the
per-distro table.

## Metrics and observability

Metrics are exposed at `/server/metrics` with an optional per-service path
segment, plus a root-level `/metrics` alias enabled by default.

| Service | Path |
|---|---|
| Prometheus | `/server/metrics/prometheus` |
| Grafana | `/server/metrics/grafana` |
| Loki | `/server/metrics/loki` |

Each has its own scraper token under `server.metrics`. All three tokens are
empty by default, and an empty token means the endpoint returns `403` — metrics
are not published to the world until you set a token deliberately.

Loki log shipping defaults to batches of 1000 entries with a 1-hour retention
window on the exposure side.

## GeoIP

A GeoIP feed supplies IP-to-location data for country rules, unusual-access
alerts, and analytics. ASN and country lookups are enabled by default; city
lookup is not.

The database refreshes weekly via the scheduler. A stale or missing GeoIP
database degrades gracefully — it never blocks hosting, and it never becomes a
reason a legitimate tenant cannot reach their own control panel.

Configuration is under `server.geoip`; the admin page is
`config/network/geoip`.

## Outbound mail

CasHp sends transactional mail — verification, password reset, alerts, invoices
— through SMTP configured under `server.notifications`.

Leaving `host` empty makes CasHp autodetect a local MTA, which is what the
first-run flow does. Port `587` with `tls: auto` is the default for an external
relay.

## Anonymous networks

Tor onion services and I2P eepsites are both supported and both disabled by
default.

| Network | Configuration | Admin page |
|---|---|---|
| Tor | `server.tor` | `config/network/tor` |
| I2P | `server.i2p` — SAM bridge at `127.0.0.1:7656` | `config/network/i2p` |

## Related

- [Configuration](configuration.md) — every key referenced above
- [Security](security.md) — trust boundaries and failure modes
- [API Reference](api.md) — authentication and response shapes
