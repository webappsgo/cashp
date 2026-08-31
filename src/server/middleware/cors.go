package middleware

import (
	"net/http"
	"strconv"
	"strings"
)

// CORSOptions configures cross-origin access. The zero value allows no
// cross-origin request at all, which is the correct default for a control
// panel: same-origin calls never involve CORS.
type CORSOptions struct {
	// AllowedOrigins lists exact origins that may call the API. The wildcard
	// "*" is honoured only when AllowCredentials is false.
	AllowedOrigins []string
	// AllowedMethods defaults to the safe read verbs plus the write verbs
	// the API actually exposes.
	AllowedMethods []string
	// AllowedHeaders lists request headers a browser may send.
	AllowedHeaders []string
	// ExposedHeaders lists response headers a browser script may read.
	ExposedHeaders []string
	// AllowCredentials permits cookie-authenticated cross-origin calls.
	AllowCredentials bool
	// MaxAge is the preflight cache lifetime in seconds.
	MaxAge int
}

// defaultCORSMethods are the verbs advertised when none are configured.
var defaultCORSMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost,
	http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions,
}

// defaultCORSHeaders are the request headers accepted when none are
// configured.
var defaultCORSHeaders = []string{"Accept", "Content-Type", "Authorization", "X-CSRF-Token", RequestIDHeader}

// CORS answers preflight requests and adds the access-control headers to
// cross-origin responses. A wildcard origin is never combined with
// credentials, which browsers reject and which would defeat the CSRF
// defences anyway.
func CORS(opts CORSOptions) func(http.Handler) http.Handler {
	methods := strings.Join(valuesOr(opts.AllowedMethods, defaultCORSMethods), ", ")
	headers := strings.Join(valuesOr(opts.AllowedHeaders, defaultCORSHeaders), ", ")
	exposed := strings.Join(opts.ExposedHeaders, ", ")
	maxAge := strconv.Itoa(opts.MaxAge)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			allowed, wildcard := originAllowed(origin, opts)
			if !allowed {
				if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			h := w.Header()
			if wildcard {
				h.Set("Access-Control-Allow-Origin", "*")
			} else {
				h.Set("Access-Control-Allow-Origin", origin)
				h.Add("Vary", "Origin")
			}
			if opts.AllowCredentials && !wildcard {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
			if exposed != "" {
				h.Set("Access-Control-Expose-Headers", exposed)
			}
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", methods)
				h.Set("Access-Control-Allow-Headers", headers)
				if opts.MaxAge > 0 {
					h.Set("Access-Control-Max-Age", maxAge)
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// originAllowed reports whether an origin may call the API, and whether the
// permission came from a wildcard entry.
func originAllowed(origin string, opts CORSOptions) (allowed, wildcard bool) {
	for _, candidate := range opts.AllowedOrigins {
		if candidate == "*" {
			if opts.AllowCredentials {
				continue
			}
			return true, true
		}
		if strings.EqualFold(candidate, origin) {
			return true, false
		}
	}
	return false, false
}

// valuesOr returns the configured slice when non-empty, otherwise the
// default slice.
func valuesOr(configured, fallback []string) []string {
	if len(configured) > 0 {
		return configured
	}
	return fallback
}
