# Command-line Reference

CasHp builds three binaries: `cashp` (server), `cashp-cli` (client), and
`cashp-agent` (optional remote agent). Each binary accepts `--help` and
`--version`; its help output is the definitive command list for that build.

## Server: `cashp`

| Flag | Description |
|---|---|
| `-h`, `--help` | Show flag help |
| `-v`, `--version` | Print build version |
| `--mode production\|development\|debug` | Set application mode |
| `--debug` | Enable debug logging and debug endpoints |
| `--color auto\|yes\|no` | Select terminal color behavior |
| `--config DIR` | Set the configuration directory |
| `--data DIR` | Set the data directory |
| `--log DIR` | Set the log directory |
| `--pid FILE` | Set the PID file path |
| `--address ADDR` | Set the listen address |
| `--baseurl PATH` | Set the URL path prefix |
| `--port PORT` | Set one HTTP port or an `HTTP,HTTPS` pair |

Without overrides, the server reads `server.yml` from the resolved config
directory. See [Configuration](configuration.md) for directory precedence and
environment variables.

## Client: `cashp-cli`

The client provides account and server commands plus resource command groups.
Use `cashp-cli --help` and `cashp-cli COMMAND --help` for the available groups
and command-specific arguments.

Global options can appear before or after command words:

| Option | Description |
|---|---|
| `-h`, `--help` | Show help |
| `-v`, `--version` | Print version |
| `--server URL` | Select a server |
| `--config NAME` | Select a client configuration |
| `--token TOKEN` | Supply an API token |
| `--token-file FILE` | Read an API token from a file |
| `--user NAME` | Select user or organization context |
| `--output`, `--format` | Select output format |
| `--json` | Use JSON output |
| `--limit N` | Limit returned items |
| `--color MODE`, `--no-color` | Select or disable color |
| `--lang CODE` | Select output language |
| `--quiet`, `--verbose`, `--debug` | Adjust output detail |
| `--yes` | Accept supported confirmation prompts |
| `--admin` | Include administrator-only command groups |
| `--shell [ACTION] [SHELL]` | Print shell completion or initialization instructions |
| `--update [ACTION]` | Check for or apply a client update |

The output formats and command groups are defined in `src/client/cmd`; the
live API reference describes the server routes used by commands.

## Remote agent: `cashp-agent`

| Option | Description |
|---|---|
| `-h`, `--help` | Show help |
| `-v`, `--version` | Print version |
| `--shell completions [SHELL]`, `--shell init [SHELL]` | Print shell integration |
| `--config DIR`, `--data DIR`, `--log DIR` | Set agent directories |
| `--server URL`, `--token TOKEN`, `--org SLUG` | Set enrollment and connection details |
| `--mode MODE`, `--debug`, `--color MODE`, `--lang CODE` | Configure runtime and output |
| `--status` | Show agent health |
| `--service CMD` | Install, uninstall, start, stop, restart, or inspect the agent service |
| `--update [CMD]` | Check for or apply an update |
| `--force` | Allow supported registration operation to replace existing state |

The positional agent commands are `status`, `test`, and `register`. The agent
opens outbound connections to the configured server and does not listen for
incoming commands.

## Related

- [Installation](installation.md)
- [Configuration](configuration.md)
- [API reference](api.md)
