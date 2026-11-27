# CI/CD Rules (PART 28)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL — NEVER DO

- Never call Makefile targets from CI/CD.
- Never expose secrets to fork pull requests or untrusted code.
- Never use mutable/unverified third-party workflow actions where a pinned reference is required.
- Never publish a release or image from an unapproved branch or tag event.

## CRITICAL — ALWAYS DO

- Maintain equivalent required workflows for GitHub, Gitea/Forgejo, GitLab, and Jenkins.
- Run build, tests, lint, coverage, security, workflow-policy, and required platform checks.
- Pin external actions/plugins as specified and verify pinned revisions.
- Keep release artifacts, provenance, tags, and release notes consistent across providers.

## Key rules

- CI commands are explicit shell commands and use the prescribed isolated toolchain.
- Provider-specific trigger and secret behavior must be checked against PART 28 before edits.

For complete details, see AI.md PART 28.
