# Contributing

CasHp is a Go project that builds to three single static binaries. Everything
about the build is designed so that the result is reproducible and so that
nobody has to install a toolchain on their workstation.

## Requirements

The only hard requirement is **Docker**. The Go toolchain runs inside a
container; you do not need Go installed on the host.

```bash
git clone https://github.com/webappsgo/cashp.git
cd cashp
make local
```

!!! warning "Never build on the host"
    Every build and test target shells out to `casjaysdev/go:latest` through
    Docker. Running `go build` directly on the host bypasses the pinned
    toolchain and the shared module cache, and its output is not what CI
    produces.

## Repository layout

```text
src/            Go source — server entry point
src/client/     cashp-cli
src/agent/      cashp-agent
docker/         Dockerfile and compose files
docs/           This documentation
binaries/       Build output (gitignored)
releases/       Release artifacts (gitignored)
```

`src/` is the server's `main` package. `src/client/` and `src/agent/` are the
two additional binaries, and each is built only if its directory exists.

## Make targets

| Target | What it does |
|---|---|
| `make local` | Build server, CLI, and agent for the local platform into `binaries/` |
| `make build` | Build all three binaries for all eight platforms |
| `make dev` | Fast build with no ldflags into an isolated temp directory |
| `make test` | Run the full test suite with coverage enforcement |
| `make docker` | Build the multi-arch container image locally |
| `make release` | Build, package, and publish a GitHub release |
| `make clean` | Remove `binaries/` and `releases/` |

`make build` and `make local` both depend on `clean`, so each starts from an
empty output directory.

### Build variables

Everything is overridable on the command line.

| Variable | Default |
|---|---|
| `VERSION` | Contents of `release.txt`, else `devel` |
| `PLATFORMS` | `linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64 freebsd/amd64 freebsd/arm64` |
| `BINDIR` | `binaries` |
| `RELDIR` | `releases` |
| `REGISTRY` | `ghcr.io/{org}/{project}` |
| `GO_CACHE` | `$(HOME)/go/pkg/mod` |
| `GO_BUILD` | `$(HOME)/.cache/go-build/{project}` |
| `DOCKER_MEM` | `4g` |
| `DOCKER_CPUS` | `2` |

`PROJECT_NAME` and `PROJECT_ORG` are inferred from the git remote — never
hardcoded — falling back to the directory path when there is no remote.

```bash
make build VERSION=1.2.0
make build PLATFORMS="linux/amd64 linux/arm64"
make docker REGISTRY=registry.example.com/cashp
```

The memory and CPU caps exist so a single build container cannot starve other
concurrent builds on the same host.

### Embedded build info

The linker embeds version metadata into every binary:

| Symbol | Source |
|---|---|
| `main.Version` | `VERSION` |
| `main.CommitID` | `git rev-parse --short=7 HEAD` |
| `main.BuildEpoch` | Unix seconds, UTC, captured once |
| `main.BuildDate` | RFC 3339 UTC, derived from `BUILD_EPOCH` |
| `main.OfficialSite` | `site.txt` or the `OFFICIAL_SITE` environment variable |

`BUILD_EPOCH` is the single captured time source; `BUILD_DATE` is derived from
it rather than sampled separately, so the two can never disagree. The epoch is
what lets the updater tell whether a daily build is newer than the running one.

`OFFICIAL_SITE` is optional and is never guessed from the project name or a
domain. Self-hosted deployments leave it empty and use `--server`.

Builds use `-trimpath` and `-s -w`, with `CGO_ENABLED=0` and
`GOFLAGS=-buildvcs=false` — the latter because a bind-mounted `.git` with a
mismatched UID makes `go build` fail inside the container.

## Testing

```bash
make test
```

Tests run `go test -v -cover ./...` inside the container with a **60% coverage
floor**. The target fails the build if total coverage drops below it, so a
change that adds code without adding tests will not pass.

Coverage profiles are written to a temporary directory under
`$TMPDIR/{org}/{project}-XXXXXX`, never into the repository — build artifacts
do not belong in the source tree.

Never skip tests to save time. `make test` must pass before every commit.

## Docker

`make docker` builds `docker/Dockerfile` for `linux/amd64` and `linux/arm64`
via buildx. It is a multi-stage build, so Go compilation happens inside the
image build — no pre-built binaries are needed.

The target builds locally and does **not** push. Publishing to a registry is
CI/CD's job, not a developer's laptop's.

## Documentation

Documentation is MkDocs with the Material theme, and it lives entirely in
`docs/` with `mkdocs.yml` at the repository root.

```bash
pip install -r docs/requirements.txt
mkdocs serve
```

Then open <http://127.0.0.1:8000>. `mkdocs build --strict` is what the docs
build must pass — a broken internal link or a page missing from the nav is an
error, not a warning.

When you change behavior, change the docs in the same commit. Documentation
accuracy is a hard rule here: every command, flag, path, port, environment
variable, and config key in these pages must be verifiable in the source. If
you cannot verify it, do not document it.

## Code conventions

- **Go directory names are singular** — `handler/`, `model/`, `middleware/` —
  to match their package names. Tooling directories (`scripts/`, `docs/`) stay
  plural.
- **`gofmt` decides formatting.** Tabs in Go, two spaces in YAML.
- **No `TODO`, `FIXME`, or `HACK` in committed code**, and no commented-out
  code. Every committed line must work as written — no stubs, no placeholders.
- **Comments go above the line they describe**, never inline, and never inside
  pure data formats such as JSON or `KEY=VALUE` files.
- **Reuse before creating.** Search for an existing helper, constant, or
  component before adding a new one.
- **Every text file ends with exactly one trailing newline.**

## Commits

One logical change per commit. Before committing:

1. `git status --porcelain` and `git diff --stat` to see the real change set
2. `make test` — every test must pass
3. Write the commit message from that output, describing every changed file

Never commit credentials, tokens, API keys, or private keys. All repositories
are treated as public.

## Reporting bugs

Open an issue at
<https://github.com/webappsgo/cashp/issues> with the output of `cashp
--version`, the operating system and version, and the steps to reproduce.

For **security** vulnerabilities, do not open a public issue — follow
[the reporting process](security.md#reporting-a-vulnerability) instead.
