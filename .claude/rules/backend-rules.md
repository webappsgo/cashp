# Backend Rules (PART 9, 10, 11, 32)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## Error Handling & Caching (PART 9)
- Canonical API error body: `{"ok": false, "error": "CODE", "message": "..."}`
- Error detail scales by audience: user (minimal) < admin (actionable) < console (full) < log file (full+context) < audit log (who/what/when)
- Never reveal to users: account existence, DB internals, internal hosts/ports, stack traces, dependency versions, specific auth-failure reasons

## Database & Cluster (PART 10)
- Drivers: `modernc.org/sqlite` (pure Go, NEVER `mattn/go-sqlite3`), `pgx/v5`, `go-sql-driver/mysql`, `microsoft/go-mssqldb`, `mongo-driver`, `tursodatabase/libsql-client-go`
- Never `SELECT *` — name columns explicitly
- Parameterized queries only, never string concatenation

## Security & Logging (PART 11)
- Passwords: Argon2id ONLY (OWASP 2023 params), never bcrypt for new hashes (bcrypt verify-then-rehash only)
- API tokens: SHA-256 hashed, never stored raw
- Rate limits: 120/min read, 10/min write, 5/15min login, 3/hr password reset (all admin-configurable)

## Overlay Networks — Tor & I2P (PART 32)
- Tor hidden service: REQUIRED, auto-enabled when Tor binary found (`github.com/cretz/bine`)
- I2P eepsite: OPTIONAL, opt-in only via `features.i2p.enabled`

---
For complete details, see AI.md PART 9, 10, 11, 32
