# TODO.AI.md — cashp implementation backlog

Generated from AI.md PART 7 onward + IDEA.md Business logic. AI.md is the
HOW (read-only, ~65k lines); IDEA.md is the WHAT. Every item below must be
implemented per the referenced PART — never invent behavior AI.md doesn't
specify, and never contradict IDEA.md's finalized Business logic.

Bootstrap (PART 0-6) is DONE: directory layout, CLAUDE.md loader,
`.claude/rules/*.md`, Makefile, go.mod, .gitignore/.dockerignore,
mode/config scaffolding (`src/mode/mode.go`, `src/config/bool.go`).

Dependency order: PART 7-8 (binary skeleton) → PART 9-12 (core
infra: errors/cache, DB, security/logging, server config) → PART 13-15
(health, API, TLS) → PART 16-17 (frontend, admin) → PART 18-25 (features,
service) → PART 26-28 (build/docker/CI — Makefile done) → PART 29-31
(testing/docs/i18n) → PART 32-33 (overlay networks, client/agent) →
PART 34-36 (multi-user, orgs, custom domains) → IDEA.md hosting-panel
domain work (depends on PART 9-17 core being in place).

---

## PART 7 — Binary Requirements
Ref: `.claude/rules/binary-rules.md`, AI.md PART 7 (line 9913)
- [x] Server binary skeleton at `src/main.go` (mode/debug resolution wired)
- [ ] Verify CGO_ENABLED=0, pure-Go deps only (no mattn/go-sqlite3) once DB lands
- [ ] Wire `main.Version`/`main.CommitID`/`main.BuildEpoch`/`main.OfficialSite`
      vars (Makefile LDFLAGS already targets them, main.go needs the vars)

## PART 8 — Server Binary CLI
Ref: `.claude/rules/binary-rules.md`, AI.md PART 8 (line 10548)
- [x] `--mode`/`--debug` flags with flag > env > default priority (`src/mode`)
- [ ] `--config`/`--server` and remaining flags per spec
- [ ] `--help`/`--version` output per spec format

## PART 9 — Error Handling & Caching
Ref: `.claude/rules/backend-rules.md`, AI.md PART 9 (line 14067)
- [ ] Central error type/wrapping strategy
- [ ] Cache layer abstraction (in-memory default, pluggable backend)

## PART 10 — Database & Cluster
Ref: `.claude/rules/backend-rules.md`, AI.md PART 10 (line 14517)
- [ ] `modernc.org/sqlite` default driver, migration runner
- [ ] Cluster node DB replication/failover per spec
- [ ] Schema for accounts, roles, sessions, orgs (coordinate with go-auth-builder — see below)

## PART 11 — Security & Logging
Ref: `.claude/rules/backend-rules.md`, AI.md PART 11 (line 15128)
- [ ] Argon2id password hashing (bcrypt only for legacy verify-then-rehash)
- [ ] Structured logger, log levels tied to app mode
- [ ] CSRF/XSS/SSRF/IDOR/path-traversal guards baked into middleware

## PART 12 — Server Configuration
Ref: `.claude/rules/config-rules.md`, AI.md PART 12 (line 18196)
- [x] `server.yml` loader + defaults + validation scaffolded
      (`src/config/config.go`, `defaults.go`, `validate.go`)
- [ ] Verify full field coverage against AI.md PART 12 schema (not yet audited)

## PART 13 — Health & Versioning
Ref: `.claude/rules/api-rules.md`, AI.md PART 13 (line 19732)
- [ ] `/healthz`/`/readyz` endpoints
- [ ] `/version` endpoint exposing Version/CommitID/BuildEpoch

## PART 14 — API Structure
Ref: `.claude/rules/api-rules.md`, AI.md PART 14 (line 20605)
- [ ] JSON API routes mirroring every Browser/PWA feature (parity requirement)
- [ ] Consistent envelope, pagination, error format

## PART 15 — SSL/TLS & Let's Encrypt
Ref: `.claude/rules/api-rules.md`, AI.md PART 15 (line 22378)
- [ ] ACME/Let's Encrypt auto-provisioning for custom domains (ties to PART 36)
- [ ] TLS config hardening (modern cipher suite, HSTS)

## PART 16 — Web Frontend
Ref: `.claude/rules/frontend-rules.md`, AI.md PART 16 (line 23369)
- [ ] Server-side Go templates, mobile-first responsive CSS, no client JS framework
- [ ] Full feature set works without JavaScript
- [ ] PWA manifest/service worker for installability

## PART 17 — Admin Panel
Ref: `.claude/rules/frontend-rules.md`, AI.md PART 17 (line 29999)
- [ ] Global admin panel: all settings surfaced, no hidden config
- [ ] Account admin panel: per-tenant scope only (see PART 34/35)
- [ ] Tamper-proof primary admin bootstrap flow (per IDEA.md Roles & permissions)

## PART 18 — Email & Notifications
Ref: AI.md PART 18 (line 32502) — **route to `notifications-builder` agent**
- [ ] Invoke `notifications-builder` against `~/.claude/TEMPLATES/NOTIFICATIONS.md`,
      adapted to cashp's Go stack; confirm channel selection with user first

## PART 19 — Scheduler
Ref: `.claude/rules/features-rules.md`, AI.md PART 19 (line 33857)
- [ ] Internal scheduler (never external cron) for backups, renewals, cleanup jobs

## PART 20 — GeoIP
Ref: `.claude/rules/features-rules.md`, AI.md PART 20 (line 34360)
- [ ] Built-in GeoIP lookup (offline DB), used in admin panel + abuse detection

## PART 21 — Metrics
Ref: `.claude/rules/features-rules.md`, AI.md PART 21 (line 34481)
- [ ] Built-in metrics collection + exposure (per spec format)

## PART 22 — Backup & Restore
Ref: `.claude/rules/features-rules.md`, AI.md PART 22 (line 36023)
- [ ] Full backup/restore of config + data + managed-service state
- [ ] Scheduled via PART 19 scheduler

## PART 23 — Update Command
Ref: `.claude/rules/features-rules.md`, AI.md PART 23 (line 36798)
- [ ] Self-update command (server + `cashp-cli` + `cashp-agent` binaries)

## PART 24 — Privilege Escalation & Service
Ref: `.claude/rules/service-rules.md`, AI.md PART 24 (line 37419)
- [ ] systemd unit (Linux), launchd plist (macOS), service install/uninstall
- [ ] Privilege drop after binding privileged ports

## PART 25 — Service Support
Ref: `.claude/rules/service-rules.md`, AI.md PART 25 (line 38326)
- [ ] Install/start/stop/status wired into `cashp` CLI subcommands

## PART 26 — Makefile
Ref: `.claude/rules/makefile-rules.md`, AI.md PART 26 (line 38639)
- [x] DONE — `Makefile` created at project root from verbatim template

## PART 27 — Docker
Ref: `.claude/rules/docker-rules.md`, AI.md PART 27 (line 39460)
- [ ] `docker/Dockerfile` (multi-stage, casjaysdev/go:latest toolchain, minimal runtime)
- [ ] `docker/Dockerfile.dev`, `docker/docker-compose.yml`
- [ ] `docker/rootfs/usr/local/bin/entrypoint.sh`

## PART 28 — CI/CD Workflows
Ref: `.claude/rules/cicd-rules.md`, AI.md PART 28 (line 41096)
- [ ] `.github/workflows/` + `.gitea/workflows/` mirrors: security-only first,
      then `ci.yml`, `release.yml`, `beta.yml`, `daily.yml`, `docker.yml`
- [ ] Third-party Actions pinned to full commit SHA
- [ ] `Jenkinsfile` at root (explicit commands, never invokes Makefile)

## PART 29 — Testing & Development
Ref: `.claude/rules/testing-rules.md`, AI.md PART 29 (line 45167)
- [ ] `tests/run_tests.sh`, `tests/incus.sh` (preferred), unit tests ≥60% coverage
- [ ] `tests/e2e.sh` headless Chromium E2E

## PART 30 — ReadTheDocs Documentation
Ref: `.claude/rules/testing-rules.md`, AI.md PART 30 (line 47211)
- [ ] `mkdocs.yml`, `.readthedocs.yaml`
- [ ] `docs/` pages: index, installation, configuration, api, cli, admin,
      security, integrations, development + `stylesheets/dark.css`

## PART 31 — I18N & A11Y
Ref: `.claude/rules/testing-rules.md`, AI.md PART 31 (line 48042)
- [ ] i18n string extraction/locale files
- [ ] WCAG 2.1 AA compliance pass on all frontend templates

## PART 32 — Overlay Networks (Tor & I2P)
Ref: `.claude/rules/backend-rules.md`, AI.md PART 32 (line 50052)
- [ ] Auto-enabled Tor hidden service when Tor is detected on host
- [ ] I2P support per spec

## PART 33 — Client & Agent
Ref: `.claude/rules/binary-rules.md`, AI.md PART 33 (line 52731)
- [ ] `cashp-cli` (required companion) — CLI/TUI, API token auth, cluster failover
- [ ] `cashp-agent` (optional) — runs on managed nodes, self-update pattern

## PART 34 — Multi-User (ACTIVE — non-negotiable for cashp)
Ref: `.claude/rules/optional-rules.md`, AI.md PART 34 (line 57421) —
**route to `go-auth-builder` agent**
- [ ] Invoke `go-auth-builder` for user accounts, sessions, API tokens,
      global-admin/account-admin/end-user roles per IDEA.md Roles & permissions
      (token-based admin bootstrap, tamper-proof primary admin)

## PART 35 — Organizations (ACTIVE — non-negotiable for cashp)
Ref: `.claude/rules/optional-rules.md`, AI.md PART 35 (line 61708) —
**route to `go-auth-builder` agent** (orgs/teams scope, same invocation as PART 34)
- [ ] Per-tenant hosting accounts, org-scoped resource isolation

## PART 36 — Custom Domains (ACTIVE — non-negotiable for cashp)
Ref: `.claude/rules/optional-rules.md`, AI.md PART 36 (line 62400) —
**route to `go-auth-builder` agent** (custom-domains scope)
- [ ] User-supplied vhosts/domains, verification, TLS issuance (ties to PART 15)

---

## IDEA.md domain-specific backlog (hosting-panel core, not in generic AI.md template)

These implement IDEA.md's Business logic sections directly; no AI.md PART
number exists for them, they extend PART 9-17 with cashp's actual product
surface. Depends on PART 9-17 core infra being in place first.

### OS package management (IDEA.md § Managed services & OS package mapping)
- [ ] Distro detection (Debian/Ubuntu/Alpine/RHEL family/Fedora/Arch)
- [ ] Per-distro package manager abstraction (apt/apk/dnf/pacman)
- [ ] Third-party repo installation with GPG key pinning, per IDEA.md's
      documented trust model

### Container/VM orchestration (IDEA.md § Service hosting model)
- [ ] Docker backend (managed containers for app-hosted services)
- [ ] Incus backend (VM/container hosting for managed nodes)
- [ ] Podman backend
- [ ] libvirt backend for full VM management
- [ ] Backend abstraction layer so admin panel is orchestrator-agnostic

### Database cluster management (IDEA.md § Managed services & OS package mapping)
- [ ] Postgres app-managed container lifecycle (provision/backup/restore/scale)
- [ ] MariaDB app-managed container lifecycle
- [ ] MongoDB app-managed container lifecycle
- [ ] Valkey app-managed container lifecycle

### Hosting services (IDEA.md § Product scope & non-goals, § Service hosting model)
- [ ] Web hosting (vhost provisioning, static + PaaS deploy targets)
- [ ] DNS service management
- [ ] Mail service management
- [ ] PaaS deployment pipeline (build/deploy from git push, Heroku-style)

### Cluster Node vs Managed Node distinction (IDEA.md § Service hosting model)
- [ ] Cluster Node: HA scaling peer running full cashp stack
- [ ] Managed Node: external host provisioned via Docker/Incus/bare-metal/VM,
      controlled via `cashp-agent`

### Billing (IDEA.md § Product scope & non-goals — multi-tenant billing/RBAC)
Ref: **route to `billing-builder` agent**
- [ ] Invoke `billing-builder`; confirm which billing models/providers with
      user first — all product features stay free, billing gates quota only
      (per global NEVER-do rule: no premium feature tiers)

### Support / ticketing (IDEA.md § Target users — agencies/resellers)
Ref: **route to `support-builder` agent**
- [ ] Invoke `support-builder` if/when ticketing scope is confirmed with user
      (not yet explicitly required by IDEA.md — confirm before building)

### Security & threat model (IDEA.md § Threat model & abuse cases, § Security decisions & exceptions)
- [ ] Implement every documented abuse-case mitigation as its own guard,
      cross-checked against `security_conventions.md` at commit time
- [ ] Any security exception IDEA.md documents must be implemented exactly
      as scoped — never broadened

---

## Blocking questions

None. The only missing bootstrap value (`internal_org`) was resolved via
AI.md's own documented first-time-setup fallback (defaults to
`project_org` = `webappsgo`) and has been added to IDEA.md's Project
variables block — this is an additive fix sanctioned by the spec's setup
flow, not an edit to Business logic content.
