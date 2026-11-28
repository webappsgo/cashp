# Installation and First Run

CasHp can run in Docker or as a native binary. Building from source requires
Docker and GNU Make; the Go toolchain runs in the project's container.

## Docker

The published image includes the server and Tor. This command publishes the
HTTP listener on the host loopback interface and keeps application data in a
named volume:

```sh
docker run -d \
  --name cashp \
  -p 127.0.0.1:64580:80 \
  -v cashp-data:/data \
  ghcr.io/webappsgo/cashp:latest
```

Place a TLS reverse proxy in front of the loopback listener when serving
clearnet traffic. The image's default health check reports server readiness.

## Binary

Download the release binary for the target operating system and architecture
from the [GitHub releases page](https://github.com/webappsgo/cashp/releases),
then mark it executable and run it:

```sh
chmod +x cashp-linux-amd64
./cashp-linux-amd64
```

Use the matching release artifact for other supported platforms. Run
`cashp --help` to see runtime options.

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

The default listener is dual-stack at `[::]`. On first run outside an
application container, the server selects an unused port from `64000–64999`
and saves it to `server.yml`; inside Docker, Podman, or Kubernetes, the
container listener defaults to port `80` and the host-side mapping supplies
the external port. Configure `server.address`, `server.port`, and, when
needed, `server.baseurl` before exposing the service.

## Linux systemd service

On a Linux host running systemd, install the service with the server binary:

```sh
sudo ./cashp --service --install
```

The installer writes and enables the system unit, then starts it. When run
without system privileges, the server installs a per-user service when the
host supports one. A system service runs as root for its lifetime under the
project-specific AI.md PART 24 exception: the control plane manages native
host services, firewall and network settings, container engines, and
hypervisors. Tenant workloads remain isolated from the server process. Check
the service manager and current status with:

```sh
cashp --service --help
systemctl status cashp
```

Use `cashp --service restart` to restart after changing settings that require
a restart. `cashp --service --disable` stops and disables the service while
keeping its data. `cashp --service --uninstall` removes the service and its
state after confirmation; it keeps the binary. For a per-user service, use
`systemctl --user status cashp` and the corresponding `systemctl --user`
commands.

## Persistent data

The configuration, database, logs, backups, and cache use the directories
resolved by the shared path package. SQLite stores server data in
`server.db` and user data in `users.db`. Container deployments use
`/config/cashp/` for configuration and `/data/` for application state,
including `/data/db/sqlite/` for SQLite, `/data/log/cashp/` for logs, and
`/data/backups/cashp/` for backups. Persist both `/config` and `/data` with
volume mounts. Native installs use their platform-specific resolved paths.

## Agent service

When the optional `cashp-agent` binary is enabled, sign in to the server and
open **Agents** in the server panel. Choose **Add Agent** to set its name,
tags, and token expiry. The panel shows a one-time command containing the
agent-scoped token; copy it to the target host and run it with the required
service-management privileges. The agent registers, stores its configuration,
and installs itself as a service. Treat the command as a secret until it has
been used, then clear it from shell history and other temporary locations.

Use `cashp-agent test` to check the server connection and enrollment, and
`cashp-agent status` to inspect the local enrollment state. For manual
registration or service lifecycle commands, run `cashp-agent --help` on the
target host to see the supported operations and privilege requirements.

## Related

- [Configuration](configuration.md)
- [Command-line reference](cli.md)
- [Integrations](integrations.md)
