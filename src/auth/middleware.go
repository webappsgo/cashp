package auth

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	apperr "github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/security"
)

// Cookie names. The admin cookie is deliberately distinct from the user cookie and is
// scoped to the admin mount point, so a panel session is never presented to tenant
// routes and vice versa.
const (
	SessionCookieName = "cashp_session"
	AdminCookieName   = "cashp_admin_session"
)

// CSRFFieldName is the hidden form field every state-changing form must carry.
const CSRFFieldName = "csrf_token"

// CSRFHeaderName is the header equivalent for API clients.
const CSRFHeaderName = "X-CSRF-Token"

// ctxKey is unexported so no other package can plant or forge a value in the request
// context. Identities only ever enter the context through the middleware below.
type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxSession
	ctxAdmin
	ctxToken
	ctxOrg
	ctxOrgRole
	ctxCSRF
)

// UserFrom returns the authenticated user, if any.
func UserFrom(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(ctxUser).(*User)
	return u, ok && u != nil
}

// SessionFrom returns the active browser session, if any.
func SessionFrom(ctx context.Context) (*Session, bool) {
	s, ok := ctx.Value(ctxSession).(*Session)
	return s, ok && s != nil
}

// AdminFrom returns the authenticated Server Admin, if any.
func AdminFrom(ctx context.Context) (*Admin, bool) {
	a, ok := ctx.Value(ctxAdmin).(*Admin)
	return a, ok && a != nil
}

// TokenFrom returns the API token that authenticated the request, if any.
func TokenFrom(ctx context.Context) (*Token, bool) {
	t, ok := ctx.Value(ctxToken).(*Token)
	return t, ok && t != nil
}

// OrgFrom returns the organization resolved from the request path, if any.
func OrgFrom(ctx context.Context) (*Org, bool) {
	o, ok := ctx.Value(ctxOrg).(*Org)
	return o, ok && o != nil
}

// OrgRoleFrom returns the caller's role in the resolved organization. An empty string
// means the caller is not a member.
func OrgRoleFrom(ctx context.Context) string {
	r, _ := ctx.Value(ctxOrgRole).(string)
	return r
}

// CSRFTokenFrom returns the form token minted for this request, for templates to embed.
func CSRFTokenFrom(ctx context.Context) string {
	t, _ := ctx.Value(ctxCSRF).(string)
	return t
}

// ClientIP extracts the caller address. Only the leftmost X-Forwarded-For entry is
// considered, and only when the server is configured to sit behind a trusted proxy,
// because any hop can append to that header.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
				return first
			}
		}
		if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
			return real
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ClientIP is the service-level helper that applies the configured proxy trust.
func (s *Service) ClientIP(r *http.Request) string {
	return ClientIP(r, s.cfg.TrustProxy)
}

// wantsJSON reports whether the caller expects a JSON error rather than an HTML page.
func (s *Service) wantsJSON(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		return true
	}
	return strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
}

// Fail writes an error response. API callers receive the JSON envelope; browsers
// receive the rendered error page, or a redirect to the sign-in form when the problem
// is simply that they are not signed in. The internal cause is logged, never sent:
// no stack trace, DSN, path, or internal address ever reaches the client.
func (s *Service) Fail(w http.ResponseWriter, r *http.Request, e *apperr.Error) {
	if e == nil {
		return
	}
	if e.Code == apperr.CodeInternal {
		s.log.Error("auth request failed",
			slog.String("path", r.URL.Path),
			slog.String("code", e.Code),
			slog.String("error", e.Error()))
	}
	if e.Code == apperr.CodeRateLimited {
		if retry, ok := e.Details["retry_after"].(int); ok && retry > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
		}
	}

	if s.wantsJSON(r) {
		if err := e.WriteJSON(w); err != nil {
			s.log.Warn("write error response", slog.String("error", err.Error()))
		}
		return
	}

	needsLogin := e.Code == apperr.CodeUnauthorized || e.Code == apperr.CodeTokenExpired
	if needsLogin && r.Method == http.MethodGet {
		target := "/auth/login?next=" + urlQueryEscape(r.URL.RequestURI())
		if strings.HasPrefix(r.URL.Path, "/"+s.cfg.AdminPath+"/") {
			target = "/" + s.cfg.AdminPath + "/login"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	if s.renderer == nil {
		http.Error(w, e.Message, e.HTTPStatus)
		return
	}
	s.renderer.RenderError(w, r, e.HTTPStatus, e.Code, e.Message)
}

// urlQueryEscape percent-encodes a redirect target for use in a query string.
func urlQueryEscape(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~', c == '/':
			b.WriteByte(c)
		default:
			const hexDigits = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0f])
		}
	}
	return b.String()
}

// SafeNext sanitizes a post-login redirect target. Only same-site absolute paths are
// accepted, which blocks the open-redirect and protocol-relative bypass cases.
func SafeNext(next, fallback string) string {
	next = strings.TrimSpace(next)
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return fallback
	}
	if strings.Contains(next, "://") || strings.ContainsAny(next, "\r\n\\") {
		return fallback
	}
	return next
}

// SetSessionCookie writes the user session cookie.
func (s *Service) SetSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		Domain:   s.cfg.CookieDomain,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearSessionCookie expires the user session cookie.
func (s *Service) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		Domain:   s.cfg.CookieDomain,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// SetAdminCookie writes the Server Admin session cookie, scoped to the panel mount.
func (s *Service) SetAdminCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     AdminCookieName,
		Value:    token,
		Path:     "/" + s.cfg.AdminPath,
		Domain:   s.cfg.CookieDomain,
		MaxAge:   int(AdminSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearAdminCookie expires the Server Admin session cookie.
func (s *Service) ClearAdminCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     AdminCookieName,
		Value:    "",
		Path:     "/" + s.cfg.AdminPath,
		Domain:   s.cfg.CookieDomain,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// cookieValue reads a cookie without leaking whether it was absent or empty.
func cookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

// bearerToken extracts an API token from the Authorization header.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) < 7 || !strings.EqualFold(h[:7], "bearer ") {
		return ""
	}
	return strings.TrimSpace(h[7:])
}

// LoadUser attaches the signed-in user when a valid session cookie is present and
// always calls the next handler. Use it on public pages that render differently for a
// signed-in visitor.
func (s *Service) LoadUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := cookieValue(r, SessionCookieName)
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		u, sess, aerr := s.ResolveSession(r.Context(), token)
		if aerr != nil {
			if aerr.Code == apperr.CodeTokenExpired {
				s.ClearSessionCookie(w)
			}
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), u, sess)))
	})
}

// withUser plants the user, session, and a freshly minted CSRF token in the context.
func withUser(ctx context.Context, u *User, sess *Session) context.Context {
	ctx = context.WithValue(ctx, ctxUser, u)
	ctx = context.WithValue(ctx, ctxSession, sess)
	return ctx
}

// RequireUser rejects the request unless a valid user session cookie is present.
func (s *Service) RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.UsersEnabled {
			s.Fail(w, r, ErrFeatureDisabled("User accounts"))
			return
		}
		token := cookieValue(r, SessionCookieName)
		u, sess, aerr := s.ResolveSession(r.Context(), token)
		if aerr != nil {
			if aerr.Code == apperr.CodeTokenExpired {
				s.ClearSessionCookie(w)
			}
			s.Fail(w, r, aerr)
			return
		}
		ctx := withUser(r.Context(), u, sess)
		ctx = context.WithValue(ctx, ctxCSRF, security.NewCSRFToken(s.csrfKey, sess.TokenHash))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAdmin rejects the request unless a valid Server Admin session cookie is
// present. Admin sessions are never accepted from the user cookie.
func (s *Service) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := cookieValue(r, AdminCookieName)
		admin, sess, aerr := s.ResolveAdminSession(r.Context(), token)
		if aerr != nil {
			if aerr.Code == apperr.CodeTokenExpired {
				s.ClearAdminCookie(w)
			}
			s.Fail(w, r, aerr)
			return
		}
		ctx := context.WithValue(r.Context(), ctxAdmin, admin)
		ctx = context.WithValue(ctx, ctxSession, sess)
		ctx = context.WithValue(ctx, ctxCSRF, security.NewCSRFToken(s.csrfKey, sess.TokenHash))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAuth accepts either a session cookie or a Bearer API token. It is the
// middleware the API routes use, so the same endpoint serves the panel and a script.
func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raw := bearerToken(r); raw != "" {
			ctx, aerr := s.authenticateToken(r.Context(), raw)
			if aerr != nil {
				s.Fail(w, r, aerr)
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		s.RequireUser(next).ServeHTTP(w, r)
	})
}

// RequireToken accepts only a Bearer API token, for endpoints that must never be
// reachable from a browser session and therefore cannot be CSRF targets.
func (s *Service) RequireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			s.Fail(w, r, ErrUnauthenticated())
			return
		}
		ctx, aerr := s.authenticateToken(r.Context(), raw)
		if aerr != nil {
			s.Fail(w, r, aerr)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// authenticateToken resolves a presented API token to its owner. The lookup is by
// SHA-256 hash, so the plaintext is never stored and never appears in a query log.
func (s *Service) authenticateToken(ctx context.Context, raw string) (context.Context, *apperr.Error) {
	hash := security.HashToken(raw)

	if strings.HasPrefix(raw, security.PrefixAdmin) || strings.HasPrefix(raw, security.PrefixAdminAgent) {
		admin, err := s.store.AdminByTokenHash(ctx, hash)
		if err != nil {
			return nil, ErrUnauthenticated()
		}
		if admin.Locked() {
			return nil, ErrForbidden()
		}
		return context.WithValue(ctx, ctxAdmin, admin), nil
	}

	tok, err := s.store.TokenByHash(ctx, hash)
	if err != nil {
		return nil, ErrUnauthenticated()
	}
	if !tok.Usable() {
		return nil, ErrUnauthenticated()
	}
	if err := s.store.TouchToken(ctx, tok.OwnerType, tok.ID); err != nil {
		s.log.Warn("touch token", slog.String("error", err.Error()))
	}
	ctx = context.WithValue(ctx, ctxToken, tok)

	switch tok.OwnerType {
	case OwnerUser:
		u, err := s.store.UserByID(ctx, tok.OwnerID)
		if err != nil {
			return nil, ErrUnauthenticated()
		}
		if u.Disabled || !u.Approved || u.Locked() {
			return nil, ErrForbidden()
		}
		return context.WithValue(ctx, ctxUser, u), nil
	case OwnerOrg:
		org, err := s.store.OrgByID(ctx, tok.OwnerID)
		if err != nil {
			return nil, ErrUnauthenticated()
		}
		if org.Suspended {
			return nil, ErrForbidden()
		}
		ctx = context.WithValue(ctx, ctxOrg, org)
		// An org token acts for the organization itself, at owner level within that org
		// and with no access to any other tenant.
		return context.WithValue(ctx, ctxOrgRole, OrgRoleOwner), nil
	}
	return nil, ErrUnauthenticated()
}

// RequireScope enforces a token scope. A request authenticated by a session cookie
// carries the full rights of its owner and is unaffected; only tokens are narrowed.
func (s *Service) RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, ok := TokenFrom(r.Context())
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			if !HasScope(tok, scope) {
				s.Fail(w, r, ErrForbidden())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// HasScope reports whether a token carries a scope. The wildcard "*" grants everything,
// and a bare group such as "domains" grants every "domains:" action.
func HasScope(t *Token, want string) bool {
	if t == nil {
		return true
	}
	group := want
	if i := strings.IndexByte(want, ':'); i > 0 {
		group = want[:i]
	}
	for _, have := range scopeList(t.Scopes) {
		if have == "*" || have == want || have == group {
			return true
		}
	}
	return false
}

// OrgSlugFrom extracts the organization slug from the routed path. The stdlib router
// populates PathValue; the manual scan is the fallback for routers that do not.
func OrgSlugFrom(r *http.Request) string {
	if slug := r.PathValue("slug"); slug != "" {
		return slug
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	for i, p := range parts {
		if p == "orgs" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// RequireOrgRole resolves the organization named in the path and enforces membership at
// or above the given role. Every org-scoped handler sits behind this, and the store
// methods it guards all take the org ID as a query predicate, so a member of one
// organization cannot reach another organization's rows even by guessing an ID.
func (s *Service) RequireOrgRole(minRole string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !s.cfg.OrgsEnabled {
				s.Fail(w, r, ErrFeatureDisabled("Organizations"))
				return
			}
			slug := NormalizeName(OrgSlugFrom(r))
			if slug == "" {
				s.Fail(w, r, ErrNotFound("Organization"))
				return
			}
			org, err := s.store.OrgBySlug(r.Context(), slug)
			if err != nil {
				// A non-member gets the same 404 as a nonexistent org, so the endpoint
				// cannot be used to discover which organization names are in use.
				s.Fail(w, r, ErrNotFound("Organization"))
				return
			}

			role := ""
			if scoped, ok := OrgFrom(r.Context()); ok {
				// An org API token is bound to exactly one organization.
				if scoped.ID != org.ID {
					s.Fail(w, r, ErrNotFound("Organization"))
					return
				}
				role = OrgRoleFrom(r.Context())
			} else if u, ok := UserFrom(r.Context()); ok {
				role, err = s.store.OrgRole(r.Context(), org.ID, u.ID)
				if err != nil {
					s.Fail(w, r, ErrInternal(err))
					return
				}
			}
			if role == "" || !roleAtLeast(role, minRole) {
				s.Fail(w, r, ErrNotFound("Organization"))
				return
			}
			if org.Suspended && minRole != OrgRoleMember {
				s.Fail(w, r, ErrForbidden())
				return
			}

			ctx := context.WithValue(r.Context(), ctxOrg, org)
			ctx = context.WithValue(ctx, ctxOrgRole, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// roleRank orders org roles so a single comparison covers the whole matrix.
func roleRank(role string) int {
	switch role {
	case OrgRoleOwner:
		return 3
	case OrgRoleAdmin:
		return 2
	case OrgRoleMember:
		return 1
	}
	return 0
}

// roleAtLeast reports whether have meets or exceeds want.
func roleAtLeast(have, want string) bool {
	return roleRank(have) >= roleRank(want)
}

// RequireCSRF rejects any state-changing request whose token is missing or invalid.
// The token is bound to the session, so a token minted for one session cannot be
// replayed in another. Safe methods pass through untouched.
func (s *Service) RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		// A Bearer-token request carries no ambient cookie authority, so it cannot be
		// forged by a third-party page and does not need a form token.
		if _, ok := TokenFrom(r.Context()); ok {
			next.ServeHTTP(w, r)
			return
		}
		sess, ok := SessionFrom(r.Context())
		if !ok {
			s.Fail(w, r, ErrUnauthenticated())
			return
		}
		token := r.Header.Get(CSRFHeaderName)
		if token == "" {
			// ParseForm is bounded by the body limit the server applies upstream.
			if err := r.ParseForm(); err == nil {
				token = r.PostFormValue(CSRFFieldName)
			}
		}
		if !security.ValidateCSRFToken(s.csrfKey, sess.TokenHash, token) {
			s.Fail(w, r, ErrCSRF())
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CSRFToken mints a form token for the current session, for handlers rendering a page.
func (s *Service) CSRFToken(r *http.Request) string {
	if t := CSRFTokenFrom(r.Context()); t != "" {
		return t
	}
	sess, ok := SessionFrom(r.Context())
	if !ok {
		return ""
	}
	return security.NewCSRFToken(s.csrfKey, sess.TokenHash)
}

// RateLimit applies a named limiter keyed on the caller address. The limiter names are
// the security package constants, so the operator tunes them in one place.
func (s *Service) RateLimit(name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ok, retry := s.limits.Allow(name, s.ClientIP(r))
			if !ok {
				s.Fail(w, r, ErrRateLimited(int(retry.Seconds())))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitByMethod applies the read limiter to safe methods and the write limiter to
// everything else, which is the default for the API surface.
func (s *Service) RateLimitByMethod(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := security.LimitWrite
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			name = security.LimitRead
		}
		ok, retry := s.limits.Allow(name, s.ClientIP(r))
		if !ok {
			s.Fail(w, r, ErrRateLimited(int(retry.Seconds())))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Chain applies middleware left to right, so the first entry is the outermost wrapper.
func Chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		if mw[i] != nil {
			h = mw[i](h)
		}
	}
	return h
}
