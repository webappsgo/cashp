// Package server implements cashp's HTTP server: listener setup, timeouts,
// graceful shutdown, the middleware chain, and an explicit mount API that
// records every route so the OpenAPI document and the GraphQL schema can be
// generated from what is actually served (AI.md PART 12, PART 14).
package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/webappsgo/cashp/src/api"
	apperr "github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/logging"
	"github.com/webappsgo/cashp/src/server/middleware"
)

// Default timeouts. They bound every phase of a request so a slow or
// malicious peer cannot hold a connection open indefinitely.
const (
	// DefaultReadHeaderTimeout bounds the request-header read.
	DefaultReadHeaderTimeout = 10 * time.Second
	// DefaultReadTimeout bounds the whole request read.
	DefaultReadTimeout = 30 * time.Second
	// DefaultWriteTimeout bounds the response write.
	DefaultWriteTimeout = 60 * time.Second
	// DefaultIdleTimeout bounds a kept-alive idle connection.
	DefaultIdleTimeout = 120 * time.Second
	// DefaultShutdownTimeout bounds graceful shutdown before connections are
	// closed the hard way.
	DefaultShutdownTimeout = 30 * time.Second
	// DefaultMaxHeaderBytes caps the request header size.
	DefaultMaxHeaderBytes = 1 << 20
)

// Options configures a Server.
type Options struct {
	// Addr is the listen address, such as ":8080".
	Addr string
	// BaseURL is the public base URL of this deployment.
	BaseURL string
	// BasePath is the mount prefix when the app is served under a subpath.
	BasePath string
	// APIVersion is the configured api_version segment. It is applied to the
	// api package so no route literal ever hardcodes a version.
	APIVersion string
	// Debug enables the debug-only response detail of AI.md PART 11.
	Debug bool
	// TrustedProxies lists the networks whose forwarded headers are believed.
	TrustedProxies []net.IPNet
	// ForceHTTPS redirects cleartext requests to HTTPS. It never applies to
	// an overlay request, because a hidden service has no clearnet TLS.
	ForceHTTPS bool
	// Headers configures the security headers.
	Headers middleware.HeaderOptions
	// CORS configures cross-origin access.
	CORS middleware.CORSOptions
	// CSRF configures the CSRF defence.
	CSRF middleware.CSRFOptions
	// RateLimit configures the per-client request budgets.
	RateLimit middleware.RateLimitOptions
	// Timeouts. Zero values use the package defaults.
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxHeaderBytes    int
}

// Server owns the route table, the middleware chain, and the listener.
type Server struct {
	opts    Options
	mux     *http.ServeMux
	limiter *middleware.Limiter

	mu     sync.RWMutex
	routes []api.Route
	// exact holds every registered non-subtree path, used to decide whether
	// a trailing slash can be canonicalised away.
	exact map[string]bool
	// mw holds caller-supplied middleware, applied closest to the handlers.
	mw []func(http.Handler) http.Handler
	// handler caches the built chain until the next registration.
	handler http.Handler

	srvMu sync.Mutex
	srv   *http.Server
}

// New builds a server. Registration happens afterwards through Mount and
// MountRoute; the chain is built lazily on the first Handler call.
func New(opts Options) *Server {
	opts.ReadHeaderTimeout = durationOr(opts.ReadHeaderTimeout, DefaultReadHeaderTimeout)
	opts.ReadTimeout = durationOr(opts.ReadTimeout, DefaultReadTimeout)
	opts.WriteTimeout = durationOr(opts.WriteTimeout, DefaultWriteTimeout)
	opts.IdleTimeout = durationOr(opts.IdleTimeout, DefaultIdleTimeout)
	opts.ShutdownTimeout = durationOr(opts.ShutdownTimeout, DefaultShutdownTimeout)
	if opts.MaxHeaderBytes <= 0 {
		opts.MaxHeaderBytes = DefaultMaxHeaderBytes
	}
	opts.CSRF.Debug = opts.Debug
	opts.RateLimit.Debug = opts.Debug

	api.Configure(api.Config{Version: opts.APIVersion, Debug: opts.Debug})

	return &Server{
		opts:    opts,
		mux:     http.NewServeMux(),
		limiter: middleware.NewLimiter(),
		exact:   map[string]bool{},
	}
}

// Options returns the server configuration, so packages that generate
// documentation can read the base URL and version without a second copy.
func (s *Server) Options() Options {
	return s.opts
}

// Mount registers a handler at a pattern. The pattern may carry a leading
// method ("GET /server/healthz") and may be a subtree ("/static/").
//
// Mounting the same handler value at several patterns is the supported way
// to publish an alias: both paths run the identical handler instance, and an
// alias is never served as a redirect.
func (s *Server) Mount(pattern string, h http.Handler) {
	method, path := api.SplitPattern(pattern)
	s.MountRoute(api.Route{Method: method, Pattern: path, Handler: h})
}

// MountFunc registers a handler function at a pattern.
func (s *Server) MountFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	s.Mount(pattern, http.HandlerFunc(h))
}

// MountRoute registers a fully described route. The description is kept in
// the route registry and drives the generated OpenAPI document and GraphQL
// schema, so documentation cannot drift from the served surface.
func (s *Server) MountRoute(rt api.Route) {
	if rt.Handler == nil || rt.Pattern == "" {
		return
	}
	if rt.Method == "" {
		if m, p := api.SplitPattern(rt.Pattern); m != "" {
			rt.Method = m
			rt.Pattern = p
		}
	}
	if rt.Name == "" {
		rt.Name = api.DeriveName(rt.Method, rt.Pattern)
	}

	pattern := rt.Pattern
	if rt.Method != "" {
		pattern = rt.Method + " " + rt.Pattern
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.mux.Handle(pattern, rt.Handler)
	if !strings.HasSuffix(rt.Pattern, "/") {
		s.exact[rt.Pattern] = true
	}
	s.routes = append(s.routes, rt)
	s.handler = nil
}

// MountAlias registers an alias that runs the same handler instance as its
// canonical route.
func (s *Server) MountAlias(pattern, canonical string, h http.Handler) {
	method, path := api.SplitPattern(pattern)
	s.MountRoute(api.Route{
		Method:    method,
		Pattern:   path,
		Handler:   h,
		Alias:     true,
		Canonical: canonical,
	})
}

// Use appends middleware that runs closest to the handlers, inside the
// built-in chain.
func (s *Server) Use(mw func(http.Handler) http.Handler) {
	if mw == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mw = append(s.mw, mw)
	s.handler = nil
}

// Routes returns a copy of the route registry.
func (s *Server) Routes() []api.Route {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]api.Route, len(s.routes))
	copy(out, s.routes)
	return out
}

// Handler returns the complete chain: request identity and logging on the
// outside, then recovery, security headers, CORS, path canonicalisation,
// content negotiation, rate limiting, CSRF, caller middleware, and finally
// the route table.
func (s *Server) Handler() http.Handler {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handler != nil {
		return s.handler
	}

	h := s.dispatch()
	for i := len(s.mw) - 1; i >= 0; i-- {
		h = s.mw[i](h)
	}
	h = middleware.CSRF(s.opts.CSRF)(h)
	h = middleware.RateLimit(s.limiter, s.opts.RateLimit)(h)
	h = middleware.Negotiate(h)
	h = s.canonicalize(h)
	h = middleware.CORS(s.opts.CORS)(h)
	h = middleware.SecurityHeaders(s.opts.Headers)(h)
	h = middleware.Recovery(h)
	h = middleware.Logging(h)
	h = middleware.RealIP(middleware.RealIPOptions{
		TrustedProxies: s.opts.TrustedProxies,
		BasePath:       s.opts.BasePath,
	})(h)
	h = middleware.RequestID(h)

	s.handler = h
	return h
}

// dispatch routes a request through the mux and converts the mux's own
// not-found and method-not-allowed answers into the canonical envelope.
func (s *Server) dispatch() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := s.mux.Handler(r)
		if pattern != "" {
			h.ServeHTTP(w, r)
			return
		}
		probe := &statusProbe{headers: http.Header{}, status: http.StatusNotFound}
		h.ServeHTTP(probe, r)
		if allow := probe.headers.Get("Allow"); allow != "" {
			w.Header().Set("Allow", allow)
		}
		code := apperr.CodeNotFound
		status := http.StatusNotFound
		if probe.status == http.StatusMethodNotAllowed {
			code = apperr.CodeMethodNotAllowed
			status = http.StatusMethodNotAllowed
		}
		api.WriteError(w, r, apperr.New(code, status, apperr.DefaultMessage(code)))
	})
}

// canonicalize applies the two redirects the spec still permits: cleartext
// to HTTPS, and a trailing slash trimmed to the registered exact path.
// Neither is ever applied to an overlay request, and an unversioned API
// alias is never redirected to its versioned form.
func (s *Server) canonicalize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		overlay := middleware.IsOverlayRequest(r.Context()) || middleware.IsOverlayHost(r.Host)
		if s.opts.ForceHTTPS && !overlay && !requestIsTLS(r) {
			target := "https://" + r.Host + r.URL.RequestURI()
			http.Redirect(w, r, target, redirectStatus(r))
			return
		}
		if path := r.URL.Path; len(path) > 1 && strings.HasSuffix(path, "/") {
			trimmed := strings.TrimRight(path, "/")
			s.mu.RLock()
			registered := s.exact[trimmed]
			s.mu.RUnlock()
			if registered {
				target := trimmed
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, redirectStatus(r))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requestIsTLS reports whether the request already reached the server over
// TLS, directly or through a trusted terminating proxy.
func requestIsTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")))
	return strings.HasPrefix(proto, "https")
}

// redirectStatus keeps the method intact on a redirected write.
func redirectStatus(r *http.Request) int {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return http.StatusMovedPermanently
	}
	return http.StatusPermanentRedirect
}

// statusProbe discards everything written to it and records only the status
// and headers, so the mux's built-in error bodies never reach a client.
type statusProbe struct {
	headers http.Header
	status  int
	wrote   bool
}

// Header returns the probe's header map.
func (p *statusProbe) Header() http.Header { return p.headers }

// Write discards the body.
func (p *statusProbe) Write(b []byte) (int, error) { return len(b), nil }

// WriteHeader records the first status written.
func (p *statusProbe) WriteHeader(status int) {
	if p.wrote {
		return
	}
	p.status = status
	p.wrote = true
}

// Serve runs the server on an existing listener until the context is
// cancelled, then shuts down gracefully within the shutdown timeout.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: s.opts.ReadHeaderTimeout,
		ReadTimeout:       s.opts.ReadTimeout,
		WriteTimeout:      s.opts.WriteTimeout,
		IdleTimeout:       s.opts.IdleTimeout,
		MaxHeaderBytes:    s.opts.MaxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(logging.L().Handler(), slog.LevelError),
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	s.srvMu.Lock()
	s.srv = srv
	s.srvMu.Unlock()

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.opts.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logging.L().Error("graceful shutdown did not finish in time", slog.String("error", err.Error()))
			_ = srv.Close()
		}
	}()

	err := srv.Serve(l)
	if errors.Is(err, http.ErrServerClosed) {
		<-shutdownDone
		return nil
	}
	return err
}

// ListenAndServe binds the configured address and serves until the context
// is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	addr := s.opts.Addr
	if addr == "" {
		addr = ":8080"
	}
	lc := net.ListenConfig{}
	l, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	logging.L().Info("http server listening", slog.String("addr", l.Addr().String()))
	return s.Serve(ctx, l)
}

// Shutdown stops the running server, refusing new connections and waiting
// for in-flight requests to finish.
func (s *Server) Shutdown(ctx context.Context) error {
	s.srvMu.Lock()
	srv := s.srv
	s.srvMu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// durationOr returns the configured duration when positive, otherwise the
// default.
func durationOr(configured, fallback time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return fallback
}
