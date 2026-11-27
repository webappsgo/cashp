# Project Rules (PART 2, 3, 4)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL — NEVER DO

- Never commit runtime data, credentials, generated build output, or test artifacts as source.
- Never place application source outside `src/`, Dockerfiles outside `docker/`, or documentation outside its prescribed location.
- Never change stable internal names or filesystem paths without an explicit migration.
- Never omit required third-party license attribution.

## CRITICAL — ALWAYS DO

- Preserve the prescribed singular Go package layout and allowed root-file list.
- Keep `AGENTS.md` a real-file copy of the root `CLAUDE.md` loader.
- Keep only user/operator-facing project intent in `IDEA.md`; technical overrides belong in `SPEC.md`.
- Keep runtime files under the OS-specific config, data, log, cache, and temp paths.

## Key rules

- Resolve `{project_name}`, `{project_org}`, `{internal_name}`, and `{internal_org}` from IDEA.md and repository state.
- `docs/` contains only the MkDocs/ReadTheDocs documentation set.
- Team AI configuration is committed; personal settings and runtime state are ignored.

For complete details, see AI.md PART 2, 3, 4.
