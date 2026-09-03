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
- [x] Verify CGO_ENABLED=0, pure-Go deps only — `modernc.org/sqlite`, never
      `mattn/go-sqlite3`; every runtime package is stdlib or pure Go
- [x] Runtime support packages (`src/common/{version,display,terminal,theme,
      banner,paths,netinfo}`): display-mode + NO_COLOR/emoji resolution,
      terminal size/symbols, dark/light palette + CSS variables, startup
      banner, PART 8 path resolution with the system/user mode locked at
      process start, PID file handling, trusted-proxy/real-IP, FQDN and URL
      detection, request IDs, auth-token header extraction
- [x] Wire `main.Version`/`main.CommitID`/`main.BuildEpoch`/`main.BuildDate`
      vars (Makefile LDFLAGS targets them, main.go declares them)

## PART 8 — Server Binary CLI
Ref: `.claude/rules/binary-rules.md`, AI.md PART 8 (line 10548)
- [x] `--mode`/`--debug` flags with flag > env > default priority (`src/mode`)
- [ ] `--config`/`--data`/`--cache`/`--log`/`--backup`/`--pid`/`--address`/
      `--port`/`--baseurl`/`--status`/`--service`/`--daemon`/`--maintenance`/
      `--update`/`--shell`/`--lang` wired in `src/main.go` — the supporting
      packages all exist now; this is the final orchestrator pass
- [x] `-v`/`--version` output wired (derives `BuildDate` from `BuildEpoch`,
      never an ldflag — AI.md PART 28)
- [ ] `-h`/`--help`: currently stdlib `flag.ExitOnError` default only (exits
      0 correctly, satisfies binary-rules.md's "exit immediately... without
      checking privilege state") but AI.md PART 8's "Server --help Output"
      (line 10842) documents a specific custom-formatted block (Information/
      Shell Integration/Server Configuration/Service Management sections,
      every flag listed) via `fs.Usage = func() { printHelp(fs) }` — stdlib's
      generic `flag.PrintDefaults()` doesn't match and is also missing every
      flag not yet wired (see the item above this one). Write the custom
      `printHelp`/`fs.Usage` once the full flag set lands, not before —
      doing it now would need immediate rework and ship an incomplete list.
- [x] `--color`/`NO_COLOR`/`COLOR` resolution wired (`resolveColor` in `src/main.go`)

## PART 9 — Error Handling & Caching
Ref: `.claude/rules/backend-rules.md`, AI.md PART 9 (line 14067)
- [x] Central error type/wrapping strategy (`src/errors/` — code taxonomy,
      HTTP status map, `{ok,error,message,details}` payload, retry/backoff)
- [x] Cache layer abstraction (`src/cache/` — memory LRU+TTL default,
      Valkey/Redis backend over a hand-written RESP2 client, distributed locks)

## PART 10 — Database & Cluster
Ref: `.claude/rules/backend-rules.md`, AI.md PART 10 (line 14517)
- [x] `modernc.org/sqlite` default driver + postgres/mysql/mssql/libsql,
      additive idempotent schema registry (`RegisterSchema`/`EnsureSchema`)
- [x] Cluster node table, heartbeats, distributed locks, primary election, quorum
- [x] Schema registry mechanism for accounts/roles/sessions/orgs (each feature
      package registers its own tables; go-auth-builder owns the auth tables)
- ~~**BUG (found while writing `src/auth` tests):** every table in
      `src/auth/schema.go` declares its `id` column as `id ` + `d.Key` (a
      bare `TEXT`/`VARCHAR`/`NVARCHAR` type from `database.DialectFor`,
      see `src/database/schema.go:109-122`) with no `PRIMARY KEY` /
      autoincrement/identity constraint, and no `Create*` function in
      `src/auth` (e.g. `CreateUser` in `store_user.go:46-57`, `CreateOrg`
      in `store_org.go:35-40`) ever includes `id` in its INSERT column
      list — there is no UUID/ID generator anywhere in the package. ID
      population relies on `lastID()` (`store.go:36-45`), i.e.
      `sql.Result.LastInsertId()`, which under sqlite's default dialect
      (`Key: "TEXT"`) returns the hidden `rowid`, not the `id` column
      value — so `id` is always `NULL` in the actual column. Every
      `Create*` table in the package (admins, admin_sessions,
      setup_tokens, users, and by grep every other `id ` + `d.Key` table
      — `schema.go` lines 22, 39, 51, 62, 94, 101, 112, 125, 140, 155,
      164, 174, 190, 199, 209, 237) is affected identically. Consequence:
      any row scan that selects `id` (which is virtually every read —
      confirmed for `UserByID`, `UserByUsername`, `UserByEmail`,
      `ListUsers`, `RecordLoginFailure`, `DeleteUser` in
      `store_user.go`) fails with either `sql: no rows in result set`
      (WHERE id = ? against all-NULL) or `sql: Scan error ...
      converting NULL to int64 is unsupported`. Reproduced live via
      `go test ./src/auth/... -cover` — 12 failing tests, coverage stuck
      at 14.1%.~~ — **FIXED**: `src/auth`'s models are `int64`-typed and
      every `Create*` already relies on `lastID()`/`LastInsertId()` (the
      auto-increment-integer convention — confirmed distinct from
      `src/billing`/`src/nodes`/`src/notify`'s app-generated-string-ID
      convention via grep), so the correct fix is a real per-dialect
      auto-increment primary key, not a switch to UUIDs. Design settled
      against AI.md PART 10's own literal DDL example for this exact
      table (`admins`: `id INTEGER PRIMARY KEY AUTOINCREMENT`,
      AI.md:12508-12509). Added `AutoIncrementPK string` to
      `database.Dialect` (`src/database/schema.go:105-114`), populated
      per driver in `DialectFor` (sqlite/default `INTEGER PRIMARY KEY
      AUTOINCREMENT`, Postgres `BIGINT GENERATED ALWAYS AS IDENTITY
      PRIMARY KEY`, MySQL `BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY`,
      SQL Server `BIGINT IDENTITY(1,1) PRIMARY KEY`), then replaced all
      16 `id ` + `d.Key` declarations in `src/auth/schema.go` with
      `id ` + `d.AutoIncrementPK`. Removed the 11 `t.Skip(...)` lines in
      `store_user_test.go`/`service_user_test.go` that had blocked on
      this bug. Verified in Docker: whole-repo `go build ./...`/
      `go vet ./...` clean; `go test ./src/auth/... -cover` now fully
      passing (0 failures, was 12), coverage 13.0% → 15.9%; whole-repo
      `go test ./...` shows no regressions elsewhere. `src/auth`
      coverage remains well below the 60% gate — admin/org/domain/
      token/session store+service layers and all handler files still
      need a follow-up test-writing pass.

## PART 11 — Security & Logging
Ref: `.claude/rules/backend-rules.md`, AI.md PART 11 (line 15128)
- [x] Argon2id password hashing (bcrypt only for legacy verify-then-rehash)
- [x] Structured logger (`src/logging/`), rotation, audit log, redaction hook
- [x] CSRF/SSRF/path-traversal/rate-limit primitives in `src/security/`
      (middleware wiring lives in `src/server/`)

## PART 12 — Server Configuration
Ref: `.claude/rules/config-rules.md`, AI.md PART 12 (line 18196)
- [x] `server.yml` loader + defaults + validation scaffolded
      (`src/config/config.go`, `defaults.go`, `validate.go`)
- [ ] Verify full field coverage against AI.md PART 12 schema (not yet audited)

## PART 13 — Health & Versioning
Ref: `.claude/rules/api-rules.md`, AI.md PART 13 (line 19732)
- [x] `/server/healthz`, `/server/readyz`, `/server/livez` with the versioned
      and `/api/...` aliases sharing one handler instance; BARE responses
- [x] `/server/version` exposing Version/CommitID/BuildEpoch/BuildDate

## PART 14 — API Structure
Ref: `.claude/rules/api-rules.md`, AI.md PART 14 (line 20605)
- [x] `src/server/` router + 10-piece middleware chain, `src/api/` route
      registry, `src/swagger/` and `src/graphql/` both generated from
      `s.Routes()` so documentation cannot drift
- [x] Consistent `{ok,data}` / `{ok,error,message,details}` envelope,
      pagination (default 250, max 1000), content negotiation
- [ ] Feature route parity: every Browser/PWA feature needs its JSON route
      registered via `s.MountRoute` — done per feature package as
      hosting/billing/support/auth land

## PART 15 — SSL/TLS & Let's Encrypt
Ref: `.claude/rules/api-rules.md`, AI.md PART 15 (line 22378)
- [x] ACME/Let's Encrypt auto-provisioning (`src/tlsmgr/` — HTTP-01 + TLS-ALPN-01
      via `golang.org/x/crypto/acme/autocert`, certbot-compatible on-disk layout,
      self-signed fallback, overlay hosts excluded)
- [x] TLS config hardening (modern cipher suite, HSTS, no HSTS on .onion/.i2p)

## PART 16 — Web Frontend
Ref: `.claude/rules/frontend-rules.md`, AI.md PART 16 (line 23369)
- [x] Server-side Go templates, mobile-first token-only CSS, no client JS
      framework (`src/web/`: layout + 11 partials + 10 reusable components +
      8 pages, `common/components/public/print.css`)
- [x] Full feature set works without JavaScript (theme, cookie consent and
      CCPA opt-out are all plain POST forms)
- [x] PWA `manifest.json`, `sw.js`, `offline.html`, maskable icons
- [x] `TemplatesFS()` / `StaticFS()` exported so `src/admin` reuses the same
      layout and named templates instead of duplicating them

## PART 17 — Admin Panel
Ref: `.claude/rules/frontend-rules.md`, AI.md PART 17 (line 29999)
- [x] Global admin panel at `/server/{admin_path}` (`src/admin/`, 9 Go files
      + 26 templates + `panel.css`): 21 settings slugs, logs/audit/CSV export,
      info, help, admins, API tokens, cluster nodes, per-admin
      profile/preferences/notifications, all settings surfaced
- [x] Tamper-proof bootstrap: one-time 32-hex setup token, only
      `security.HashToken` persisted, plaintext to the server log and never
      to an HTTP response, 24h expiry, single-use, no reissue-by-refresh,
      rate-limited setup gate, generic 401 on a wrong token
- [x] 11 tables via `database.RegisterSchema("admin", …)`; panel stylesheet
      served outside the panel prefix so it cannot disclose the admin path
- [ ] Account admin panel: per-tenant scope only — lands with PART 34/35
- [ ] Tamper-proof primary admin bootstrap flow (per IDEA.md Roles & permissions)

## PART 18 — Email & Notifications
Ref: AI.md PART 18 (line 32502)
- [x] `src/notify/` — SMTP channel (auto-detect 127.0.0.1->172.17.0.1->gateway->
      fqdn->global IPv4->mail./smtp. subdomains, auto-enable on passing test),
      WebUI channel (toast/banner/notification-center), webhook channels
      (telegram/discord/slack/mattermost/pushover/gotify/generic fanned out via
      `server.contact.*.webhooks`), state machine, dedup, retry/backoff, audit
      trail, preferences, embedded+live-reload email templates; `go vet`/`go test`
      now pass (fixed two bugs found during integration: unexported
      `smtpChannelName` vs test-referenced `ChannelSMTP`, and `NewWebhookID`
      non-monotonic within the same millisecond causing flaky ID ordering —
      `src/config/webhook.go` now uses a per-ms random prefix + monotonic
      counter per RFC 9562 6.2 method 1; `WebhookChannel.Endpoints()` was also
      resolving fallback-chain roles as "configured", now reports only
      directly-configured roles)
- [x] `src/notifysvc/` — composition root: builds `*notify.Notifier` at server
      startup, runs SMTP auto-detect, registers `notify.TaskRetry`/`TaskCleanup`
      with the scheduler, dispatches `startup`/`shutdown`
- [x] Call sites wired: `src/backup` (backup_complete/failed), `src/update`
      (update_available/installed), `src/tlsmgr` (ssl_expiring/renewed/
      renewal_failed), `src/scheduler` (scheduler_error, with same-execution
      suppression), `src/overlay` (tor_ready), `src/admin` (admin_login/logout,
      smtp_not_configured)
- [x] `src/api/notifications.go` — `/api/{api_version}/notifications` CRUD
      (list/read/read-all/dismiss/count) + unversioned alias, `{ok,data}`
      envelope, `.txt`/Accept negotiation
- [x] Admin notification-preferences page (`src/admin/pages.go`,
      `notifications.tmpl`) rewired to `notify.Store.Preferences`/
      `SavePreferences` with the Security/Server/Backup/Scheduler/Other-Admins
      categories from AI.md PART 18
- [ ] **Auth call sites NOT wired** (welcome, email_verify, password_reset,
      login_alert, security_alert, password_changed, 2fa_enabled/disabled,
      token_regenerated/created/revoked, profile_updated, session_expired,
      account_suspended, password_reset_required, recovery_keys_low,
      email_verified) — blocked because `src/auth` does not currently compile:
      `PublicDomain` redeclared in `src/auth/models.go` vs
      `src/auth/models_public.go`, and `models_public.go:129` references
      undefined `RecordName`/`RecordValue` on `PublicDomain`. Fix `src/auth`
      first, then wire these call sites.
- [ ] `disk_space_low`, `database_issue`, `geoip_outdated` events not wired —
      no existing health-check/threshold logic anywhere in the repo to hook
      into; needs that monitoring logic built first (out of scope for a pure
      notification-plumbing pass)
- [ ] WebUI notification-center surface not built: bell icon + unread badge in
      the shared header partial, notification-center inbox page (mark-read/
      mark-all/dismiss via no-JS-first POST forms + small polling enhancement
      for the live badge — no WebSocket/SSE precedent exists elsewhere in the
      repo yet), toast/banner rendering usable from any page, admin channel
      management screen (enable/test each channel, `[?]` help from each
      channel's `Help()`), admin email-template management screen at
      `/server/{admin_path}/config/email/templates` (list/edit/preview/
      reset-to-default) per AI.md PART 18 "Admin Panel"
- [ ] No HTTP server composition root exists yet anywhere in the project
      (`src/main.go` never constructs `src/server.Server`), so the new
      `/api/{api_version}/notifications` routes and admin pages above are not
      actually mounted/reachable yet — this blocks all HTTP-surface testing
      until the server composition root is built (separate, larger task,
      likely its own PART/TODO item)

## PART 19 — Scheduler
Ref: `.claude/rules/features-rules.md`, AI.md PART 19 (line 33857)
- [x] Internal scheduler (never external cron) for backups, renewals, cleanup jobs
      (`src/scheduler/` — cron parser, 14 built-ins, catch-up window, cluster locks)

## PART 20 — GeoIP
Ref: `.claude/rules/features-rules.md`, AI.md PART 20 (line 34360)
- [x] Built-in GeoIP lookup (offline DB), used in admin panel + abuse detection
      (`src/geoip/` — sapics/ip-location-db via jsDelivr, maxminddb-golang)

## PART 21 — Metrics
Ref: `.claude/rules/features-rules.md`, AI.md PART 21 (line 34481)
- [ ] Built-in metrics collection + exposure (per spec format)

## PART 22 — Backup & Restore
Ref: `.claude/rules/features-rules.md`, AI.md PART 22 (line 36023)
- [x] Full backup/restore (`src/backup/` — content-defined chunk dedup, gzip,
      Argon2id+AES-256-GCM envelope, GFS retention, full verify chain)
- [x] Scheduled via PART 19 scheduler (`backup_daily`/`backup_hourly` built-ins)

## PART 23 — Update Command
Ref: `.claude/rules/features-rules.md`, AI.md PART 23 (line 36798)
- [x] Self-update command (server + `cashp-cli` + `cashp-agent` binaries)
      (`src/update/` — stable/beta/daily channels, sha256-verified, rollback)

## PART 24 — Privilege Escalation & Service
Ref: `.claude/rules/service-rules.md`, AI.md PART 24 (line 37419)
- [x] systemd/OpenRC/SysVinit/runit units, launchd plist, rc.d script,
      Windows `sc.exe` manager, install/uninstall/enable/disable
      (`src/service/`, 26 impl files + 6 test files)
- [x] Privilege drop deliberately NOT implemented — cashp is the documented
      permanent-root exception (IDEA.md "Security decisions & exceptions");
      generated units pin `User=root` with the rationale comment inline in
      the unit and `ProtectSystem=off`/`NoNewPrivileges=no`

## PART 25 — Service Support
Ref: `.claude/rules/service-rules.md`, AI.md PART 25 (line 38326)
- [x] Install/start/stop/status/restart/reload wired via `service.Detect()`
      returning a `Manager` per platform init system

## PART 26 — Makefile
Ref: `.claude/rules/makefile-rules.md`, AI.md PART 26 (line 38639)
- [x] DONE — `Makefile` created at project root from verbatim template
- [x] go-lint finding FIXED: `test` and `dev` targets invoked `GO_DOCKER`
      without a preceding `@mkdir -p $(GO_CACHE) $(GO_BUILD)` as the
      recipe's first line — other targets already created these dirs
      first; a clean tmp/cache dir could make the Docker run fail on the
      mount. Added the same `@mkdir -p $(GO_CACHE) $(GO_BUILD)` line as
      the first recipe line in both targets.
- [x] go-lint finding FIXED: `LDFLAGS` variable passed
      `-X 'main.BuildDate=$(BUILD_DATE)'` to `go build` — violates AI.md
      PART 28 / `.claude/rules/cicd-rules.md` (`BUILD_DATE` is Docker
      OCI-label-only, never an ldflag; only `VERSION`/`COMMIT_ID`/
      `BUILD_EPOCH` are legitimate ldflags). Removed that `-X` entry from
      `LDFLAGS`. To keep `--version`/admin/web pages showing a build date
      without the ldflag, `src/common/version.Set()` now derives
      `BuildDate` from `BuildEpoch` (RFC3339, still a legitimate ldflag)
      instead of taking it directly from an injected value.

## PART 27 — Docker
Ref: `.claude/rules/docker-rules.md`, AI.md PART 27 (line 39460)
- [x] `docker/Dockerfile` (multi-stage, casjaysdev/go:latest toolchain, minimal runtime)
- [x] `docker/Dockerfile.dev`, `docker/Dockerfile.aio`, all four compose files
- [x] `docker/rootfs/usr/local/bin/entrypoint.sh` (+ `rootfs-aio/` variant)
- [x] go-lint finding FIXED: `docker/Dockerfile`, `docker/Dockerfile.dev`,
      and `docker/Dockerfile.aio` all passed `BuildDate` to `go build`
      via `-X 'main.BuildDate=${BUILD_DATE}'` — same PART 28 violation as
      the Makefile. Removed the `BuildDate` `-X` entry from all three
      files' ldflags strings; kept the `ARG BUILD_DATE` declarations in
      both build and runtime stages unchanged, matching AI.md PART 27's
      own canonical Dockerfile example (line 39995/40020), which declares
      `ARG BUILD_DATE` but omits it from the `go build -ldflags` line.
- [x] go-lint finding FIXED: `docker/Dockerfile`, `docker/Dockerfile.dev`,
      and `docker/Dockerfile.aio`'s `go build` lines were missing
      `-trimpath` (present in the Makefile's build targets —
      AI.md:39221/39231/39244/39258/39280/39286/39293). AI.md PART 27's own
      canonical Dockerfile example (line 39980-40034) omits it too, but
      that's an inconsistency in the spec's literal template, not a
      documented prohibition — `-trimpath` is legitimate, harmless Go build
      practice already used identically elsewhere in this codebase, and
      adding it does not violate any AI.md NEVER rule. Added `-trimpath` as
      a separate flag (not inside `-ldflags`) to all three Dockerfiles' `go
      build` invocations to match the Makefile convention.
- [x] go-lint findings FIXED (regression from the `BuildDate`-ldflag
      removal above): removing `-X 'main.BuildDate=...'` left
      `src/main.go`'s and `src/client/main.go`'s local `BuildDate` package
      vars permanently `"unknown"` (nothing set them anymore), and
      `src/agent/main.go` called `version.Set()` — which derives `BuildDate`
      from `BuildEpoch` correctly — but then printed its own stale local
      `BuildDate` var instead of `version.Get().BuildDate`. Fixed by adding
      a `buildDate()` helper (same RFC3339-from-epoch pattern already used
      in `src/api/version.go`'s `Build.DateString()`) to `src/main.go` and
      `src/client/main.go`, and switching `src/agent/main.go`'s `--version`
      print to `version.Get().BuildDate`. Verified via Docker `go build
      ./... && go vet ./... && go test ./src/... -cover` — clean, no
      regressions.
- [x] go-lint finding CHECKED, false positive: flagged all three
      Dockerfiles' `go build` lines for missing `-buildvcs=false`. That
      flag exists to avoid Git's "dubious ownership" exit-128 failure when
      a *bind-mounted* `.git` has a UID mismatch with the container user
      (`~/.claude/memory/go_conventions.md` § Docker Build Pattern — a
      host-Docker-run concern). Inside these Dockerfiles the build context
      is a `COPY`, not a bind mount, and `.dockerignore` excludes `.git/`
      entirely (confirmed) — there is no `.git` directory present for
      `go build` to VCS-stamp or trip on. AI.md PART 27's own canonical
      Dockerfile example also omits `-buildvcs=false`. No change made.

## PART 28 — CI/CD Workflows
Ref: `.claude/rules/cicd-rules.md`, AI.md PART 28 (line 41096)
- [x] `.github/workflows/` + `.gitea/workflows/` mirrors: `ci.yml`,
      `release.yml`, `beta.yml`, `daily.yml`, `docker.yml`, `docker-aio.yml`
- [x] Third-party Actions pinned to full commit SHA
- [x] `Jenkinsfile` at root (explicit commands, never invokes Makefile)
- [x] go-lint flagged `ci.yml`/`release.yml` go build ldflags as "missing
      BuildDate" — investigated and this is correct as-is, not a bug: per
      `.claude/rules/cicd-rules.md` and AI.md PART 28, `BUILD_DATE` is
      derived from `BUILD_EPOCH` for Docker OCI labels only and must
      NEVER be an ldflag; `VERSION`/`COMMIT_ID`/`BUILD_EPOCH` are the only
      ldflags, which both workflows already set correctly. No change made.

## PART 29 — Testing & Development
Ref: `.claude/rules/testing-rules.md`, AI.md PART 29 (line 45167)
- [x] `tests/run_tests.sh`, `tests/incus.sh`, `tests/docker.sh`, `tests/e2e.sh`,
      `tests/test_endpoints.sh`, `tests/test_content_negotiation.sh`,
      `tests/test_admin_auth.sh`, `scripts/verify-licenses.sh` (all 0755)
- [x] `tests/e2e.sh` headless Chromium E2E driver (chromedp/headless-shell)

## PART 30 — ReadTheDocs Documentation
Ref: `.claude/rules/testing-rules.md`, AI.md PART 30 (line 47211)
- [x] `mkdocs.yml`, `.readthedocs.yaml`, `docs/requirements.txt`
- [x] `docs/` pages: index, installation, configuration, api, cli, admin,
      security, integrations, development + `stylesheets/dark.css` + `light.css`

## PART 31 — I18N & A11Y
Ref: `.claude/rules/testing-rules.md`, AI.md PART 31 (line 48042)
- [x] i18n bundle + 7 locale catalogs (`src/i18n/`, en/es/fr/de/ar/zh/ja,
      905 keys each, CLDR plural rules, Accept-Language negotiation)
- [x] WCAG 2.1 AA helpers + ARIA template FuncMaps (`src/i18n/a11y.go`,
      `funcs.go`) — landmarks `#main-content` / `#navigation`

## PART 32 — Overlay Networks (Tor & I2P)
Ref: `.claude/rules/backend-rules.md`, AI.md PART 32 (line 50052)
- [x] Auto-enabled Tor hidden service when the `tor` binary is detected
      (`src/overlay/`, `github.com/cretz/bine`, never relay/exit)
- [x] I2P support (opt-in, SAM bridge) per spec

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
- [ ] `src/auth` test coverage gate: currently 63.5% (cleared the 60%
      gate) after three test-writer passes (pure logic; store/service for
      users/admins/orgs/sessions/tokens/invites/domains;
      handlers/middleware for API auth/org/domain/admin routes). Remaining
      known 0%-covered code, deliberately left for a follow-up pass:
      `handler_web.go` (~60 server-rendered form handlers — needs a real
      or fake `html/template` `Renderer` wired into tests, materially
      larger effort than the API handler pass), `domain.go:LookupTXT`
      (live DNS resolution, needs a `fakeResolver` injection point),
      `service.go`'s `Config` accessor, `store_admin.go:Public`,
      `templates.go:render`/`StaticHandler`, `TombstoneName`, and the
      scheduler-bound `RunDomainSSLRenewal`. No bugs found in production
      code during any of the three passes.

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
- [x] Cluster Node: HA scaling peer running the full cashp stack — the only
      role that reaches `database/cluster.go` primitives, via the private
      `*ControlPlane` constructor gated on `RoleCluster`
- [x] Managed Node: external host controlled via `cashp-agent` — structurally
      barred from heartbeat/lock/election/membership (`*Service` exposes no
      cluster primitive at all; proven by a reflection test plus a test that
      the fake cluster is never even dialled)
- [x] Enrollment tokens (`adm_agt_` prefix, hashed at rest, single-use,
      expiring, revocable, re-keyable), 7-state machine, node facts,
      authenticated dispatch with retry/timeout, drain/cordon/maintenance,
      confirmed removal — `src/nodes/` (10 impl files, 6 test files)
- [x] Tables `node_registry`, `node_enrollment_tokens`, `node_credentials`,
      `node_facts`, `node_tasks` + 6 indexes; tasks `nodes_liveness_sweep`,
      `nodes_task_reaper`, `nodes_token_expiry` (all cluster-wide)

### Billing (IDEA.md § Product scope & non-goals — multi-tenant billing/RBAC)
Ref: **route to `billing-builder` agent**
- [x] Core implementation exists in `src/billing/` (26 files: accounts,
      audit, dunning, errors, export, handlers_tenant, http, invoices,
      metering, metrics, models, money, notify, payments, plans,
      provider/stripe, providers, quota, reconcile, schema, service, state,
      subscriptions, tasks, tax, templates, webhook) plus full
      `templates/{layout,partial,page}/*.tmpl` set — built by ad-hoc scoped
      agent runs, not a single full `billing-builder` invocation; all
      product features stay free, billing gates quota only (no premium
      tiers, per global NEVER-do rule)
- [x] `go build`/`go vet ./src/billing/...` clean; inline `style="width:...%"`
      progress-bar violation (frontend-rules.md) fixed — replaced with
      discrete `.w-0`..`.w-100` CSS classes + `barWidthClass` template func
      in `tenant_usage.tmpl` and `tenant_overview.tmpl`
- [ ] Zero `*_test.go` files in `src/billing/` (0.0% coverage) — violates
      testing-rules.md's "Create/update matching `*_test.go` in the same
      pass you add or change package logic" and the ≥60% coverage gate;
      needs a dedicated test-writing pass before this can be marked done
- [ ] Full `billing-builder` compliance sweep not yet run — confirm which
      billing models/providers are actually wanted with the user (stripe
      provider stub exists but provider selection was never confirmed),
      then verify the ad-hoc implementation matches `billing-builder`'s
      full spec (help system, admin controls, tax compliance surfaces)

### Support / ticketing (IDEA.md § Target users — agencies/resellers)
Ref: **route to `support-builder` agent**
- [ ] Invoke `support-builder` if/when ticketing scope is confirmed with user
      (not yet explicitly required by IDEA.md — confirm before building)

### Security & threat model (IDEA.md § Threat model & abuse cases, § Security decisions & exceptions)
- [x] Every documented abuse case has its own enforced guard in `src/guard/`
      (11 impl + 10 test files): deny-by-default `Authorize`/`TenantFilter`
      (cross-tenant → `NOT_FOUND`), `ValidateWorkload`/`ValidateVM` (no
      privileged, no host ns, no engine socket, no host bind mount, cap-drop
      ALL, mandatory bounded limits), `NewExecPolicy`/`NewCommand` (argv-only,
      closed binary registry, pinned env), `CheckOutboundHost` (SSRF + DNS
      rebinding, fails closed), `CheckQuota`/`OutboundControl`/`Lockout`
      (quota bypass, port sweeps, abuse ports, credential stuffing),
      `Secret`/`ScrubText`/`RedactPayload` (never-log-a-secret),
      `CheckNameAvailability`/`VerifyOrDecoy`/`Pacer` (anti-enumeration,
      timing-uniform), `BodyLimit`/`AllowedHosts`/`RequireOrigin`/
      `RequireContentType`, and 12 identifier validators
- [x] The permanent-root exception is implemented exactly as IDEA.md scopes
      it and never broadened — no privilege drop, `User=root` in the units
- [ ] Username tombstoning persistence: `CheckNameAvailability` takes a
      `TakenFunc` and documents that tombstoned names must return true, but
      the tombstone table belongs to `src/auth`. Verify the auth builder's
      store satisfies that contract once PART 34 lands.

---

## Follow-ups surfaced during implementation

Recorded here so they are not lost in conversation. None of these are
blockers; each is a concrete piece of spec-mandated work whose dependency
landed in a different package than the one that discovered it.

- [ ] PART 15: DNS-01 challenge support with the full lego provider list, plus
      AES-256-GCM-encrypted provider credentials managed at
      `/server/{admin_path}/config/ssl`. `src/tlsmgr/` implements HTTP-01 and
      TLS-ALPN-01 only; DNS-01 needs `github.com/go-acme/lego/v4` plus config
      and admin-UI wiring.
- [ ] PART 9: HTTP-layer cache machinery — `setCacheHeaders`, version-purge,
      the `asset()` build-stamp helper, and `warmCache`. `src/cache/` provides
      the store; these belong in `src/server/`/`src/web/` and depend on
      build info + DB.
- [ ] PART 19: scheduler task state currently persists to
      `{state_dir}/scheduler.json`; PART 19 specifies the `server.db` column
      set. Swap `src/scheduler`'s store to `src/database` and supply a
      DB-backed `Locker` via `Options.Locker` (no public API change needed).
- [ ] PART 22: free-space / `disk_threshold` pre-check emitting
      `backup.skipped_disk_full`, plus backup audit-event emission — belongs to
      the scheduler/audit wiring, not `src/backup` itself.
- [ ] PART 28: `.gitlab-ci.yml` (AI.md PART 28 GitLab CI section, lines
      43319-44201) is not implemented — GitHub, Gitea, and Jenkins are done.
- [ ] PART 21: AI.md names `github.com/prometheus/client_golang` as the metrics
      library; `src/metrics/` implements the registry and Prometheus text
      exposition with the stdlib only (no new module). Decide whether to keep
      the stdlib implementation or add the module and wrap `promhttp` — the
      public API of the package would not change either way.
- [ ] PART 10: MongoDB is not a `database/sql` driver and is intentionally not
      behind the `*sql.DB` wrapper. If AI.md requires Mongo as a cashp backing
      store (as opposed to a managed service cashp provisions for tenants), it
      needs its own package.
- [ ] PART 29: `tests/e2e/*_test.go` (the tiers behind the build tag
      `tests/e2e.sh` uses) — in progress now that the PART 16 public routes
      and the PART 17 admin routes are both final. `tests/e2e.sh` is complete
      and exits 66 with a pointer until they land.
- [ ] PART 29: the route arrays in `tests/test_endpoints.sh` cover only
      health/metrics/static/admin. PART 29 requires every IDEA.md endpoint to
      be tested — grow the arrays once `src/api`, `src/hosting`, `src/billing`
      and `src/support` register their routes.
- [ ] PART 31: AI.md places the i18n package at `src/common/i18n/` with its
      catalogs at `src/common/i18n/locales/`; it was written to `src/i18n/`.
      Relocate once the PART 16 / PART 17 template agents have finished so
      their import paths can be updated in the same pass.
- [ ] Security: `guard.Authorize` / `guard.NewTenantFilter` must replace the
      ad-hoc per-package tenant filtering in `src/api` and in each feature
      package (hosting, dbservice, orchestrator, billing, support) so an IDOR
      is a compile-visible omission rather than a forgotten `WHERE tenant_id`.
      Do this once all feature packages have landed.
- [ ] PART 14: `src/server/middleware/ratelimit.go` implements its own token
      buckets; `src/security/ratelimit.go` already exports the same rule set
      (`RuleRead` 120/min, `RuleWrite` 10/min, `RuleLogin` 5/15m,
      `RulePasswordReset` 3/hr, `RuleRegistration` 5/hr via `security.Limits`).
      Repoint the middleware at `src/security` and delete the duplicate once
      the security-guard agent has finished extending that package.
- [ ] PART 7/8: `src/common` substitutes stdlib implementations for four
      libraries AI.md names — TTY detection via `os.ModeCharDevice` instead
      of `golang.org/x/term`, `TIOCGWINSZ`/`stty`/`COLUMNS` instead of
      `golang.org/x/sys`, a built-in public-suffix table behind the
      overridable `netinfo.PublicSuffixFunc`/`EffectiveTLDPlusOneFunc`
      instead of `golang.org/x/net/publicsuffix`, and a crypto/rand UUID v4
      instead of a uuid module. Decide whether to adopt the modules; the
      public-suffix swap is two lines.
- [ ] PART 31: `golang.org/x/text` is named by AI.md for plural rules and
      locale formatting; `src/i18n/plural.go` and `format.go` implement
      CLDR-equivalent behaviour with the stdlib only. Decide whether to adopt
      the module (the swap is confined to those two files).
- [x] Frontend compliance: billing progress-bar inline `style="width:...%"`
      violation fixed (see Billing section above) — discrete `.w-0`..`.w-100`
      CSS classes now used instead.

## Verified build/test ground truth (whole-repo, Docker toolchain)

`go build ./...` and `go vet ./...` are both clean as of this pass (fixed:
missing go.mod deps; missing `src/billing/templates/`; `src/support`
undefined `DefaultEscalatePercent`/`DefaultAgentChatLimit`; `src/auth`
`PublicDomain` redeclared — dead duplicate in `models.go` removed, kept
the `models_public.go` version with `RecordName`/`RecordValue` DNS
instructions; `config.FormatDuration` fixed so sub-hour/sub-minute units
don't win over a multi-unit fallback, e.g. 90m now formats as `1h30m0s`).

`go test ./... -cover` — NOT yet clean. Real per-package status:

**Packages with zero `*_test.go` files (0.0% coverage — violates
testing-rules.md "matching `*_test.go` in the same pass" + ≥60% gate):**
`src/agent/{agentlog,banner,collector,paths,reporter,service,settings,shell,updater}`,
`src/auth`, `src/billing`, `src/billing/provider`,
`src/billing/provider/stripe`, `src/client` (+ `api`, `auth`, `cmd`,
`output`, `paths`, `settings`, `term`, `urlutil` subpackages),
`src/dbservice`, `src/support`.

**Packages with existing tests currently FAILING (real assertion
failures/panics, not flaky — need root-cause fixes, not reruns):**
- ~~`src/common/display` — `TestDetectDisplayEnv`~~ — **FIXED**: `DetectDisplayEnv`
  now falls back to a documented `DefaultCols`/`DefaultRows` (80x24) whenever the
  real terminal size can't be determined (headless/CI has no TTY), so `Cols`/`Rows`
  are never 0.
- ~~`src/common/netinfo` — `TestSetTrustedProxies`~~ — **FIXED (test, not code)**:
  the test asserted an explicit `trusted_proxies.additional` list *replaces* the
  default private ranges; AI.md PART 12 "Trusted Proxies" says the opposite —
  "private ranges always trusted" in addition to the allow-list, never replaced.
  Code was already spec-correct; corrected the test's assertion instead.
- ~~`src/guard` — `TestValidateWorkloadAcceptsTheSafeBaseline`~~ — **FIXED**:
  classic Go typed-nil-interface bug. `ValidateWorkload`'s last statement was
  `return checkLimits(spec, policy)`, where `checkLimits` returns the
  concrete type `*DenyError`; converting a nil `*DenyError` straight into
  the function's `error` return type produces a non-nil interface (non-nil
  type descriptor + nil pointer), so every caller's `err != nil` check saw a
  denial even on the safe baseline — `(*DenyError).Error()`'s nil-receiver
  guard is why the message printed as the misleading `<nil>`. Fixed by
  checking `checkLimits`'s result explicitly and returning a bare `nil` on
  the success path. Swept the rest of `src/guard` for the same
  `return check*(...)`/`return Deny(...)` pattern as a final unconditional
  return — no other instance found; every other `Deny(...)` call sits
  inside its own `if`/error branch. Coverage 92.6%.
- ~~`src/nodes` — `TestRecordContactBringsNodeOnline`, `TestValidateFactsRejectsHostileInput`~~ —
  **FIXED**: real bug. `truncate()` silently strips control/null bytes
  (`security.StripControlChars`) before `isPrintableToken` ever ran on the
  result, so a hostile string like `"1.0\x00bad"` or `"6.6.0\x00"` was
  sanitized down to a clean value and *accepted* instead of rejected —
  directly contradicting AI.md PART 11's "Reject control chars / null
  bytes at input" defense-in-depth rule (input layer must reject, not
  silently sanitize). This let `RecordContact`'s hostile-version case fall
  through to a stale, already-online node attempting an invalid
  online→online self-transition, which surfaced as a confusing
  `CONFLICT: Node state change is not allowed` instead of the expected
  `ErrInvalidFacts`. Fixed by checking `isPrintableToken` against the raw
  trimmed input *before* truncation, in both `RecordContact` (state.go) and
  `ValidateFacts`'s Kernel field (validate.go). Coverage 86.2%.
- ~~`src/server` — `TestNotFoundAndMethodNotAllowedUseTheCanonicalEnvelope`,
  `TestCSRFRejectsACrossOriginWrite`~~ — **FIXED**: test bug, not a code bug.
  Both requests were built with `httptest.NewRequest(...)` and no `Accept`
  header, giving an empty User-Agent — per AI.md PART 14's Content
  Negotiation Priority table for `/api/**` routes (priority 4:
  `isNonInteractiveClient` — curl/wget/httpie UA, empty UA → plain text),
  that's a plain-text response by design, not JSON. The implementation was
  already spec-correct. Fixed by setting `Accept: application/json`
  explicitly on both requests, matching the file's existing convention.
  Also checked the panic-recovery leak concern
  (`TestPanicIsRecoveredWithoutLeakingAStackTrace`): the panic value
  `"database credentials postgres://user:pass@10.0.0.5/db"` is a test
  fixture that only asserts the string is absent from the HTTP response
  body (Tier 1, backend-rules.md); it does not touch logging, and the test
  was already passing — confirmed non-issue, no code change needed.
  Coverage 75.9%.
- ~~`src/server/middleware` — `TestCSRFRejectsAForeignOriginWithoutDisclosingTheCheck`,
  `TestRateLimitRejectsWithGenericDetail`, `TestRecoveryReturnsTheEnvelopeWithoutAStackTrace`~~ —
  **FIXED**: same root cause and fix as `src/server` above — each request
  omitted the `Accept` header, so per AI.md PART 14 priority 4 it correctly
  received plain text instead of the JSON the test expected to decode.
  Fixed by setting `Accept: application/json` explicitly on all three
  requests. `TestRecoveryKeepsAnAlreadyWrittenResponse` needed no change —
  it only checks the raw status/body, not a JSON envelope. The
  `dsn postgres://cashp:hunter2@10.0.0.5:5432/cashp` panic fixture in
  `TestRecoveryReturnsTheEnvelopeWithoutAStackTrace` is the same confirmed
  non-issue as above (response-body-only assertion, already passing).
  Coverage 86.3%.
- ~~`src/service` — `TestEvaluateWindowsEscalation`, `TestRenderSystemdUnitSystemScope`~~ —
  **FIXED**: the escalation-reason message used `%q` on a `DOMAIN\user` account, which
  escapes the backslash and no longer matched the test's literal `account "DESKTOP\bob"`
  substring — switched to a plain `"%s"` quote. The systemd unit test failed because a
  *comment* in `templates.go` (explaining why sandboxing is disabled) contained the literal
  substring `ProtectSystem=strict`, which `strings.Contains` matched even though the actual
  directive renders as `ProtectSystem=off` — reworded the comment to not spell out the
  directive name. Coverage for `src/service` is 36.4%, still below the 60% gate — needs
  more tests, not a correctness bug.
- ~~`src/web` — `TestRenderErrorText`~~ — **FIXED**: `DetectClientType`'s
  `cliAgents` (curl/wget/httpie/...) now map to `ClientText`, not
  `ClientJSON`, per AI.md PART 16/14's non-interactive-client rule
  ("curl/wget/httpie UA, empty UA → Plain text", AI.md:21235). The stale
  `client_test.go` expectations for curl/wget were also corrected to
  `ClientText` to match spec.

**Action**: this needs (1) a test-writing pass for the zero-coverage
packages above (`src/auth`/`src/billing`/`src/support` are the highest-value
targets — real handler/service code with no tests at all), and (2) root-cause
fixes for the ~13 failing tests, several of which look like real bugs
(CSRF/rate-limit responses not JSON-shaped per api-rules.md envelope rule;
possible credential leakage into panic-recovery logs needs a Tier-1 check
against backend-rules.md; systemd unit ProtectSystem=strict directly
contradicts the documented permanent-root exception in service-rules.md).
Not yet started — logged here so it isn't lost to context/session limits.

## Blocking questions

None. The only missing bootstrap value (`internal_org`) was resolved via
AI.md's own documented first-time-setup fallback (defaults to
`project_org` = `webappsgo`) and has been added to IDEA.md's Project
variables block — this is an additive fix sanctioned by the spec's setup
flow, not an edit to Business logic content.
