# Frontend Rules (PART 16, 17)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL — NEVER DO

- Never use React/Vue or client-side rendering for application pages; use Go templates.
- Never require JavaScript for forms, navigation, validation, or core page flows when HTML/CSS can do it.
- Never put inline CSS in application templates or use JavaScript alerts.
- Never expose admin routes or sensitive data in public pages.

## CRITICAL — ALWAYS DO

- Render pages server-side with mobile-first responsive styling, accessible landmarks, and keyboard support.
- Apply the shared light/dark/auto theme system across public, admin, Swagger, and GraphQL pages.
- Translate every visible string and set each document's `lang` and `dir` from the resolved locale.
- Keep admin routes under `/server/{admin_path}` and provide the complete settings and management UI.

## Key rules

- Escape untrusted content, enforce CSRF for writes, and validate all values on the server.
- Every form has a no-JavaScript path; scripts exist only for capabilities HTML/CSS cannot provide.
- Error pages use the canonical safe error message and preserve a navigable page shell.

For complete details, see AI.md PART 16, 17.
