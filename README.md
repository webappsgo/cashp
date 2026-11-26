# CasHp

[![CI](https://github.com/webappsgo/cashp/actions/workflows/ci.yml/badge.svg)](https://github.com/webappsgo/cashp/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/webappsgo/cashp)](LICENSE.md)
[![Documentation](https://readthedocs.org/projects/webappsgo-cashp/badge/?version=latest)](https://webappsgo-cashp.readthedocs.io/en/latest/)

## About

CasHp is an open-source, self-hosted hosting control panel project. It is in
active development; the product described in [IDEA.md](IDEA.md) is not yet
fully implemented, and this repository is not ready for production use.
[AI.md](AI.md) is the technical implementation specification.

## Features

The current codebase provides a Go server and companion `cashp-cli` and
`cashp-agent` packages, server-rendered web pages, configuration and runtime
scaffolding, an administration panel foundation, and feature packages for
billing and support. Many product workflows and API routes remain incomplete;
see [TODO.AI.md](TODO.AI.md) for the alignment backlog.

## Production

CasHp has no stable release yet. Do not use it for production workloads.
Installation and deployment instructions are being completed alongside the
implementation. The Docker assets are available under [`docker/`](docker/),
but have not been declared production-ready.

## Client

The companion CLI source is in `src/client/`. It is under active development
and does not yet provide complete API parity. Build it with the local target
below; use `cashp-cli --help` to inspect the commands available in that build.

## Configuration

The server reads `server.yml` from its configured config directory and creates
a default configuration on first run. The key names, defaults, and environment
overrides are documented in [docs/configuration.md](docs/configuration.md).
The admin interface is available at `/server/{admin_path}` when the server is
running; the default path segment is `administration`.

## API

The current API and protocol surface is documented in
[docs/api.md](docs/api.md). Implemented health endpoints are described there;
other documented routes may still be under development. The server's default
HTTP port is selected at runtime; see the generated configuration for the
actual listener address.

## Other

- [Installation guide](docs/installation.md)
- [Admin panel guide](docs/admin.md)
- [Security notes](docs/security.md)
- [Integrations](docs/integrations.md)
- [Translation guide](docs/translating.md)
- [Issue tracker](https://github.com/webappsgo/cashp/issues)

## Development

Requirements: Docker and GNU Make. Go builds and tests run in the configured
`casjaysdev/go` container.

```sh
git clone https://github.com/webappsgo/cashp.git
cd cashp
make local
make test
```

`make build` builds the server, CLI, and agent for all eight supported OS and
architecture combinations. See [docs/development.md](docs/development.md) for
build details and contribution guidance.

## Disclaimer

This software is provided "as is" without warranty of any kind. It is under
active development and may be incomplete, insecure, or unsuitable for any
particular purpose. Review and test it thoroughly before use. The authors are
not responsible for data loss, service interruption, or other damages arising
from its use. Third-party services remain subject to their own terms.

## License

MIT — see [LICENSE.md](LICENSE.md).
