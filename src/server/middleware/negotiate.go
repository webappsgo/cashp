package middleware

import (
	"net/http"

	"github.com/webappsgo/cashp/src/api"
)

// Negotiate resolves the response format once per request and records it on
// the context, so every handler in the chain answers in the same format.
//
// The ".txt" suffix is stripped from the path before routing, which is what
// lets one registered route serve both "/api/{v}/server/healthz" and
// "/api/{v}/server/healthz.txt" without a second registration and without a
// redirect.
func Negotiate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if api.HasTxtSuffix(r.URL.Path) {
			trimmed := api.TrimTxtSuffix(r.URL.Path)
			if trimmed != "" {
				r.URL.Path = trimmed
				if r.URL.RawPath != "" {
					r.URL.RawPath = api.TrimTxtSuffix(r.URL.RawPath)
				}
				r = r.WithContext(api.WithFormat(r.Context(), api.FormatText))
				next.ServeHTTP(w, r)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(api.WithFormat(r.Context(), api.Negotiate(r))))
	})
}
