# Admin Panel

The current server mounts a server-rendered administration panel at
`/server/{admin_path}`. The segment is configured by `server.admin_path` and
defaults to `administration`. The panel uses a separate admin store and
session-based authentication. It is still a foundation: several controls
render settings or data that are not yet connected to the full product
services.

## First run and login

On first request, the panel initializes its bootstrap state and presents the
setup flow. Follow the instructions shown by the running instance. After setup,
sign in through the panel login form. Protected pages require an admin session;
debug mode does not bypass authentication. Passwords and session credentials
are not displayed in the panel.

## Current page groups

The panel currently provides these route groups beneath
`/server/{admin_path}`:

| Route | Purpose |
|---|---|
| `/` | Dashboard or login/setup entry point |
| `/setup` | Initial administrator setup |
| `/settings` | Core server settings |
| `/branding`, `/ssl`, `/email`, `/notifications` | Identity, TLS, email, and notification settings |
| `/scheduler`, `/backup`, `/updates`, `/maintenance` | Operations settings |
| `/security/auth`, `/security/ratelimit`, `/security/firewall`, `/security/allowlist` | Security settings |
| `/network/tor`, `/network/i2p`, `/network/geoip`, `/network/blocklists` | Network settings |
| `/moderation/users`, `/users/invites`, `/cluster/add`, `/url` | Additional settings pages |
| `/logs` | Log view and filtering |
| `/logs/audit` | Audit log view |
| `/logs/export` | Log export |
| `/info` | Server information |
| `/info/help` | Built-in help |
| `/admins` | Administrator accounts |
| `/security/tokens` | Admin API tokens |
| `/cluster/nodes` | Cluster node view |
| `/{admin}/profile` | Signed-in administrator profile |
| `/{admin}/preferences` | Signed-in administrator preferences |
| `/{admin}/notifications` | Signed-in administrator notifications |

The admin package keeps some legacy `config/` strings internally, but its URL
builder removes that grouping. The routes above use the public canonical
paths. A rendered settings page does not imply that its associated service is
fully implemented; consult the integration and API guides for runtime status.

## API availability

The panel does not currently expose a matching REST API for every page. The
currently mounted machine API is listed in the [API reference](api.md) and the
live OpenAPI document. The CLI's `--admin` command surface is also incomplete.

## Product scope still in development

The complete user, organization, custom-domain, hosting, billing, support,
security, and integration administration flows described in the product
specification are not yet available in this panel. Do not treat a settings
label or a route in an older guide as proof that its backing feature is
operational. See the [implementation backlog](https://github.com/webappsgo/cashp/blob/main/TODO.AI.md).

## Related

- [Configuration](configuration.md)
- [Security](security.md)
- [API reference](api.md)
