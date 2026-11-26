# CasHp

[![CI](https://github.com/webappsgo/cashp/actions/workflows/ci.yml/badge.svg)](https://github.com/webappsgo/cashp/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/webappsgo/cashp)](LICENSE.md)
[![Documentation](https://readthedocs.org/projects/webappsgo-cashp/badge/?version=latest)](https://webappsgo-cashp.readthedocs.io/en/latest/)

CasHp is a self-hosted hosting control panel for web hosting, applications,
containers, virtual machines, email, DNS, databases, security, and support.
The server is written in Go and serves a server-rendered web interface. The
project also builds `cashp-cli` and the optional `cashp-agent` for remote
machines. [IDEA.md](IDEA.md) defines the product; [AI.md](AI.md) is the
read-only implementation specification.

The server composition includes configuration and database startup and health,
administrator and account authentication, billing and support services,
notifications, scheduling, metrics, GeoIP, update checks, TLS handling,
server-rendered public and admin pages, and versioned HTTP APIs. OpenAPI and
GraphQL documentation are generated from registered routes. See the
[API reference](docs/api.md) for route families and the live OpenAPI document
for the exact contract of a running build.

## Build and start

Requirements: Docker and GNU Make. The Go toolchain runs inside
`casjaysdev/go:latest`.

```sh
git clone https://github.com/webappsgo/cashp.git
cd cashp
make local
./binaries/cashp
```

The server creates its default `server.yml` on first run, chooses and persists
an available port in the 64000–64999 range when no port is configured, and
prints a one-time administrator setup token. Redeem the token at the setup URL
shown in startup output. See the [installation](docs/installation.md) and
[configuration](docs/configuration.md) guides.

## Binaries

| Binary | Role |
|---|---|
| `cashp` | Server and web application |
| `cashp-cli` | Command-line client for the server API |
| `cashp-agent` | Optional remote agent that connects outbound to the server |

`make local` builds all three binaries for the local platform. `make build`
cross-compiles them for Linux, macOS, Windows, and FreeBSD on amd64 and arm64.
The server manages Linux hosts, as specified in [IDEA.md](IDEA.md).

## Documentation

- [Installation and first run](docs/installation.md)
- [Configuration](docs/configuration.md)
- [API reference](docs/api.md)
- [Command-line reference](docs/cli.md)
- [Admin panel](docs/admin.md)
- [Security](docs/security.md)
- [Integrations](docs/integrations.md)
- [Development](docs/development.md)
- [Translation and accessibility](docs/translating.md)

## Development

```sh
make test
```

The browser end-to-end suite is separate and runs on demand in Docker:

```sh
./tests/e2e.sh
```

See [docs/development.md](docs/development.md) for build, verification,
documentation, and contribution instructions.

## License

MIT — see [LICENSE.md](LICENSE.md).
