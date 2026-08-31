# Configuration

CasHp reads a single YAML file, `server.yml`, from the config directory. On
first run it writes that file with **every** key present and every default
filled in — nothing is implicit and nothing is hidden. Editing the file is the
supported way to change any setting the admin panel does not expose.

## File locations

| Purpose | Root (system) | Non-root (per-user) |
|---|---|---|
| Config file | `/etc/webappsgo/cashp/server.yml` | `~/.config/webappsgo/cashp/server.yml` |
| Data | `/var/lib/webappsgo/cashp/` | `~/.local/share/webappsgo/cashp/` |
| Cache | `/var/cache/webappsgo/cashp/` | `~/.cache/webappsgo/cashp/` |
| Logs | `/var/log/webappsgo/cashp/` | `~/.local/log/webappsgo/cashp/` |
| Backups | `{data_dir}/backups/` | `{data_dir}/backups/` |
| PID file | `/var/run/webappsgo/cashp.pid` | inside the data directory |

The `webappsgo`/`cashp` path segments are frozen internal identifiers. They do
not change if the binary is renamed.

!!! note "Legacy filename"
    A `server.yaml` left over from an older install is renamed to `server.yml`
    automatically on startup — unless a `server.yml` already exists, in which
    case both files are left alone and the canonical one is used.

Point at a different file or directory with `--config`; see the
[CLI Reference](cli.md#paths-and-runtime).

## Value types

Four config value types accept more than plain YAML scalars. In every case an
unparseable value never crashes the server: the key keeps its default and a
startup warning is logged.

=== "Bool"

    Accepts the full truthy/falsy word list, not just `true`/`false` — for
    example `yes`, `on`, `enabled`, `1` and their negatives. The file is
    rewritten with canonical `true`/`false`.

    ```yaml
    enabled: yes
    ```

=== "Duration"

    Go duration syntax plus `d` (day), `w` (week), and `y` (year) suffixes. A
    bare number means seconds. Round-trips in the largest whole unit that
    divides evenly, so `720h` is written back as `30d`.

    ```yaml
    max_age: 30d
    idle_timeout: 24h
    retry_delay: 1h
    ```

=== "Size"

    Plain bytes or a `KB`/`MB`/`GB`/`TB` suffix, case-insensitive, with or
    without a trailing `B`.

    ```yaml
    max_body_size: 10MB
    ```

=== "PortSpec"

    A single port, or a comma-separated HTTP/HTTPS pair.

    ```yaml
    port: 8080
    port: 80,443
    ```

## `server` — core

```yaml
server:
  mode: production
  address: "[::]"
  port: ""
  fqdn: ""
  baseurl: /
  admin_path: administration
  api_version: v1
  user: ""
  group: ""
  pidfile: true
  daemonize: false
```

| Key | Default | Notes |
|---|---|---|
| `mode` | `production` | `production`, `development`, or `debug` |
| `address` | `[::]` | Listen address; dual-stack by default |
| `port` | unset | Empty means a random port in `64000`–`64999` on first run; `80` in a container |
| `fqdn` | unset | Public hostname; used for generated URLs, contact addresses, and certificates |
| `baseurl` | `/` | URL path prefix when mounted under a subpath |
| `admin_path` | `administration` | Path segment for the admin panel, mounted at `/server/{admin_path}/` |
| `api_version` | `v1` | Version segment in `/api/{api_version}/…` |
| `user` / `group` | unset | Service account names used by `--service --install` |
| `pidfile` | `true` | Write a PID file |
| `daemonize` | `false` | Ignored under systemd/launchd/runit and in containers |

!!! warning "`admin_path` is configurable, and so is `api_version`"
    Never hardcode `/server/administration` or `/api/v1` in scripts,
    monitoring checks, or reverse-proxy rules. Read the configured values
    instead. `admin_path` must be 2–32 characters of `[a-z0-9-]`, may not start
    or end with a hyphen, and may not collide with a reserved path segment
    (`api`, `health`, `healthz`, `metrics`, `version`, `.well-known`, `about`,
    `privacy`, `contact`, `help`, `terms`, `preferences`, `docs`, `auth`,
    `security`, `static`, `assets`).

### `server.healthz`

```yaml
server:
  healthz:
    root:
      enabled: false
```

`/server/healthz` is always available. Setting `root.enabled: true`
additionally mounts the same handler at `/healthz`. It is a second mount, never
a redirect.

### `server.branding` and `server.seo`

```yaml
server:
  branding:
    title: cashp
    tagline: ""
    description: ""
  seo:
    keywords: []
```

### `server.ssl`

```yaml
server:
  ssl:
    enabled: false
    cert: ""
    key: ""
    min_version: TLS1.2
    redirect_http: false
    letsencrypt:
      enabled: false
      email: ""
      challenge: http-01
      staging: false
    hsts:
      enabled: false
      max_age: 1y
      include_subdomains: false
      preload: false
```

TLS is off until enabled explicitly, or auto-enabled during validation when
the configured port is `443`.

### `server.limits` and `server.compression`

```yaml
server:
  limits:
    max_body_size: 10MB
    read_timeout: 30s
    write_timeout: 30s
    idle_timeout: 2m
  compression:
    enabled: true
    level: 5
    types:
      - text/html
      - text/css
      - text/javascript
      - application/json
      - application/xml
```

### `server.trusted_proxies`

```yaml
server:
  trusted_proxies:
    additional: []
```

Only requests arriving from a trusted proxy have their forwarded-client-IP
headers honored. Everything else is treated as a direct connection, so a
spoofed `X-Forwarded-For` cannot forge a client address.

### `server.session`

```yaml
server:
  session:
    admin:
      cookie_name: admin_session
      max_age: 30d
      idle_timeout: 24h
    user:
      cookie_name: user_session
      max_age: 7d
      idle_timeout: 24h
    extend_on_activity: true
    secure: auto
    http_only: true
    same_site: strict
```

Admin sessions and user sessions are separate cookies backed by separate
tables. `secure: auto` sets the `Secure` flag whenever the request is served
over HTTPS.

### `server.rate_limit`

Per-IP sliding windows. `requests` is the count, `window` is the window length
in seconds.

```yaml
server:
  rate_limit:
    enabled: true
    read:
      requests: 120
      window: 60
    write:
      requests: 10
      window: 60
    health:
      requests: 120
      window: 60
    global_burst: 240
    auth:
      login:
        requests: 5
        window: 900
      password_reset:
        requests: 3
        window: 3600
      registration:
        requests: 5
        window: 3600
```

A throttled request answers `429` with a `Retry-After` header.

### `server.security`

```yaml
server:
  security:
    encryption_key: ""
    encryption_key_version: 1
    allowlist: []
    blocklist: []
    breach_detection:
      brute_force:
        attempts: 10
        window: 5m
        block_duration: 1h
      credential_stuffing:
        attempts: 50
        window: 10m
      unusual_access:
        new_country_alert: true
```

`encryption_key` is generated on first run. It encrypts data at rest — losing
it means losing access to encrypted fields, so it belongs in your backup plan.

### `server.i18n`

```yaml
server:
  i18n:
    default_language: en
    supported:
      - en
```

### `server.contact`

Four contact roles, each with an email address and a map of webhook
transports.

```yaml
server:
  contact:
    admin:
      email: admin@{fqdn}
      webhooks:
        telegram: ""
        discord: ""
        slack: ""
        generic: ""
    security:
      email: security@{fqdn}
    abuse:
      email: ""
    general:
      email: ""
```

`{fqdn}` is substituted at runtime. `abuse` and `general` start empty on
purpose: the server never advertises an address the operator has not actually
provisioned. The `security` address is what `/.well-known/security.txt`
publishes. The webhook map is open — any additional key is treated as a
transport name at dispatch time.

### `server.privacy`

Drives the consent banner, the cookie categories, and the generated privacy
policy. The consent model is opt-out: non-essential cookies stay on until a
visitor declines.

```yaml
server:
  privacy:
    data:
      sold: false
      stored_on_server: true
    retention:
      export_available: true
      deletion_available: true
    consent:
      show_until_acknowledged: true
      default_enabled: true
      position: bottom
      show_preferences: true
      preferences_text: Manage Preferences
      cookie_name: cookie_consent
      policy:
        text: Privacy Policy
        url: /server/privacy
      buttons:
        decline: Decline
        accept: I Agree
    cookies:
      essential:
        enabled: true
      preferences:
        enabled: true
      analytics:
        enabled: true
```

Setting `data.sold: true` swaps the banner text for the disclosure wording and
changes the analytics cookie description. Essential cookies (session,
authentication, CSRF) cannot be disabled. Consent state lives in a cookie,
never in `localStorage`.

### `server.cache`

```yaml
server:
  cache:
    type: memory
    url: ""
    host: localhost
    port: 6379
    username: ""
    password: ""
    db: 0
    tls: false
    tls_skip_verify: false
    pool_size: 10
    min_idle: 2
    timeout: 5s
    prefix: "cashp:"
    ttl: 1h
    cluster: false
    cluster_nodes: []
```

`memory` works standalone with no dependency. Clustering and mixed mode
require a shared backend — Valkey or Redis. The shipped compose stack sets
`CACHE_URL` to `valkey://cashp-cache:6379`.

### `server.database`

```yaml
server:
  database:
    driver: sqlite
    dir: /var/lib/webappsgo/cashp
```

For a networked driver, use the connection keys instead of `dir`:

```yaml
server:
  database:
    driver: postgres
    host: localhost
    port: 5432
    name: cashp
    username: cashp
    password: ""
    sslmode: require
```

`url` may be given instead of the discrete host/port/name/credential keys.

### `server.cluster`

```yaml
server:
  cluster:
    enabled: false
    node_id: ""
    heartbeat_interval: 30s
    mixed_mode: false
```

### `server.logs`

One log level plus six independently configurable log files.

```yaml
server:
  logs:
    level: warn
    dir: /var/log/webappsgo/cashp
```

| Log | Filename | Format | Rotate | Enabled |
|---|---|---|---|---|
| `access` | `access.log` | `apache` | `monthly` | yes |
| `server` | `server.log` | `text` | `weekly,50MB` | yes |
| `error` | `error.log` | `text` | `weekly,50MB` | yes |
| `security` | `security.log` | `json` | `monthly` | yes |
| `scheduler` | `scheduler.log` | `text` | `weekly` | yes |
| `audit` | `audit.log` | `json` | `daily` | yes |

Every log file accepts `enabled`, `filename`, `format`, `custom`, `rotate`,
`keep`, and `compress`. `keep` defaults to `none` (rotate in place, keep no
archives) and `compress` defaults to `false`. The access log additionally takes
`log_health_checks` (default `false`), which keeps monitoring probes out of the
access log.

Audit logging selects which event classes are recorded:

```yaml
server:
  logs:
    audit:
      events:
        authentication: true
        configuration: true
        security: true
        tokens: true
        data_access: false
```

`data_access` is off by default because it is high-volume; turn it on when a
compliance regime requires it.

### `server.scheduler`

Nine built-in tasks, all enabled, all cron-scheduled.

```yaml
server:
  scheduler:
    enabled: true
```

| Task | Schedule | Extra |
|---|---|---|
| `geoip_update` | `0 3 * * 0` | retries hourly on failure |
| `blocklist_update` | `0 4 * * *` | retries hourly on failure |
| `cve_update` | `0 5 * * *` | retries hourly on failure |
| `log_rotation` | `0 0 * * *` | |
| `session_cleanup` | `@hourly` | |
| `backup` | `0 2 * * *` | `retention: 4` |
| `ssl_renewal` | `0 3 * * *` | `renew_before: 7d` |
| `health_check` | `*/5 * * * *` | |
| `tor_health` | `*/10 * * * *` | |

Each task takes `enabled`, `schedule`, `retry_on_fail`, and `retry_delay`.

### `server.backup`

```yaml
server:
  backup:
    enabled: true
    dir: /var/lib/webappsgo/cashp/backups
    retention:
      max_backups: 1
      keep_weekly: 0
      keep_monthly: 0
      keep_yearly: 0
      max_total_size: 10%
    encryption:
      enabled: false
```

`max_total_size` accepts a percentage of the backup volume or an absolute
size. The `keep_weekly`/`keep_monthly`/`keep_yearly` keys implement
grandfather-father-son retention on top of `max_backups`.

### `server.update`

```yaml
server:
  update:
    branch: stable
    auto_install: false
    defer_days: 0
```

Branches: `stable`, `beta`, `daily`.

### `server.metrics`

```yaml
server:
  metrics:
    enabled: true
    root:
      enabled: true
    auth:
      allow_unauthenticated: false
      tokens:
        prometheus: ""
        grafana: ""
        loki: ""
    include_system: true
    include_runtime: true
    loki:
      max_entries: 1000
      max_age: 1h
```

Each scraper service gets its own bearer token. All three start empty, so
every metrics endpoint answers `403` until you set one — the endpoint is
enabled but closed. `root.enabled: true` (the default) also mounts the handler
at `/metrics` alongside `/server/metrics`.

`duration_buckets` and `size_buckets` set the histogram boundaries and rarely
need changing.

### `server.geoip`

```yaml
server:
  geoip:
    enabled: true
    dir: /var/lib/webappsgo/cashp/security/geoip
    deny_countries: []
    allow_countries: []
    databases:
      asn: true
      country: true
      city: false
```

Both country lists start empty, which means no country-based blocking is in
effect.

### `server.notifications`

```yaml
server:
  notifications:
    email:
      enabled: true
      smtp:
        host: ""
        port: 587
        username: ""
        password: ""
        tls: auto
      from:
        name: ""
        email: ""
```

An empty `host` makes the server autodetect a local MTA at startup. `tls`
accepts `auto`, and features that depend on outbound mail (password reset,
email verification, invites) enable themselves once SMTP resolves.

### `server.maintenance`

```yaml
server:
  maintenance:
    self_healing:
      enabled: true
      retry_interval: 30s
      max_attempts: 0
    cleanup:
      disk_threshold: 90
      log_retention_days: 7
      backup_keep_count: 5
    notify:
      on_enter: true
      on_exit: true
```

`max_attempts: 0` means retry forever. `disk_threshold` is the percentage full
that triggers automatic cleanup.

### `server.features`

```yaml
server:
  features:
    multi_user: true
    organizations: true
    custom_domains:
      enabled: true
      max_domains_per_user: 5
      max_domains_per_org: 20
      require_ssl: true
      allow_apex: true
    tor: false
    i2p: false
    registration: true
    api_tokens: true
```

These are structural toggles, not billing gates. Nothing here is tied to a
subscription tier.

### `server.users`

```yaml
server:
  users:
    enabled: true
    registration:
      mode: open
      require_email_verification: true
      allowed_domains: []
      blocked_domains: []
      invite_expiration_days: 7
    roles:
      available:
        - admin
        - user
      default: user
    tokens:
      enabled: true
      max_per_user: 10
```

!!! danger "Registration never confers administration"
    Anonymous self-service registration always produces an ordinary end user.
    There is no "first user becomes admin" path. The primary global admin is
    created only through the setup-token flow, and that account's primary flag
    cannot be changed or revoked through the UI or API by anyone — including
    other global admins.

### `server.orgs`

```yaml
server:
  orgs:
    enabled: true
    creation:
      mode: open
    profile:
      default_visibility: public
    members:
      default_role: member
      require_2fa: false
      allow_invites: true
```

### `server.i2p`

```yaml
server:
  i2p:
    enabled: false
    binary: ""
    sam_address: 127.0.0.1:7656
    virtual_port: 80
    inbound_length: 3
    outbound_length: 3
    inbound_quantity: 5
    outbound_quantity: 5
    signature_type: 7
    bootstrap_timeout: 5m
    b32_address: ""
```

I2P is strictly opt-in: while `enabled` is `false`, nothing is contacted and
no port is allocated.

## `tor`

```yaml
tor:
  enabled: false
  onion_address: ""
  contact_email: ""
```

`onion_address` is populated by the server once the hidden service publishes.

## `web`

```yaml
web:
  ui:
    theme: dark
  cors: "*"
```

Dark is the default theme; light and auto are selectable in the UI.

## Environment variables

Environment variables fall into two classes with different lifetimes. This
distinction matters in containers, where the same variables are present on
every start.

### Init-only

Read **once**, during first-run config generation, and ignored on every
subsequent start. To change one of these later, edit `server.yml`.

| Variable | Sets |
|---|---|
| `LISTEN` | `server.address` |
| `PORT` | `server.port` |
| `APPLICATION_NAME` | `server.branding.title` |
| `APPLICATION_TAGLINE` | `server.branding.tagline` |
| `DATABASE_DIR` | `server.database.dir` |
| `BACKUP_DIR` | `server.backup.dir` |
| `LOG_DIR` | `server.logs.dir` |

### Runtime

Read on **every** load and always taking priority over `server.yml`.

| Variable | Sets |
|---|---|
| `MODE` | `server.mode` |
| `DOMAIN` | `server.fqdn` |
| `DATABASE_DRIVER` | `server.database.driver` |
| `DATABASE_URL` | `server.database.url` |
| `SMTP_HOST` | `server.notifications.email.smtp.host` |
| `SMTP_PORT` | `server.notifications.email.smtp.port` |
| `SMTP_USERNAME` | `server.notifications.email.smtp.username` |
| `SMTP_PASSWORD` | `server.notifications.email.smtp.password` |
| `SMTP_TLS` | `server.notifications.email.smtp.tls` |
| `SMTP_FROM_NAME` | `server.notifications.email.from.name` |
| `SMTP_FROM_EMAIL` | `server.notifications.email.from.email` |

The SMTP overrides are runtime-scoped specifically so a container can supply
mail credentials without rewriting `server.yml`.

### Path and output variables

| Variable | Equivalent flag |
|---|---|
| `CONFIG_DIR` | `--config` |
| `DATA_DIR` | `--data` |
| `LOG_DIR` | `--log` |
| `PID_FILE` | `--pid` |
| `NO_COLOR` | `--color=no` (also disables emoji) |
| `TERM=dumb` | disables color, emoji, and ANSI escapes |
| `LANG` | `--lang` source for auto-detection |

Precedence throughout is: **CLI flag → config file → environment variable →
built-in default**, except for the runtime variables above, which are applied
after the config file by design.

## Applying changes

```bash
sudo cashp --service reload
```

`reload` re-reads `server.yml` without dropping connections. Changes to the
listen address, port, or TLS configuration need a full `restart`.
