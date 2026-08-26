# Testing Rules (PART 29, 30, 31)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

Covers: Testing & Development (29), ReadTheDocs Documentation (30), I18N & A11Y (31).

## CRITICAL - NEVER DO
- Never run `go build`/`go test`/any binary directly on the host — no Go on local machine
- Never bypass or skip auth in admin-route tests, debug mode, or CI — test that auth works, don't work around it
- Never use bare `/tmp` or `mktemp -d` without `{project_org}/{internal_name}-XXXXXX` structure
- Never mount `./volumes/` or any project-directory path as a runtime volume
- Never run `docker-compose.yml` or `docker-compose.dev.yml` (human-only, production/dev data)
- Never put non-ReadTheDocs files in `docs/`
- Never let `make test` invoke E2E — E2E is manual/on-demand only, never a commit gate
- Never treat an agent-driven exploratory browser pass as a gate — only the committed chromedp suite is repeatable

## CRITICAL - ALWAYS DO
- Build/test/run binaries only inside Docker (`casjaysdev/go:latest`) or Incus (`debian:latest`)
- Create/update matching `*_test.go` in the same pass you add or change package logic
- Achieve ≥60% Go coverage (`go test -cover`) and 100% endpoint/route coverage
- Test every route with all applicable Accept headers (`text/html`, `text/plain`, `application/json`) and every `.txt` endpoint
- Test admin routes via real flow: unauthenticated rejected → setup token → create admin → login → session-based access → invalid creds rejected
- Run all three E2E tiers (SSR, no-JS browser, full browser) when E2E runs; every IDEA.md feature needs a Tier-1/2/3 scenario
- Log every finding (from manual, exploratory, or automated testing) in `TODO.AI.md` if not fixed immediately
- Keep `docs/` current with every operator/admin/API/integration-affecting feature; ship on ReadTheDocs via `mkdocs.yml` + `.readthedocs.yaml`
- Include accessibility checks (skip link, alt text, label association, heading hierarchy, landmarks) in automated tests

## KEY DECISIONS (pre-answered)

| Question | Answer | Spec Reference |
|----------|--------|----------------|
| Where do unit tests live vs integration tests? | `*_test.go` = package logic (no server needed); `./tests/*.sh` = full running binary, routes, auth | PART 29 § "What Goes in *_test.go vs ./tests/*.sh" |
| What coverage is required? | ≥60% unit (`go test -cover`), 100% endpoint/route | PART 29 § "Test Coverage Gates" |
| Is E2E part of the commit gate? | No — manual/on-demand only (`./tests/e2e.sh`), `make test` never runs it | PART 29 § "Browser E2E Testing" |
| What engine drives E2E? | `chromedp` (pure Go, headless Chromium via CDP), tests in `tests/e2e/` behind `e2e` build tag | PART 29 § "Browser E2E Testing" |
| Can debug mode skip admin auth? | Never, in any mode — verbose logging only | PART 29 § "Debug Mode - Never an Auth Bypass" |
| Where does test/runtime data go? | `/tmp/{project_org}/{internal_name}-XXXXXX/`, never project dir or bare `/tmp` | PART 29 § "Temporary Directory Structure" |
| Which docker-compose file can AI use? | `docker/docker-compose.test.yml` only, preferably via `tests/` scripts | PART 29 § "AI Docker Compose Rules" |
| Where does docs source live? | `docs/` — ONLY MkDocs/ReadTheDocs files | PART 30 § "Overview" |
| What accessibility standard applies? | WCAG 2.1 AA, verified with axe/WAVE/Lighthouse/screen readers/keyboard-only | PART 31 § "Testing Requirements" |

## TERMINOLOGY

| Term | Meaning |
|------|---------|
| Phase 1 — Toolchain Gate | `*_test.go` via `go test`, pre-commit, ≥60% coverage |
| Phase 2 — Binary Validation | `./tests/*.sh` against the compiled/running binary, manual/developer-initiated |
| Tier 1 (SSR) | Plain HTTP client checks raw server-rendered HTML, no browser |
| Tier 2 (No-JS browser) | chromedp with script execution disabled — progressive enhancement |
| Tier 3 (Full browser) | chromedp with JS on — full flows, zero console/asset errors |
| Setup token | First-run credential used to bootstrap the initial admin account in tests |

## QUICK REFERENCE

Required scripts (six-target set, fixed):
- `tests/run_tests.sh` — auto-detects docker/incus, runs Phase 2 suite
- `tests/docker.sh` — container-based binary tests
- `tests/incus.sh` — full-OS/systemd integration tests
- `tests/e2e.sh` — Docker-wraps `go test -tags e2e ./tests/e2e/...`; on-demand only, never in `make test`

Additional tooling/requirements found in PART 29-31:
- `go test -cover -coverprofile=coverage.out ./...` — CI coverage check, fails build if <60%
- `tests/test_content_negotiation.sh`-style helper scripts allowed (not required) alongside the four required scripts
- Docs build: `docs/requirements.txt` (Python deps for MkDocs), validated via `.readthedocs.yaml`
- Accessibility: axe DevTools, WAVE, Lighthouse, NVDA/VoiceOver, keyboard-only manual pass

---
For complete details, see AI.md PART 29, 30, 31
