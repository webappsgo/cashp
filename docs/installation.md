# Installation and First Run

CasHp is built from Go source using the project toolchain container. Docker
and GNU Make are required for the documented build targets.

## Build from source

```sh
git clone https://github.com/webappsgo/cashp.git
cd cashp
make local
```

`make local` builds `cashp`, `cashp-cli`, and `cashp-agent` for the local
platform into `binaries/`. `make build` cross-compiles the three binaries for
Linux, macOS, Windows, and FreeBSD on amd64 and arm64. The server manages Linux
hosts, as specified in [IDEA.md](https://github.com/webappsgo/cashp/blob/main/IDEA.md).

`make docker` invokes Docker Buildx for Linux amd64 and arm64. The target does
not push the resulting build to a registry.

## Start the server

Run the server in the foreground:

```sh
./binaries/cashp
```

On first start, CasHp creates `server.yml` in the resolved configuration
directory and initializes its runtime data. The startup output provides the
one-time setup token and setup URL for creating the primary administrator.
Keep the token private and redeem it once. See [Configuration](configuration.md)
for path resolution, environment variables, and server flags.

The default listener is dual-stack at `[::]`; the first-run port is selected
from the configured default range and persisted in `server.yml`. Configure
`server.address`, `server.port`, and, when needed, `server.baseurl` before
exposing the service.

## Persistent data

The configuration, database, logs, backups, and cache use the directories
resolved by the shared path package. SQLite stores server data in
`server.db` and user data in `users.db`. Container deployments use
`/data/db/sqlite` for SQLite by default. Mount persistent storage for the
configured data and backup directories.

## Agent service

`cashp-agent` supports service installation and lifecycle operations on the
platforms handled by its service package. Run `cashp-agent --help` on the
target host to see the supported operations and privilege requirements. Agent
enrollment uses a server URL, token, and optional organization slug.

## Related

- [Configuration](configuration.md)
- [Command-line reference](cli.md)
- [Integrations](integrations.md)
