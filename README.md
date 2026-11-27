# CasHp

[![CI](https://github.com/webappsgo/cashp/actions/workflows/ci.yml/badge.svg)](https://github.com/webappsgo/cashp/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/webappsgo/cashp)](https://github.com/webappsgo/cashp/releases)
[![License](https://img.shields.io/github/license/webappsgo/cashp)](LICENSE.md)
[![Docs](https://readthedocs.org/projects/webappsgo-cashp/badge/?version=latest)](https://webappsgo-cashp.readthedocs.io/en/latest/)

## About

CasHp is a self-hosted hosting control panel for websites, applications,
containers, virtual machines, email, DNS, databases, security, backups,
monitoring, and support. It includes the `cashp` server, the required
`cashp-cli` companion, and the optional `cashp-agent` for remote machines.
Product scope and behavior are defined in [IDEA.md](IDEA.md); [AI.md](AI.md)
is the read-only implementation specification.

The server renders public, account, and administration pages with Go
templates. Pages work without JavaScript, with browser scripts used only for
progressive enhancement. The running server exposes versioned APIs and
generates its OpenAPI contract from registered routes.

## Features

- Server-rendered public, account, and administration pages
- User, organization, and API-token management
- Hosting, database, workload, billing, and support management
- Scheduling, backups, notifications, metrics, GeoIP, and updates
- Tor hidden-service support and optional I2P hosting
- Static server, client, and optional remote-agent binaries

The embedded language catalogs support English, Spanish, French, German,
Italian, Arabic, Japanese, and Chinese. The server's `available_languages`
setting can narrow this list while retaining the six required languages.

## Production

Requirements: Docker and GNU Make. The Go toolchain runs inside
`casjaysdev/go:latest`.

```sh
git clone https://github.com/webappsgo/cashp.git
cd cashp
make local
./binaries/cashp
```

The server creates its default `server.yml` on first run, selects and persists
an available port in the configured range when no port is set, and prints a
one-time administrator setup token. Redeem it at the setup URL shown in
startup output. See [installation](docs/installation.md) and
[configuration](docs/configuration.md).

For a container image, use `make docker`. Persistent application data belongs
in the configured data and backup directories; see the installation guide
before deploying.

## Client

`cashp-cli` is the command-line companion for interacting with a CasHp server.
`cashp-agent` is an optional service for remote machines and connects outbound
to the configured server. `make local` builds all three binaries for the local
platform. See the [CLI reference](docs/cli.md) and run each binary with
`--help` for its supported commands and options.

## Configuration

The server reads `server.yml` from its resolved configuration directory.
Command-line directory options and environment variables can override the
default paths. See the [configuration guide](docs/configuration.md) for path
precedence, environment variables, and settings.

## API

The server includes health and readiness probes, client autodiscovery,
account and organization APIs, tenant hosting and managed database APIs,
billing and support APIs, and administration endpoints. The running server's
generated OpenAPI document is the exact route contract. See the [API
reference](docs/api.md).

## Other

- [Admin panel](docs/admin.md)
- [Security](docs/security.md)
- [Integrations](docs/integrations.md)
- [Translation and accessibility](docs/translating.md)
- [Development](docs/development.md)

## Development

Build and run the unit coverage gate with:

```sh
make test
```

Phase-2 endpoint checks run against an already-running server and do not build
or start one. They read the live OpenAPI document, enumerate public and
module/admin browser page routes, and exercise every advertised API method
and path. GET routes receive JSON, plain-text, and HTML content-negotiation
requests, alongside explicit metrics and static-route checks:

```sh
CASHP_BASE_URL=http://localhost:64580 ./tests/test_endpoints.sh
```

The browser end-to-end suite is separate and runs on demand in Docker:

```sh
./tests/e2e.sh
```

See the [development guide](docs/development.md) for build, verification, and
contribution instructions.

## Disclaimer

This software is provided "as is" without warranty of any kind. Use at your
own risk.

- **No Warranty**: The authors are not responsible for damages, data loss, or
  issues arising from use of this software.
- **Not Professional Advice**: This software does not constitute legal,
  financial, medical, or other professional advice.
- **Third-Party Services**: External APIs and services have separate terms of
  service.
- **Security**: No software is guaranteed to be free of vulnerabilities.
- **Production Use**: Evaluate thoroughly before deploying in production.

By using this software, you acknowledge that you have read and understood this
disclaimer.

## License

MIT — see [LICENSE.md](LICENSE.md).
