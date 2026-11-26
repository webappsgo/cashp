# Configuration

The server reads `server.yml` from the resolved configuration directory. Its
typed schema and defaults are in `src/config`; settings are grouped under the
top-level `server`, `web`, and `tor` keys. Invalid values are reported during
validation. A missing file is created from defaults; a legacy
`server.yaml` is renamed to `server.yml` on startup.

## Paths

Directory flags take precedence over their matching environment variables,
which take precedence over operating-system defaults. The process chooses the
system or per-user path set from its privileges at startup.

| Path | Flag | Environment | Linux system default | Linux user default |
|---|---|---|---|---|
| Configuration | `--config` | `CONFIG_DIR` | `/etc/webappsgo/cashp` | `~/.config/webappsgo/cashp` |
| Data | `--data` | `DATA_DIR` | `/var/lib/webappsgo/cashp` | `~/.local/share/webappsgo/cashp` |
| Cache | — | `CACHE_DIR` | `/var/cache/webappsgo/cashp` | `~/.cache/webappsgo/cashp` |
| Logs | `--log` | `LOG_DIR` | `/var/log/webappsgo/cashp` | `~/.local/log/webappsgo/cashp` |
| PID file | `--pid` | `PID_FILE` | `/var/run/webappsgo/cashp.pid` | under the data directory |

`DATABASE_DIR` selects the database directory and `BACKUP_DIR` selects the
backup directory. In containers the SQLite directory defaults to
`/data/db/sqlite`. Other operating systems use their platform's application
data and support directories. See `src/common/paths` for the exact resolved
paths.

## Main settings

```yaml
server:
  mode: production
  address: "[::]"
  port: 64580
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

The schema includes listener and URL detection, TLS, request limits, trusted
proxies, sessions, rate limits, security, privacy, database, logging,
scheduler, backup, compliance, updates, metrics, GeoIP, notifications,
maintenance, feature, user, organization, I2P, web UI, and CORS settings. The
admin panel exposes field-driven server settings; restart-required values take
effect after restart. Scheduler settings are loaded from persisted admin
settings when the scheduler starts.

## Environment overrides

These names are read by the config loader. Values marked **first run** are
copied into a newly generated configuration and are not re-applied after
`server.yml` exists. Runtime values override the file each time it is loaded.

| Variable | When | Setting |
|---|---|---|
| `LISTEN` | First run | `server.address` |
| `PORT` | First run | `server.port` |
| `APPLICATION_NAME` | First run | `server.branding.title` |
| `APPLICATION_TAGLINE` | First run | `server.branding.tagline` |
| `DATABASE_DIR` | First run and path resolver | Database directory |
| `BACKUP_DIR` | First run and path resolver | Backup directory |
| `LOG_DIR` | First run and path resolver | Log directory |
| `MODE` | Runtime | `server.mode` |
| `DOMAIN` | Runtime | `server.fqdn` |
| `DATABASE_DRIVER`, `DATABASE_URL` | Runtime | Database connection |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_TLS` | Runtime | Outbound SMTP |
| `SMTP_FROM_NAME`, `SMTP_FROM_EMAIL` | Runtime | SMTP sender identity |

`COLOR` and `NO_COLOR` control terminal color handling. Directory environment
variables such as `CONFIG_DIR`, `DATA_DIR`, `CACHE_DIR`, `LOG_DIR`, and
`PID_FILE` are resolved by the shared path package rather than the YAML loader.

## Related

- [Command-line reference](cli.md)
- [Admin panel](admin.md)
- [Integrations](integrations.md)
