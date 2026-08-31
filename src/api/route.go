package api

import (
	"net/http"
	"strings"
)

// ParamIn identifies where a parameter is carried.
type ParamIn string

const (
	// InPath marks a parameter that identifies a resource.
	InPath ParamIn = "path"
	// InQuery marks a filter, sort, or pagination parameter.
	InQuery ParamIn = "query"
	// InHeader marks a request header parameter.
	InHeader ParamIn = "header"
)

// Param documents one request parameter of a route.
type Param struct {
	Name        string
	In          ParamIn
	Type        string
	Description string
	Required    bool
}

// Field documents one property of a request or response object.
type Field struct {
	Name        string
	Type        string
	Description string
	Required    bool
	// Fields holds nested properties when Type is "object".
	Fields []Field
}

// Route describes a mounted HTTP route. The server records one Route per
// mount, and the OpenAPI document and GraphQL schema are both generated
// from that record, so neither can drift from what is actually served.
type Route struct {
	// Method is the HTTP method; empty means the route accepts any method.
	Method string
	// Pattern is the http.ServeMux pattern, without the method prefix.
	Pattern string
	// Name is a short machine-friendly identifier used as the GraphQL field
	// name and the OpenAPI operationId. It is derived when empty.
	Name string
	// Summary is a one-line human description.
	Summary string
	// Description is the longer human description.
	Description string
	// Tags group the route in generated documentation.
	Tags []string
	// Auth marks routes that require a token or session.
	Auth bool
	// Bare marks routes whose success body is not enveloped, such as the
	// health endpoints (AI.md PART 13 envelope exception).
	Bare bool
	// Params documents path, query, and header parameters.
	Params []Param
	// Request documents the request body properties.
	Request []Field
	// Response documents the success response properties.
	Response []Field
	// Alias marks a route that mounts the same handler as Canonical.
	Alias bool
	// Canonical is the canonical pattern an alias points at.
	Canonical string
	// Internal hides a route from generated documentation.
	Internal bool
	// Handler is the mounted handler instance. Aliases share the exact
	// handler value with their canonical route — never a redirect.
	Handler http.Handler
}

// Documented reports whether a route belongs in generated documentation.
func (rt Route) Documented() bool {
	return !rt.Internal && rt.Pattern != ""
}

// OperationID returns the stable identifier used for this route in the
// OpenAPI document and as the GraphQL field name.
func (rt Route) OperationID() string {
	if rt.Name != "" {
		return rt.Name
	}
	return DeriveName(rt.Method, rt.Pattern)
}

// DeriveName builds a lowerCamelCase identifier from a method and pattern,
// for example "GET /api/v1/server/healthz" becomes "getServerHealthz".
func DeriveName(method, pattern string) string {
	verb := strings.ToLower(method)
	if verb == "" {
		verb = "any"
	}
	parts := []string{verb}
	for _, seg := range strings.Split(pattern, "/") {
		seg = strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")
		seg = strings.TrimSuffix(seg, "...")
		if seg == "" || seg == "api" {
			continue
		}
		if seg == Current().Version {
			continue
		}
		parts = append(parts, seg)
	}
	var b strings.Builder
	for i, p := range parts {
		p = sanitizeIdentifier(p)
		if p == "" {
			continue
		}
		if i == 0 {
			b.WriteString(p)
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String()
}

// sanitizeIdentifier reduces a path segment to identifier-safe characters.
func sanitizeIdentifier(s string) string {
	var b strings.Builder
	upperNext := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if upperNext {
				b.WriteString(strings.ToUpper(string(r)))
				upperNext = false
				continue
			}
			b.WriteRune(r)
		default:
			upperNext = b.Len() > 0
		}
	}
	return b.String()
}

// SplitPattern separates an optional leading method from a ServeMux
// pattern, so callers may write either "GET /path" or "/path".
func SplitPattern(pattern string) (method, path string) {
	pattern = strings.TrimSpace(pattern)
	if i := strings.IndexByte(pattern, ' '); i > 0 && !strings.HasPrefix(pattern, "/") {
		return strings.ToUpper(pattern[:i]), strings.TrimSpace(pattern[i+1:])
	}
	return "", pattern
}
