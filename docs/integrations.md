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

## Public discovery and protocol endpoints

- `/api/autodiscover` provides client bootstrap data before a client selects an
  API version. It is unversioned by design and has no versioned form.
- `/api/{api_version}/server/swagger` and `/api/swagger` serve generated
  OpenAPI JSON. `/server/docs/swagger` is the browser explorer.
- `/api/{api_version}/server/graphql` and `/api/graphql` expose the GraphQL
  query endpoint; `/server/docs/graphql` is the browser explorer.
- `/api/{api_version}/server/healthz` and `/api/healthz` expose health data.
  The only unversioned API aliases are `/api/swagger`, `/api/graphql`,
  `/api/healthz`, `/api/metrics[/{service}]`, and `/api/mcp`; `/mcp` is the
  root alias for MCP. Readiness, liveness, and build metadata are available on
  their versioned API routes without unversioned aliases.
- `/server/help/mcp` explains how to connect an assistant to the Streamable
  HTTP MCP endpoint. `/api/{api_version}/server/mcp` is the canonical endpoint;
  `/api/mcp` and `/mcp` are aliases. Requests require a Bearer API token, and
  available tools follow that token's permissions. The matching JSON help
  response is `/api/{api_version}/server/help/mcp`.
- `/.well-known/security.txt` provides vulnerability reporting details and
  `/.well-known/llms.txt` describes the public site for language-model
  clients. These are the two generated well-known files; other well-known
  paths are not enabled by this server.
- Health and readiness endpoints are listed in the [API reference](api.md).

The generated OpenAPI document is the complete route inventory for the
running configuration. The documentation host is separate from a CasHp
instance; use a configured instance URL when following these runtime paths.

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
registrar integrations. The main HTTP composition exposes tenant-scoped
hosting APIs for domain claims, sites, DNS zones and records, transfer
accounts, hosted mail and mailboxes, application deployments, and usage. The
matching server-rendered pages are available below `/users/` and
`/orgs/{slug}/` for signed-in tenants. Hosting domain claims use the separate
`/users/hosting/domains` and `/orgs/{slug}/hosting/domains` paths, apart from
account custom-domain management. These APIs and pages manage tenant
resources through the configured service backends; package mappings alone do
not mean that every host service is configured or available. The generated
OpenAPI document shows the routes mounted by a running build; see the [API
reference](api.md#tenant-hosting-routes) for the route families.

## Related

- [Configuration](configuration.md)
- [Admin panel](admin.md)
- [Security](security.md)
- [API reference](api.md)
