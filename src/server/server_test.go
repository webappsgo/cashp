package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/webappsgo/cashp/src/api"
	apperr "github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/server/middleware"
)

// echoHandler answers with a fixed document in the negotiated format.
type echoHandler struct {
	name string
}

// ServeHTTP writes the handler's name so a test can tell two handlers apart.
func (h *echoHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	api.Write(w, r, http.StatusOK, api.Body{
		JSON:  map[string]any{"handler": h.name},
		Title: "Echo",
	})
}

// newTestServer builds a server with predictable options.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	api.Configure(api.Config{})
	return New(Options{Addr: "127.0.0.1:0", BaseURL: "https://panel.example.com"})
}

// do runs one request through the complete chain.
func do(s *Server, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestMountRouteAndAliasShareOneHandlerInstance(t *testing.T) {
	s := newTestServer(t)
	h := &echoHandler{name: "health"}
	s.MountRoute(api.Route{Method: http.MethodGet, Pattern: "/server/healthz", Name: "health", Handler: h})
	s.MountAlias("GET "+api.APIPath("server", "healthz"), "/server/healthz", h)
	s.MountAlias("GET "+api.UnversionedPath("healthz"), "/server/healthz", h)

	var mounted int
	for _, rt := range s.Routes() {
		if rt.Handler == http.Handler(h) {
			mounted++
		}
	}
	if mounted != 3 {
		t.Fatalf("the alias must reuse the canonical handler instance, found %d mounts", mounted)
	}

	for _, path := range []string{"/server/healthz", "/api/v1/server/healthz", "/api/healthz"} {
		w := do(s, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200 (an alias is never a redirect)", path, w.Code)
		}
		if loc := w.Header().Get("Location"); loc != "" {
			t.Fatalf("%s redirected to %q", path, loc)
		}
	}
}

func TestAliasesServeEveryNegotiatedFormat(t *testing.T) {
	s := newTestServer(t)
	h := &echoHandler{name: "health"}
	s.MountRoute(api.Route{Method: http.MethodGet, Pattern: "/server/healthz", Handler: h})

	cases := []struct {
		accept string
		agent  string
		want   string
	}{
		{"application/json", "Mozilla/5.0", "application/json"},
		{"text/plain", "Mozilla/5.0", "text/plain"},
		{"text/html", "Mozilla/5.0", "text/html"},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodGet, "/server/healthz", nil)
		r.Header.Set("Accept", tc.accept)
		r.Header.Set("User-Agent", tc.agent)
		w := do(s, r)
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, tc.want) {
			t.Fatalf("Accept %q gave Content-Type %q", tc.accept, ct)
		}
	}
}

func TestTxtSuffixIsServedByTheSameRoute(t *testing.T) {
	s := newTestServer(t)
	s.MountRoute(api.Route{Method: http.MethodGet, Pattern: api.APIPath("server", "healthz"), Handler: &echoHandler{name: "health"}})

	w := do(s, httptest.NewRequest(http.MethodGet, api.APIPath("server", "healthz")+".txt", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", ct)
	}
}

func TestNotFoundAndMethodNotAllowedUseTheCanonicalEnvelope(t *testing.T) {
	s := newTestServer(t)
	s.MountRoute(api.Route{Method: http.MethodGet, Pattern: "/server/healthz", Handler: &echoHandler{name: "health"}})

	// AI.md PART 14 "Content Negotiation Priority" for /api/**: an empty
	// User-Agent is treated as a non-interactive HTTP tool and gets plain
	// text unless Accept: application/json wins priority 2 first — set it
	// explicitly since this test exercises the JSON envelope shape.
	notFound := httptest.NewRequest(http.MethodGet, "/api/v1/nothing", nil)
	notFound.Header.Set("Accept", "application/json")
	w := do(s, notFound)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	var failure api.Failure
	if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil {
		t.Fatalf("the 404 body is not the canonical envelope: %s", w.Body.String())
	}
	if failure.OK || failure.Error != apperr.CodeNotFound {
		t.Fatalf("failure = %+v", failure)
	}

	methodNotAllowed := httptest.NewRequest(http.MethodDelete, "/server/healthz", nil)
	methodNotAllowed.Header.Set("Accept", "application/json")
	w = do(s, methodNotAllowed)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil {
		t.Fatalf("the 405 body is not the canonical envelope: %s", w.Body.String())
	}
	if failure.Error != apperr.CodeMethodNotAllowed {
		t.Fatalf("error code = %q", failure.Error)
	}
}

func TestTrailingSlashIsCanonicalisedButAnAliasIsNot(t *testing.T) {
	s := newTestServer(t)
	h := &echoHandler{name: "swagger"}
	s.MountRoute(api.Route{Method: http.MethodGet, Pattern: api.APIPath("server", "swagger"), Handler: h})
	s.MountAlias("GET "+api.UnversionedPath("swagger"), api.APIPath("server", "swagger"), h)

	w := do(s, httptest.NewRequest(http.MethodGet, "/api/swagger/", nil))
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301 for trailing-slash canonicalisation", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/api/swagger" {
		t.Fatalf("Location = %q, want /api/swagger", got)
	}

	w = do(s, httptest.NewRequest(http.MethodGet, "/api/swagger", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("the unversioned alias must be served directly, got %d", w.Code)
	}
}

func TestSecurityHeadersAndRequestID(t *testing.T) {
	s := newTestServer(t)
	s.MountRoute(api.Route{Method: http.MethodGet, Pattern: "/server/healthz", Handler: &echoHandler{name: "health"}})

	r := httptest.NewRequest(http.MethodGet, "/server/healthz", nil)
	r.Header.Set(middleware.RequestIDHeader, "abc-123")
	w := do(s, r)

	if got := w.Header().Get(middleware.RequestIDHeader); got != "abc-123" {
		t.Fatalf("request id = %q, want the inbound value echoed", got)
	}
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "SAMEORIGIN",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := w.Header().Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestOverlayRequestNeverReceivesHSTSOrAnUpgradeRedirect(t *testing.T) {
	api.Configure(api.Config{})
	s := New(Options{
		ForceHTTPS: true,
		Headers:    middleware.HeaderOptions{TLS: true, HSTS: true, OnionAddress: "example.onion"},
	})
	s.MountRoute(api.Route{Method: http.MethodGet, Pattern: "/server/healthz", Handler: &echoHandler{name: "health"}})

	r := httptest.NewRequest(http.MethodGet, "http://example.onion/server/healthz", nil)
	r.Host = "example.onion"
	r.Header.Set("Accept", "text/html")
	r.Header.Set("User-Agent", "Mozilla/5.0")
	w := do(s, r)

	if w.Code != http.StatusOK {
		t.Fatalf("an overlay request must not be redirected, got %d to %q", w.Code, w.Header().Get("Location"))
	}
	if got := w.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("HSTS must never be sent to an overlay host, got %q", got)
	}
	if got := w.Header().Get("Onion-Location"); got != "" {
		t.Fatalf("Onion-Location must never be sent to an overlay host, got %q", got)
	}
}

func TestCSRFRejectsACrossOriginWrite(t *testing.T) {
	api.Configure(api.Config{})
	s := New(Options{})
	things := api.APIPath("things")
	s.MountRoute(api.Route{Method: http.MethodPost, Pattern: things, Handler: &echoHandler{name: "things"}})

	r := httptest.NewRequest(http.MethodPost, things, nil)
	r.Host = "panel.example.com"
	r.Header.Set("Origin", "https://evil.example.net")
	// AI.md PART 14 "Content Negotiation Priority": an empty User-Agent is a
	// non-interactive HTTP tool on /api/** and gets plain text unless
	// Accept: application/json wins first — set it explicitly since this
	// test exercises the JSON envelope shape.
	r.Header.Set("Accept", "application/json")
	w := do(s, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	var failure api.Failure
	if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if failure.Error != apperr.CodeForbidden {
		t.Fatalf("error = %q", failure.Error)
	}
	if failure.Details != nil {
		t.Fatalf("the failing check must not be disclosed outside debug mode: %+v", failure.Details)
	}
}

func TestRateLimitRejectsWithRetryAfter(t *testing.T) {
	api.Configure(api.Config{})
	s := New(Options{
		RateLimit: middleware.RateLimitOptions{Read: middleware.Rule{Limit: 1, Window: time.Minute}},
	})
	things := api.APIPath("things")
	s.MountRoute(api.Route{Method: http.MethodGet, Pattern: things, Handler: &echoHandler{name: "things"}})

	first := do(s, httptest.NewRequest(http.MethodGet, things, nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first request status = %d", first.Code)
	}
	second := do(s, httptest.NewRequest(http.MethodGet, things, nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Fatal("a 429 must carry Retry-After")
	}
	if strings.Contains(second.Body.String(), "\"limit\"") {
		t.Fatalf("the threshold must not be disclosed outside debug mode: %s", second.Body.String())
	}
}

func TestPanicIsRecoveredWithoutLeakingAStackTrace(t *testing.T) {
	api.Configure(api.Config{})
	s := New(Options{Debug: true})
	boom := api.APIPath("boom")
	s.MountFunc("GET "+boom, func(http.ResponseWriter, *http.Request) {
		panic("database credentials postgres://user:pass@10.0.0.5/db")
	})

	w := do(s, httptest.NewRequest(http.MethodGet, boom, nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	for _, leak := range []string{"postgres://", "10.0.0.5", "goroutine", "server_test.go"} {
		if strings.Contains(body, leak) {
			t.Fatalf("the recovered response leaked %q: %s", leak, body)
		}
	}
}

func TestMountEndpointsRouteTable(t *testing.T) {
	api.Configure(api.Config{})
	s := New(Options{})
	health := api.NewHealth(api.HealthOptions{Project: api.ProjectInfo{Name: "cashp"}, Build: api.Build{Version: "1.0.0"}})
	s.MountEndpoints(EndpointOptions{
		Health:       health,
		Ready:        api.NewReady(health),
		Version:      api.NewVersionHandler("cashp", api.Build{Version: "1.0.0"}, "go1.27.0"),
		Autodiscover: api.NewAutodiscover(api.AutodiscoverOptions{Project: "cashp"}),
		RootHealthz:  true,
		RootReadyz:   true,
	})

	for _, path := range []string{
		"/server/healthz",
		"/healthz",
		"/api/v1/server/healthz",
		"/api/healthz",
		"/server/readyz",
		"/readyz",
		"/api/v1/server/readyz",
		"/api/readyz",
		"/server/livez",
		"/livez",
		"/server/version",
		"/api/v1/server/version",
		"/api/v1/server/autodiscover",
		"/api/autodiscover",
	} {
		w := do(s, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK && w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d", path, w.Code)
		}
	}
}

func TestRootHealthzIsNotMountedByDefault(t *testing.T) {
	api.Configure(api.Config{})
	s := New(Options{})
	health := api.NewHealth(api.HealthOptions{Project: api.ProjectInfo{Name: "cashp"}})
	s.MountEndpoints(EndpointOptions{Health: health})

	w := do(s, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 while server.healthz.root.enabled is off", w.Code)
	}
}

func TestMountDocsSurface(t *testing.T) {
	api.Configure(api.Config{})
	s := New(Options{})
	health := api.NewHealth(api.HealthOptions{Project: api.ProjectInfo{Name: "cashp"}, Build: api.Build{Version: "1.0.0"}})
	s.MountEndpoints(EndpointOptions{Health: health})
	s.MountDocs(DocsOptions{Title: "cashp", Version: "1.0.0", BaseURL: "https://panel.example.com"})

	spec := do(s, httptest.NewRequest(http.MethodGet, "/api/v1/server/swagger", nil))
	if spec.Code != http.StatusOK {
		t.Fatalf("swagger status = %d", spec.Code)
	}
	if ct := spec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("the OpenAPI document must be JSON, got %q", ct)
	}
	var doc map[string]any
	if err := json.Unmarshal(spec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	paths, _ := doc["paths"].(map[string]any)
	if _, found := paths["/server/healthz"]; !found {
		t.Fatalf("the document does not describe the mounted health route: %v", paths)
	}

	alias := do(s, httptest.NewRequest(http.MethodGet, "/api/swagger", nil))
	if alias.Code != http.StatusOK || alias.Header().Get("Location") != "" {
		t.Fatalf("the unversioned swagger alias must be served directly, got %d", alias.Code)
	}

	for _, path := range []string{"/server/docs/swagger", "/server/docs/graphql"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Accept", "text/html")
		r.Header.Set("User-Agent", "Mozilla/5.0")
		w := do(s, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), "<!DOCTYPE html>") {
			t.Fatalf("%s did not render a page", path)
		}
	}

	query := strings.NewReader(`{"query":"{ health }"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/graphql", query)
	r.Header.Set("Content-Type", "application/json")
	w := do(s, r)
	if w.Code != http.StatusOK {
		t.Fatalf("graphql alias status = %d: %s", w.Code, w.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, found := result["data"]; !found {
		t.Fatalf("the GraphQL response carries no data field: %s", w.Body.String())
	}
}
