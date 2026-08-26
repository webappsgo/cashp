# Backend Rules (PART 9, 10, 11, 32)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never expose stack traces, DB DSN/credentials, internal IPs/hostnames, or filesystem paths in any HTTP response (Tier 1 — not even in debug mode)
- Never log passwords, full API/session tokens, recovery keys, TOTP secrets, or private keys — even to audit.log (mask/hash only)
- Never use `DROP COLUMN`, `DROP TABLE`, `DELETE`, or column renames in schema updates — additive only, migrate-in-app-code for renames
- Never build SQL via `fmt.Sprintf`/string concatenation — parameterized queries only, never `SELECT *`
- Never store tokens in plaintext — SHA-256 hash only, show full value once at creation
- Never let an inbound `.onion`/`.b32.i2p` request's payload choose the destination of an outbound Tor/I2P request (SSRF via overlay network)
- Never run the app's Tor instance as a relay/exit (`ExitRelay 0`, no `ORPort`, no `PublishServerDescriptor`)
- Never skip query timeouts or connection pooling on any DB call
- Never return `_debug` fields, CSP/CSRF/CORS internals, or specific auth-failure reasons ("wrong password" vs "not found") outside `DEBUG=true`
- Never use `mattn/go-sqlite3` (cgo) — `modernc.org/sqlite` (pure Go) only
- Never use bcrypt for new password hashes — Argon2id only (bcrypt verify-then-rehash on legacy only)

## CRITICAL - ALWAYS DO
- Use `CREATE TABLE IF NOT EXISTS` + idempotent `ALTER TABLE`/`ADD COLUMN IF NOT EXISTS` for all schema — no migration files, no version table
- Wrap every DB call in `context.WithTimeout` (5s simple SELECT, 10s writes, 15s JOINs, 60s bulk)
- Support cluster mode (config sync, session sharing, distributed locks, primary election) as base functionality
- Log every error with `request_id`, `error_code`, `http_status`; log level ERROR ≥500, WARN ≥400
- Constant-time compare (`subtle.ConstantTimeCompare`) for all secret/token comparisons; pad auth-endpoint response time to fixed minimum
- Rotate `installation_secret`, `cookie_signing_key`, `csrf_token_secret`, `server.security.encryption_key` via the sensitive-operation flow (re-prompt admin password, audit-log the rotation)
- Enforce App-Scoped-Only on Tor/I2P: outbound destinations must be app/config-determined, never visitor-supplied
- Auto-enable Tor hidden service whenever the Tor binary is found (`github.com/cretz/bine`) — no toggle

## KEY DECISIONS (pre-answered)

| Question | Answer | Spec Reference |
|---|---|---|
| Password hashing algorithm | Argon2id only (OWASP 2023 params) | PART 11 |
| Token format | `{prefix}_{32 alphanumeric}` (`adm_`/`usr_`/`org_`, `*_agt_` for agents), SHA-256 hashed at rest | PART 11 |
| Audit log format | JSON only, append-only, `daily` rotation, `keep: none` default | PART 11 |
| Rate limits | 120/min read, 10/min write, 5/15min login, 3/hr password reset (admin-configurable) | PART 11 |
| DB drivers | `modernc.org/sqlite`, `pgx/v5`, `go-sql-driver/mysql`, `microsoft/go-mssqldb`, `mongo-driver`, `tursodatabase/libsql-client-go` | PART 10 |
| Tor hidden service enablement | Auto-enabled whenever Tor binary is found — no toggle | PART 32.1 |
| I2P eepsite enablement | Opt-in only, `features.i2p.enabled: true`, default off | PART 32.2 |
| Cache invalidation on user update | Event-based: delete all `user:{id}*` keys on write | PART 9 |
| Serialization/write conflicts | Retry with backoff on `40001`/`1213`, optimistic locking via `version` column | PART 10 |
| Cluster secret rotation safety | Advisory lock + majority-quorum check, never during partition minority | PART 10 |

## TERMINOLOGY

| Term | Meaning |
|---|---|
| Tier 1/2/3 | Never-public secrets / always-public operational info / debug-only detail |
| `app_secrets` | DB table holding `installation_secret`, `cookie_signing_key`, `csrf_token_secret` |
| Cluster node | Another instance of this server binary sharing DB+cache, distinct from an "agent" |
| Agent | Separate `{project_name}-agent` binary reporting into the server via bearer token |
| App-Scoped Only | Tor/I2P outbound client may only hit app-determined destinations, never visitor-supplied ones |
| Eepsite | I2P's term for a hidden site (`.b32.i2p`), the I2P analogue of a Tor `.onion` |

---
For complete details, see AI.md PART 9, 10, 11, 32
