// Package graphql serves cashp's GraphQL endpoint. The schema is generated
// from the routes the server mounted, and every field is executed by calling
// the same handler the REST route calls, so the two surfaces can never drift
// apart or diverge in behaviour (AI.md PART 14).
package graphql

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/webappsgo/cashp/src/api"
)

// RouteProvider returns the currently mounted routes.
type RouteProvider func() []api.Route

// field is one generated schema field bound to a mounted route.
type field struct {
	name  string
	args  []argument
	route api.Route
}

// argument is one generated field argument.
type argument struct {
	name     string
	kind     string
	required bool
}

// schemaFields splits the documented routes into query and mutation fields.
// Read routes become queries; everything else becomes a mutation.
func schemaFields(routes []api.Route) (queries, mutations []field) {
	seen := map[string]bool{}
	for _, rt := range routes {
		if !rt.Documented() {
			continue
		}
		name := rt.OperationID()
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		f := field{name: name, args: argumentsFor(rt), route: rt}
		if rt.Method == "" || rt.Method == http.MethodGet || rt.Method == http.MethodHead {
			queries = append(queries, f)
			continue
		}
		mutations = append(mutations, f)
	}
	sort.Slice(queries, func(i, j int) bool { return queries[i].name < queries[j].name })
	sort.Slice(mutations, func(i, j int) bool { return mutations[i].name < mutations[j].name })
	return queries, mutations
}

// argumentsFor derives the field arguments from the route's path wildcards
// and declared parameters.
func argumentsFor(rt api.Route) []argument {
	var args []argument
	seen := map[string]bool{}
	for _, seg := range strings.Split(rt.Pattern, "/") {
		if !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") {
			continue
		}
		name := strings.TrimSuffix(strings.Trim(seg, "{}"), "...")
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		args = append(args, argument{name: name, kind: "String", required: true})
	}
	for _, p := range rt.Params {
		if seen[p.Name] {
			continue
		}
		seen[p.Name] = true
		args = append(args, argument{name: p.Name, kind: graphQLType(p.Type), required: p.Required || p.In == api.InPath})
	}
	for _, f := range rt.Request {
		if seen[f.Name] {
			continue
		}
		seen[f.Name] = true
		args = append(args, argument{name: f.Name, kind: graphQLType(f.Type), required: f.Required})
	}
	return args
}

// graphQLType maps a declared type onto a GraphQL scalar.
func graphQLType(t string) string {
	switch strings.ToLower(t) {
	case "int", "integer", "int64":
		return "Int"
	case "float", "number":
		return "Float"
	case "bool", "boolean":
		return "Boolean"
	default:
		return "String"
	}
}

// SDL renders the schema definition language document for a route set.
func SDL(routes []api.Route) string {
	queries, mutations := schemaFields(routes)

	var b strings.Builder
	b.WriteString("\"\"\"\nAn arbitrary JSON document returned by a cashp endpoint.\n\"\"\"\nscalar JSON\n\n")
	b.WriteString("schema {\n  query: Query\n")
	if len(mutations) > 0 {
		b.WriteString("  mutation: Mutation\n")
	}
	b.WriteString("}\n\n")
	b.WriteString(renderType("Query", queries))
	if len(mutations) > 0 {
		b.WriteString("\n")
		b.WriteString(renderType("Mutation", mutations))
	}
	return b.String()
}

// renderType renders one object type block.
func renderType(name string, fields []field) string {
	var b strings.Builder
	fmt.Fprintf(&b, "type %s {\n", name)
	if len(fields) == 0 {
		b.WriteString("  _empty: JSON\n}\n")
		return b.String()
	}
	for _, f := range fields {
		if summary := f.route.Summary; summary != "" {
			fmt.Fprintf(&b, "  \"%s\"\n", strings.ReplaceAll(summary, "\"", "'"))
		}
		fmt.Fprintf(&b, "  %s%s: JSON\n", f.name, renderArgs(f.args))
	}
	b.WriteString("}\n")
	return b.String()
}

// renderArgs renders a field's argument list.
func renderArgs(args []argument) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args))
	for _, a := range args {
		kind := a.kind
		if a.required {
			kind += "!"
		}
		parts = append(parts, a.name+": "+kind)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
