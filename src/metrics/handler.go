package metrics

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// bearerPrefix is the only accepted Authorization scheme. Query-string
// tokens are FORBIDDEN because they leak into access logs and proxies.
const bearerPrefix = "Bearer "

// Handler returns the prometheus service handler: the Prometheus text
// exposition of every metric, behind the mandatory bearer token check.
func (r *Registry) Handler() http.Handler {
	return r.promHandler
}

// GrafanaHandler returns the grafana service handler.
func (r *Registry) GrafanaHandler() http.Handler {
	return r.grafanaHandler
}

// LokiHandler returns the loki service handler.
func (r *Registry) LokiHandler() http.Handler {
	return r.lokiHandler
}

// Handlers returns the complete route table for the router to mount
// verbatim. Every alias of a service maps to the SAME handler instance, so
// no alias can ever become a redirect: redirects break scrapers.
func (r *Registry) Handlers() map[string]http.Handler {
	out := make(map[string]http.Handler)

	for _, prefix := range r.prefixes() {
		out[prefix] = r.promHandler
		out[prefix+"/"+ServicePrometheus] = r.promHandler
		out[prefix+"/"+ServiceGrafana] = r.grafanaHandler
		out[prefix+"/"+ServiceLoki] = r.lokiHandler
	}

	return out
}

// prefixes returns the mount points of the metrics endpoint set: the server
// path, the versioned API path, its unversioned alias, and the root alias
// when server.metrics.root.enabled is on.
func (r *Registry) prefixes() []string {
	out := []string{"/server/metrics"}

	if version := strings.Trim(strings.TrimSpace(r.opts.APIVersion), "/"); version != "" {
		out = append(out, "/api/"+version+"/server/metrics")
	}

	out = append(out, "/api/metrics")

	if r.opts.RootAliasEnabled {
		out = append(out, "/metrics")
	}

	return out
}

// authHandler guards one metrics service with its own bearer token. It is a
// pointer type so the router, and the tests, can prove that every alias path
// resolves to the very same handler instance.
type authHandler struct {
	registry *Registry
	service  string
	next     http.Handler
}

// authenticate wraps a service handler with the mandatory per-service bearer
// token check. There is no unauthenticated default: the only bypass is the
// firewalled escape hatch, and an empty token disables the service outright.
func (r *Registry) authenticate(service string, next http.Handler) *authHandler {
	return &authHandler{registry: r, service: service, next: next}
}

// ServeHTTP enforces the token rules before the service handler ever runs.
func (h *authHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

		return
	}

	w.Header().Set("Cache-Control", "no-store")

	// Firewalled escape hatch - skips token checks for ALL services.
	if h.registry.opts.AllowUnauthenticated {
		h.next.ServeHTTP(w, req)

		return
	}

	token := h.registry.opts.ServiceTokens[h.service]

	// Empty token = service disabled: 403 with an empty body, the reason
	// having been logged once at startup.
	if strings.TrimSpace(token) == "" {
		w.WriteHeader(http.StatusForbidden)

		return
	}

	// Header only - a token in the query string is never read - compared in
	// constant time, and never logged or echoed back.
	if subtle.ConstantTimeCompare([]byte(req.Header.Get("Authorization")), []byte(bearerPrefix+token)) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	h.next.ServeHTTP(w, req)
}

// servePrometheus writes the full Prometheus text exposition.
func (r *Registry) servePrometheus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", ContentTypePrometheus)
	w.WriteHeader(http.StatusOK)

	// A write error means the scraper hung up; there is nothing left to
	// report to it and nothing to retry.
	_ = WriteText(w, r.Collect())
}
