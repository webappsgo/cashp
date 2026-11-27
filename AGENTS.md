# Project SPEC

Project: cashp
Role: Efficient loader for AI.md

⚠️ **THIS FILE IS AUTO-LOADED EVERY CONVERSATION. FOLLOW IT EXACTLY.** ⚠️

Purpose:
- This file is a short loader for the most important project rules.
- `AI.md` is the read-only implementation source of truth.
- `IDEA.md` defines product intent; `SPEC.md` activates optional parts and records technical exceptions.

## FIRST TURN — MANDATORY

On every new conversation or after context compaction:
1. Read the relevant `.claude/rules/*.md` for the current task.
2. Verify every implementation choice against the applicable AI.md PART.
3. Read the relevant IDEA.md business logic before changing product behavior.

## Project terminology

- Server: `cashp`.
- Required client: `cashp-cli`.
- Agent: `cashp-agent` (optional by AI.md; implemented only when IDEA.md requires it).
- Project/org: `cashp` / `webappsgo`.
- Frozen internal name/org: `cashp` / `webappsgo`.
- Admin URL root: `/server/{admin_path}` (default `administration`).

## Non-negotiable project rules

- Never edit `AI.md`; it is read-only. Fix project behavior and docs to match it.
- Use Argon2id for passwords, SHA-256 hashes for tokens, and never store plaintext credentials.
- Keep Dockerfiles under `docker/`; build with `CGO_ENABLED=0` for all eight required platforms.
- Use the built-in scheduler, server-rendered Go templates, mobile-first CSS, and no-JavaScript form flows.
- Validate all input on the server; never expose internal causes, secrets, or paths in user-facing errors.
- Tor support is required and auto-enabled when detected; implement I2P per PART 32.
- Keep all required features free; product billing may enforce resource quotas only, never feature access.
- Never use Makefile targets in CI/CD; use explicit commands.

## Work and verification

- Read the applicable AI.md PART in full before implementation and follow its cross-references.
- Use project TODO rules: `TODO.AI.md` is project implementation work; template issues go only in the parent SERVER inbox.
- Run tests/build/lint through the prescribed container workflow and report actual results.
- Keep runtime/test artifacts in `/tmp/webappsgo/cashp-XXXXXX/`; preserve user changes.
- Only the primary session prepares commits and pushes, through `gitcommit --dir {project_dir} all` after required gates.

## Rule index

- AI behavior: `.claude/rules/ai-rules.md` (PART 0–1).
- Project/layout: `.claude/rules/project-rules.md` (PART 2–4).
- Config and modes: `.claude/rules/config-rules.md` (PART 5–6, 12).
- Binaries and CLI: `.claude/rules/binary-rules.md` (PART 7–8, 33).
- Backend and overlays: `.claude/rules/backend-rules.md` (PART 9–11, 32).
- API and TLS: `.claude/rules/api-rules.md` (PART 13–15).
- Pages and admin: `.claude/rules/frontend-rules.md` (PART 16–17).
- Features: `.claude/rules/features-rules.md` (PART 18–23).
- Services: `.claude/rules/service-rules.md` (PART 24–25).
- Makefile, Docker, CI/CD: `.claude/rules/{makefile,docker,cicd}-rules.md` (PART 26–28).
- Tests, docs, i18n: `.claude/rules/testing-rules.md` (PART 29–31).
- Optional user/org/domain parts: `.claude/rules/optional-rules.md` (PART 34–36).

## Optional parts

`SPEC.md` activates PART 34 (Multi-User), PART 35 (Organizations), and PART 36 (Custom Domains) as REQUIRED for cashp.

For complete details, read `AI.md` and the applicable rule files. Do not treat this loader as a substitute for the spec.
