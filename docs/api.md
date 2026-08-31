# API Reference

Every control-panel action is reachable through the API. The panel is a client
of the API, never a privileged path around it — anything you can do in the web
UI you can automate.

Three API surfaces are always available:

| Surface | Entry point |
|---|---|
| REST | `/api/{api_version}/…` |
| OpenAPI (Swagger) | `/api/swagger` — JSON spec; UI at `/server/docs/swagger` |
| GraphQL | `/api/graphql` — UI at `/server/docs/graphql` |

Both the OpenAPI document and the GraphQL schema are generated from the code
at build time, so they can never drift from the running server.

## The `{api_version}` segment

`{api_version}` is the value of `server.api_version` in `server.yml` — `v1` by
default, but it is **configurable**. Read it from the config or from
`/api/autodiscover` rather than hardcoding it. Every example below uses
`{api_version}` for exactly that reason.

## Route map

### Root-level

| Endpoint | Method | Auth | Description |
|---|---|---|---|
| `/` | GET | none | Web interface |
| `/server/healthz` | GET | none | Health check; HTML, JSON, or text by content negotiation |
| `/healthz` | GET | none | Alias for `/server/healthz`, gated by `server.healthz.root.enabled` (default `false`) |
| `/server/docs/swagger` | GET | none | Swagger UI |
| `/server/docs/graphql` | GET | none | GraphiQL UI |
| `/server/metrics[/{service}]` | GET | bearer, per service | Prometheus/Grafana/Loki metrics |
| `/metrics[/{service}]` | GET | bearer, per service | Root alias, gated by `server.metrics.root.enabled` (default `true`) |
| `/server/{admin_path}` | GET | session | Admin panel login |
| `/server/{admin_path}/*` | all | session | Admin panel |

### API

| Endpoint | Method | Auth | Description |
|---|---|---|---|
| `/api/autodiscover` | GET | none | Server settings and config schema for the CLI and agent; deliberately unversioned |
| `/api/swagger` | GET | none | OpenAPI JSON, alias for the current version |
| `/api/graphql` | POST | none | GraphQL, alias for the current version |
| `/api/healthz` | GET | none | Health JSON, alias for the current version |
| `/api/metrics[/{service}]` | GET | bearer | Metrics, unversioned alias |
| `/api/{api_version}/server/swagger` | GET | none | OpenAPI JSON |
| `/api/{api_version}/server/graphql` | POST | none | GraphQL |
| `/api/{api_version}/server/healthz` | GET | none | Health check |
| `/api/{api_version}/server/metrics[/{service}]` | GET | bearer | Metrics |
| `/api/{api_version}/server/{admin_path}/*` | all | bearer | Admin API |

!!! note "Aliases are mounts, not redirects"
    The unversioned aliases mount the *same handler* at both paths. This
    matters most for `/api/graphql`: a POST redirect silently degrades to a GET
    on some HTTP clients, which would break every mutation. Nothing here ever
    answers `301`/`302`.

    The OpenAPI document is JSON only. There is no `.yaml` variant and no
    `.json` suffix on the URL. The legacy paths `/openapi`, `/openapi.json`,
    and a root-level `/graphql` were removed and are not redirected — they
    `404`.

### Route scopes

| Scope | Prefix |
|---|---|
| Server | `/server/*` |
| Auth | `/server/auth/*` |
| Users | `/users/*` — no ID segment; the current user comes from the session |
| Organizations | `/orgs/{slug}/*` |
| Server admin | `/server/{admin_path}/*` |
| Admin self | `/server/{admin_path}/{admin_username}/*` |
| Admin config | `/server/{admin_path}/config/*` |

Route naming is uniform: versioned, plural nouns, lowercase, hyphens rather
than underscores, no trailing slash, and never a verb — the HTTP method is the
verb.

## Authentication

Credentials are looked for in this order; the first one found wins, on every
authenticated endpoint.

| Priority | Header or parameter |
|---|---|
| 1 | `Authorization: Bearer {token}` (also `Basic` and `Digest`) |
| 2 | `X-API-Key`, `X-Api-Key`, `API-Key`, `ApiKey` |
| 3 | `X-Auth-Token`, `X-Access-Token` |
| 4 | `X-Token`, `Token` |
| 5 | `?token=` query parameter |

The query parameter works but is the least preferred form — URLs end up in
proxy logs, browser history, and referrer headers. Avoid it in production.

```bash
curl -fsS -H "Authorization: Bearer adm_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://panel.example.com/api/v1/server/administration/config/info
```

### Token format

Tokens are `{prefix}_{32 alphanumeric characters}`.

| Prefix | Token type |
|---|---|
| `adm_` | Admin token |
| `usr_` | User token |
| `org_` | Organization token |
| `adm_agt_`, `usr_agt_`, `org_agt_` | Agent tokens |

Each token carries a name (default `default`), a scope (`global`,
`read-write`, or `read`; default `global`), and an expiration (never, 7 days,
1 month, 6 months, 1 year, or a custom date; default never).

!!! danger "Tokens are shown once"
    Only a SHA-256 hash is stored. The first 8 characters are kept for display
    so you can identify a token in a list. The full value appears exactly once,
    at creation. If it is lost, revoke it and issue a new one — it cannot be
    recovered.

### Sessions

Browser clients use cookies instead: `admin_session` (30 days by default) for
administrators and `user_session` (7 days) for end users. They are separate
cookies backed by separate tables. See
[Configuration](configuration.md#serversession).

## Response format

### Single item

A `GET` for one resource returns it bare, with no wrapper.

```json
{
  "id": "vhost_123",
  "domain": "example.com",
  "php_version": "8.3"
}
```

### Action results

Create, update, and delete return an envelope.

```json
{
  "ok": true,
  "data": {
    "id": "vhost_123",
    "message": "Virtual host created"
  }
}
```

### Errors

Every error uses this exact shape. `details` is optional.

```json
{
  "ok": false,
  "error": "VALIDATION_FAILED",
  "message": "Validation failed",
  "details": {
    "field": "email",
    "rule": "format"
  }
}
```

There is never a separate `code` field, never a `status` duplicated from the
HTTP response, never a bare `{"error": "..."}` without `ok`, and never ad-hoc
top-level fields. Anything extra goes in `details` or in an HTTP header —
`Retry-After`, for instance, is a header, not a body field.

### Pagination

Collections are paginated at 250 items per page by default.

```json
{
  "data": [],
  "pagination": {
    "page": 1,
    "limit": 250,
    "total": 1000,
    "pages": 4
  }
}
```

### Health responses

Health endpoints are the one documented exception to the envelope rule: they
return a bare object, in every format, on every health route.

## Error codes

| Code | HTTP | Meaning |
|---|---|---|
| `BAD_REQUEST` | 400 | Invalid request format |
| `VALIDATION_FAILED` | 400 | Validation failed |
| `UNAUTHORIZED` | 401 | Authentication required |
| `TOKEN_EXPIRED` | 401 | Token has expired |
| `TOKEN_INVALID` | 401 | Invalid token |
| `2FA_REQUIRED` | 401 | Two-factor authentication required |
| `2FA_INVALID` | 401 | Invalid 2FA code |
| `FORBIDDEN` | 403 | Permission denied |
| `CSRF_FAILED` | 403 | CSRF token validation failed |
| `ACCOUNT_LOCKED` | 403 | Account locked |
| `NOT_FOUND` | 404 | Resource not found |
| `METHOD_NOT_ALLOWED` | 405 | Method not allowed |
| `CONFLICT` | 409 | Resource already exists |
| `RATE_LIMITED` | 429 | Too many requests |
| `SERVER_ERROR` | 500 | Internal server error |
| `MAINTENANCE` | 503 | Service unavailable |

### Status codes in use

`200`, `201`, `204`, `301`, `302`, `400`, `401`, `403`, `404`, `405`, `409`,
`422`, `429`, `500`, `503`.

Health status maps as follows:

| Health state | HTTP |
|---|---|
| `healthy` | 200 |
| `degraded` | 200 |
| `restart_required` | 200 |
| `unhealthy` | 503 |
| `maintenance` | 503 |
| `shutting_down` | 503 |

`degraded` deliberately returns `200`: the server is serving requests, and a
load balancer should not evict a node that is merely running with a subsystem
impaired.

## Rate limits

Per-IP sliding windows, all configurable under `server.rate_limit`.

| Class | Limit | Window |
|---|---|---|
| Read (GET, HEAD) | 120 requests | 60s |
| Write (POST, PUT, PATCH, DELETE) | 10 requests | 60s |
| Health and status | 120 requests | 60s |
| Global burst ceiling | 240 requests | 60s |
| Login attempts | 5 | 15 min, then lockout |
| Password reset | 3 | 1 hour |
| Registration | 5 | 1 hour |
| File upload | 10 | 1 hour |

A throttled request returns `429` with a `Retry-After` header giving the
number of seconds to wait. Password-reset throttling never hints at whether
the email address exists.

## Content negotiation

For `/api/{api_version}/*` routes, the response format is chosen in this
order:

1. A `.txt` extension on the path forces plain text
2. `Accept: application/json` returns JSON
3. `Accept: text/plain` returns text
4. A non-interactive client — curl, wget, HTTPie, or an empty User-Agent —
   gets text
5. Otherwise, JSON

Frontend routes use User-Agent detection instead: browsers get HTML, the CasHp
CLI gets JSON, text browsers get a no-JavaScript HTML variant, and HTTP tools
get formatted plain text.

```bash
curl -fsS https://panel.example.com/server/healthz
curl -fsS -H 'Accept: application/json' https://panel.example.com/server/healthz
```

## Client identity

Each binary sends its own User-Agent, and the server uses it for the client
detection above:

| Binary | User-Agent |
|---|---|
| Server | `cashp/{version}` |
| Client | `cashp-cli/{version}` |
| Agent | `cashp-agent/{version}` |

The User-Agent always reports the internal project name even when the binary
has been renamed on disk.

## Discovery

`GET /api/autodiscover` returns the server's settings and configuration schema
so the CLI and agent can configure themselves against an unfamiliar server —
including the current `api_version`. It is intentionally not versioned: a
client needs to read it *before* it knows which version segment to use.

Public protocol endpoints under `/.well-known/` are covered in
[Integrations](integrations.md#well-known-endpoints).
