package swagger

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"

	"github.com/webappsgo/cashp/src/api"
)

// UIHandler serves the interactive API explorer. It is server-rendered and
// works with JavaScript disabled: every operation is an HTML <details>
// element, so a text browser sees the same content a graphical browser does.
func (g *Generator) UIHandler(specPath string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routes := g.provider()
		if api.Negotiate(r) == api.FormatJSON {
			api.WriteJSON(w, http.StatusOK, g.Document())
			return
		}
		if api.Negotiate(r) == api.FormatText {
			api.WriteText(w, http.StatusOK, routeText(routes))
			return
		}
		api.WriteHTML(w, http.StatusOK, Page(g.info.Title+" - API Explorer", g.renderUI(routes, specPath)))
	})
}

// renderUI builds the explorer markup from the live route table.
func (g *Generator) renderUI(routes []api.Route, specPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "      <h1>%s API</h1>\n", html.EscapeString(g.info.Title))
	if g.info.Description != "" {
		fmt.Fprintf(&b, "      <p class=\"muted\">%s</p>\n", html.EscapeString(g.info.Description))
	}
	fmt.Fprintf(&b, "      <p>OpenAPI %s document: <a href=\"%s\">%s</a></p>\n",
		OpenAPIVersion, html.EscapeString(specPath), html.EscapeString(specPath))

	for _, tag := range groupNames(routes) {
		fmt.Fprintf(&b, "      <section class=\"section-card\">\n        <h2>%s</h2>\n", html.EscapeString(tag))
		for _, rt := range routesForTag(routes, tag) {
			b.WriteString(renderOperation(rt))
		}
		b.WriteString("      </section>\n")
	}
	return b.String()
}

// renderOperation renders one collapsible operation entry.
func renderOperation(rt api.Route) string {
	var b strings.Builder
	method := rt.Method
	if method == "" {
		method = "ANY"
	}
	b.WriteString("        <details>\n")
	fmt.Fprintf(&b, "          <summary><span class=\"%s\">%s</span> <code>%s</code> %s</summary>\n",
		methodClass(method), html.EscapeString(method), html.EscapeString(rt.Pattern), html.EscapeString(rt.Summary))
	if rt.Description != "" {
		fmt.Fprintf(&b, "          <p>%s</p>\n", html.EscapeString(rt.Description))
	}
	if rt.Alias && rt.Canonical != "" {
		fmt.Fprintf(&b, "          <p class=\"muted\">Alias of <code>%s</code> — the same handler, not a redirect.</p>\n",
			html.EscapeString(rt.Canonical))
	}
	if rt.Auth {
		b.WriteString("          <p class=\"muted\">Requires authentication.</p>\n")
	}
	if len(rt.Params) > 0 {
		b.WriteString("          <table>\n            <tr><th>Parameter</th><th>In</th><th>Type</th><th>Required</th><th>Description</th></tr>\n")
		for _, p := range rt.Params {
			fmt.Fprintf(&b, "            <tr><td><code>%s</code></td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
				html.EscapeString(p.Name), html.EscapeString(string(p.In)), html.EscapeString(typeOr(p.Type)),
				boolLabel(p.Required || p.In == api.InPath), html.EscapeString(p.Description))
		}
		b.WriteString("          </table>\n")
	}
	schema, err := json.MarshalIndent(responses(rt), "", "  ")
	if err == nil {
		fmt.Fprintf(&b, "          <pre class=\"code-block\">%s</pre>\n", html.EscapeString(string(schema)))
	}
	b.WriteString("        </details>\n")
	return b.String()
}

// routeText renders the route table as plain text for non-interactive
// clients.
func routeText(routes []api.Route) string {
	var b strings.Builder
	for _, rt := range routes {
		if !rt.Documented() {
			continue
		}
		method := rt.Method
		if method == "" {
			method = "ANY"
		}
		fmt.Fprintf(&b, "%s %s: %s\n", method, rt.Pattern, rt.Summary)
	}
	return b.String()
}

// groupNames returns the tag groups in display order, with untagged routes
// collected under "other".
func groupNames(routes []api.Route) []string {
	seen := map[string]bool{}
	for _, rt := range routes {
		if !rt.Documented() {
			continue
		}
		if len(rt.Tags) == 0 {
			seen["other"] = true
			continue
		}
		for _, t := range rt.Tags {
			seen[t] = true
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// routesForTag returns the documented routes carrying a tag, sorted by
// pattern then method so the page order is stable.
func routesForTag(routes []api.Route, tag string) []api.Route {
	var out []api.Route
	for _, rt := range routes {
		if !rt.Documented() {
			continue
		}
		if len(rt.Tags) == 0 {
			if tag == "other" {
				out = append(out, rt)
			}
			continue
		}
		for _, t := range rt.Tags {
			if t == tag {
				out = append(out, rt)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pattern == out[j].Pattern {
			return out[i].Method < out[j].Method
		}
		return out[i].Pattern < out[j].Pattern
	})
	return out
}

// boolLabel renders a boolean for the parameter table.
func boolLabel(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
