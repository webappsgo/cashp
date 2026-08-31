package guard

import (
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strings"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// DefaultMaxBodyBytes is the ceiling applied to a JSON or form request body
// when a route does not set its own. It is generous for a control-panel
// payload and far below what an unbounded reader would let a single client
// pin in memory.
const DefaultMaxBodyBytes int64 = 1 << 20

// Middleware is the standard handler-wrapping shape, so these guards
// compose with the existing src/server/middleware chain.
type Middleware func(http.Handler) http.Handler

// writeDenial renders a denial to the client as the generic code for its
// reason and logs the log-only detail. The detail never reaches the
// response, so a probe cannot learn which guard refused it or why.
func writeDenial(w http.ResponseWriter, r *http.Request, logger *slog.Logger, status int, denial *DenyError) {
	if logger != nil {
		logger.WarnContext(r.Context(), "guard denied request",
			slog.String("reason", string(denial.Reason)),
			slog.String("detail", denial.Detail),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
		)
	}
	appError := apperr.Wrap(denial, denial.Code, status, apperr.DefaultMessage(denial.Code))
	_ = appError.WriteJSON(w)
}

// BodyLimit caps how many bytes a handler can read from a request body.
// The cap is enforced by the server rather than trusted from Content-Length,
// so a client that lies about its length or streams a chunked body still
// hits the ceiling; a declared length over the ceiling is refused before a
// single byte is read.
//
// A limit of zero or less falls back to DefaultMaxBodyBytes: this guard has
// no "unlimited" setting, because an unlimited body is the memory-exhaustion
// vector it exists to close.
func BodyLimit(maxBytes int64, logger *slog.Logger) Middleware {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBodyBytes
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxBytes {
				denial := Deny(ReasonBodyTooLarge, "declared content length exceeds the route ceiling")
				writeDenial(w, r, logger, http.StatusRequestEntityTooLarge, denial)
				return
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireContentType refuses a request whose body carries a media type the
// route does not accept. It is the CSRF companion to the token check: a
// cross-origin form post cannot set a JSON content type without a preflight,
// so a JSON-only route rejects it outright.
//
// Only requests that carry a body are checked; a GET or DELETE with no body
// has no media type to validate. An empty allowlist accepts nothing, which
// is the deny-by-default posture: a route must name what it takes.
func RequireContentType(logger *slog.Logger, accepted ...string) Middleware {
	allowed := make(map[string]struct{}, len(accepted))
	for _, mediaType := range accepted {
		allowed[strings.ToLower(strings.TrimSpace(mediaType))] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body == nil || r.ContentLength == 0 {
				next.ServeHTTP(w, r)
				return
			}
			header := r.Header.Get("Content-Type")
			if header == "" {
				denial := Deny(ReasonUnsupportedMediaType, "request body carries no content type")
				writeDenial(w, r, logger, http.StatusUnsupportedMediaType, denial)
				return
			}
			mediaType, _, err := mime.ParseMediaType(header)
			if err != nil {
				denial := Deny(ReasonUnsupportedMediaType, "content type is unparseable: "+header)
				writeDenial(w, r, logger, http.StatusUnsupportedMediaType, denial)
				return
			}
			if _, ok := allowed[strings.ToLower(mediaType)]; !ok {
				denial := Deny(ReasonUnsupportedMediaType, "content type "+mediaType+" is not accepted by this route")
				writeDenial(w, r, logger, http.StatusUnsupportedMediaType, denial)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AllowedHosts refuses a request whose Host header is not one this
// deployment answers for. Without it, an attacker-chosen Host is reflected
// into absolute URLs in password-reset mails and redirects, which turns a
// public endpoint into a credential-delivery mechanism aimed at a host the
// attacker controls.
//
// An empty list refuses every request rather than accepting every request:
// a misconfigured allowlist must fail closed. A single "*" entry disables
// the check and is meant only for a development deployment behind no proxy.
func AllowedHosts(hosts []string, logger *slog.Logger) Middleware {
	wildcard := false
	allowed := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		normalized := strings.ToLower(strings.TrimSpace(host))
		if normalized == "*" {
			wildcard = true
			continue
		}
		if normalized == "" {
			continue
		}
		allowed[normalizeHost(normalized)] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if wildcard {
				next.ServeHTTP(w, r)
				return
			}
			if _, ok := allowed[normalizeHost(r.Host)]; !ok {
				denial := Deny(ReasonHostNotAllowed, "host header "+r.Host+" is not a configured host")
				writeDenial(w, r, logger, http.StatusBadRequest, denial)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// normalizeHost lowercases a Host header and strips the port and trailing
// dot, so "Example.COM:8443" and "example.com." both compare as
// "example.com" and neither can slip past an allowlist entry.
func normalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if stripped, _, err := net.SplitHostPort(host); err == nil {
		host = stripped
	}
	host = strings.TrimSuffix(host, ".")
	return strings.Trim(host, "[]")
}

// RequireOrigin refuses a state-changing request whose Origin or Referer
// names a site this deployment does not serve. It backs up the CSRF token
// rather than replacing it: a request that carries neither header is passed
// through here and still has to satisfy the token check.
//
// An empty allowlist refuses every request that carries either header.
func RequireOrigin(origins []string, logger *slog.Logger) Middleware {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		normalized := originKey(origin)
		if normalized == "" {
			continue
		}
		allowed[normalized] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			candidate := r.Header.Get("Origin")
			if candidate == "" {
				candidate = r.Header.Get("Referer")
			}
			if candidate == "" {
				next.ServeHTTP(w, r)
				return
			}
			if _, ok := allowed[originKey(candidate)]; !ok {
				denial := Deny(ReasonHostNotAllowed, "origin "+candidate+" is not a configured origin")
				writeDenial(w, r, logger, http.StatusForbidden, denial)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// originKey reduces an Origin or Referer value to its scheme-and-authority
// form, so a Referer carrying a path still compares against a bare origin
// entry and a crafted path cannot smuggle an allowed origin into the match.
func originKey(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	scheme := ""
	switch {
	case strings.HasPrefix(value, "https://"):
		scheme = "https://"
	case strings.HasPrefix(value, "http://"):
		scheme = "http://"
	default:
		return value
	}
	authority := value[len(scheme):]
	if slash := strings.IndexByte(authority, '/'); slash >= 0 {
		authority = authority[:slash]
	}
	return scheme + authority
}

// Chain composes middlewares so the first named runs outermost. It exists
// so a route can state its full guard posture in one expression and a
// reviewer can see at a glance that nothing was left off.
func Chain(middlewares ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			if middlewares[i] == nil {
				continue
			}
			next = middlewares[i](next)
		}
		return next
	}
}
