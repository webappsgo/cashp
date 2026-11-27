# AI Assistant Rules (PART 0, 1)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL — NEVER DO

- Never edit `AI.md`; it is the read-only implementation authority.
- Never guess values a command can produce; run the command or ask.
- Never skip required security, validation, accessibility, platform, or test work.
- Never use client-side rendering where the specification requires server-rendered Go templates.

## CRITICAL — ALWAYS DO

- Read the complete relevant PART before implementing and follow every cross-reference.
- Keep product intent in `IDEA.md` and technical exceptions in `SPEC.md`.
- Resolve placeholders from project files and commands; preserve established names and paths.
- Record template defects only in the designated parent `TODO.{TEMPLATE}.md` inbox.

## Key rules

- `SPEC.md` overrides `AI.md`; `AI.md` overrides global defaults. `IDEA.md` never overrides implementation rules.
- Every user-visible feature is complete, secure, accessible, responsive, and usable without JavaScript unless AI.md explicitly requires JavaScript.
- Track this project's implementation work in `TODO.AI.md`; do not mix it with AI.md defects.

For complete details, see AI.md PART 0, 1.
