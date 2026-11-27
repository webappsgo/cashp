# Configuration

The server reads `server.yml` from the resolved configuration directory. Its
typed schema and defaults are in `src/config`; settings are grouped under the
top-level `server`, `web`, and optional `data` keys. Tor and I2P settings are
under `server.tor` and `server.i2p`. Invalid values are reported during
validation. A missing file is created from defaults; a legacy
`server.yaml` is renamed to `server.yml` on startup.

Shared page content belongs in area files under `{config_dir}/yaml.d`, not in
`server.yml`. Public Help Markdown and the MCP guide's project section are
stored at `server.pages.help.content` and `server.pages.help.mcp` in
`pages.yml`. Operator-authored About, Privacy,
Terms, header, and footer content belongs under `server.branding.content` in
`branding.yml`; page bodies use `{format: md, body: ...}` and header/footer
fragments use `{format: html, body: ...}`. Body may be one string or a map of
language codes to strings with an optional `default`. About and Privacy extend
their built-in pages; Terms replaces its built-in United States text. Header
and footer content is sanitized before rendering. Drop-ins use private
permissions (`0700` directory and `0600` files), and the normal config merge
applies their values.

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
  i18n:
    default_language: en
    available_languages: [en, es, fr, de, it, ar, ja, zh]
  tor:
    binary: ""
    expose: false
    use_network: false
    startup_timeout: 60
    bootstrap_timeout: 180
  i2p:
    enabled: false
    expose: false
web:
  ui:
    theme: dark
```

Tor has no enable toggle: a detected Tor binary starts the hidden service.
I2P is opt-in on bare metal and virtual machines and defaults on in a
container. The example above explicitly disables I2P.

The schema includes listener and URL detection, TLS, request limits, trusted
proxies, sessions, rate limits, security, privacy, database, logging,
scheduler, backup, compliance, updates, metrics, GeoIP, notifications,
maintenance, feature, user, organization, I2P, web UI, and CORS settings. The
admin panel exposes field-driven server settings; restart-required values take
effect after restart. Scheduler settings are loaded from persisted admin
settings when the scheduler starts.

`server.i18n.default_language` selects the fallback for server-rendered pages.
`server.i18n.available_languages` defaults to every embedded locale; operators
may narrow the list while keeping English, Spanish, French, German, Italian,
and Arabic enabled. The available list drives request negotiation and the
language selector. The embedded languages are English, Spanish, French,
German, Italian, Arabic, Japanese, and Chinese; see the
[translation guide](translating.md) for locale details.

## Privacy and consent

Privacy settings are under `server.privacy` and can be edited in the protected
admin page at `/server/{admin_path}/privacy`. The page validates and saves
changes to `server.yml` and applies them to public pages immediately. A
representative configuration is:

```yaml
server:
  privacy:
    data:
      sold: false
      stored_on_server: true
      sharing: []
    consent:
      show_until_acknowledged: true
      policy:
        text: Privacy Policy
        url: /server/privacy
        version: "1"
      buttons:
        decline: Decline
        accept: I Agree
      show_preferences: true
    cookies:
      preferences:
        enabled: true
      analytics:
        enabled: true
```

Non-essential cookie categories remain off until a visitor accepts them.
Disabling a category hides its control and forces its effective consent state
off. The `data.sold` setting selects the consent and analytics wording and
whether the privacy page displays the California opt-out control. The
`consent.policy.version` value is included in consent-update audit events. The
built-in data-collection, data-usage, and data-security copy is localized
project text; it is not loaded from `server.yml`. Operator-written additions
belong in `server.branding.content.privacy`.

## Analytics tracking

`server.tracking` selects one supported analytics provider. Tracking scripts
are rendered only when analytics is configured, the analytics category is
offered, and the visitor has accepted analytics cookies. Tracking is omitted
for Tor and I2P requests. Accepting in the banner activates external scripts
without reloading; the server still records consent and supplies the tracking
configuration. Configured provider hosts are allowed by CSP when analytics is
enabled, but no provider is requested before consent. The protected
`/server/{admin_path}/tracking` page validates and saves these settings,
checks configured self-hosted URLs, and previews the generated script.

```yaml
server:
  tracking:
    type: matomo
    id: "1"
    url: https://analytics.example.com
```

Supported types are `google`, `matomo`, `piwik`, `owa`, `fathom`,
`plausible`, `umami`, `simple`, and `cloudflare`. An empty type disables
tracking. Matomo, Piwik, OWA, and Umami require an absolute HTTP or HTTPS URL;
Fathom and Plausible accept one for self-hosted deployments. Google IDs,
Matomo/Piwik IDs, Plausible domains, and Umami UUIDs are validated before
configuration is saved or used. The configured provider is added automatically
to the privacy page's third-party disclosures; operator-entered services are
kept alongside it.

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
