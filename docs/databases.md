# Managed databases

The account database pages and API provision tenant-owned database engines
through the server's container runtime. The supported engines are PostgreSQL,
MariaDB, MongoDB, and Valkey. Tenant data is stored in managed volumes and
instances are attached to isolated tenant networks.

## Account pages

Sign in and open **Managed databases** under your account. Organization
database pages are scoped to the organization in the URL. Organization owners
and administrators can create and manage instances; members can view them.
The pages use server-rendered forms and work without JavaScript.

The instance page supports lifecycle controls, database and user management,
resource limits, replicas, and version upgrades when the selected engine
supports the operation. Engine capabilities and offered versions are returned
by the API.

## API

All routes require an authenticated user or organization membership. The
request does not choose its tenant; the server resolves ownership from the
session or token and organization path.

| Owner | Base path |
|---|---|
| User | `/api/{api_version}/users/database-instances` |
| Organization | `/api/{api_version}/orgs/{slug}/database-instances` |

The instance routes cover creation, lifecycle, limits, health and usage,
databases, users and grants, backups, replicas, and upgrade paths. Engine
catalogues and offered versions are listed under the matching
`database-engines` path. See the running server's OpenAPI document for exact
methods, request schemas, and response envelopes.

## Runtime limits

Managed databases require a container backend that supports isolated managed
networks, volume mounts, and guest file transfer. A requested disk ceiling is
rejected when the host runtime cannot enforce filesystem quotas. Backup and
restore operations return an explicit unavailable response until a bounded
memory artifact repository is configured. Stdin-dependent online restore is
also rejected by runtimes that do not provide safe streaming input.
