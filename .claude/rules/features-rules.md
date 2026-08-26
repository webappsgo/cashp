# Features Rules (PART 18-23)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

Covers: Email & Notifications (18), Scheduler (19), GeoIP (20), Metrics (21), Backup & Restore (22), Update Command (23).

## CRITICAL - NEVER DO
- Send/queue an email or log "would have sent email" when SMTP isn't configured/working
- Use an external scheduler (cron, systemd timers, Task Scheduler, K8s CronJob, cloud schedulers) for ANY scheduled task
- Treat GeoIP/country as the sole access gate, or block a request solely because a GeoIP lookup failed (GeoIP fails open, real auth fails closed)
- Look up or country-block private/internal IPs (RFC 1918/4193, loopback)
- Serve `/server/metrics` (or aliases) without a per-service bearer token; accept tokens via query string
- Redirect any metrics/healthz alias route — same handler only
- Store a backup encryption password, or allow unencrypted backups when `compliance.enabled: true`
- Restore without full verification (checksum, manifest, decrypt test, version compat) passing
- Grant immediate admin access after restoring to a new server without setup-token re-authentication
- Auto-install updates when `update.auto_install` is false (default) — notify only
- Surface update-available status on public pages (Tier 3 info, admin-only)
- Embed GeoIP `.mmdb` files in the binary, or use MaxMind GeoLite2

## CRITICAL - ALWAYS DO
- Embed default email templates in binary; custom templates in `{config_dir}/template/email/`, live reload, fall back to embedded default
- Auto-detect SMTP on first run (loopback → docker gateway → gateway IP → fqdn → global IPv4 → mail./smtp. subdomains); test connection every startup
- Provide WebUI notifications (toast/banner/center) always, regardless of SMTP; use email only per the decision matrix (critical, permanent record, or user away)
- Run the built-in scheduler always, with persistent state (survives restart), catch-up window, cluster-aware (one node per task), logs to file only (never console)
- Include ALL required built-in tasks (ssl_renewal, geoip_update, blocklist_update, cve_update, update_check, session/token cleanup, log_rotation, backups, healthcheck_self, tor/i2p health, cluster_heartbeat)
- Use `github.com/oschwald/maxminddb-golang` (not geoip2-golang) for ip-location-db `.mmdb` files
- Show DB-IP + NRO CC BY 4.0 attribution verbatim on any page displaying GeoIP data, and in LICENSE.md
- Gate metrics/healthz access with firewall/proxy rules as defense in depth even though token auth is mandatory
- Include `server.yml`, `server.db`, `users.db` in every backup; verify ALL checks before restoring
- Require re-authentication (setup token) for Primary Admin after restore to a new server
- Honor `update.defer_days` for the scheduled `update_check` task only — manual `--update` always sees/installs true latest

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| What if no SMTP configured? | All email features disabled (password reset hidden, verification auto-skipped, no queuing) | PART 18 § SMTP Requirement |
| WebUI vs email for an event? | Use Notification vs Email Decision Matrix (critical/security/away → email; confirmation/routine → WebUI only) | PART 18 § Notification vs Email Decision Matrix |
| User asks to use cron instead of scheduler? | Refuse; built-in scheduler is cluster-aware, has catch-up + state tracking | PART 19 § What If User Asks for Cron |
| Which GeoIP databases/library? | sapics/ip-location-db via jsDelivr CDN; `oschwald/maxminddb-golang`; never MaxMind GeoLite2 | PART 20 § Database Sources |
| Both `deny_countries` and `allow_countries` set? | `allow_countries` wins (allowlist mode) | PART 20 § Configuration |
| Is `geo-whois-asn-country` a WHOIS lookup? | No — only exposes `country_code`; org name comes from ASN's `autonomous_system_organization`, never call it "WHOIS" | PART 20 § Database Sources |
| Metrics endpoint public? | No — internal only; firewall/proxy blocks external access; token auth mandatory regardless | PART 21 § Access Control |
| Empty metrics token? | That service's endpoints return 403 with empty body, logged once at startup | PART 21 § Authentication |
| Backup encryption required? | Only when `server.compliance.enabled: true`; otherwise optional (password set → encrypted) | PART 22 § Backup Encryption |
| Restoring to a new server? | Primary Admin must re-auth with one-time setup token; other local admins log in immediately | PART 22 § Primary Admin Re-Setup on Restore |
| Default update behavior? | `auto_install: false` — `update_check` task only notifies; install is always explicit operator action | PART 23 § Update Configuration |
| Update channels relationship? | Cumulative: beta = beta+stable, daily = daily+beta+stable, never older than a more-stable channel | PART 23 § Channel Semantics |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Toast | Auto-dismissing pop-up notification (corner of screen) |
| Banner | Persistent bar at top of page until dismissed/resolved |
| Notification Center | Bell-icon history of notifications, persisted in DB, 30-day retention |
| Catch-up window | Duration after restart within which missed scheduler tasks still run |
| Risk signal (GeoIP) | A factor that raises/lowers risk score or triggers extra checks — never a sole access gate |
| ip-location-db | sapics' CC BY 4.0 GeoIP dataset family (ASN, Country, City), downloaded not embedded |
| Service token | Per-service (`prometheus`/`grafana`/`loki`) bearer token for `/server/metrics` |
| Root alias | `/metrics` path (default enabled) mirroring `/server/metrics` for scraper compatibility |
| Manifest | `manifest.json` inside a backup archive describing version, contents, checksum |
| Setup token | One-time token shown after restore-to-new-server, used to re-verify Primary Admin |
| Update branch | Release channel: `stable` (default), `beta`, `daily` |
| `defer_days` | Minimum age (days) a release must have before the scheduled task adopts it |

---
For complete details, see AI.md PART 18, 19, 20, 21, 22, 23
