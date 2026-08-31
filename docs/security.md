# Security

CasHp's security posture is deliberately not a set of toggles. The platform's
defenses are always on, identical for every tenant regardless of billing tier,
and not weakenable by a tenant for their own account — because a tenant who
weakens their own defenses endangers everyone sharing the node.

This page describes how the platform is defended, what it trusts, what it does
not, and how to report a vulnerability.

## Roles and authorization

| Role | Scope |
|---|---|
| **Global admin** | The whole server: all tenants, all nodes, all configuration |
| **Account admin** | One account or organization: its users, resources, and quotas |
| **End user** | Only their own resources within an account |

Every resource access carries a mandatory tenant-scoped authorization check.
There is no implicit trust elevation: an end user granted access to one narrow
service — a single mailbox, say — has no path to account-admin or global-admin
capability.

The primary global admin created during setup is tamper-proof; its primary
flag cannot be changed or revoked through the UI or API by anyone, including
other global admins. Anonymous self-service registration never produces
anything but an ordinary end user.

## Running as root

!!! danger "This is an intentional, documented exception"
    The CasHp server process requires elevated host privileges to manage VMs
    (libvirt/KVM), containers (Docker/Incus/Podman), and system services
    (mail, DNS, firewall). This is a **non-negotiable exception** to
    least-privilege-by-default.

    The mitigation is internal: strict RBAC plus per-tenant isolation
    (network, filesystem, resource limits) — not running the server
    unprivileged, which would make it unable to do its job.

Related and equally intentional: **CasHp runs untrusted tenant code by
design.** PaaS builds, container workloads, and VM guests are arbitrary code
execution by definition. That is a product feature, not a vulnerability. The
control is isolation, never content review.

## Authentication

### Sessions

| Audience | Cookie | Lifetime | Idle timeout |
|---|---|---|---|
| Admin | `admin_session` | 30 days | 24 hours |
| User | `user_session` | 7 days | 24 hours |

Cookies are `HttpOnly`, `SameSite=strict`, and `Secure` whenever the request
arrives over HTTPS. Sessions extend on activity by default. Admins and end
users are separate account types with separate tables and separate cookies —
an admin is not a user with a flag.

Every form is CSRF-protected; a failed check returns `403` with error code
`CSRF_FAILED`.

### Multi-factor

TOTP and passkeys are both supported, configurable per account. Organizations
can require 2FA of their members via `server.orgs.members.require_2fa`.

The 2FA step runs on a partial session — issued after the password check and
before the second factor — so a correct password alone never yields a usable
session.

### External identity

OIDC, LDAP, and SAML providers are all supported. See
[Integrations](integrations.md#identity-providers) for the route surface.

### Tokens

API tokens are `{prefix}_{32 alphanumeric}` with `adm_`, `usr_`, `org_`, or
the compound agent prefixes. Only a SHA-256 hash is stored; the first 8
characters are retained for display; the full value is shown once at creation
and never recoverable.

Scopes are `global`, `read-write`, or `read`, with optional expiration.

## Anti-enumeration

Several behaviors exist specifically to keep an attacker from learning who
exists on the platform:

- A failed login never reveals whether the username exists.
- A username-availability probe returns a generic "unavailable" for a name
  that is taken. Only *blacklisted* names get the distinct "reserved" message
   — so "taken" and "reserved" are not distinguishable for real accounts.
- Password-reset requests never hint at whether the email address is
  registered, and are throttled to 3 per hour.
- There is no "forgot password" link on the admin login page at all; admin
  passwords are reset from the CLI.

### Username tombstoning

A deleted tenant's username is **never** released for reuse. Permanent
tombstoning prevents a new registrant from inheriting a former tenant's
identity — including stale inbound email routing and dangling DNS references
that still point at the old name.

## Rate limiting and breach detection

Per-IP sliding windows, all tunable under `server.rate_limit`:

| Class | Limit | Window |
|---|---|---|
| Read | 120 requests | 60s |
| Write | 10 requests | 60s |
| Health | 120 requests | 60s |
| Global burst | 240 requests | 60s |
| Login | 5 attempts | 15 min, then lockout |
| Password reset | 3 | 1 hour |
| Registration | 5 | 1 hour |
| File upload | 10 | 1 hour |

Breach detection sits on top, under `server.security.breach_detection`:

| Detector | Threshold | Response |
|---|---|---|
| Brute force | 10 attempts in 5 minutes | Block for 1 hour |
| Credential stuffing | 50 attempts in 10 minutes | Block |
| Unusual access | Login from a new country | Alert |

## Data sensitivity

Data is classified, and the classification drives how it is stored and
whether it may appear in a log.

| Class | Data | Handling |
|---|---|---|
| **Highest** | Password hashes, 2FA secrets, API and session tokens, database credentials, backup encryption keys, DNSSEC private keys, VM/container root credentials, payment provider tokens | Encrypted at rest, **never logged** |
| **High** | Site and app source, environment variables, mailbox contents, database contents, VM disk images, DNS zone records | Tenant-isolated, access-controlled |
| **Moderate** | Billing and invoice data, support ticket contents | Access-controlled |
| **Low** | Usernames, public site content, resource usage metrics | Still subject to the tombstoning and anti-enumeration rules above |

At-rest encryption uses the key in `server.security.encryption_key`, generated
on first run. Back it up: without it, encrypted fields are unrecoverable.

## Network and perimeter

| Control | Configuration |
|---|---|
| TLS | `server.ssl` — minimum TLS 1.2, optional HSTS, Let's Encrypt HTTP-01 |
| Firewall | nftables, managed by CasHp |
| Intrusion prevention | fail2ban, always on |
| Web application firewall | Platform-controlled, not tenant-configurable |
| Anti-virus | ClamAV, always on |
| IP allow/blocklists | `server.security.allowlist` / `blocklist`, plus scheduled blocklist feeds |
| GeoIP rules | `server.geoip.deny_countries` / `allow_countries` |
| Trusted proxies | `server.trusted_proxies.additional` |

Forwarded-client-IP headers are honored **only** from a configured trusted
proxy. A request from anywhere else is treated as a direct connection, so a
spoofed `X-Forwarded-For` cannot forge a client address for rate limiting or
geo rules.

Blocklist and CVE feeds refresh daily; the GeoIP database refreshes weekly.
See the [scheduler defaults](configuration.md#serverscheduler).

## Multi-tenant isolation

Isolation is required at every layer the product touches:

- Per-tenant containers get isolated networks.
- Per-tenant VMs are fully separate guests.
- Per-tenant databases and mailboxes are unreadable by other tenants.

A tenant must not be able to access, enumerate, or affect another tenant's
sites, containers, VMs, databases, email, or DNS zones through *any* surface —
UI, API, container escape, or shared-network leakage.

Quota enforcement is server-side at every resource-creation path, never
client-trusted. A free-tier tenant cannot bypass quotas to consume capacity
reserved for paid tiers — and, equally, a free-tier tenant runs the same code
with the same protections, just with lower limits.

!!! warning "What isolation does and does not promise"
    Malware scanning covers stored files. It is not, and is not claimed to be,
    a guarantee against a zero-day container or VM escape. Isolation is the
    primary defense; scanning is a supplementary control.

## Trust boundaries

| External party | Trusted for | Failure mode |
|---|---|---|
| Third-party OS package repos | Package delivery only, GPG-key-pinned | The specific feature's setup step fails with a clear message; signature checking is never disabled and unrelated install steps are not blocked |
| Let's Encrypt (ACME) | Domain-validated certificate issuance only | Site keeps its existing certificate or falls back to HTTP with a warning; issuance retries on schedule |
| External DNS registrars | Nameserver and DNS-record read/write via API | DNS changes queue and retry, operator is alerted; never falls back to unauthenticated access |
| Payment providers | Payment processing and webhook billing events only | Events queue for retry; service is never suspended for a transient billing-check failure |
| Custom/private OCI registries | **Untrusted** | Pulled images run inside the same per-tenant isolation as any other workload; contents are not vetted |
| Tenant-submitted source, containers, VM images | **Untrusted** | Contained by isolation, not by static review |
| GeoIP feed | IP-to-location data only | A stale or missing database degrades gracefully and never blocks hosting |

CasHp never stores raw card or bank data — PCI scope stays with the payment
provider — and never holds registrar credentials beyond the DNS API scope it
needs. Payment webhooks require signature verification; billing state changes
only from verified provider events, which is what closes off fake-webhook and
replay fraud.

## Public security endpoints

| Endpoint | Purpose |
|---|---|
| `/.well-known/security.txt` | RFC 9116 security contact, generated from `server.contact.security` |
| `/.well-known/pgp-key.asc` | Public key for encrypted reports, published when a security PGP keypair exists |
| `/.well-known/change-password` | RFC 8615 password-change discovery for password managers |

`/security.txt` at the site root is deliberately not served and returns `404`.
The `/.well-known/` namespace is documented in full under
[Integrations](integrations.md#well-known-endpoints).

## Audit logging

`audit.log` is JSON, rotated daily, and records four event classes by default:

| Class | Default |
|---|---|
| `authentication` | on |
| `configuration` | on |
| `security` | on |
| `tokens` | on |
| `data_access` | off |

`data_access` is off because of its volume; enable it when a compliance regime
requires it. `security.log` is separately maintained in JSON with monthly
rotation.

## Reporting a vulnerability

Send reports to the address published in your deployment's
`/.well-known/security.txt`, which is `server.contact.security` — by default
`security@{fqdn}`, following RFC 2142.

For vulnerabilities in CasHp itself rather than in a specific deployment, use
the [project's security contact](https://github.com/webappsgo/cashp/security).
Please do not open a public issue for an unpatched vulnerability.
