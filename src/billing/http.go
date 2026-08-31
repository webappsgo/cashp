package billing

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Identity is everything billing needs to know about the caller of a request.
// It is deliberately small: billing decides nothing about who a person is, it
// only asks the server who this is and which tenant they are acting for.
type Identity struct {
	// UserID identifies the person acting, for the audit trail.
	UserID string
	// TenantID is the organization the request is scoped to, empty on an
	// administration request that is not about one tenant.
	TenantID string
	// TenantSlug is the human-facing organization name from the path.
	TenantSlug string
	// Manager reports whether the caller may change this tenant's billing
	// rather than only read it.
	Manager bool
	// ServerAdmin reports whether the caller administers the whole server.
	ServerAdmin bool
	// CSRFToken is the token the server's own CSRF middleware expects back on
	// a form post. Billing renders it into every form; the server must mount
	// these web routes behind that middleware, because billing does not carry
	// a second CSRF implementation of its own.
	CSRFToken string
}

// IdentityFunc resolves the caller of a request. It returns false when the
// request carries no identity at all.
type IdentityFunc func(r *http.Request) (Identity, bool)

// caller resolves the identity of a request, refusing everything when the
// server has not wired an identity source.
func (s *Service) caller(r *http.Request) (Identity, error) {
	if s.identity == nil {
		return Identity{}, ErrUnauthorized("Billing is not wired to an identity source.")
	}
	id, ok := s.identity(r)
	if !ok || (id.UserID == "" && !id.ServerAdmin) {
		return Identity{}, ErrUnauthorized("Sign in to view billing.")
	}
	return id, nil
}

// tenantCaller resolves a caller acting for a tenant. Every tenant-facing
// handler starts here, which is what makes the tenant filter on the queries
// below impossible to forget: the handler has no other source of a tenant id
// than the one the server resolved from the session.
func (s *Service) tenantCaller(r *http.Request, needManager bool) (Identity, error) {
	id, err := s.caller(r)
	if err != nil {
		return Identity{}, err
	}
	if id.TenantID == "" {
		return Identity{}, ErrForbidden("This request is not scoped to an organization.")
	}
	if slug := strings.TrimSpace(r.PathValue("slug")); slug != "" && id.TenantSlug != "" && slug != id.TenantSlug {
		// The resolved tenant and the tenant in the path disagree, so the
		// request is reaching across an organization boundary.
		return Identity{}, ErrForbidden("You do not have access to that organization's billing.")
	}
	if needManager && !id.Manager && !id.ServerAdmin {
		return Identity{}, ErrForbidden("Only an organization owner or administrator can change billing.")
	}
	return id, nil
}

// adminCaller resolves a server administrator.
func (s *Service) adminCaller(r *http.Request) (Identity, error) {
	id, err := s.caller(r)
	if err != nil {
		return Identity{}, err
	}
	if !id.ServerAdmin {
		return Identity{}, ErrForbidden("Only a server administrator can change billing configuration.")
	}
	return id, nil
}

// actor is the audit-trail name for a caller.
func (id Identity) actor() string {
	if id.UserID == "" {
		return ActorSystem
	}
	return id.UserID
}

// clientIP is the address an operation is recorded against. It reads the
// forwarded headers only for their first entry, because everything after it is
// supplied by the client and cannot be trusted.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if first := strings.TrimSpace(strings.Split(fwd, ",")[0]); first != "" {
			return first
		}
	}
	if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
		return real
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// formValue reads a trimmed form field.
func formValue(r *http.Request, name string) string {
	return strings.TrimSpace(r.FormValue(name))
}

// formInt reads a form field as an integer, falling back when it is absent or
// malformed.
func formInt(r *http.Request, name string, fallback int64) int64 {
	raw := formValue(r, name)
	if raw == "" {
		return fallback
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

// formBool reads a checkbox or boolean form field.
func formBool(r *http.Request, name string) bool {
	switch strings.ToLower(formValue(r, name)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// wantsJSON reports whether a caller asked for JSON rather than a page. It is
// what lets one handler serve both the API and the browser form that posts to
// it, so the browser flow needs no JavaScript at all.
func wantsJSON(r *http.Request) bool {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		return true
	}
	return strings.HasPrefix(r.URL.Path, "/api/")
}

// redirect sends a browser back to a page after a form post, using 303 so a
// refresh never repeats the operation.
func redirect(w http.ResponseWriter, r *http.Request, target, message string) {
	if message != "" {
		if strings.Contains(target, "?") {
			target += "&notice=" + url.QueryEscape(message)
		} else {
			target += "?notice=" + url.QueryEscape(message)
		}
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// tenantBasePath is the browser path for one organization's billing pages.
func tenantBasePath(slug string) string {
	return "/orgs/" + slug + "/billing"
}

// adminBasePath is the browser path for the billing administration pages.
func (s *Service) adminBasePath() string {
	return "/server/" + s.adminPth + "/config/billing"
}
