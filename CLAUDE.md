# cashp — Claude Code Memory

Read `AI.md` (source of truth, read-only, ~65k lines) and `IDEA.md`
(business logic, WHAT this project is) before implementing anything.

## FIRST TURN - READ THIS

On EVERY new conversation or after context compaction:
1. Read the relevant `.claude/rules/*.md` files for the current task.
2. NEVER assume — always verify against `AI.md` before implementing.

## Binary Terminology
- **server** = `cashp` (main binary, runs as service)
- **client** = `cashp-cli` (REQUIRED companion, CLI/TUI/GUI)
- **agent** = `cashp-agent` (optional, runs on remote machines)

## Key Placeholders
- `{project_name}` = cashp
- `{project_org}` = webappsgo
- `{internal_name}` = cashp (frozen)
- `{internal_org}` = webappsgo (frozen)

## NEVER Do — VIOLATIONS ARE BUGS
1. Use bcrypt → Argon2id
2. Put Dockerfile in root → `docker/Dockerfile`
3. Use CGO → CGO_ENABLED=0 always
4. Hardcode dev values → detect at runtime
5. Use external cron → internal scheduler (PART 19)
6. Store passwords plaintext → Argon2id (tokens use SHA-256)
7. Create premium tiers → all features free, billing gates quota only
8. Use Makefile in CI/CD → explicit commands only
9. Guess a value a command can produce → run the command, or ask
10. Skip platforms → build all 8 (linux/darwin/windows/freebsd × amd64/arm64)
11. Client-side rendering (React/Vue) → server-side Go templates
12. Add JS for anything HTML5+CSS already does → JS is a last resort
13. Let long strings break mobile → word-break CSS
14. Skip validation → server validates everything
15. Implement without reading spec → read relevant PART first
16. Modify `AI.md` — read-only; project changes go in `IDEA.md`/`SPEC.md`
17. Edit `## Project variables` in IDEA.md without confirming with the user
18. Read an image >1000x1000 directly — resize first
19. Use non-conforming IDEA.md without migration

## ALWAYS Do — NON-NEGOTIABLE
1. Read AI.md before implementing any feature
2. Server-side processing
3. Mobile-first responsive CSS
4. All features work without JavaScript
5. Tor hidden service support (auto-enabled if Tor found)
6. Built-in scheduler, GeoIP, metrics, email, backup, update
7. Full admin panel with ALL settings
8. Client binary for the project (`cashp-cli`)
9. Commit often, small focused commits; subagents never commit

## File Locations
- Config: `{config_dir}/server.yml`
- Data: `{data_dir}/`
- Logs: `{log_dir}/`
- Source: `src/`
- Docker: `docker/`

## Where to Find Details
- AI behavior: `.claude/rules/ai-rules.md` (PART 0, 1)
- Project structure: `.claude/rules/project-rules.md` (PART 2, 3, 4)
- Config/modes: `.claude/rules/config-rules.md` (PART 5, 6, 12)
- Binaries/CLI: `.claude/rules/binary-rules.md` (PART 7, 8, 33)
- Backend: `.claude/rules/backend-rules.md` (PART 9, 10, 11, 32)
- API: `.claude/rules/api-rules.md` (PART 13, 14, 15)
- Frontend/admin: `.claude/rules/frontend-rules.md` (PART 16, 17)
- Features: `.claude/rules/features-rules.md` (PART 18-23)
- Service: `.claude/rules/service-rules.md` (PART 24, 25)
- Makefile: `.claude/rules/makefile-rules.md` (PART 26)
- Docker: `.claude/rules/docker-rules.md` (PART 27)
- CI/CD: `.claude/rules/cicd-rules.md` (PART 28)
- Testing/docs/i18n: `.claude/rules/testing-rules.md` (PART 29-31)
- Optional (multi-user/orgs/domains): `.claude/rules/optional-rules.md` (PART 34-36)
- Full spec: `AI.md` (~65k lines) ← **SOURCE OF TRUTH**
- Product/business logic: `IDEA.md`

## Current Project State
- Bootstrap (PART 0-6) DONE: layout, CLAUDE.md loader, .claude/rules/,
  Makefile, go.mod, gitignore/dockerignore, mode/config scaffolding
- Next: work through `TODO.AI.md` in dependency order (PART 7 onward)
- Relevant PARTs: 0-6 (done), 7-36 + IDEA.md domain backlog (see TODO.AI.md)
