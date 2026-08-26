# Project Rules (PART 2, 3, 4)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## License & Attribution (PART 2)
- License: MIT (`LICENSE.md`, root)
- Copyright holder: CasjaysDev (maintainer_name in IDEA.md)
- Reference AI.md in generated code/comments/docs where template attribution is expected

## Directory Structure (PART 3)
- `src/` — all Go source
- `scripts/` — production/install scripts
- `tests/` — repo-root integration test scripts (`run_tests.sh`, `docker.sh`, `incus.sh`, `e2e.sh`); Go unit tests stay next to code as `*_test.go`
- `docker/` — `Dockerfile`, `Dockerfile.dev`, compose files, `rootfs/` (committed overlay)
- `docs/` — MkDocs/ReadTheDocs ONLY
- `binaries/`, `releases/`, `volumes/` — gitignored, runtime/build output only

## NEVER Create These Directories
`config/`, `data/`, `logs/`, `tmp/`, `temp/`, `test-data/`, `build/`, `dist/`, `out/`, `vendor/`, `node_modules/`, `lib/`, `libs/`, `utils/`, `common/` at repo root.
Exception: `src/data/` is allowed for static JSON embedded in the binary.

## Allowed Root Files (Exhaustive)
`AI.md`, `IDEA.md`, `CLAUDE.md`, `SPEC.md` (optional), `PLAN.md`/`PLAN.AI.md` (optional), `TODO.md`/`TODO.AI.md` (optional), `CLAUDE.local.md` (gitignored, optional), `README.md`, `LICENSE.md`, `Makefile`, `go.mod`, `go.sum`, `release.txt`, `site.txt` (optional), `.gitignore`, `.dockerignore`, `.gitattributes` (optional), `Jenkinsfile`, `mkdocs.yml`, `.readthedocs.yaml`, `.editorconfig` (optional).
**If a root file isn't in this list, it MUST NOT exist — ask before creating.**

## OS-Specific Paths (PART 4)
All on-disk paths use `{internal_org}/{internal_name}` = `webappsgo/cashp`, resolved per-OS (Linux/macOS/BSD/Windows/Docker). Config file is ALWAYS `server.yml`. Docker uses simplified `/config`, `/data` paths — never the host OS paths inside a container.

| Type | Linux (root) | Linux (user) | Docker |
|------|-------------|--------------|--------|
| Config | `/etc/webappsgo/cashp/server.yml` | `~/.config/webappsgo/cashp/server.yml` | `/config/cashp/server.yml` |
| Data | `/var/lib/webappsgo/cashp/` | `~/.local/share/webappsgo/cashp/` | `/data/cashp/` |
| Logs | `/var/log/webappsgo/cashp/` | `~/.local/log/webappsgo/cashp/` | `/data/log/cashp/` |
| Cache | `/var/cache/webappsgo/cashp/` | `~/.cache/webappsgo/cashp/` | `/data/cashp/cache/` |

Full per-OS table (macOS/BSD/Windows privileged+user): AI.md PART 4.

---
For complete details, see AI.md PART 2, 3, 4
