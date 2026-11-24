# Admin Panel

The admin panel is mounted at `/server/{admin_path}/`, where `{admin_path}` is
`server.admin_path` from `server.yml` — `administration` by default, and
changeable. Nothing in this page hardcodes it, and neither should your
bookmarks or monitoring checks.

## First run and setup

The server is fully functional before setup completes. Setup is optional
customization, not a gate: CasHp starts serving immediately on first run.

On first start the server writes a default `server.yml`, creates an empty
`server.db`, autodetects a local SMTP relay, picks a random port in the
`64000`–`64999` range, generates a one-time setup token, prints it to the
console, and begins serving.

!!! danger "The setup token is shown exactly once"
    It is 32 hex characters (128 bits of randomness), never stored in
    plaintext, and invalidated the moment setup completes. If you lose it
    before redeeming it, the only way to get a new one is to reset the
    database.

### Wizard steps

Visit `/server/{admin_path}/`, enter the token, and you are redirected to
`/server/{admin_path}/setup`:

1. **Create the admin account** — username defaults to `administrator`. The
   normal username blocklist does not apply to this account.
2. **Generate an API token** — displayed once, at creation.
3. **Server configuration** — application name, FQDN, deployment mode,
   timezone.
4. **Security settings** — backup encryption password, optional 2FA.
5. **Optional services** — review HTTPS, enable multi-user.
6. **Complete** — `server.yml` is written, the setup token is invalidated, and
   you land on the dashboard already logged in.

## Authentication

The login form is what `/server/{admin_path}/` serves when you have no
session. Authentication is cookie-based (`admin_session`, 30 days by default;
"remember me" extends it to 90), and every form is CSRF-protected.

Multi-factor authentication is available as TOTP or a passkey, and is
configurable per account.

!!! note "Admins are a distinct account type, not privileged users"
    Admin credentials live in the `admins` table in `server.db`; end users live
    in the `users` table in `users.db`. Sessions are likewise separate
    (`admin_sessions` vs `user_sessions`, `admin_session` vs `user_session`
    cookie). An admin is not "a user with a flag set" — the two are different
    account types with different storage.

    Consequently, an admin login never redirects into `/users/*`, and an
    administrator visiting `/users/*` is treated as a guest there and
    redirected back to `/server/{admin_path}/`.

    Admin credentials are never written to the config file.

A failed login shows a generic error that does not reveal whether the username
exists. There is deliberately **no "forgot password" link** on the admin login
page: an admin password is reset from the CLI, not over email.

Login attempts are rate limited to 5 per 15 minutes, after which the account
locks; the brute-force detector additionally blocks a source after 10 attempts
in 5 minutes for an hour.

## URL structure

The hierarchy is strict. The dashboard, the administrator's own username,
and the fixed server-management sections live directly beneath
`/server/{admin_path}/`. There is no `config` grouping segment.

```text
/server/{admin_path}/                       # dashboard
/server/{admin_path}/{admin_username}/      # the admin's own account
/server/{admin_path}/settings              # server management sections
```

### The admin's own account

| Path | Purpose |
|---|---|
| `{admin_username}/profile` | Password, email, and 2FA for this account |
| `{admin_username}/preferences` | Theme, language, and UI preferences |
| `{admin_username}/notifications` | Where this admin's alerts are delivered |

### Server management

| Path | Purpose |
|---|---|
| `setup` | First-run wizard |
| `settings` | Core server settings |
| `branding` | Title, tagline, logo, colors |
| `ssl` | Certificates, Let's Encrypt, HSTS |
| `email` | SMTP and outbound mail |
| `scheduler` | The built-in scheduled tasks |
| `logs` | Log levels, files, rotation |
| `logs/audit` | Audit trail viewer |
| `backup` | Backup destination, retention, encryption |
| `maintenance` | Maintenance mode and self-healing |
| `updates` | Update channel and installation |
| `info` | Version, build, host facts |
| `metrics` | Metrics endpoints and scraper tokens |
| `info/help` | Built-in help |

### Network

| Path | Purpose |
|---|---|
| `network/tor` | Onion service |
| `network/i2p` | Eepsite |
| `network/geoip` | GeoIP databases and country rules |
| `network/blocklists` | IP blocklist feeds |

### Security

| Path | Purpose |
|---|---|
| `security/auth` | Authentication policy |
| `security/auth/oidc` | OIDC providers |
| `security/auth/ldap` | LDAP providers |
| `security/auth/saml` | SAML providers |
| `security/tokens` | API token management |
| `security/ratelimit` | Rate-limit tuning |
| `security/firewall` | Firewall rules |
| `security/allowlist` | IP allowlist |

### Conditional sections

These appear only when the corresponding feature is enabled:

| Path | Requires |
|---|---|
| `users/` | `server.features.multi_user` |
| `users/invites` | `server.features.multi_user` |
| `moderation/users` | `server.features.multi_user` |
| `orgs/` | `server.features.organizations` |
| `cluster/nodes` | clustering enabled |
| `cluster/add` | clustering enabled |
| `agents/` | agents enabled |

## Roles

CasHp has three roles, and they are not interchangeable:

| Role | Scope |
|---|---|
| **Global admin** | The whole server: all tenants, all nodes, all configuration |
| **Account admin** | One account or organization: its users, resources, and quotas |
| **End user** | Only their own resources within an account |

The primary global admin account created during setup is tamper-proof. Its
primary flag cannot be changed or revoked through the UI or the API by anyone
— including other global admins. Recovering a lost primary admin means
re-running the setup-token flow, not promoting a different account.

Anonymous self-service registration never grants global-admin or account-admin
privileges under any configuration.

## Admin API

Everything the panel does is also available over the API at
`/api/{api_version}/server/{admin_path}/…`, authenticated with a bearer token
carrying the `adm_` prefix. It is available regardless of whether multi-user
mode is on.

```bash
curl -fsS -H "Authorization: Bearer adm_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://panel.example.com/api/v1/server/administration/info
```

The setup wizard has its own API endpoints, used by the wizard UI itself:

| Endpoint | Method | Purpose |
|---|---|---|
| `setup` | GET | Current setup status |
| `setup/verify` | POST | Verify the setup token |
| `setup/account` | POST | Create the admin account |
| `setup/token` | POST | Generate the initial API token |
| `setup/config` | POST | Server configuration |
| `setup/security` | POST | Security settings |
| `setup/services` | POST | Optional services |
| `setup/complete` | POST | Finish setup and invalidate the token |

Each is relative to `/api/{api_version}/server/{admin_path}/`.

The `/.well-known/` namespace is also administered from here:

| Endpoint | Method | Purpose |
|---|---|---|
| `web/well-known` | GET, PATCH | Read and update well-known entry configuration |
| `web/well-known/preview/{name}` | GET | Preview an entry's rendered output |

## Audit trail

Configuration changes, authentication events, security events, and token
operations are recorded in `audit.log` as JSON, rotated daily, and viewable at
`logs/audit`. Data-access events are recorded too, but that class is off
by default because of its volume — enable it under
`server.logs.audit.events.data_access` when a compliance regime requires it.

## Related

- [Configuration](configuration.md) — every key the panel writes
- [Security](security.md) — trust boundaries and the threat model
- [API Reference](api.md) — authentication, response shapes, error codes
