# Security

Security behavior is implemented in the server middleware, authentication,
admin, and `src/security` packages. The server applies request timeouts,
security headers, request-size limits, rate limits, path validation, and
trusted-proxy handling. Forwarded headers are only accepted from configured
trusted proxies; untrusted forwarded headers are removed before handlers
run. HSTS and HTTPS redirects are disabled for Tor and I2P requests.

## Credentials and sessions

- New account and administrator passwords are stored as Argon2id hashes.
- Random API tokens are shown at creation and stored as SHA-256 hashes.
- Session cookies are `HttpOnly`; admin cookies use `SameSite=Strict`.
  User sessions default to seven days and administrator sessions to 30 days.
- State-changing browser forms validate CSRF tokens. Bearer-token API requests
  authenticate through the API middleware.
- Login attempts are rate-limited. Authentication failures use generic
  messages and do not disclose whether an account exists.
- The server creates and persists installation, cookie-signing, and CSRF
  secrets. Encrypted application secrets use AES-256-GCM.

## Operating the service

Restrict configuration, data, database, backup, and log directories to the
service account. Do not place credentials in source control, public issue
reports, or documentation. Use TLS for clearnet deployments that carry
credentials. When operating behind a reverse proxy, configure its addresses in
`server.trusted_proxies`; do not trust forwarding headers from arbitrary
clients.

The server publishes a machine-readable `/.well-known/security.txt` with the
project's GitHub security advisory URL and disclosure policy. Operator contact
information can be configured under `server.contact`. For a vulnerability in
CasHp, use the [private vulnerability reporting page](https://github.com/webappsgo/cashp/security/advisories/new)
instead of filing a public issue.

The public security surface also includes `/.well-known/llms.txt`,
`/server/security`, and `/server/security/policy`. The server exposes public
health probes at `/server/healthz`, `/server/readyz`, and `/server/livez`, with
versioned and unversioned API aliases documented in the [API reference](api.md).
Only `security.txt` and `llms.txt` are generated under `/.well-known/`; unknown
entries return 404. Operators can provide override files for these two entries
under `{data_dir}/web/.well-known/`.

The security page links to the private reporting form when the encrypted
reporting pipeline is available. Submitted reports are encrypted before being
stored, and the submitter receives a tracking link with a status token. Keep
that link private; it grants access to status updates for the report. An
authenticated administrator reviews and decrypts reports in
`/server/{admin_path}/security/reports`, updates their status and severity,
and can add a researcher-facing comment and disclosure date. The reporter is
notified of status changes when outgoing mail is available. If the form is
unavailable, the security page lists the configured alternate reporting
channels.

## Related

- [Configuration](configuration.md)
- [Admin panel](admin.md)
- [Integrations](integrations.md)
