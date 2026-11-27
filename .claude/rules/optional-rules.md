# Optional Feature Rules (PART 34–36)

⚠️ **These rules are NON-NEGOTIABLE when a PART is enabled.** ⚠️

## CRITICAL — NEVER DO

- Never implement PART 34, 35, or 36 unless `SPEC.md` explicitly activates it.
- Never assume a route, table, middleware, or UI exists when its optional feature is disabled.
- Never allow cross-user, cross-organization, or custom-domain ownership bypasses.

## CRITICAL — ALWAYS DO

- When enabled, implement the full feature contract, including schema, migration, auth, API, pages, admin controls, and tests.
- Enforce user ownership, organization roles, and domain proof at every operation.
- Keep optional routes absent when their feature is not mounted.
- Audit sensitive membership, ownership, token, and domain changes.

## Key rules

- Activation is declared only under `## Optional parts` in `SPEC.md`; `IDEA.md` describes product intent but does not activate these PARTs.
- Multi-user, organization, and custom-domain flows share the baseline security, localization, and accessibility requirements.

For complete details, see AI.md PART 34, 35, 36.
