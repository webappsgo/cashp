# Integrations

This page describes services that are initialized by the server composition
in `src/main.go` and integrations that can be configured at runtime.

## Database

SQLite is the default. The server opens `server.db` and `users.db` in separate
SQLite mode; remote SQL drivers use one shared database connection. Supported
driver identifiers are defined by `src/database` and include SQLite,
PostgreSQL (`pgx`), MySQL/MariaDB, SQL Server, and libSQL. Configure a remote
driver with `server.database.driver` and `server.database.url`. The server
creates the schema during startup and exposes both database connections to its
health checks.

## TLS and certificates

The server can open HTTP and HTTPS listeners. It loads a configured
certificate/key pair and initializes the TLS manager for configured
Let's Encrypt certificate lifecycle operations. HTTP-01 challenges are
handled by the HTTP listener. See `server.ssl` in
[Configuration](configuration.md) before enabling TLS.

## Notifications and scheduling

The notification service loads channel configuration from `server.yml` and
detects an available SMTP relay at startup when no host is configured. The
built-in scheduler persists job state in the server database. Registered jobs
include TLS renewal, GeoIP updates, update checks, session and token cleanup,
log rotation, database health checks, and the notification, billing, and
support services' scheduled work. Task schedules and timezone can be managed
in the admin panel.

## Authentication, billing, and support

The server initializes account and administrator authentication with the
configured user, organization, and custom-domain feature settings. Billing
and customer-support services are composed with the shared identity service;
their APIs and server-rendered routes are listed in the [API reference](api.md)
and generated OpenAPI document.

## Metrics, GeoIP, and updates

Metrics handlers are mounted when `server.metrics.enabled` is true. Their
paths, authentication, and metric families follow `server.metrics` settings.
The GeoIP database is initialized from the configured data directory and can
be updated by the scheduler. The update service checks the configured channel
and can install updates when automatic installation is enabled.

## Host services and external providers

The product requirements include web servers and PHP, application runtimes,
container and VM backends, mail and DNS services, security scanners, and
registrar integrations. Package mappings and service abstractions live in the
repository, but the main HTTP composition does not expose all of those as
host-management APIs yet. Do not infer that a host service is configured or
managed merely because its package or setting exists. The route inventory in
OpenAPI shows the APIs currently mounted by a running build.

## Related

- [Configuration](configuration.md)
- [Admin panel](admin.md)
- [Security](security.md)
- [API reference](api.md)
