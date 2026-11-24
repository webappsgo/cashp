# CasHp

CasHp is an open-source hosting control panel project in active development.
The running application currently provides a Go server, server-rendered public
pages, an administration panel foundation, health and API documentation
endpoints, and companion CLI and agent binaries. The complete product described
in [IDEA.md](https://github.com/webappsgo/cashp/blob/main/IDEA.md) is not yet
implemented; this project is not ready for production use.

!!! warning "Implementation status"
    Some pages in this documentation describe the planned interface or
    configuration. Check [TODO.AI.md](https://github.com/webappsgo/cashp/blob/main/TODO.AI.md)
    for unfinished implementation work. An endpoint or feature described in a
    guide is not available until the running server implements it.

## Run a development build

Requirements: Docker and GNU Make. Go builds run inside the project's Go
container.

```sh
git clone https://github.com/webappsgo/cashp.git
cd cashp
make local
```

The local build creates `cashp`, `cashp-cli`, and `cashp-agent` in `binaries/`.
Run `binaries/cashp --help` to inspect the server flags available in that
build. The server creates its default configuration on first run. See the
[configuration guide](configuration.md) for the current configuration
structure.

## Current implementation

- Go server composition with HTTP and optional HTTPS listeners.
- Server-rendered public pages and an administration panel foundation.
- Health, readiness, version, metrics, Swagger, and GraphQL documentation
  handlers.
- SQLite and other configured database drivers, scheduler, notifications,
  and feature packages under active development.
- Companion CLI and agent binaries for the required platform matrix.

The route and feature surface is still being completed. The API, admin,
integration, and security guides describe intended behavior and are being
reconciled with the mounted routes.

## Planned product scope

The product direction includes web hosting, application deployment,
containers, virtual machines, mail, DNS, databases, security, clustering,
multi-user accounts, organizations, custom domains, quotas and billing,
backups, monitoring, and support. [IDEA.md](https://github.com/webappsgo/cashp/blob/main/IDEA.md)
contains the business requirements. The project backlog tracks the work needed
to implement and verify them.

## Documentation

- [Installation](installation.md)
- [Configuration](configuration.md)
- [API reference](api.md)
- [CLI reference](cli.md)
- [Admin panel](admin.md)
- [Security](security.md)
- [Integrations](integrations.md)
- [Development](development.md)

## Links

- [Repository](https://github.com/webappsgo/cashp)
- [Issue tracker](https://github.com/webappsgo/cashp/issues)
- [Implementation backlog](https://github.com/webappsgo/cashp/blob/main/TODO.AI.md)

## License

MIT — see [LICENSE.md](https://github.com/webappsgo/cashp/blob/main/LICENSE.md).
