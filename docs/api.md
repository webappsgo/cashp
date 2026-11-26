# API Reference

The server exposes health and build metadata endpoints, optional metrics,
client autodiscovery, account and organization APIs, tenant billing APIs,
support APIs, administration settings, generated OpenAPI, and GraphQL
documentation routes. The OpenAPI document is built from the routes mounted by
the running server and is the authoritative API contract for that build.

## Server routes

| Method | Path | Access | Purpose |
|---|---|---|---|
| `GET` | `/server/healthz` | Public | Health status and dependency checks |
| `GET` | `/server/readyz` | Public | Readiness probe |
| `GET` | `/server/livez` | Public | Liveness probe |
| `GET` | `/server/version` | Public | Build identity |
| `GET` | `/api/{api_version}/server/autodiscover` | Public | Client bootstrap information |
| `GET` | `/api/{api_version}` | Public | Homepage content and instance API links |
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

## Account and organization routes

The following versioned routes use the configured API version. Authentication
uses a bearer token or session cookie where required; organization operations
also enforce the role shown by the organization membership policy.

| Method | Path | Access | Purpose |
|---|---|---|---|
| `POST` | `/api/{api_version}/server/auth/register` | Public | Register an account |
| `GET` | `/api/{api_version}/server/auth/check-name` | Public | Check username availability |
| `POST` | `/api/{api_version}/server/auth/login` | Public | Authenticate an account |
| `POST` | `/api/{api_version}/server/auth/logout` | Authenticated | End the current session |
| `POST` | `/api/{api_version}/server/auth/password/forgot` | Public | Request password recovery |
| `POST` | `/api/{api_version}/server/auth/password/reset` | Public | Complete password recovery |
| `POST` | `/api/{api_version}/server/auth/verify` | Public | Verify an email address |
| `GET`, `PATCH`, `DELETE` | `/api/{api_version}/users` | Authenticated | Read, update, or delete the current account |
| `POST` | `/api/{api_version}/users/password` | Authenticated | Change the current password |
| `GET`, `DELETE` | `/api/{api_version}/users/sessions[/{id}]` | Authenticated | List and revoke sessions |
| `GET`, `POST`, `DELETE` | `/api/{api_version}/users/tokens[/{id}]` | Authenticated | Manage personal API tokens |
| `POST`, `PUT`, `DELETE` | `/api/{api_version}/users/security/totp` | Authenticated | Enroll, confirm, or disable TOTP |
| `GET`, `POST` | `/api/{api_version}/orgs` | Authenticated | List or create organizations |
| `GET`, `PATCH`, `DELETE` | `/api/{api_version}/orgs/{slug}` | Membership / role checked | Read, update, or delete an organization |
| `GET`, `POST`, `PATCH`, `DELETE` | `/api/{api_version}/orgs/{slug}/members[/{id}]` | Membership / admin role | Manage organization members |
| `POST` | `/api/{api_version}/orgs/{slug}/transfer` | Owner | Transfer organization ownership |
| `GET`, `POST`, `DELETE` | `/api/{api_version}/orgs/{slug}/invites[/{id}]` | Organization admin | Manage organization invitations |
| `GET`, `POST`, `DELETE` | `/api/{api_version}/orgs/{slug}/tokens[/{id}]` | Organization admin | Manage organization API tokens |
| `POST` | `/api/{api_version}/server/auth/invite/accept` | Public | Accept an organization invitation |
| `GET`, `POST`, `DELETE` | `/api/{api_version}/users/domains[/{domain}]` | Authenticated | Manage user custom domains |
| `POST` | `/api/{api_version}/users/domains/{domain}/verify` | Authenticated | Verify a user custom domain |
| `GET`, `POST`, `DELETE` | `/api/{api_version}/orgs/{slug}/domains[/{domain}]` | Organization admin | Manage organization custom domains |
| `POST` | `/api/{api_version}/orgs/{slug}/domains/{domain}/verify` | Organization admin | Verify an organization custom domain |

## Server administration routes

The server settings API is restricted to Server Admin bearer tokens. Password
and secret values are omitted from GET responses; PATCH validates each field
against the same schema as the server-rendered settings pages.

| Method | Path | Access | Purpose |
|---|---|---|---|
| `GET`, `PATCH` | `/api/{api_version}/server/{admin_path}/settings` | Server Admin token | Read or update supported server settings |

## Billing routes

Tenant billing endpoints are available below `/api/{api_version}/orgs/{slug}/billing`.
They use organization membership and billing policy checks. The matching
server-rendered pages are below `/orgs/{slug}/billing`.

| Method | Path suffix | Purpose |
|---|---|---|
| `GET` | `` (base) | Billing overview |
| `GET` | `/plans` | Available plans |
| `POST` | `/subscribe`, `/cancel`, `/resume` | Manage a subscription |
| `POST` | `/account` | Update billing account details |
| `GET` | `/invoices`, `/invoices/{id}` | List or read invoices |
| `POST` | `/invoices/{id}/pay` | Pay an invoice |
| `GET` | `/usage` | Read metered usage |
| `GET`, `POST` | `/payment-methods` | List or add payment methods |
| `POST` | `/payment-methods/{id}/remove`, `/payment-methods/{id}/default` | Remove or select a payment method |
| `GET` | `/export` | Export billing records |

## Support routes

Support API routes are mounted below `/api/{api_version}/support`. They mirror
the support feature set for guest, authenticated user, support-agent, and
administrator access; each handler enforces its own role and ownership rules.
Public support assets are served under `/support/assets/` and are not API
routes. The API route families cover bot sessions and escalation;
tickets, drafts, messages, status, and attachments; chats; knowledge-base
articles and feedback; agent queues, ticket/chat handling, canned responses,
and support mode; plus administration of settings, service levels, departments,
categories, agents, canned responses, and reports. The complete live method and
path inventory is available from the generated OpenAPI document.

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

The route list above groups the public API by feature. The generated OpenAPI
document includes the complete method and path inventory registered by the
running server.
