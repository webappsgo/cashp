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
| `GET` | `/api/{api_version}/server/about` | Public | About content source, title, and format |
| `GET` | `/api/{api_version}/server/privacy` | Public | Privacy settings, cookies, data sharing, retention, tracking, and conditional CCPA status |
| `GET` | `/api/{api_version}/server/terms` | Public | Localized Terms source, title, and format |
| `GET` | `/api/{api_version}/server/help` | Public | Public Help Markdown source and registered help pages |
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

The settings mirror exposes public Help content and the project MCP help
section. It also exposes `branding.content.terms`; edits are persisted to the
appropriate private area file and are used immediately by the corresponding
server-rendered page.

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

## Managed database routes

Managed database API routes are available to signed-in users and organization
members at `/api/{api_version}/users/database-instances` and
`/api/{api_version}/orgs/{slug}/database-instances`. The route families cover
instance lifecycle, limits, health and usage, databases, users and grants,
backups, replicas, and upgrades. Engine catalogues and offered versions are
listed under the corresponding `database-engines` paths. The server resolves
the tenant from the authenticated account and organization membership; callers
cannot select a tenant in the request body. The generated OpenAPI document
provides exact methods and schemas.

## Tenant hosting routes

Hosting routes are mounted for each signed-in user and organization member at
`/api/{api_version}/users/...` and
`/api/{api_version}/orgs/{slug}/...`. Organization writes require an
administrator or owner; members may read tenant hosting data. The server
resolves tenant identity from the session and organization membership rather
than accepting a tenant ID from the request.

| Methods | Path suffix | Purpose |
|---|---|---|
| `GET`, `POST` | `/hosting/domains` | List claimed domains or start DNS/HTTP ownership verification |
| `POST` | `/hosting/domains/{domain}/verify` | Verify a claimed domain |
| `DELETE` | `/hosting/domains/{domain}` | Release a claim; requires `?confirm=true` and no dependent resources |
| `GET`, `POST` | `/sites` | List or create sites |
| `GET`, `PATCH`, `DELETE` | `/sites/{id}` | Read, update, or delete a site; deletion requires `?confirm=true` |
| `GET` | `/sites/{id}/usage` | Read site disk and bandwidth usage |
| `GET` | `/sites/{id}/releases` | List site deployment history |
| `POST` | `/sites/{id}/deploy`, `/sites/{id}/rollback` | Deploy or roll back a site release |
| `POST` | `/sites/{id}/enable`, `/sites/{id}/disable` | Enable or disable a site |
| `GET`, `POST` | `/sites/{id}/transfer-accounts` | List or create FTP/SFTP access accounts |
| `GET`, `DELETE` | `/sites/{id}/transfer-accounts/{accountID}` | Read or remove an access account |
| `GET`, `POST` | `/dns/zones` | List or create DNS zones |
| `GET`, `PATCH`, `DELETE` | `/dns/zones/{id}` | Read, update, or delete a zone |
| `GET`, `POST` | `/dns/zones/{id}/records` | List or create DNS records |
| `GET`, `PATCH`, `DELETE` | `/dns/records/{id}` | Read, update, or delete a DNS record |
| `GET` | `/dns/zones/{id}/file` | Download a zone file |
| `GET`, `POST` | `/mail/domains` | List or enable hosted mail domains |
| `GET`, `DELETE` | `/mail/domains/{id}` | Read or remove a hosted mail domain |
| `GET` | `/mail/domains/{id}/dns` | Read mail DNS records |
| `GET`, `POST` | `/mail/domains/{id}/mailboxes` | List or create mailboxes |
| `PATCH`, `DELETE` | `/mail/mailboxes/{id}` | Update or delete a mailbox |
| `POST` | `/mail/mailboxes/{id}/password` | Change a mailbox password |
| `GET`, `POST` | `/mail/domains/{id}/aliases` | List or create mail aliases |
| `DELETE` | `/mail/aliases/{id}` | Delete a mail alias |
| `GET`, `POST` | `/mail/mailboxes/{mailboxID}/messages` | List messages or send mail |
| `GET` | `/mail/mailboxes/{mailboxID}/security` | Read scanner status |
| `GET`, `DELETE` | `/mail/mailboxes/{mailboxID}/messages/{id}` | Read or delete a message |
| `GET`, `POST` | `/apps` | List or create applications |
| `GET`, `PATCH`, `DELETE` | `/apps/{id}` | Read, update, or delete an application |
| `POST` | `/apps/{id}/deploy`, `/apps/{id}/rollback`, `/apps/{id}/start`, `/apps/{id}/stop`, `/apps/{id}/scale` | Deploy, roll back, start, stop, or scale an application |
| `GET` | `/apps/{id}/releases`, `/apps/{id}/logs`, `/apps/{id}/env` | Read releases, logs, or environment variables |
| `PUT`, `DELETE` | `/apps/{id}/env/{key}` | Set or remove an environment variable |
| `GET` | `/usage` | Read aggregate tenant hosting usage |

Matching no-JavaScript pages are under `/users/{sites,dns,mail,apps,usage}`
and `/orgs/{slug}/{sites,dns,mail,apps,usage}`, with domain claims at
`/users/hosting/domains` and `/orgs/{slug}/hosting/domains`. Site deployment and
transfer-account pages are nested below each site; mailbox pages are nested
below the mail section. Exact schemas and response envelopes are available in
the running server's generated OpenAPI document.

## Agent routes

When the agent component is enabled, agents are managed under the owner scope:

| Owner | API base |
|---|---|
| Server Admin | `/api/{api_version}/server/{admin_path}/agents` |
| User | `/api/{api_version}/users/agents` |
| Organization | `/api/{api_version}/orgs/{slug}/agents` |

Each base supports listing and creating agents. A named agent supports reading,
updating its name or tags, removing it, rotating its token, and reading its
metrics. Registration, heartbeat, and metric reports use the corresponding
`/register`, `/heartbeat`, and `/report` routes. Agent credentials are
owner-scoped bearer tokens with the `agent` scope; they can only register and
send data for their own agent. The generated OpenAPI document lists the exact
methods, schemas, and response envelopes for the configured server.

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
