# cashp AI.md Alignment Backlog

AI.md is the implementation authority. Resolve dependencies before sequencing
work. Each item remains until its implementation commit succeeds, per AI.md
PART 0. AI.md itself is read-only; template defects belong only in
`~/Projects/github/webappsgo/TODO.SERVER.md`.

## P0 — Security, data integrity, and core correctness

- [ ] Verify and complete core configuration, path security, runtime modes, startup validation, and binary behavior (PARTS 4–8, 12).
- [ ] Verify and complete authentication, authorization, tenant isolation, credential handling, `app_secrets` consumption and rotation, database access, cluster behavior, rate limiting, and error/logging controls (PARTS 9–11).
- [ ] Consolidate the incompatible `src/admin` and `src/auth` admin/session schemas and make both the admin panel and shared auth use the canonical account records (PARTS 10–11).
- [ ] Verify and complete API route behavior, content negotiation, health/version endpoints, OpenAPI/GraphQL parity, and endpoint integration coverage (PARTS 13–14, 29).
- [ ] Complete the running HTTP/TLS composition root by wiring shared auth, the complete API, and every configured service alongside the existing middleware, public pages, admin panel, health/readiness/version, metrics, Swagger, and GraphQL handlers (PARTS 8, 12–17).
- [ ] Rebuild the admin route tree and settings/pages to match PART 17's canonical `/server/{admin_path}/...` structure; remove obsolete route aliases and close every required page/form flow.
- [ ] Mount all registered user, organization, custom-domain, billing, support, hosting, backup, notification, security, and integration API handlers and their required server-rendered pages (PARTS 14, 16–23, 32–36; IDEA.md).
- [ ] Verify and complete server rendered public pages, admin pages, no-JavaScript flows, accessibility, content safety, and mobile styling (PARTS 16–17, 31).
- [ ] Verify and complete activated multi-user, organization, and custom-domain flows, including schema, authorization, ownership proofs, routes, admin controls, and tests (PARTS 34–36; activation governed by SPEC.md).

## P1 — Required product features and operations

- [ ] Verify and complete TLS/ACME, email and notifications, internal scheduling, GeoIP, metrics, backup/restore, and updates (PARTS 15, 18–23).
- [ ] Verify and complete privilege/service management for every required platform and init system, including the documented permanent-root exception (PARTS 24–25).
- [ ] Verify and complete the required `cashp-cli` and optional `cashp-agent`, including command/API parity, authentication, terminal behavior, and platform builds (PART 33).
- [ ] Verify and complete Tor auto-detection/hidden service and opt-in I2P behavior with overlay-network security and privacy requirements (PART 32).
- [ ] Implement and verify all product capabilities and abuse defenses in IDEA.md, including hosting, deployment, containers, VMs, mail, DNS, databases, security, clustering, billing, backups, monitoring, and support.

## P2 — Build, release, CI/CD, and project structure

- [ ] Verify and complete repository layout, license/attribution, file naming, generated assets, configuration samples, and forbidden-file checks (PARTS 2–4).
- [ ] Verify and complete Makefile targets, Docker images/compose files/rootfs overlays, and safe container execution (PARTS 26–27).
- [ ] Align GitHub, Gitea, Forgejo, GitLab, and Jenkins pipelines with required build, test, coverage, security, release, provenance, and platform matrices (PART 28).
- [ ] Verify the full eight-platform build matrix and release artifacts for every required binary (PARTS 7, 28, 33).

## P3 — Documentation, translations, and final consistency

- [ ] Bring README, MkDocs/ReadTheDocs, operator/admin/API/integration/security/CLI docs, generated API references, and examples into sync with implemented behavior (PARTS 3, 14, 30).
- [ ] Complete translation parity, locale selection, pluralization, accessibility, and server/client text coverage (PART 31).
- [ ] Raise aggregate unit coverage from the currently measured 55.4% to at least AI.md's 60% gate and reach 100% coverage for all HTTP endpoints. Package tests pass, but `make test` fails the aggregate threshold. Auth, billing, and support handlers are now mounted; full route integration coverage remains incomplete.
- [ ] Reconcile IDEA.md with AI.md and actual behavior; ensure root instruction files and all 14 `.claude/rules/` files accurately load/summarize the authoritative spec.
- [ ] Complete the AI.md FINAL CHECKPOINT, required unit/route/accessibility/security/E2E validation, documentation checks, and provider workflow validation; resolve every failure.
