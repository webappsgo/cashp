# API Rules (PART 13, 14, 15)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- NEVER hardcode `v1` — use `APIBasePath()` / `{api_version}`
- NEVER keep legacy/removed routes for backwards compat (delete, don't shim/redirect)
- NEVER redirect unversioned aliases (`/api/swagger`, `/api/graphql`, `/api/healthz`) — mount the SAME handler at both paths
- NEVER manually edit generated `openapi.json` or GraphQL schema
- NEVER put swagger/graphql files outside `src/swagger/` and `src/graphql/`
- NEVER wrap health responses in `{ok, data}` — bare object on every route/format
- NEVER expose secrets, DB strings, internal IPs, paths, or usernames in `/server/healthz`
- NEVER use singular nouns, uppercase, underscores, verbs, or trailing slashes in routes
- NEVER add a `.yaml`/`.json` suffix to OpenAPI paths — JSON only

## CRITICAL - ALWAYS DO
- ALWAYS version API routes: `/api/{api_version}/...`
- ALWAYS keep Swagger/GraphQL auto-generated, in sync with code and each other, regenerated at build
- ALWAYS provide all three: REST + Swagger + GraphQL
- ALWAYS support `.txt` + `Accept: text/plain` + non-interactive client detection on `/api/**`
- ALWAYS give user-facing API routes a matching frontend route (CRUD parity)
- ALWAYS use error shape `{ok:false, error:CODE, message, details?}`, success shape `{ok:true, data:{...}}`
- ALWAYS end responses with one trailing newline, 2-space indent (JSON/HTML), tabs (Go)

## KEY DECISIONS (pre-answered)

| Question | Answer | Spec Reference |
|---|---|---|
| Plural or singular resource names? | Always plural (`/users`, `/orgs`) | PART 14 § Route Naming |
| Path params or query params? | Path for identity, query for filter/sort/page | PART 14 § URL Parameters |
| Does `/server/healthz` need auth? | No — public, public-safe only | PART 13 § Security |
| Health JSON uses `{ok,data}` envelope? | No — bare object, exception | PART 13, 14 |
| OpenAPI format? | JSON only, no YAML | PART 14 § API Types |
| Swagger/graphql file location? | `src/swagger/*.go`, `src/graphql/*.go` only | PART 14 |
| Default pagination size? | 250 items | PART 14 § Pagination |
| Version string format? | SemVer `1.0.0`, no `v` prefix (git tags use `v`) | PART 13 § Versioning |
| First stable version? | `1.0.0`, never `0.x.x` | PART 13 § Versioning |
| Trailing-slash 301 on alias — still OK? | Yes, that's canonicalization not version routing | PART 14 |

## TERMINOLOGY

| Term | Meaning |
|---|---|
| Compatibility endpoint | Mimics an external service (pastebin, microbin) |
| Legacy endpoint | Old/removed endpoint from this project — delete, never keep |
| Unversioned alias | `/api/<thing>` on the same handler as `/api/{api_version}/<thing>` |
| Non-interactive client | curl/wget/httpie/empty UA — gets text, not JSON/HTML |
| Bare health response | `/server/healthz` JSON with no `{ok,data}` wrapper |

## QUICK REFERENCE — required endpoints

- `/server/healthz` — HTML/text/JSON, no auth; `/healthz` optional alias (`server.healthz.root.enabled`)
- `/api/{api_version}/server/healthz` — JSON default, text via `.txt`/Accept/CLI; `/api/healthz` unversioned alias
- `/server/docs/swagger`, `/server/docs/graphql` — interactive UIs
- `/api/swagger` (GET), `/api/graphql` (POST) — unversioned aliases
- `/api/{api_version}/server/swagger`, `/api/{api_version}/server/graphql` — versioned canonical
- `/api/autodiscover` — unversioned, CLI/agent config schema
- `/server/metrics[/{service}]`, `/api/{api_version}/server/metrics[/{service}]`, `/api/metrics` — Bearer-protected
- Version: stable `MAJOR.MINOR.PATCH`; beta `YYYYMMDDHHMMSS-beta`; daily = short commit hash
