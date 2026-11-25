# API Reference

The server currently exposes health and build metadata endpoints, optional
metrics, client autodiscovery, generated OpenAPI, and GraphQL documentation
routes. The generated OpenAPI document reflects the routes mounted in the
running server. The broader account, organization, billing, support, and hosting
API described by the product specification is still being implemented.

## Server routes

| Method | Path | Access | Purpose |
|---|---|---|---|
| `GET` | `/server/healthz` | Public | Health status and dependency checks |
| `GET` | `/server/readyz` | Public | Readiness probe |
| `GET` | `/server/livez` | Public | Liveness probe |
| `GET` | `/server/version` | Public | Build identity |
| `GET` | `/api/{api_version}/server/autodiscover` | Public | Client bootstrap information |
| `GET` | `/api/autodiscover` | Public | Unversioned autodiscovery alias |
| `GET` | `/api/{api_version}/server/healthz` | Public | Versioned health alias |
| `GET` | `/api/healthz` | Public | Unversioned health alias |
| `GET` | `/api/{api_version}/server/readyz` | Public | Versioned readiness alias |
| `GET` | `/api/readyz` | Public | Unversioned readiness alias |
| `GET` | `/api/{api_version}/server/livez` | Public | Versioned liveness alias |
| `GET` | `/api/livez` | Public | Unversioned liveness alias |
| `GET` | `/api/{api_version}/server/version` | Public | Versioned build identity alias |
| `GET` | `/api/version` | Public | Unversioned build identity alias |
| `GET` | `/{configured metrics path}` | Configured | Metrics when metrics are enabled |
| `GET` | `/api/{api_version}/server/swagger` | Public | OpenAPI JSON |
| `GET` | `/api/swagger` | Public | Unversioned OpenAPI alias |
| `POST` | `/api/{api_version}/server/graphql` | Public | GraphQL endpoint |
| `POST` | `/api/graphql` | Public | Unversioned GraphQL alias |
| `GET` | `/server/docs/swagger` | Public | OpenAPI explorer |
| `GET` | `/server/docs/graphql` | Public | GraphQL explorer |

The API version and metrics paths come from `server.yml`. Health aliases are
mounted directly to the same handlers rather than redirecting. Optional root
health aliases depend on configuration. Metrics are only mounted when enabled
and may require a configured token.

## Inspect the live route contract

The OpenAPI document is generated from routes mounted by the server. Fetch it
from the versioned or unversioned path:

```sh
curl -fsS http://127.0.0.1:64580/api/v1/server/swagger
curl -fsS http://127.0.0.1:64580/api/swagger
```

Use the configured API version instead of `v1` when it differs from the
default. The documentation explorers are available at `/server/docs/swagger`
and `/server/docs/graphql` on a running development instance.

## Not yet implemented

The current server composition does not mount the account authentication,
organization, custom-domain, tenant billing, or support handlers. The
administration panel has server-rendered routes, but it does not provide the
complete REST API promised by the product specification. Client command names
for these areas are not evidence that the corresponding API routes are
available. Consult the generated OpenAPI document for the actual live route
set.
