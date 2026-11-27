# Testing, Documentation, and I18N Rules (PART 29, 30, 31)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL — NEVER DO

- Never run host-affecting tests or use the repository for runtime/test data.
- Never use production or development Compose files for AI testing.
- Never ship hardcoded human-readable strings outside the untranslated exceptions in PART 31.
- Never let locale key sets, placeholders, plural categories, or registry metadata drift.

## CRITICAL — ALWAYS DO

- Run tests in the prescribed isolated container/VM and use `/tmp/{project_org}/{internal_name}-XXXXXX/` for runtime data.
- Use the project test scripts and verify the actual command result; use Docker only as specified in PART 29.
- Keep `docs/` exclusively for MkDocs/ReadTheDocs and document every shipped operator, admin, API, and integration feature.
- Translate all human-visible web, admin, API, CLI, agent, email, health, and legal text; set HTML `lang` and `dir` correctly.

## Key rules

- English is the source catalog; every supported locale has identical keys and required CLDR plural categories.
- Unsupported locales fall back silently to English. Missing English keys fail the build.
- Keep README, docs, translation credits, and supported-language counts synchronized with actual files and features.
- Preserve server-rendered, keyboard-accessible, mobile-first pages and no-JavaScript forms.

For complete details, see AI.md PART 29, 30, 31.
