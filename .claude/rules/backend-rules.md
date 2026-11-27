# Backend Rules (PART 9, 10, 11, 32)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL — NEVER DO

- Never return raw causes, stack traces, database details, internal addresses, or filesystem paths to callers.
- Never store passwords in plaintext or use bcrypt for new hashes; use Argon2id and hash tokens with SHA-256.
- Never fail open for authentication, authorization, rate limiting, or tenant boundaries.
- Never let Tor/I2P failure take down ordinary server service.

## CRITICAL — ALWAYS DO

- Use the canonical error code, HTTP status, safe message, and optional redacted details.
- Enforce authorization and ownership at the data boundary, including every tenant-scoped query.
- Audit sensitive actions and keep secret-bearing fields out of logs.
- Auto-enable Tor when detected; implement I2P support with runtime activation defaults from PART 32.

## Key rules

- Database access uses the project’s canonical schema, migrations, transactions, and backup/recovery rules.
- Cache failure must preserve security controls; never silently lift a limit.
- Overlay addresses and private keys follow the distinct public/private handling rules in PART 32.

For complete details, see AI.md PART 9, 10, 11, 32.
