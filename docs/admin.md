# Admin Panel

The server-rendered administration panel lives at
`/server/{admin_path}`. `server.admin_path` selects the path segment; the
default is `administration`. Panel pages use server-side Go templates and remain
usable without JavaScript.

## First run and sign-in

On first start, the server bootstraps the primary administrator if none exists.
The one-time setup token is shown in the startup output and can be redeemed at
`/server/{admin_path}/setup`. After setup, sign in at
`/server/{admin_path}/`. Setup tokens are single-use. Protected panel pages
require an administrator session, and state-changing forms require CSRF
validation. Debug mode does not bypass authentication.

## Panel pages

The panel provides a dashboard, setup and sign-in flows, server settings,
administrator account management, logs and audit history, server information,
help, administrator profile and preferences, and agent management when the
optional agent component is enabled. The agent pages list connected hosts and
their health, show system and recent metric details, and provide forms to add,
edit, remove, or rotate an agent credential. A newly generated agent command is
shown once and should be treated as a secret. Set `server.fqdn` (or the
runtime `DOMAIN` value) so the panel can build a safe `--server` URL for that
command. Settings sections include
branding, TLS, email, notifications, scheduler, backups, updates, maintenance,
security, network services, moderation, invites, and cluster nodes. The
running server's route list and settings schema are authoritative for the
configured build.

The panel's administrator list does not expose other administrators'
credentials or private profile details. The primary administrator cannot be
removed through the panel. Token plaintext is shown only when a token is
created; the stored token is hashed.

## Settings API

Server Admin bearer tokens can read and update supported settings at
`/api/{api_version}/server/{admin_path}/settings` using `GET` and `PATCH`.
Responses omit password fields. PATCH accepts a JSON object and validates each
field using the same setting definitions as the panel. The endpoint records
successful changes in the audit log. The generated OpenAPI document describes
the current request and response contract.

## Related

- [Configuration](configuration.md)
- [Security](security.md)
- [API reference](api.md)
