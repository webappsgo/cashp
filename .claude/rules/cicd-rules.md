# CI/CD Rules (PART 28)

## CRITICAL - NEVER DO
- Never use the Makefile inside any workflow — explicit `go build`/`go test` commands only
- Never install Go tooling inline — everything runs in `container: image: casjaysdev/go:latest`
- Never add `build-toolchain.yml` or an `ensure-build-image` preflight job (Go projects don't need one)
- Never pin third-party Actions to a tag — full commit SHA only
- Never use `default_branch` for secret-scan diff range — use `github.event.before`/`github.event.after`
- Never cross-cancel different release tag refs — only the exact same tag ref auto-cancels
- Never reference local user paths (`~/.local/share/go`) — use `/tmp/` or CI-native caching
- Never give `docker/Dockerfile.dev` (`:devel`) its own `-aio` variant — none exists

## CRITICAL - ALWAYS DO
- All jobs run `container: image: casjaysdev/go:latest`
- Auto-cancel older in-progress runs for pushes to `main`/`master`/`devel`/`dev`/`beta` via `concurrency`
- Set `VERSION`, `COMMIT_ID`, `BUILD_EPOCH` explicitly in a "Set build info" step (`BUILD_DATE` derived from `BUILD_EPOCH`, Docker OCI labels only, never an ldflag)
- Enforce 60% coverage threshold in `ci.yml` test job
- Build all 8 release platforms in the release matrix (linux/darwin/windows/freebsd × amd64/arm64)
- Skip build/test/coverage/artifact jobs on `schedule` runs with `if: github.event_name != 'schedule'`; security jobs (`secret-scan`, `workflow-policy`, `vuln-scan`, `image-scan`) run on push, PR, and weekly cron

## KEY DECISIONS (pre-answered)

| Question | Answer | Spec Reference |
|---|---|---|
| Which image builds Go? | `casjaysdev/go:latest`, no build-toolchain.yml | AI.md:41170 |
| Makefile in CI? | Never — explicit commands only | AI.md:41110 |
| Coverage threshold? | 60%, enforced in `ci.yml` test job | AI.md:41216-41223 |
| Release platforms? | linux/darwin/windows/freebsd × amd64/arm64 (8 targets) | AI.md:41279-41299 |
| Docker registry? | `ghcr.io` | AI.md:41909 |
| Docker image types? | Standard (alpine), All-in-One (`-aio`, debian), Development (`:devel`, alpine) | AI.md:41891-41895 |
| Does AIO get its own workflow? | Yes — `docker-aio.yml`, only image type with a dedicated file | AI.md:41912 |
| `:devel` tag built where? | `build-devel` job inside `docker.yml`, from `Dockerfile.dev`, daily 4am UTC + every non-tag push | AI.md:41911 |
| Secret-scan diff range? | `github.event.before`/`github.event.after`, never `default_branch` | AI.md:41249 |
| Action pinning? | Full commit SHA, never a tag | AI.md:41197 |

## TERMINOLOGY

| Term | Meaning |
|---|---|
| Standard image | `docker/Dockerfile`, alpine, app only, no tag suffix |
| All-in-One (AIO) | `docker/Dockerfile.aio`, debian, app+PostgreSQL+Valkey+Tor, `-aio` suffix |
| Development image | `docker/Dockerfile.dev`, alpine, app+debug tooling, `:devel` tag |
| `{commit_id}` | Short SHA, 7 chars, `git rev-parse --short=7 HEAD` |
| `YYMM` tag | Year/month build tag, e.g. `2512` |
| Build Info Variables | `VERSION`, `COMMIT_ID`, `BUILD_EPOCH` (ldflags) + `BUILD_DATE` (OCI labels only) |

## QUICK REFERENCE

GitHub Actions files (`.github/workflows/`):
- `ci.yml` — push/PR to default branch + weekly cron for security jobs: lint, test+coverage, build, vuln-scan, secret-scan, workflow-policy, image-scan
- `release.yml` — tag push (`v*`, `*.*.*`): production release, 8-platform matrix
- `beta.yml` — push to `beta` branch: beta releases
- `daily.yml` — 3am UTC daily + push to main/master: daily builds
- `docker.yml` — any branch push + version tags + 4am UTC daily: `build-standard` + `build-devel` (`:devel`) jobs
- `docker-aio.yml` — any branch push + version tags: All-in-One image only

Mirrors: Gitea/Forgejo use `.gitea/workflows/` or `.forgejo/workflows/` (same job structure). GitLab uses `.gitlab-ci.yml`.
