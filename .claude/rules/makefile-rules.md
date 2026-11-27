# Makefile Rules (PART 26)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL — NEVER DO

- Never use Makefile targets in CI/CD workflows.
- Never run Go build or test commands directly on the host.
- Never leave generated binaries or release artifacts tracked.

## CRITICAL — ALWAYS DO

- Keep Makefile targets for local development and use the prescribed container toolchain.
- Build with `CGO_ENABLED=0`, reproducible metadata, and the complete platform matrix.
- Provide the required build, test, lint, coverage, docs, and cleanup targets with explicit behavior.
- Keep build output in ignored `binaries/` and release output in ignored `releases/`.

## Key rules

- CI expresses each command directly instead of invoking Makefile targets.
- Resolve the official site and build identity only from the sources prescribed by PART 26.

For complete details, see AI.md PART 26.
