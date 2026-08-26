# Optional Rules (PART 34-36)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

Covers: Multi-User (34), Organizations (35), Custom Domains (36) — marked OPTIONAL in the AI.md template, but **ALL THREE ARE ACTIVE for cashp**: IDEA.md's Business Logic defines global admin / account admin / end-user roles (multi-user), per-tenant hosting accounts (organizations), and user-supplied vhosts/domains (custom domains) as core product scope, not optional add-ons.

- Once active per the note above, these PARTs are as non-negotiable as any REQUIRED part — "optional" only means "not every AI.md-based project needs them," not "cashp may skip them"

## CRITICAL - NEVER DO
- Never implement any PART 34/35/36 feature (users table, orgs table, custom_domains table, `/users/*`, `/orgs/*`, domain verification) without the corresponding AI.md heading marker set to REQUIRED (already flipped for cashp — see KEY DECISIONS)
- Never edit TEMPLATE.md — PART 34-36 flips happen only in this project's generated AI.md
- Never let an admin set or view a regular user's password, 2FA secret, or private data
- Never treat routing DNS records (CNAME/A/AAAA) as proof of custom-domain ownership — only the `_verify.{domain}` TXT record proves control
- Never confuse `server.orgs.creation.mode` (server-level policy) with a per-org `visibility: public/private` setting
- Never create Organizations support without Multi-User (PART 35 requires PART 34 first)

## CRITICAL - ALWAYS DO
- Flip both files together when adopting a feature: `multi_user`/`organizations`/`custom_domains: true` in IDEA.md `## Project variables`, AND the matching AI.md heading `OPTIONAL...` → `REQUIRED - NON-NEGOTIABLE`
- Build server-rendered form → POST → redirect first (no-JS-first, PART 16), JS only enhances
- Map account_admin → org Owner/Admin and end_user → org Member with granted permissions per IDEA.md's role table
- Prove custom-domain ownership via TXT at `_verify.{domain}` before activating routing

## KEY DECISIONS (pre-answered)

| Question | Answer | Spec Reference |
|---|---|---|
| Does cashp need Multi-User? | Yes — global admin / account admin / end user roles | IDEA.md:76-131; AI.md PART 34 Overview |
| Does cashp need Organizations? | Yes — tenant hosting accounts = multi-tenant SaaS w/ team billing | AI.md PART 35 "Organization Decision Matrix" |
| Does cashp need Custom Domains? | Yes — sites/mail/DNS zones are branded per tenant | IDEA.md:53,299,309; AI.md PART 36 "When Needed" |
| Are these flipped to REQUIRED yet? | **Yes** — AI.md headings read REQUIRED and IDEA.md `## Project variables` has `multi_user`/`organizations`/`custom_domains: true` | AI.md:57377-57416 "Flip Mechanism" |
| Default registration mode | `open`, unless IDEA.md overrides | AI.md PART 34 "Registration Modes" |
| Default org creation mode | `open` (any authenticated user) | AI.md PART 35 "Organization Creation Modes" |
| Domain ownership proof | DNS TXT at `_verify.{domain}`, not CNAME/A/AAAA | AI.md PART 36 "Verification Flow" |

## TERMINOLOGY

| Term | Meaning |
|---|---|
| Server Admin | Admin-panel account (PART 17), always required, `admins` table, `/server/{admin_path}/*` |
| Regular User | End-user account (PART 34, optional), `users` table, `/users/*` |
| Global admin (cashp) | IDEA.md's whole-installation role — maps to Server Admin |
| Account admin (cashp) | IDEA.md's per-tenant admin role — maps to Organization Owner/Admin |
| End user (cashp) | IDEA.md's scoped-access role — maps to Organization Member |
| Organization | Canonical internal term for team/tenant; UI may label it "account," "team," or "workspace" |
| Custom domain | User/org-owned domain verified via TXT and routed to the platform |

## ACTIVATION
Activation is a two-file flip: (1) IDEA.md `## Project variables` carries `multi_user: true` / `organizations: true` / `custom_domains: true`; (2) this project's AI.md heading for each PART reads `REQUIRED - NON-NEGOTIABLE`. Both files agree as of this bootstrap (AI.md:57421/61708/62400); the flip is one-way once shipped (AI.md:57377-57416).

---
For complete details, see AI.md PART 34, 35, 36
