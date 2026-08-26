# Binary Rules (PART 7, 8, 33)

## CRITICAL - NEVER DO
- Never hand-roll flag parsing for the server binary (`switch`/`os.Args` loops) — use stdlib `flag`
- Never embed security databases (GeoIP, blocklists, CVE, Trivy) in the binary — download to `{data_dir}/security/`
- Never use CGO — `CGO_ENABLED=0`, pure Go only
- Never add `--tui`/`--cli`/`--gui`/`--mode tui` flags to `cashp-cli` — display mode is auto-detected, override via `cli.yml` `display.mode` only
- Never use `strconv.ParseBool()` for CLI/agent booleans — use `config.ParseBool()`/`config.IsTruthy()`
- Never display the hardcoded project name where the actual (possibly renamed) binary name belongs (`--help`, `--version`, error messages) — and never the reverse for User-Agent/config paths
- Never require root/escalation for `--help` or `--version` at any command level
- Never invent server subcommands — PART 8's command set is fixed and complete
- Never auto-retry after a `401 TOKEN_REVOKED`/`TOKEN_EXPIRED` — re-auth is a deliberate user action

## CRITICAL - ALWAYS DO
- Always use stdlib `flag` for `cashp` (server, single-command); always use `cobra`/`viper` for `cashp-cli` (multi-command)
- Always respect `NO_COLOR` (disables ANSI colors + emojis, not bold/underline/box-drawing) with priority: CLI flag > config > `NO_COLOR` env > auto-detect
- Always accept both `--flag=value` and `--flag value` syntax on every binary
- Always hardcode `cashp`/`cashp-cli`/`cashp-agent` in User-Agent headers and internal identifiers, regardless of the actual (renamable) binary filename
- Always create directories referenced by directory flags (`--config`, `--data`, `--cache`, `--log`, `--backup`) if missing
- Always enforce `0600` perms (Unix) / owner-only ACL (Windows) on `cli.yml` and `token`; refuse to load and warn on looser perms
- Always cache and refresh the autodiscover cluster URL list in `cli.yml` for CLI failover
- Always exit immediately on `-h`/`--help`/`-v`/`--version` without launching TUI, without checking privilege state

## KEY DECISIONS (pre-answered)

| Question | Answer | Spec Reference |
|---|---|---|
| Server flag library? | stdlib `flag` (single-command, no subcommands) | PART 8 § Flag Parsing |
| CLI flag library? | `cobra` + `viper` | PART 33; Common Go Modules |
| Is CLI optional? | No — required for every project | PART 33 § CLI Is Required |
| Is Agent optional? | Yes — only for monitoring/remote-mgmt projects | PART 33 § Overview |
| CLI default mode with no args, interactive TTY? | TUI | PART 33 § Modes |
| CLI default mode piped/redirected/cron? | Plain (non-interactive) output | PART 33 § Modes |
| Where does GeoIP/CVE/blocklist data live? | `{data_dir}/security/...`, downloaded not embedded | PART 7 § External Data |
| Token source priority? | flag > token-file flag > env `{PROJECT_NAME}_TOKEN` > `cli.yml` > `{config_dir}/token` | PART 33 § API Token Auth |
| CLI exit code for auth failure? | 4 | PART 33 § Exit Codes |
| Server binary subcommands? | None — fixed flag set only (`--help`, `--status`, `--service`, `--maintenance`, `--update`, `--shell`, etc.) | PART 8 § Server Binary Commands |

## TERMINOLOGY

| Term | Meaning |
|---|---|
| `{project_name}` | Hardcoded internal identifier `cashp` — used in User-Agent, default paths, config keys; never changes even if binary is renamed |
| Binary name / `os.Args[0]` | The actual (possibly user-renamed) executable filename — shown in `--help`/`--version`/errors only |
| Server binary | `cashp` — runs the HTTP server, single-command, stdlib `flag` |
| Client/CLI binary | `cashp-cli` — required, multi-command, `cobra`/`viper`, TUI+CLI+GUI modes |
| Agent binary | `cashp-agent` — optional, reports to server |
| Display mode | Auto-detected GUI/TUI/CLI/plain output selection; never a CLI flag |
| NO_COLOR | Env var standard that disables ANSI colors and emojis (not styling/structure) |
| Truthy/Falsey | Shared boolean parsing (`true/yes/on/1/enable(d)` vs `false/no/off/0/disable(d)/none`) used by CLI/agent/server config |

## QUICK REFERENCE

**Server (`cashp`) — flags only, no subcommands:**
`--help` `--version` `--shell {completions,init}` `--mode {production|development|debug}` `--config` `--data` `--cache` `--log` `--backup` `--pid` `--address` `--port` `--baseurl` `--status` `--service {start,restart,stop,reload,--install,--uninstall,--disable}` `--daemon` `--debug` `--color {auto|yes|no}` `--lang` `--maintenance {backup,restore,update,mode,setup}` `--update [check|yes|branch]`

**CLI (`cashp-cli`) — universal flags + project commands:**
- Universal: `--help`/`-h`, `--version`/`-v`, `--color`, `--lang`
- Optional (as needed): `--token`, `--token-file`, `--server`, `--config`, `--output {json|table|plain}`, `--debug`
- No `--tui`/`--cli`/`--gui`/`config` command/`tui` command — mode and config are auto/file-driven
- Smart argument detection: stdin → file → text (avoid extra flags like `--file`/`--text` unless overriding)

**Binary structure (shared modules):**
`src/common/{display,theme,terminal,banner,version}/` shared by all three binaries; `src/server/`, `src/client/{cli,tui,gui}/`, `src/agent/{cli,collector}/`

**Exit codes (CLI):** `0` success · `1` general · `2` config · `3` connection · `4` auth · `5` not found · `64` usage
