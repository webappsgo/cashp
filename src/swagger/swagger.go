// Package swagger generates cashp's OpenAPI document from the routes the
// server actually mounted, so the published API description cannot drift
// from the served surface (AI.md PART 14). The document is JSON only: there
// is no YAML form and no path suffix.
package swagger

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/webappsgo/cashp/src/api"
)

// OpenAPIVersion is the specification version of the generated document.
const OpenAPIVersion = "3.1.0"

// Info carries the document metadata that does not come from the routes.
type Info struct {
	Title       string
	Description string
	Version     string
	BaseURL     string
	Contact     string
	License     string
	LicenseURL  string
}

// RouteProvider returns the currently mounted routes. The server itself
// satisfies it through its Routes method, which is what keeps the document
// generated from the live route table.
type RouteProvider func() []api.Route

// Generator renders the OpenAPI document and caches it until the route table
// changes.
type Generator struct {
	provider RouteProvider
	info     Info

	mu     sync.Mutex
	cached map[string]any
	count  int
}

// NewGenerator builds a generator over a route provider.
func NewGenerator(provider RouteProvider, info Info) *Generator {
	if info.Title == "" {
		info.Title = "cashp"
	}
	if info.Version == "" {
		info.Version = api.DevVersion
	}
	return &Generator{provider: provider, info: info}
}

// Document returns the OpenAPI document, regenerating it whenever routes
// have been added since the last call.
func (g *Generator) Document() map[string]any {
	routes := g.provider()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cached != nil && g.count == len(routes) {
		return g.cached
	}
	g.cached = Generate(routes, g.info)
	g.count = len(routes)
	return g.cached
}

// Generate renders the OpenAPI document for a set of routes.
func Generate(routes []api.Route, info Info) map[string]any {
	paths := map[string]any{}
	for _, rt := range routes {
		if !rt.Documented() {
			continue
		}
		item, _ := paths[rt.Pattern].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[rt.Pattern] = item
		}
		method := strings.ToLower(rt.Method)
		if method == "" {
			method = "get"
		}
		item[method] = operation(rt)
	}

	doc := map[string]any{
		"openapi": OpenAPIVersion,
		"info": map[string]any{
			"title":       info.Title,
			"description": info.Description,
			"version":     info.Version,
		},
		"paths": paths,
		"tags":  tagList(routes),
	}
	if info.BaseURL != "" {
		doc["servers"] = []any{map[string]any{"url": strings.TrimRight(info.BaseURL, "/")}}
	}
	if info.License != "" {
		license := map[string]any{"name": info.License}
		if info.LicenseURL != "" {
			license["url"] = info.LicenseURL
		}
		doc["info"].(map[string]any)["license"] = license
	}
	if info.Contact != "" {
		doc["info"].(map[string]any)["contact"] = map[string]any{"name": info.Contact}
	}
	return doc
}

// operation renders one OpenAPI operation object.
func operation(rt api.Route) map[string]any {
	op := map[string]any{
		"operationId": rt.OperationID(),
		"summary":     rt.Summary,
		"responses":   responses(rt),
	}
	description := rt.Description
	if rt.Alias && rt.Canonical != "" {
		alias := "Alias of " + rt.Canonical + ". It serves the same handler; it is not a redirect."
		if description == "" {
			description = alias
		} else {
			description += " " + alias
		}
	}
	if description != "" {
		op["description"] = description
	}
	if len(rt.Tags) > 0 {
		op["tags"] = toAny(rt.Tags)
	}
	if params := parameters(rt); len(params) > 0 {
		op["parameters"] = params
	}
	if len(rt.Request) > 0 {
		op["requestBody"] = map[string]any{
			"required": true,
			"content": map[string]any{
				"application/json": map[string]any{"schema": objectSchema(rt.Request)},
			},
		}
	}
	if rt.Auth {
		op["security"] = []any{map[string]any{"bearerAuth": []any{}}}
	}
	return op
}

// parameters renders the path, query, and header parameters of a route,
// including the pagination parameters every collection route accepts.
func parameters(rt api.Route) []any {
	var out []any
	seen := map[string]bool{}
	for _, p := range rt.Params {
		seen[p.Name] = true
		out = append(out, map[string]any{
			"name":        p.Name,
			"in":          string(p.In),
			"required":    p.Required || p.In == api.InPath,
			"description": p.Description,
			"schema":      map[string]any{"type": typeOr(p.Type)},
		})
	}
	for _, seg := range strings.Split(rt.Pattern, "/") {
		if !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") {
			continue
		}
		name := strings.TrimSuffix(strings.Trim(seg, "{}"), "...")
		if name == "" || seen[name] {
			continue
		}
		out = append(out, map[string]any{
			"name":     name,
			"in":       "path",
			"required": true,
			"schema":   map[string]any{"type": "string"},
		})
	}
	return out
}

// responses renders the response object for a route, applying the standard
// envelope unless the route is one of the bare-document endpoints.
func responses(rt api.Route) map[string]any {
	var schema map[string]any
	switch {
	case len(rt.Response) > 0 && rt.Bare:
		schema = objectSchema(rt.Response)
	case len(rt.Response) > 0:
		schema = map[string]any{
			"type":     "object",
			"required": []any{"ok", "data"},
			"properties": map[string]any{
				"ok":   map[string]any{"type": "boolean", "const": true},
				"data": objectSchema(rt.Response),
			},
		}
	case rt.Bare:
		schema = map[string]any{"type": "object"}
	default:
		schema = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ok":   map[string]any{"type": "boolean", "const": true},
				"data": map[string]any{"type": "object"},
			},
		}
	}
	out := map[string]any{
		"200": map[string]any{
			"description": "Success",
			"content": map[string]any{
				"application/json": map[string]any{"schema": schema},
				"text/plain":       map[string]any{"schema": map[string]any{"type": "string"}},
			},
		},
		"400": errorResponse("The request was malformed or failed validation."),
		"429": errorResponse("The client exceeded its request budget."),
		"500": errorResponse("An unexpected server-side failure occurred."),
	}
	if rt.Auth {
		out["401"] = errorResponse("Authentication is required.")
		out["403"] = errorResponse("The caller lacks permission.")
	}
	return out
}

// errorResponse renders the canonical error envelope schema.
func errorResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{
					"type":     "object",
					"required": []any{"ok", "error", "message"},
					"properties": map[string]any{
						"ok":      map[string]any{"type": "boolean", "const": false},
						"error":   map[string]any{"type": "string"},
						"message": map[string]any{"type": "string"},
						"details": map[string]any{"type": "object"},
					},
				},
			},
		},
	}
}

// objectSchema renders a field list as a JSON Schema object.
func objectSchema(fields []api.Field) map[string]any {
	props := map[string]any{}
	var required []any
	for _, f := range fields {
		props[f.Name] = fieldSchema(f)
		if f.Required {
			required = append(required, f.Name)
		}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// fieldSchema renders one field, recursing into nested objects.
func fieldSchema(f api.Field) map[string]any {
	if len(f.Fields) > 0 {
		nested := objectSchema(f.Fields)
		if f.Description != "" {
			nested["description"] = f.Description
		}
		return nested
	}
	schema := map[string]any{"type": typeOr(f.Type)}
	if f.Description != "" {
		schema["description"] = f.Description
	}
	return schema
}

// typeOr maps a declared type to a JSON Schema type, defaulting to string.
func typeOr(t string) string {
	switch strings.ToLower(t) {
	case "int", "integer", "int64":
		return "integer"
	case "float", "number":
		return "number"
	case "bool", "boolean":
		return "boolean"
	case "object":
		return "object"
	case "array", "list":
		return "array"
	default:
		return "string"
	}
}

// tagList collects the distinct tags used by the documented routes.
func tagList(routes []api.Route) []any {
	seen := map[string]bool{}
	for _, rt := range routes {
		if !rt.Documented() {
			continue
		}
		for _, t := range rt.Tags {
			seen[t] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]any, 0, len(names))
	for _, name := range names {
		out = append(out, map[string]any{"name": name})
	}
	return out
}

// toAny converts a string slice for embedding in the document.
func toAny(in []string) []any {
	out := make([]any, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	return out
}

// SpecHandler serves the OpenAPI document. The response is always JSON,
// regardless of negotiation, because an OpenAPI document has exactly one
// wire format.
func (g *Generator) SpecHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, g.Document())
	})
}
