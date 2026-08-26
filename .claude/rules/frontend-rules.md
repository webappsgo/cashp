# Frontend Rules (PART 16, 17)

⚠️ **NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Client-side frameworks (React/Vue/Angular) — `html/template` only
- Require JavaScript for core functionality (nav, forms, CRUD)
- Client-side routing (SPA) or business logic in JS
- Inline CSS/JS (`onclick=`, `style=`) — CSP blocks it
- JS `alert()`/`confirm()`/`prompt()` — use `<dialog>` or toasts
- Desktop-first CSS (`max-width` media queries)
- Let long strings (IPv6, .onion, tokens, hashes) overflow mobile
- Hardcode hex colors — use `--color-*` properties only
- Link to `/server/{admin_path}` from any public route
- Export via JS `Blob`/`showSaveFilePicker()` as the only path
- Hide `<input type="file">` behind a JS-only trigger
- Invent admin routes outside `{admin_username}/*` or `config/*`
- Duplicate an existing `.btn`/badge class

## CRITICAL - ALWAYS DO
- Server-render first: form → POST → redirect works before JS
- Mobile-first CSS: base = phone, `min-width` queries scale up
- `word-break: break-all; overflow-wrap: break-word;` on long strings
- Detect client type (browser/CLI/text-browser) via `Accept`/UA
- Touch targets ≥44x44px; base font-size ≥16px
- WCAG 2.1 AA: labels, focus rings, `aria-live`, skip link
- Copy buttons show visible "Copied!" feedback
- Theme via `theme` cookie/DB pref, server-rendered; default dark
- Admin gate: unauth → login, non-admin → own dashboard, admin → proceeds
- Reuse existing components/CSS vars first

## KEY DECISIONS (pre-answered)

| Question | Answer | Spec Reference |
|---|---|---|
| Templating engine | Go `html/template`, no client framework | PART 16 § Technology Stack |
| JS framework | None — vanilla JS, last resort after HTML5/CSS | PART 16 § HTML5 & CSS Over JavaScript |
| Mobile breakpoints | base <768px, `min-width:768px`, `min-width:1024px` | PART 16 § Mobile-First Responsive Design |
| Default theme | Dark | PART 16 § Themes |
| Theme persistence | `theme` cookie (guests) / DB pref (users) | PART 16 § Themes |
| Admin default path | `/server/administration`, via `server.admin_path` | PART 17 § Configurable Admin Path |
| Admin route structure | Only `{admin_username}` and `config` under admin root | PART 17 § Admin Route Hierarchy |
| Non-admin hits admin path | Redirected to own dashboard, no hint | PART 17 § Access Control on Admin Routes |
| File export mechanism | Plain `<a>`/GET, `Content-Disposition: attachment` | PART 16 § Import/Export UI Convention |
| Modal implementation | Native `<dialog>` + `<form method="dialog">` | PART 16 § HTML5 & CSS Over JavaScript |

## TERMINOLOGY

| Term | Meaning |
|---|---|
| `{admin_path}` | Configurable admin URL segment, default `administration` |
| No-JS-first | Build server-rendered flow first; JS only enhances |
| Text Browser | Non-graphical client (lynx, w3m); gets no-JS HTML |
| Vanity URL | Short root URL (`/{username}`) proxying to an API |
| Smart Content Detection | Picks HTML/text/JSON by `Accept`/User-Agent |
| Admin root | `/server/{admin_path}/` — dashboard, `config`, `{admin_username}` only |
| Progressive enhancement | Works with plain HTML/CSS; JS adds convenience only |

---
Full details: AI.md PART 16, 17
