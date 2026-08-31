package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
)

// Cookie names owned by the panel. The admin session is deliberately distinct
// from the application user session so the two can never be confused.
const (
	adminSessionCookie = "admin_session"
	csrfCookieName     = "csrf_token"
)

// contextKey is the private type used for values the panel puts on a request
// context, so no other package can collide with it.
type contextKey string

// adminContextKey carries the authenticated admin through the middleware.
const adminContextKey = contextKey("admin.record")

// csrfToken returns the request's double-submit CSRF token, minting one when
// the visitor does not have it. The value is shared with the public site so a
// single token works across both surfaces.
func (p *Panel) csrfToken(w http.ResponseWriter, req *http.Request) string {
	if req != nil {
		if cookie, err := req.Cookie(csrfCookieName); err == nil && len(cookie.Value) >= 32 {
			return cookie.Value
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	if w != nil {
		http.SetCookie(w, &http.Cookie{
			Name:     csrfCookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: false,
			SameSite: http.SameSiteLaxMode,
			Secure:   isSecureRequest(req),
		})
	}
	return token
}

// isSecureRequest reports whether the request arrived over TLS. Onion and I2P
// requests are plain HTTP by design, so Secure is not forced on them.
func isSecureRequest(req *http.Request) bool {
	if req == nil {
		return false
	}
	if req.TLS != nil {
		return true
	}
	return strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https")
}

// setSessionCookie stores the admin session cookie, scoped to the panel prefix
// so it is never sent to a public route.
func (p *Panel) setSessionCookie(w http.ResponseWriter, req *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookie,
		Value:    value,
		Path:     p.base(),
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isSecureRequest(req),
	})
}

// clearSessionCookie expires the admin session cookie.
func (p *Panel) clearSessionCookie(w http.ResponseWriter, req *http.Request) {
	p.setSessionCookie(w, req, "", -1)
}

// sessionValue returns the raw admin session cookie value.
func sessionValue(req *http.Request) string {
	cookie, err := req.Cookie(adminSessionCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// currentSession resolves the request's admin session of the given kind.
func (p *Panel) currentSession(req *http.Request, kind string) (*sessionRecord, error) {
	sess, err := p.lookupSession(req.Context(), sessionValue(req))
	if err != nil {
		return nil, err
	}
	if sess.Kind != kind {
		return nil, errNoRow
	}
	return sess, nil
}

// currentAdmin returns the signed-in admin, or errNoRow when the request has no
// usable session. A disabled account is treated as signed out.
func (p *Panel) currentAdmin(req *http.Request) (*adminRecord, error) {
	sess, err := p.currentSession(req, sessionKindActive)
	if err != nil {
		return nil, err
	}
	rec, err := p.adminByID(req.Context(), sess.AdminID)
	if err != nil {
		return nil, err
	}
	if rec.Disabled {
		return nil, errNoRow
	}
	return rec, nil
}

// adminFromContext returns the admin the middleware attached to the request.
func adminFromContext(ctx context.Context) *adminRecord {
	rec, _ := ctx.Value(adminContextKey).(*adminRecord)
	return rec
}

// withAdmin attaches an authenticated admin to a request context.
func withAdmin(req *http.Request, rec *adminRecord) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), adminContextKey, rec))
}

// RequireAdmin guards a handler. An unauthenticated visitor is sent to the
// shared sign-in form; an authenticated non-admin is sent to their own area and
// is never told that an administrative panel exists.
func (p *Panel) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rec, err := p.currentAdmin(req)
		if err == nil {
			next.ServeHTTP(w, withAdmin(req, rec))
			return
		}
		if !errors.Is(err, errNoRow) {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		if hasUserSession(req) {
			http.Redirect(w, req, "/users", http.StatusSeeOther)
			return
		}
		http.Redirect(w, req, "/server/auth/login", http.StatusSeeOther)
	})
}

// hasUserSession reports whether the visitor is signed in as an application
// user. Only the presence of the cookie matters here: the panel never inspects
// or validates a user session.
func hasUserSession(req *http.Request) bool {
	cookie, err := req.Cookie("user_session")
	return err == nil && cookie.Value != ""
}
