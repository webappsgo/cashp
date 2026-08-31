package swagger

import (
	"strings"

	"github.com/webappsgo/cashp/src/api"
)

// methodCSS styles the HTTP method badges of the explorer. The palette itself
// lives in the shared page stylesheet, so this adds only the rules that are
// specific to the API explorer and inherits every colour from the project
// theme variables. That is what keeps the explorer switching between light and
// dark with the rest of the site (AI.md PART 14).
const methodCSS = `<style>
      .method { display: inline-block; min-width: 4.5rem; text-align: center; padding: 0.05rem 0.4rem; border-radius: 0.25rem; border: 1px solid var(--border); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.85rem; }
      .method-get { color: var(--accent); }
      .method-post { color: var(--ok); }
      .method-put, .method-patch { color: var(--warn); }
      .method-delete { color: var(--error); }
      .method-any { color: var(--purple); }
      details { border-bottom: 1px solid var(--border); padding: 0.4rem 0; }
      details:last-child { border-bottom: none; }
      summary { cursor: pointer; }
    </style>
`

// Page wraps explorer markup in the shared themed document.
func Page(title, body string) string {
	return api.Page(title, methodCSS+body)
}

// methodClass returns the CSS class of a method badge.
func methodClass(method string) string {
	return "method method-" + strings.ToLower(method)
}
