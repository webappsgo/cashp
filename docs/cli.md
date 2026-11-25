# Command Line Reference

CasHp currently builds three binaries: `cashp` (server), `cashp-cli` (client),
and `cashp-agent` (remote agent). Their available flags and commands are listed
below. Some client command names describe planned server capabilities; check the
[API reference](api.md) for the routes currently implemented by the server.

Show the exact version and help from the binary you installed:

```sh
cashp --version
cashp-cli --help
cashp-agent --help
```

## Server: `cashp`

The server accepts these flags:

| Flag | Description |
|---|---|
| `-color auto\|yes\|no` | Terminal color mode |
| `-debug` | Enable debug logging |
| `-mode production\|development\|debug` | Select runtime mode |
| `-v`, `-version` | Print version |

The server reads `server.yml` from its detected configuration directory. Its
listen address, ports, TLS, data paths, and other settings are configured there;
the server binary does not currently accept `--config`, `--data`, `--port`,
`--status`, or service-management flags.

## Client: `cashp-cli`

With no arguments, the client starts its interactive terminal interface. It
also supports these global options:

| Option | Description |
|---|---|
| `-h`, `--help` | Show help |
| `-v`, `--version` | Print version |
| `--shell completions\|init\|help` | Print shell completion instructions or scripts |
| `--server URL` | Select the server |
| `--token TOKEN` | Authenticate with a token |
| `--token-file FILE` | Read a token from a file |
| `--user NAME` | Select a user or organization context; use `@NAME` or `+NAME` to specify the type |
| `--config NAME` | Select the client configuration |
| `--output table\|json\|yaml\|plain\|csv` | Select output format |
| `--json` | Shorthand for JSON output |
| `--limit N` | Limit returned results |
| `--color MODE` | Select color mode |
| `--lang CODE` | Select output language |
| `--quiet`, `--verbose`, `--debug` | Adjust output detail |
| `--yes` | Confirm supported prompts |
| `--update [check\|yes]` | Check for or apply an update |
| `--admin COMMAND` | Run an administrative client command |

The command tree includes agents, alerts, apps, autodiscover, backups, billing,
certificates, containers, databases, DNS, domains, health, invoices, jobs,
login/logout, mailboxes, metrics, nodes, notifications, organizations, plans,
quotas, sites, status, tickets, tokens, users, version, VMs, and whoami. Use
`cashp-cli COMMAND --help` for command-specific syntax. Server-side API coverage
is still in progress; an advertised client command does not guarantee its
corresponding server operation is available.

## Remote agent: `cashp-agent`

The agent supports `status`, `test`, and `register` commands. Its options
include:

| Option | Description |
|---|---|
| `-h`, `--help` | Show help |
| `-v`, `--version` | Print version |
| `--shell completions\|init\|help` | Print shell completion instructions or scripts |
| `--config DIR`, `--data DIR`, `--log DIR` | Set agent paths |
| `--server URL`, `--token TOKEN`, `--org SLUG` | Set server connection details |
| `--mode MODE`, `--debug`, `--color MODE`, `--lang CODE` | Configure runtime/output |
| `--status` | Print agent status |
| `--service install\|uninstall\|start\|stop\|restart\|status` | Manage the agent service |
| `--update [CMD]` | Check for or apply an update |

Use `cashp-agent COMMAND --help` for details. Agent registration and server
integration are under active development.

## Completion and output behavior

Use `--help` on the installed binary as the authoritative list of supported
options for that build. Color and language handling can vary by command and
terminal; `NO_COLOR` is respected by supported terminal output.
