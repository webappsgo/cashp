# Configuration

The server reads `server.yml` from the configuration directory selected by the
shared path resolver. The configuration model is defined in `src/config`; this
guide summarizes the file shape and the settings currently consumed by the
running server. Feature packages may define additional settings that are not
yet connected to runtime behavior.

## File location and defaults

The file name is `server.yml`. The default system and per-user paths are
resolved by the process identity and operating system. Related data, cache,
backup, and log directories use the same shared path rules. The server does
not accept a `--config` command-line flag; see the [command reference](cli.md).

If the file is missing, the config package starts from built-in defaults and
creates the configuration file. Invalid setting values are replaced with
defaults and reported as warnings. Unreadable or malformed YAML prevents
startup.

## Main structure

The configuration document has three top-level sections:

```yaml
server:
  mode: production
  address: "[::]"
  port: 8080
  admin_path: administration
  api_version: v1
  database:
    driver: sqlite
web:
  ui:
    theme: dark
tor:
  enabled: false
```

The exact accepted keys and default values are defined by the typed structs in
`src/config/config.go` and `src/config/defaults_values.go`. The schema includes
server identity and listeners, TLS, limits, trusted proxies, sessions, rate
limits, security, privacy, database, logs, scheduler, backups, metrics, GeoIP,
notifications, maintenance, feature flags, users, organizations, I2P, web UI,
and CORS settings. A schema entry may exist before its feature is wired into
the server.

## Settings consumed by the current server composition

The current main application reads these areas directly:

| Setting area | Runtime effect |
|---|---|
| `server.mode` | Selects runtime mode and debug behavior |
| `server.address`, `server.port` | Bind HTTP/HTTPS listeners |
| `server.baseurl`, `server.api_version`, `server.admin_path` | Configure routes and generated API documentation |
| `server.branding` | Public page and documentation identity |
| `server.ssl` | Configure HTTPS, certificate loading, redirects, and HSTS |
| `server.limits`, `server.rate_limit` | Configure request timeouts and rate limiting |
| `server.database` | Select and open the application database |
| `server.metrics` | Enable metrics routes and registry options |
| `server.notifications` | Initialize notification delivery |
| `web.ui.theme` | Set the default rendered page theme |
| `tor.enabled` | Configuration value; Tor service wiring is not complete |

Consult the [API reference](api.md) and [integration status](integrations.md)
before relying on settings for features not listed as active here.

## Environment variables

Environment overrides are implemented by the config package. The exact
first-run and runtime override lists are in `src/config/env.go`; they are not
repeated here because they change with the configuration implementation. Flags
supported by the server are documented in the [CLI reference](cli.md#server-cashp).

## Security

Keep the configuration file and its containing directory restricted to the
service account. Do not commit credentials or copy live secrets into issue
reports. The project is not yet ready for production deployment; review the
[security status](security.md) before exposing a development instance.
