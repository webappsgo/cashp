package graphql

import (
	"github.com/webappsgo/cashp/src/api"
)

// explorerCSS styles the GraphQL explorer. Every colour comes from the shared
// theme variables, so the explorer follows the project theme in both light and
// dark without a second palette to keep in sync (AI.md PART 14).
const explorerCSS = `<style>
      .explorer-form textarea {
        width: 100%;
        min-height: 12rem;
        background: var(--bg);
        color: var(--fg);
        border: 1px solid var(--border);
        border-radius: 0.3rem;
        padding: 0.75rem;
        font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
        font-size: 0.9rem;
        resize: vertical;
      }
      .explorer-form label { display: block; color: var(--muted); margin: 0.75rem 0 0.25rem; }
      .explorer-form button {
        margin-top: 0.75rem;
        background: var(--bg-elevated, var(--border));
        color: var(--fg);
        border: 1px solid var(--border);
        border-radius: 0.3rem;
        padding: 0.4rem 1.2rem;
        cursor: pointer;
        font-size: 1rem;
      }
      .explorer-form button:hover { color: var(--accent); border-color: var(--accent); }
      .field-name { color: var(--accent); }
      details { border-bottom: 1px solid var(--border); padding: 0.4rem 0; }
      details:last-child { border-bottom: none; }
      summary { cursor: pointer; }
    </style>
`

// Page wraps explorer markup in the shared themed document.
func Page(title, body string) string {
	return api.Page(title, explorerCSS+body)
}
