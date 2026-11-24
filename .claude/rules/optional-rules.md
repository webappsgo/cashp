# Optional Rules (PART 34-36)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

PARTS 34-36 are OPTIONAL in AI.md and become mandatory only through the
declaration in `SPEC.md`. This project activates all three there. The
activation mechanism is defined by AI.md; IDEA.md describes product behavior
and does not activate these parts.

## CRITICAL - NEVER DO
- Never implement PART 34, 35, or 36 in a project without the matching `SPEC.md` declaration.
- Never edit AI.md or change its OPTIONAL markers to activate a part.
- Never let an admin set or view a regular user's password, 2FA secret, or private data.
- Never treat routing DNS records (CNAME/A/AAAA) as proof of custom-domain ownership; only `_verify.{domain}` TXT proves control.
- Never confuse `server.orgs.creation.mode` with per-org visibility.
- Never create Organizations support without Multi-User; PART 35 requires PART 34.

## CRITICAL - ALWAYS DO
- Declare adopted parts under `## Optional parts` in SPEC.md; SPEC.md contains only these activation declarations.
- Keep the product behavior in IDEA.md without implementation or activation instructions.
- Build server-rendered form → POST → redirect flows first; JavaScript may enhance only.
- Map account_admin to org Owner/Admin and end_user to org Member with granted permissions per IDEA.md.
- Prove custom-domain ownership with a TXT record at `_verify.{domain}` before activating routing.
- Treat activation as one-way after shipping; removal requires a data migration.

## KEY DECISIONS

| Question | Answer | Spec Reference |
|---|---|---|
| Which parts are active for cashp? | PART 34, PART 35, PART 36 | `SPEC.md` |
| Default registration mode | `open`, unless IDEA.md overrides | PART 34 |
| Default org creation mode | `open` (any authenticated user) | PART 35 |
| Domain ownership proof | DNS TXT at `_verify.{domain}` | PART 36 |

## TERMINOLOGY

| Term | Meaning |
|---|---|
| Server Admin | Admin-panel account (PART 17), stored in `admins`, under `/server/{admin_path}/*` |
| Regular User | End-user account (PART 34), stored in `users`, under `/users/*` |
| Global admin | IDEA.md whole-installation role; maps to Server Admin |
| Account admin | IDEA.md tenant role; maps to Organization Owner/Admin |
| End user | IDEA.md scoped-access role; maps to Organization Member |
| Organization | Canonical internal term for a team/tenant |
| Custom domain | User/org-owned domain verified via TXT and routed to the platform |

---
For complete details, see AI.md PART 34, 35, 36
