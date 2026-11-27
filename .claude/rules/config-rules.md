# Configuration Rules (PART 5, 6, 12)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL — NEVER DO

- Never check in generated runtime configuration or secrets.
- Never hardcode development hostnames, addresses, ports, or machine capabilities.
- Never parse booleans with language-specific shortcuts; use the shared truthy/falsey contract.
- Never let a configuration error silently select a different runtime mode.

## CRITICAL — ALWAYS DO

- Generate configuration from embedded defaults at runtime in `{config_dir}/server.yml`.
- Validate configuration before starting listeners or services and report actionable errors safely.
- Apply precedence consistently: command flags, environment, config file, then embedded defaults.
- Support the specified production, development, and debug modes without changing product behavior accidentally.

## Key rules

- Keep server settings available through the admin panel and the documented configuration schema.
- Discover machine-specific defaults at runtime and validate paths and addresses before use.
- Never expose secrets in config dumps, health output, logs, or user-facing errors.

For complete details, see AI.md PART 5, 6, 12.
