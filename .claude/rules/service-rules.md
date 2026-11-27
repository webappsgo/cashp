# Service Rules (PART 24, 25)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL — NEVER DO

- Never run privileged operations without validating the requested action and target.
- Never assume a service manager or init system exists.
- Never silently broaden an elevation fallback or hide an elevation failure.

## CRITICAL — ALWAYS DO

- Detect supported service managers and provide install, uninstall, start, stop, restart, and status behavior per platform.
- Keep privilege escalation explicit, consent-based, and limited to the requested operation.
- Use the documented permanent-root exception only where PART 24 permits it.
- Report unsupported service behavior clearly and safely.

## Key rules

- Prefer the detected elevation methods in the exact order specified in PART 23; use terminal-based `su` only after the documented consent flow.
- Service configuration and lifecycle behavior must be covered on supported platforms.

For complete details, see AI.md PART 24, 25.
