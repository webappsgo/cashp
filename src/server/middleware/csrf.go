package middleware

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/webappsgo/cashp/src/api"
	apperr "github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/security"
)

// CSRFHeader is the header a JavaScript-free form post mirrors into, and the
// header an interactive client sends.
const CSRFHeader = "X-CSRF-Token"

// CSRFFormField is the hidden form field carrying the token on a plain HTML
// form submission.
const CSRFFormField = "csrf_token"

// CSRFOptions configures the CSRF defence.
type CSRFOptions struct {
	// Secret keys the token HMAC. When empty the middleware falls back to
	// the origin check alone, which is still enforced.
	Secret []byte
	// SessionID extracts the session identifier a token is bound to. When
	// nil, or when it returns an empty string, the request is treated as
	// token-authenticated and only the origin check applies.
	SessionID func(*http.Request) string
	// TrustedOrigins lists additional origins allowed to post, beyond the
	// request's own host.
	TrustedOrigins []string
	// Debug enables the debug-only reason detail described in AI.md PART 11.
	Debug bool
}

// safeMethods never change state and are therefore exempt.
var safeMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
	http.MethodTrace:   true,
}

// CSRF rejects cross-site state-changing requests. It applies two
// independent checks: the Origin or Referer must match this host, and a
// cookie-authenticated request must also carry a valid bound token. Requests
// authenticated with a bearer token are not cookie-driven and therefore
// cannot be forged by a third-party page, so they skip the token check.
func CSRF(opts CSRFOptions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if safeMethods[r.Method] {
				next.ServeHTTP(w, r)
				return
			}
			if reason := csrfFailure(r, opts); reason != "" {
				rejectCSRF(w, r, opts, reason)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// csrfFailure returns the name of the check that tripped, or an empty string
// when the request passes.
func csrfFailure(r *http.Request, opts CSRFOptions) string {
	if !originMatches(r, opts.TrustedOrigins) {
		return "origin_mismatch"
	}
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "bearer ") {
		return ""
	}
	if opts.SessionID == nil || len(opts.Secret) == 0 {
		return ""
	}
	session := opts.SessionID(r)
	if session == "" {
		return ""
	}
	token := csrfToken(r)
	if token == "" {
		return "token_absent"
	}
	if !security.ValidateCSRFToken(opts.Secret, session, token) {
		return "token_mismatch"
	}
	return ""
}

// csrfToken reads the token from the header first, then from a form field so
// a plain HTML form works without JavaScript.
func csrfToken(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get(CSRFHeader)); v != "" {
		return v
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseForm(); err == nil {
			return strings.TrimSpace(r.PostFormValue(CSRFFormField))
		}
	}
	return ""
}

// originMatches checks the Origin header, falling back to Referer, against
// this request's own host and the configured trusted origins.
func originMatches(r *http.Request, trusted []string) bool {
	candidate := strings.TrimSpace(r.Header.Get("Origin"))
	if candidate == "" || candidate == "null" {
		candidate = strings.TrimSpace(r.Header.Get("Referer"))
	}
	if candidate == "" {
		return true
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Host == "" {
		return false
	}
	if strings.EqualFold(parsed.Host, r.Host) {
		return true
	}
	for _, t := range trusted {
		if strings.EqualFold(t, parsed.Scheme+"://"+parsed.Host) || strings.EqualFold(t, parsed.Host) {
			return true
		}
	}
	return false
}

// rejectCSRF writes the canonical 403 envelope. The reason a check tripped is
// debug-only detail; production callers see the generic message only.
func rejectCSRF(w http.ResponseWriter, r *http.Request, opts CSRFOptions, reason string) {
	err := apperr.New(apperr.CodeForbidden, http.StatusForbidden, "CSRF validation failed")
	if opts.Debug {
		err = err.WithDetails(map[string]any{"check": reason})
	}
	api.WriteError(w, r, err)
}
