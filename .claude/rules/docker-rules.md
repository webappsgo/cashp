# Docker Rules (PART 27)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- NEVER place `Dockerfile` or `docker-compose.yml` in project root — always `docker/`
- NEVER write `docker/Dockerfile.build` for this Go project — `casjaysdev/go:latest` covers it
- NEVER use `LABEL` blocks in the Dockerfile — all OCI metadata is applied at build time by CI (`--annotation`/`--label`, e.g. via `docker/metadata-action`)
- NEVER modify `ENTRYPOINT` or `CMD` — all customization goes in `entrypoint.sh`
- NEVER include `build:` or `version:` keys in any compose file
- NEVER set `MODE`/`DEBUG` in production `docker-compose.yml` — only `.dev.yml`/`.test.yml`
- NEVER use `.env` files or list-style `environment:` — hardcode `KEY: value` map style
- NEVER push `:dev` or `:test` tags to the production registry
- Entrypoint NEVER creates dirs/perms/users or manages Tor — binary owns all of that

## CRITICAL - ALWAYS DO
- Multi-stage build: builder `casjaysdev/go:latest` → runtime `alpine:latest`
- Build context is project root (`.`); build with `-f docker/Dockerfile`
- Copy build-time overlay from `docker/rootfs/` (committed, mirrors container filesystem)
- Runtime volumes only: `./volumes/config:/config:z` and `./volumes/data:/data:z`
- `ENTRYPOINT ["tini", "-p", "SIGTERM", "--", "/usr/local/bin/entrypoint.sh"]`
- `STOPSIGNAL SIGRTMIN+3`; `EXPOSE 80` (internal port is always 80)
- `HEALTHCHECK` runs `{binary} --status`
- Every compose service uses the `x-logging: &default-logging` anchor
- Provide `docker-compose.yml` (prod), `docker-compose.dev.yml`, `docker-compose.test.yml`
- Release images build for `linux/amd64` AND `linux/arm64`

## KEY DECISIONS (pre-answered)

| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| Dockerfile location | `docker/Dockerfile` (never root) | Docker Directory Structure |
| Builder image | `casjaysdev/go:latest` | Dockerfile Requirements |
| Runtime image | `alpine:latest` | Dockerfile Requirements |
| Internal port | `80`, always | Dockerfile Requirements |
| Init system | `tini` | Dockerfile Requirements |
| How OCI labels are set | CI `--annotation`/`--label` at build, not `LABEL` | OCI Meta Labels / Multi-Arch Annotations |
| `docker/Dockerfile.build` for Go? | No — not needed | Dockerfile Requirements |
| prod `image:` tag | `:latest`; dev/test use `:devel` | Docker Compose Requirements |
| `MODE` default when unset | production (binary default) | Dockerfile Requirements |
| Tor in container | Auto-enabled if `tor` binary present, binary owns config | Tor in Container |

## TERMINOLOGY

| Term | Meaning |
|------|---------|
| `docker/rootfs/` | Committed build-time overlay copied into image (e.g. entrypoint.sh) |
| `./volumes/` | Host-side runtime bind mounts (`config/`, `data/`) — not committed |
| All-in-One (AIO) | Single container: app + embedded DB/cache, `Dockerfile.aio` + `all-in-one.yml` |
| Multi-Service | Separate containers per service (app/db/cache), standard `docker-compose.yml` |
| Annotations vs Labels | Annotations = manifest index (multiarch); Labels = per-platform image config |

---
For complete details, see AI.md PART 27
