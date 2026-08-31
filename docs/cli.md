# CLI Reference

CasHp ships three binaries. All of them are single static executables with no
runtime dependencies.

| Binary | Purpose | Key flags |
|---|---|---|
| `cashp` | Runs the control-panel server | `--config`, `--data`, `--port`, `--mode` |
| `cashp-cli` | User/admin client for a running server | `--server`, `--token`, `--user` |
| `cashp-agent` | Reports a remote node back to a server | `--server`, `--token`, `--config` |

Every binary can be renamed. Help and version output always show the **actual**
binary name; internal identifiers (User-Agent, default paths, config keys)
always use the frozen `cashp` / `webappsgo` names regardless of the filename.

## Universal flags

Present on all three binaries.

| Flag | Short | Description |
|---|---|---|
| `--help` | `-h` | Show help and exit. `--help` after any command shows that command's help |
| `--version` | `-v` | Show version and exit |
| `--shell completions [SHELL]` | — | Print the completion script to stdout (shell auto-detected when omitted) |
| `--shell init [SHELL]` | — | Print the eval-wrapped completions command |
| `--debug` | — | Enable debug logging and debug features |
| `--color {auto\|yes\|no}` | — | Color output; default `auto`, respects `NO_COLOR` |
| `--lang CODE` | — | Output language; default auto-detected from `LANG` |

`--version` output is identical in shape across all three:

```console
$ cashp --version
cashp 1.0.0 (abc1234) built 2026-08-26
```

Supported completion shells: `bash`, `zsh`, `fish`, `sh`, `dash`, `ksh`,
`powershell`, `pwsh`.

```bash
eval "$(cashp --shell init)"
cashp --shell completions bash > /etc/bash_completion.d/cashp
```

## Color and emoji

All three binaries honor the [NO_COLOR](https://no-color.org/) standard. Any
non-empty `NO_COLOR` value disables both ANSI colors **and** emoji in terminal
output. `TERM=dumb` does the same.

Resolution order, highest first:

1. `--color=yes` / `--color=no`
2. Config file (`output.color`)
3. `NO_COLOR` environment variable
4. Auto-detect (TTY check plus `TERM`)

`NO_COLOR` does not affect bold/underline/italic styling, Unicode box-drawing
characters, or output structure.

```bash
NO_COLOR=1 cashp --status
NO_COLOR=1 cashp --status --color=yes
```

## `cashp` — server

This is the complete server command set.

### Information

| Flag | Description |
|---|---|
| `--status` | Show server status and health; exit `0` healthy, `1` unhealthy |

### Paths and runtime

All directory flags create the directory if it does not exist.

| Flag | Description | Default (root) | Default (non-root) |
|---|---|---|---|
| `--config DIR` | Config directory | `/etc/webappsgo/cashp/` | `~/.config/webappsgo/cashp/` |
| `--data DIR` | Data directory | `/var/lib/webappsgo/cashp/` | `~/.local/share/webappsgo/cashp/` |
| `--cache DIR` | Cache directory | `/var/cache/webappsgo/cashp/` | `~/.cache/webappsgo/cashp/` |
| `--log DIR` | Log directory | `/var/log/webappsgo/cashp/` | `~/.local/log/webappsgo/cashp/` |
| `--backup DIR` | Backup directory | `/mnt/Backups/webappsgo/cashp/` when writable, else `{data_dir}/backup/` | `~/.local/share/Backups/webappsgo/cashp/` |
| `--pid FILE` | PID file | `/var/run/webappsgo/cashp.pid` | inside the data directory |

`--config` also accepts a *file* selector rather than a directory:

| Value | Resolves to |
|---|---|
| `--config test` | `{config_dir}/test.yml` |
| `--config dev.yml` | `{config_dir}/dev.yml` |
| `--config /etc/webappsgo/cashp/prod.yml` | that absolute path |

| Flag | Description |
|---|---|
| `--address ADDR` | Listen address (default `0.0.0.0`) |
| `--port PORT` | Listen port (default: a random port in `64000`–`64999`; `80` in a container) |
| `--baseurl PATH` | URL path prefix (default `/`) |
| `--mode {production\|development\|debug}` | Application mode (default `production`) |
| `--daemon` | Fork and detach from the terminal |

`--port` accepts a single port or a comma-separated HTTP/HTTPS pair:

| Value | Ports | Privilege | Scheme |
|---|---|---|---|
| `--port 8080` | 8080 | user | HTTP |
| `--port 443` | 443 | root | HTTPS |
| `--port 80,443` | 80, 443 | root | HTTP + HTTPS |
| `--port 8080,8443` | 8080, 8443 | user | HTTP + HTTPS |

`--mode` accepts aliases: `dev`/`devel`/`development` → development,
`prod`/`production` → production, `debug` → debug (which turns the debug flag
on by default).

!!! note "`--daemon` under a supervisor"
    Under systemd, launchd, runit/s6, or in a container, the server always runs
    in the foreground and `--daemon` is ignored — the supervisor owns the
    process lifecycle. Under SysV init and BSD `rc.d` it always daemonizes.

### `--service`

```bash
cashp --service {start|stop|restart|reload|--install|--disable|--uninstall|--help}
```

| Command | Effect |
|---|---|
| `start` | Start the service |
| `stop` | Stop the service |
| `restart` | Restart the service |
| `reload` | Reload configuration without restarting |
| `--install` | Install, enable, and start the service |
| `--disable` | Stop and disable the service, keeping the service file and all data |
| `--uninstall` | Stop, disable, and remove the service file, data, config, cache, logs, and system user; leaves the binary in place |

`--service --help` prints the current install state (installed/not installed,
running/stopped/disabled, auto-start, PID).

When run as root, a system service is installed; otherwise a per-user service
is installed. See [Installation](installation.md#service-install) for the
per-init file locations.

### `--maintenance`

```bash
cashp --maintenance {backup|restore|update|mode|setup|--help} [argument]
```

| Command | Effect |
|---|---|
| `backup [file]` | Create a backup of all data. Default target: `{backup_dir}/cashp-{timestamp}.tar.gz` |
| `restore <file>` | Stop the server, restore data from the backup, restart |
| `update [check\|yes\|branch <name>]` | Same as `--update` below |
| `mode <mode>` | Set the application mode (`production`, `development`) |
| `setup` | Run the interactive setup wizard: create the primary global admin and configure the server |

```bash
cashp --maintenance backup
cashp --maintenance backup /path/to/backup.tar.gz
cashp --maintenance restore /path/to/backup.tar.gz
cashp --maintenance mode development
cashp --maintenance setup
```

!!! warning "Privileged maintenance commands"
    `setup` runs only on first run or with a valid setup token. `restore`
    requires admin authentication, root, or an empty database — it overwrites
    all data. `mode` requires admin authentication or root.

    `--maintenance mode development` relaxes development defaults but does
    **not** enable debug output; use `--debug` or `DEBUG=true` for that.

### `--update`

```bash
cashp --update [check|yes|branch {stable|beta|daily}|--help]
```

| Command | Effect |
|---|---|
| `check` | Compare the running version against the latest release |
| `yes` | Download the latest release, replace the binary, and restart |
| `branch <name>` | Switch the update channel: `stable` (default), `beta`, `daily` |

### Flag-to-environment mapping

Several server flags have equivalent environment variables. The flag always
wins.

| Flag | Environment variable |
|---|---|
| `--config` | `CONFIG_DIR` |
| `--data` | `DATA_DIR` |
| `--log` | `LOG_DIR` |
| `--pid` | `PID_FILE` |
| `--port` | `PORT` |
| `--address` | `LISTEN` |
| `--mode` | `MODE` |

The full environment-variable surface, including which variables apply only on
first run, is documented in [Configuration](configuration.md#environment-variables).

## `cashp-cli` — client

Running `cashp-cli` with no arguments launches interactive TUI mode. Version
and help are flags, never subcommands. There is no `config` subcommand — edit
`cli.yml` directly or pass `--server`/`--token`. `cli.yml` is auto-created on
first run with working defaults.

| Flag | Description |
|---|---|
| `--server URL` | Server URL (default: from config) |
| `--token TOKEN` | API token |
| `--token-file FILE` | Read the API token from a file |
| `--user NAME` | Target user or organization |
| `--config NAME` | Config profile name (default `cli.yml`) |
| `--admin CMD` | Admin operations; requires an admin token |
| `--admin server CMD` | Server-admin operations |

### User and organization context

`--user` resolves against both users and organizations. Prefixes force the
namespace when a user and an org share a name:

| Form | Meaning |
|---|---|
| `--user NAME` | Auto-detect: server decides user or org |
| `--user @NAME` | Force user context |
| `--user +NAME` | Force org context |

The client translates that into URL-scoped API routes:

| Invocation | Request |
|---|---|
| `--user @alice list` | `GET /api/{api_version}/users/alice/{resource}` |
| `--user +acme-corp list` | `GET /api/{api_version}/orgs/acme-corp/{resource}` |
| `--user alice list` | Auto-detected to whichever of the two exists |

### Token resolution

Highest priority first:

1. `--token` flag — saved into `cli.yml` only when the stored value is empty
   or invalid
2. `CASHP_TOKEN` environment variable — never written to config
3. `auth.token` in `cli.yml`

The same "save only if empty or invalid" rule applies to `--server` and
`server.primary`. `cli.yml` is written with restrictive permissions because it
holds a bearer token.

Config lives at `{config_dir}/cli.yml` — `~/.config/webappsgo/cashp/cli.yml`
for a normal user.

## `cashp-agent` — remote node agent

The agent uses the same flag style as the server, minus `--port` and
`--address`: it never serves HTTP.

### Commands

| Command | Description |
|---|---|
| `status` | Show agent status |
| `test` | Test the connection to the server |
| `register` | Interactive registration with a server |

### Flags

| Flag | Description |
|---|---|
| `--status` | Health check; exit `0` healthy, `1` unhealthy |
| `--config DIR` | Config directory |
| `--data DIR` | Data directory |
| `--log DIR` | Log directory |
| `--server URL` | Server URL to connect to |
| `--token TOKEN` | Authentication token issued by the server |
| `--mode {production\|development\|debug}` | Force the runtime mode (auto-detected by default) |
| `--service {install\|uninstall\|start\|stop\|restart\|status}` | Service management |
| `--update [check\|yes]` | Self-update |

`--server` and `--token` can be set in `agent.yml` instead of passed on every
invocation.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success — for `--status`, also "healthy" |
| `1` | Failure — for `--status`, also "unhealthy" |

`--status` exiting `0`/`1` is what the Docker image's health check depends on.
