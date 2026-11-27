# API Rules (PART 13, 14, 15)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL — NEVER DO

- Never use ad-hoc success or error envelopes, duplicate HTTP status in error bodies, or expose unsafe error details.
- Never let OpenAPI and GraphQL diverge from the mounted API routes.
- Never accept writes through GraphQL; mutations belong to REST.
- Never disable TLS verification or leak private/internal origins into public URL generation.

## CRITICAL — ALWAYS DO

- Implement health/readiness/version endpoints and the required versioned API and aliases.
- Negotiate JSON, HTML, and plain text according to the shared response contract.
- Use one canonical response format and stable uppercase error codes; translate user-visible messages.
- Build URLs from the current request and configured public origins; implement built-in ACME/TLS support.

## Key rules

- Pagination, content negotiation, OpenAPI, GraphQL, and MCP preserve their distinct specified response shapes.
- GraphQL is query-only and schema descriptions are translated.
- TLS automation and renewal must be safe, observable, and non-blocking for unrelated service behavior.

For complete details, see AI.md PART 13, 14, 15.
