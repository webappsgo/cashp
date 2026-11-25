# Integrations

This guide describes integrations currently connected to the server composition.
The product specification includes additional providers and managed services
that are still under development; they are not available simply because a
package exists in the repository.

## HTTP, TLS, and ACME

The server can bind configured HTTP and HTTPS listeners. Configured certificate
and key paths are loaded for HTTPS. The TLS manager can also be enabled for
Let's Encrypt HTTP-01 challenges and certificate lifecycle handling. Review
the SSL settings in [Configuration](configuration.md) before enabling TLS.

## Database

The server opens its configured database during startup and exposes a
readiness/health check backed by the database connection. SQLite is the default
driver. Other drivers depend on the configured driver and a valid connection
URL. With SQLite, startup creates `server.db` and `users.db` in
`server.database.dir`; the default directory is `{data_dir}/db` for native
installs and `/data/db/sqlite` in containers. `DATABASE_DIR` can override that
directory. Admin accounts, preferences, invites, recovery codes, and API tokens
are stored in `users.db`; admin sessions, settings, audit records, secrets, and
cluster nodes are stored in `server.db`.
The server initializes `app_secrets` with base64-encoded installation, cookie
signing, and CSRF keys in one transaction; existing values are preserved on
restart. Operator-managed encrypted secrets remain in the admin panel's
separate `admin_secrets` table.

The shared user, organization, and custom-domain auth service is not wired into
the server yet. Remote database drivers share one connection and use
`srv_`-prefixed server-owned tables and `usr_`-prefixed user-owned tables.

## Notifications and scheduling

The notification service initializes from server configuration and checks for
a local SMTP service during startup. Notification delivery and scheduled tasks
are available only where the relevant providers and tasks are configured.

## Metrics

Metrics routes are mounted when `server.metrics.enabled` is enabled. Their
actual paths, authentication requirements, and exported metric sets depend on
the server configuration. Use the generated OpenAPI route list and the metrics
settings in `server.yml`; older examples for named Prometheus, Grafana, and Loki
scrapers may not match the current configuration.

## Product integrations still in development

The current server does not yet implement or mount the complete integration
surface described by the product plan. In particular, external identity
providers, domain registrars, payment webhooks, tenant container registries,
well-known account discovery, GeoIP enrichment, Tor/I2P publication, and
automated managed-service installation are not complete end-to-end server
features. See the [API reference](api.md) and [implementation backlog](https://github.com/webappsgo/cashp/blob/main/TODO.AI.md)
for current implementation status.
