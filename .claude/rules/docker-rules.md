# Docker Rules (PART 27)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL — NEVER DO

- Never put a Dockerfile in the repository root; use `docker/`.
- Never run production or development Compose stacks during automated AI testing.
- Never use the project tree for runtime volume data or secrets.
- Never run the container as root when a non-root runtime is specified.

## CRITICAL — ALWAYS DO

- Keep Dockerfiles, Compose files, rootfs overlays, and image documentation under `docker/`.
- Use minimal, reproducible images and the prescribed non-root user, paths, health checks, and signal handling.
- Use only the automated test Compose file through project test scripts, with runtime data in project-scoped temp storage.

## Key rules

- Runtime directories follow `{config_dir}`, `{data_dir}`, and `{log_dir}`; image paths must preserve those contracts.
- Keep image tags, labels, and build arguments aligned with project metadata.

For complete details, see AI.md PART 27.
