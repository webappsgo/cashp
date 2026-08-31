package graphql

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/webappsgo/cashp/src/api"
)

// UIHandler serves the interactive GraphQL explorer. It is server-rendered and
// works with JavaScript disabled: the query box is a plain form that submits
// with GET, and the result is rendered into the same page. Because the form is
// a safe GET it needs no CSRF token, and mutations still require a POST to the
// API endpoint, which the executor enforces.
func (h *Handler) UIHandler(endpointPath string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sdl := h.SDL()
		switch api.Negotiate(r) {
		case api.FormatJSON:
			api.WriteJSON(w, http.StatusOK, map[string]any{"endpoint": endpointPath, "sdl": sdl})
			return
		case api.FormatText:
			api.WriteText(w, http.StatusOK, sdl)
			return
		}

		query := r.URL.Query().Get("query")
		var result string
		if strings.TrimSpace(query) != "" {
			result = h.runForExplorer(r, query)
		}
		api.WriteHTML(w, http.StatusOK, Page("GraphQL Explorer", h.renderExplorer(endpointPath, sdl, query, result)))
	})
}

// runForExplorer executes an explorer query and returns the JSON response body
// the API endpoint would have produced.
func (h *Handler) runForExplorer(r *http.Request, query string) string {
	target := r.URL.Path + "?" + url.Values{"query": {query}}.Encode()
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		return "{\n  \"errors\": [\n    {\n      \"message\": \"the query could not be submitted\"\n    }\n  ]\n}"
	}
	req.Host = r.Host
	req.RemoteAddr = r.RemoteAddr
	req.TLS = r.TLS
	for _, name := range forwardedHeaders {
		if v := r.Header.Get(name); v != "" {
			req.Header.Set(name, v)
		}
	}
	req.Header.Set("Accept", api.FormatJSON.ContentType())

	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	h.ServeHTTP(rec, req)
	return prettyJSON(rec.body.Bytes())
}

// prettyJSON re-indents a response body for display, falling back to the raw
// text when it is not JSON.
func prettyJSON(raw []byte) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}

// renderExplorer builds the explorer markup: the query form, the last result,
// and the generated schema browser.
func (h *Handler) renderExplorer(endpointPath, sdl, query, result string) string {
	routes := h.provider()
	queries, mutations := schemaFields(routes)

	var b strings.Builder
	b.WriteString("      <h1>GraphQL Explorer</h1>\n")
	fmt.Fprintf(&b, "      <p class=\"muted\">Queries run from this page. Mutations must be sent with POST to <code>%s</code>.</p>\n",
		html.EscapeString(endpointPath))

	b.WriteString("      <section class=\"section-card\">\n        <h2>Run a query</h2>\n")
	b.WriteString("        <form class=\"explorer-form\" method=\"get\">\n")
	b.WriteString("          <label for=\"query\">Query</label>\n")
	fmt.Fprintf(&b, "          <textarea id=\"query\" name=\"query\" spellcheck=\"false\">%s</textarea>\n",
		html.EscapeString(defaultQuery(query, queries)))
	b.WriteString("          <button type=\"submit\">Run</button>\n        </form>\n      </section>\n")

	if result != "" {
		b.WriteString("      <section class=\"section-card\">\n        <h2>Result</h2>\n")
		fmt.Fprintf(&b, "        <pre class=\"code-block\">%s</pre>\n      </section>\n", html.EscapeString(result))
	}

	b.WriteString(renderFieldSection("Queries", queries))
	if len(mutations) > 0 {
		b.WriteString(renderFieldSection("Mutations", mutations))
	}

	b.WriteString("      <section class=\"section-card\">\n        <h2>Schema</h2>\n")
	fmt.Fprintf(&b, "        <pre class=\"code-block\">%s</pre>\n      </section>\n", html.EscapeString(sdl))
	return b.String()
}

// defaultQuery keeps the submitted query in the box, or seeds the box with the
// first available field so the page is usable on a first visit.
func defaultQuery(query string, queries []field) string {
	if strings.TrimSpace(query) != "" {
		return query
	}
	for _, f := range queries {
		if len(f.args) == 0 {
			return "{\n  " + f.name + "\n}\n"
		}
	}
	return "{\n\n}\n"
}

// renderFieldSection renders one collapsible list of schema fields.
func renderFieldSection(title string, fields []field) string {
	var b strings.Builder
	fmt.Fprintf(&b, "      <section class=\"section-card\">\n        <h2>%s</h2>\n", html.EscapeString(title))
	for _, f := range fields {
		b.WriteString("        <details>\n")
		fmt.Fprintf(&b, "          <summary><span class=\"field-name\">%s</span>%s</summary>\n",
			html.EscapeString(f.name), html.EscapeString(renderArgs(f.args)))
		if f.route.Summary != "" {
			fmt.Fprintf(&b, "          <p>%s</p>\n", html.EscapeString(f.route.Summary))
		}
		method := f.route.Method
		if method == "" {
			method = http.MethodGet
		}
		fmt.Fprintf(&b, "          <p class=\"muted\">Resolves <code>%s %s</code>.</p>\n",
			html.EscapeString(method), html.EscapeString(patternPath(f.route.Pattern)))
		if len(f.args) > 0 {
			b.WriteString("          <table>\n            <tr><th>Argument</th><th>Type</th><th>Required</th></tr>\n")
			for _, a := range f.args {
				required := "no"
				if a.required {
					required = "yes"
				}
				fmt.Fprintf(&b, "            <tr><td><code>%s</code></td><td>%s</td><td>%s</td></tr>\n",
					html.EscapeString(a.name), html.EscapeString(a.kind), required)
			}
			b.WriteString("          </table>\n")
		}
		b.WriteString("        </details>\n")
	}
	b.WriteString("      </section>\n")
	return b.String()
}
