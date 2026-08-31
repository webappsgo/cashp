package cmd

import (
	"strings"

	"github.com/webappsgo/cashp/src/client/api"
)

// Scope kinds for the --user flag.
const (
	ScopeSelf = "self"
	ScopeUser = "user"
	ScopeOrg  = "org"
)

// ReservedNames cannot be a user or an org. me/self/@me resolve to the
// token owner instead of being looked up.
var ReservedNames = []string{"me", "self", "@me", "admin", "system", "api", "www"}

// Scope is the resolved --user context used to build resource paths.
type Scope struct {
	Kind string
	Name string
}

// IsReservedName reports whether name is on the reserved list.
func IsReservedName(name string) bool {
	lowered := strings.ToLower(strings.TrimSpace(name))
	for _, reserved := range ReservedNames {
		if reserved == lowered {
			return true
		}
	}
	return false
}

// ParseScope classifies a --user value without contacting the server.
// "@name" is a user, "+name" is an org, me/self/@me are the token owner,
// and a bare name is left unresolved for ResolveScope.
func ParseScope(value string) (Scope, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return Scope{Kind: ScopeSelf}, true
	}

	lowered := strings.ToLower(trimmed)
	if lowered == "me" || lowered == "self" || lowered == "@me" {
		return Scope{Kind: ScopeSelf}, true
	}

	switch {
	case strings.HasPrefix(trimmed, "@"):
		return Scope{Kind: ScopeUser, Name: strings.TrimPrefix(trimmed, "@")}, true
	case strings.HasPrefix(trimmed, "+"):
		return Scope{Kind: ScopeOrg, Name: strings.TrimPrefix(trimmed, "+")}, true
	default:
		return Scope{Kind: "", Name: trimmed}, false
	}
}

// ResolveScope resolves a bare --user name by asking the server whether it
// is a user or an org. Reserved names are rejected before any request.
func ResolveScope(ctx *Context, value string) (Scope, error) {
	scope, resolved := ParseScope(value)
	if resolved {
		if scope.Name != "" && IsReservedName(scope.Name) {
			return Scope{}, usagef("%q is a reserved name and cannot be a user or organization", scope.Name)
		}
		return scope, nil
	}

	if IsReservedName(scope.Name) {
		return Scope{}, usagef("%q is a reserved name and cannot be a user or organization", scope.Name)
	}

	client, err := ctx.APIClient()
	if err != nil {
		return Scope{}, err
	}

	if _, err := client.Do(ctx.Ctx, api.Request{
		Path:       client.VersionedPath("users/{name}"),
		PathParams: map[string]string{"name": scope.Name},
	}); err == nil {
		return Scope{Kind: ScopeUser, Name: scope.Name}, nil
	} else if api.KindOf(err) != api.KindNotFound {
		return Scope{}, err
	}

	if _, err := client.Do(ctx.Ctx, api.Request{
		Path:       client.VersionedPath("orgs/{name}"),
		PathParams: map[string]string{"name": scope.Name},
	}); err == nil {
		return Scope{Kind: ScopeOrg, Name: scope.Name}, nil
	} else if api.KindOf(err) != api.KindNotFound {
		return Scope{}, err
	}

	return Scope{}, &api.Error{
		Kind:    api.KindNotFound,
		Message: "no user or organization named " + scope.Name,
	}
}

// ResourcePath builds the URL-scoped path for a resource collection.
// Without a --user flag the token owner's own collection is used.
func (s Scope) ResourcePath(resource string) (string, map[string]string) {
	switch s.Kind {
	case ScopeUser:
		return "users/{scope}/" + resource, map[string]string{"scope": s.Name}
	case ScopeOrg:
		return "orgs/{scope}/" + resource, map[string]string{"scope": s.Name}
	default:
		return "users/" + resource, map[string]string{}
	}
}

// AdminResourcePath builds the admin-namespace path for a resource, used
// when --admin is passed.
func AdminResourcePath(adminPath, resource string) string {
	return strings.Trim(adminPath, "/") + "/" + resource
}
