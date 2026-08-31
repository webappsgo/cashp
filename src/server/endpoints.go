package server

import (
	"net/http"

	"github.com/webappsgo/cashp/src/api"
)

// EndpointOptions carries the handlers mounted by MountEndpoints. Every
// handler is optional; a nil handler simply mounts nothing.
type EndpointOptions struct {
	// Health serves the PART 13 health payload.
	Health http.Handler
	// Ready serves the readiness probe.
	Ready http.Handler
	// Live serves the liveness probe. When nil a trivial handler is used.
	Live http.Handler
	// Version serves the build identity.
	Version http.Handler
	// Autodiscover serves the client bootstrap document.
	Autodiscover http.Handler
	// RootHealthz mounts the /healthz root alias. It corresponds to
	// server.healthz.root.enabled and is off by default.
	RootHealthz bool
	// RootReadyz mounts the /readyz and /livez root aliases.
	RootReadyz bool
	// Metrics maps a path to the handler serving it, as returned by the
	// metrics registry. Each entry is mounted verbatim, and the same handler
	// instance is reused across its aliases.
	Metrics map[string]http.Handler
}

// MountEndpoints registers the PART 13 health and version surface plus the
// unversioned API aliases of PART 14.
//
// Every alias mounts the exact same handler instance as its canonical route.
// None of them is a redirect: a probe that asks for /api/healthz receives the
// health document, not a 301 to the versioned path.
func (s *Server) MountEndpoints(opts EndpointOptions) {
	if opts.Health != nil {
		canonical := "/server/healthz"
		s.MountRoute(api.Route{
			Method:      http.MethodGet,
			Pattern:     canonical,
			Name:        "health",
			Summary:     "Server health status",
			Description: "Public health document. Answers 200 while the instance can serve traffic and 503 while it cannot.",
			Tags:        []string{"server"},
			Bare:        true,
			Response:    healthFields(),
			Handler:     opts.Health,
		})
		s.mountAPIAliases(canonical, "healthz", opts.Health)
		if opts.RootHealthz {
			s.MountAlias("GET /healthz", canonical, opts.Health)
		}
	}

	if opts.Ready != nil {
		canonical := "/server/readyz"
		s.MountRoute(api.Route{
			Method:      http.MethodGet,
			Pattern:     canonical,
			Name:        "ready",
			Summary:     "Readiness probe",
			Description: "Answers 200 when the instance is ready to receive traffic and 503 while it is not.",
			Tags:        []string{"server"},
			Bare:        true,
			Handler:     opts.Ready,
		})
		s.mountAPIAliases(canonical, "readyz", opts.Ready)
		if opts.RootReadyz {
			s.MountAlias("GET /readyz", canonical, opts.Ready)
		}
	}

	live := opts.Live
	if live == nil {
		live = http.HandlerFunc(liveness)
	}
	liveCanonical := "/server/livez"
	s.MountRoute(api.Route{
		Method:      http.MethodGet,
		Pattern:     liveCanonical,
		Name:        "live",
		Summary:     "Liveness probe",
		Description: "Answers 200 for as long as the process is running. It performs no dependency checks.",
		Tags:        []string{"server"},
		Bare:        true,
		Handler:     live,
	})
	s.mountAPIAliases(liveCanonical, "livez", live)
	if opts.RootReadyz {
		s.MountAlias("GET /livez", liveCanonical, live)
	}

	if opts.Version != nil {
		canonical := "/server/version"
		s.MountRoute(api.Route{
			Method:      http.MethodGet,
			Pattern:     canonical,
			Name:        "version",
			Summary:     "Build identity",
			Description: "Reports the running version, release channel, commit, and build date.",
			Tags:        []string{"server"},
			Bare:        true,
			Handler:     opts.Version,
		})
		s.mountAPIAliases(canonical, "version", opts.Version)
	}

	if opts.Autodiscover != nil {
		canonical := api.APIPath("server", "autodiscover")
		s.MountRoute(api.Route{
			Method:      http.MethodGet,
			Pattern:     canonical,
			Name:        "autodiscover",
			Summary:     "Client bootstrap document",
			Description: "Describes the API base path, endpoints, auth scheme, and the configuration keys a CLI or agent should store.",
			Tags:        []string{"server"},
			Bare:        true,
			Handler:     opts.Autodiscover,
		})
		s.MountAlias("GET "+api.UnversionedPath("autodiscover"), canonical, opts.Autodiscover)
	}

	for path, h := range opts.Metrics {
		if h == nil || path == "" {
			continue
		}
		s.MountRoute(api.Route{
			Method:   http.MethodGet,
			Pattern:  path,
			Summary:  "Metrics exposition",
			Tags:     []string{"server"},
			Bare:     true,
			Internal: true,
			Handler:  h,
		})
	}
}

// mountAPIAliases mounts the versioned API route and the unversioned alias
// for a server endpoint, both running the handler instance already mounted
// at the frontend path.
func (s *Server) mountAPIAliases(canonical, name string, h http.Handler) {
	s.MountAlias("GET "+api.APIPath("server", name), canonical, h)
	s.MountAlias("GET "+api.UnversionedPath(name), canonical, h)
}

// liveness answers the liveness probe. It deliberately checks nothing: a
// process that can run this handler is alive by definition.
func liveness(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	api.Write(w, r, http.StatusOK, api.Body{
		JSON:  map[string]any{"alive": true},
		Text:  "alive: true",
		Title: "Liveness",
	})
}

// healthFields documents the health payload for the generated OpenAPI and
// GraphQL artifacts.
func healthFields() []api.Field {
	return []api.Field{
		{Name: "project", Type: "object", Description: "Public branding identity.", Fields: []api.Field{
			{Name: "name", Type: "string"},
			{Name: "tagline", Type: "string"},
			{Name: "description", Type: "string"},
		}},
		{Name: "status", Type: "string", Description: "healthy, degraded, restart_required, unhealthy, maintenance, or shutting_down."},
		{Name: "version", Type: "string", Description: "Running version."},
		{Name: "go_version", Type: "string", Description: "Go runtime version."},
		{Name: "build", Type: "object", Fields: []api.Field{
			{Name: "commit", Type: "string"},
			{Name: "date", Type: "string"},
		}},
		{Name: "uptime", Type: "string", Description: "Process uptime."},
		{Name: "mode", Type: "string", Description: "Resolved run mode."},
		{Name: "timestamp", Type: "string", Description: "RFC 3339 time the document was produced."},
		{Name: "cluster", Type: "object", Description: "Public cluster topology."},
		{Name: "features", Type: "object", Description: "Public feature flags."},
		{Name: "checks", Type: "object", Description: "Component checks, each ok or error."},
		{Name: "stats", Type: "object", Description: "Aggregate public counters."},
	}
}
