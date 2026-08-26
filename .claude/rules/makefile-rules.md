# Makefile Rules (PART 26)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

- Makefile is for LOCAL DEV ONLY — never used in CI/CD (CI uses explicit commands)
- All Go builds happen inside `casjaysdev/go:latest` via Docker — NEVER run `go` on the host
- Targets: `make build` (all 8 platforms), `make local` (host platform, ldflags), `make dev` (fast, temp dir), `make release` (gh release), `make docker`, `make test`, `make clean`
- `make dev` output goes to `${TMPDIR:-/tmp}/{project_org}/{project_name}-XXXXXX/`, never the repo tree
- Version precedence: `VERSION` env > `release.txt` > `"devel"`

---
For complete details, see AI.md PART 26
