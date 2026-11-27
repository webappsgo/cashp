# Binary Rules (PART 7, 8, 33)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL — NEVER DO

- Never enable CGO in required release binaries.
- Never omit any required OS/architecture build target.
- Never expose credentials in CLI output, logs, process arguments, or error text.
- Never make help or version output require root, a server, or a configuration file.

## CRITICAL — ALWAYS DO

- Build the server as `cashp` and the required companion client as `cashp-cli`; build the optional agent as `cashp-agent` when enabled.
- Use the actual executable name in user-facing help and version output.
- Support the shared flags, output formats, secure configuration paths, and shell completions.
- Keep client and agent API behavior aligned with server contracts and send the selected language.

## Key rules

- Build linux, darwin, windows, and freebsd for amd64 and arm64 with `CGO_ENABLED=0`.
- Resolve client/agent language from flag, config, system locale, then English.
- Keep service and privilege behavior platform-specific and fail safely when unsupported.

For complete details, see AI.md PART 7, 8, 33.
