# Config Rules (PART 5, 6, 12)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## Configuration (PART 5)
- Config file is ALWAYS `server.yml` (never `.yaml`); auto-migrate `server.yaml` → `server.yml` on startup
- Location: `/etc/webappsgo/cashp/server.yml` (root) or `~/.config/webappsgo/cashp/server.yml` (user)
- Single instance: server.yml is source of truth. Cluster mode: remote DB is source of truth, server.yml caches a read-only fallback copy
- Default port: random unused port in 64000-64999 on first run, then persisted to server.yml
- Port 80 → enables Let's Encrypt HTTP-01; port 443 → TLS-ALPN-01 + auto SSL
- Boolean env/config values accept a wide truthy/falsy word list (see `src/config/bool.go`) — unrecognized value is an error, never a silent default

## Environment Variables
Runtime (always checked): `NO_COLOR`, `TERM`, `DOMAIN`, `MODE`, `DATABASE_DRIVER`, `DATABASE_URL`, `SMTP_*`.
Init-only (first run only): `CONFIG_DIR`, `DATA_DIR`, `LOG_DIR`, `DATABASE_DIR`, `BACKUP_DIR`, `PORT`, `LISTEN`, `APPLICATION_NAME`, `APPLICATION_TAGLINE`.

URL resolution precedence: `{fqdn}` = Reverse Proxy Headers → `DOMAIN` → `os.Hostname()` → `$HOSTNAME` → Global IP → `localhost`.

## Application Modes (PART 6)
Mode priority: `--mode` flag > `MODE` env > default `production`.
Debug priority: `--debug` flag > `DEBUG` env (truthy) > mode-implied default > `false`.
`MODE=debug` is explicit opt-in only, never implied; it defaults the debug flag on unless overridden.

Six states: Production, Production+Debug, Development, Development+Debug, Debug, Debug+Endpoints.
Debug endpoints/pprof/expvar are 404 unless `--debug`/`DEBUG=true`. Debug NEVER bypasses auth in any mode.

Already implemented: `src/mode/mode.go` (`Resolve`, `ResolveDebug`) — reuse this, do not reimplement mode/debug detection elsewhere.

## Server Configuration (PART 12)

### CRITICAL - NEVER DO
- Never fail startup on invalid config — warn and default
- Never trust `X-Forwarded-*` from a peer outside `trusted_proxies`
- Never send `Onion-Location`, HSTS, or HTTP→HTTPS redirect on Tor/I2P
- Never leak clearnet FQDN, email, or local timezone on a Tor request
- Never expose `admin.email` or any `webhooks.*` URL publicly
- Never auto-populate `abuse@{fqdn}` — opt-in only
- Never let real-IP middleware rewrite `RemoteAddr` before the trust check
- Never store consent state in localStorage — cookie only

### CRITICAL - ALWAYS DO
- Validate every config value; invalid → default + warning, never crash
- Preserve original TCP peer before real-IP rewrite; trust check uses it
- Resolve baseurl: `X-Forwarded-Prefix` → `-Path` → `X-Script-Name` → config → `/`
- Sign outbound webhooks (`X-Webhook-Signature`, HMAC-SHA256), retry non-2xx
- Support Valkey/Redis; required (not `memory`) for Cluster/Mixed mode
- Gate preference/analytics cookies behind `cookie_consent` state

### KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| Invalid port/timeout? | Default (random 64000-64999) + warning | Config Validation Rule |
| TLS on `.onion`/`.b32.i2p`? | No — always `http://` | Tor HTTP Semantics |
| Default rate limits? | Read 120/min, Write 10/min, Login 5/15min | Rate Limiting |
| Session defaults? | Admin 30d/24h idle; User 7d/24h idle | Session Configuration |
| `abuse@{fqdn}` auto-populate? | No — opt in explicitly | Contact Configuration |
| Consent default state? | Opt-out — enabled by default | Consent Banner |
| Cache for Cluster/Mixed mode? | `valkey`/`redis`, not `memory` | Cache Configuration |

### TERMINOLOGY
| Term | Meaning |
|------|---------|
| Trusted proxy | Peer allowed to set `X-Forwarded-*` (private ranges + allow-list) |
| Tor parity | Tor request behaves like clearnet, minus identity leaks |
| Onion-Location | Header on clearnet responses advertising the `.onion` mirror |
| Contact role | `admin`/`security`/`abuse`/`general` under `server.contact.*` |
| Webhook transport | Named adapter under a role's `webhooks` map |
| Consent state | `ConsentState` JSON in the `cookie_consent` cookie |

---
For complete details, see AI.md PART 5, 6, 12
