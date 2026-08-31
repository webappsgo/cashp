package metrics

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// get performs a GET against h with the given Authorization header value.
func get(h http.Handler, target, authorization string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestHandlerRequiresBearerToken(t *testing.T) {
	r := newTestRegistry(t)

	cases := []struct {
		name string
		auth string
	}{
		{"no header", ""},
		{"wrong token", "Bearer nope"},
		{"wrong scheme", "Basic prom-token"},
		{"bare token", "prom-token"},
		{"other service token", "Bearer loki-token"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(r.Handler(), "/server/metrics", tc.auth)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}

			if strings.Contains(rec.Body.String(), "prom-token") {
				t.Fatal("response echoed the expected token")
			}
		})
	}
}

func TestHandlerRejectsQueryStringToken(t *testing.T) {
	r := newTestRegistry(t)

	rec := get(r.Handler(), "/server/metrics?token=prom-token", "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 - query-string tokens are forbidden", rec.Code)
	}
}

func TestHandlerAcceptsCorrectToken(t *testing.T) {
	r := newTestRegistry(t)

	rec := get(r.Handler(), "/server/metrics", "Bearer prom-token")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != ContentTypePrometheus {
		t.Fatalf("content type = %q", ct)
	}

	if !strings.Contains(rec.Body.String(), "cashp_app_info{") {
		t.Fatalf("body missing app_info:\n%s", rec.Body.String())
	}
}

func TestEmptyTokenReturns403WithEmptyBody(t *testing.T) {
	var logged []string

	r := New(Options{
		ServiceTokens: map[string]string{ServicePrometheus: "prom-token", ServiceGrafana: "  "},
		Logf:          func(format string, args ...any) { logged = append(logged, format) },
	})

	for _, service := range []string{ServiceGrafana, ServiceLoki} {
		handler := r.GrafanaHandler()
		if service == ServiceLoki {
			handler = r.LokiHandler()
		}

		rec := get(handler, "/server/metrics/"+service, "Bearer anything")

		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403", service, rec.Code)
		}

		if rec.Body.Len() != 0 {
			t.Fatalf("%s: body = %q, want empty", service, rec.Body.String())
		}
	}

	disabled := r.DisabledServices()
	sort.Strings(disabled)

	if len(disabled) != 2 || disabled[0] != ServiceGrafana || disabled[1] != ServiceLoki {
		t.Fatalf("disabled services = %v", disabled)
	}

	// The reason is logged once per disabled service, at startup only.
	if len(logged) != 2 {
		t.Fatalf("startup log lines = %d, want 2", len(logged))
	}
}

func TestAllowUnauthenticatedSkipsTokenChecks(t *testing.T) {
	r := New(Options{AllowUnauthenticated: true})

	rec := get(r.Handler(), "/server/metrics", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestNonGetMethodRejected(t *testing.T) {
	r := newTestRegistry(t)

	req := httptest.NewRequest(http.MethodPost, "/server/metrics", nil)
	req.Header.Set("Authorization", "Bearer prom-token")

	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}

	if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Fatalf("Allow = %q", allow)
	}
}

func TestHandlersRouteTable(t *testing.T) {
	r := newTestRegistry(t)

	got := r.Handlers()

	want := []string{
		"/server/metrics",
		"/server/metrics/prometheus",
		"/server/metrics/grafana",
		"/server/metrics/loki",
		"/api/v1/server/metrics",
		"/api/v1/server/metrics/prometheus",
		"/api/v1/server/metrics/grafana",
		"/api/v1/server/metrics/loki",
		"/api/metrics",
		"/api/metrics/prometheus",
		"/api/metrics/grafana",
		"/api/metrics/loki",
		"/metrics",
		"/metrics/prometheus",
		"/metrics/grafana",
		"/metrics/loki",
	}

	if len(got) != len(want) {
		t.Fatalf("route count = %d, want %d: %v", len(got), len(want), got)
	}

	for _, path := range want {
		if _, ok := got[path]; !ok {
			t.Fatalf("missing route %q", path)
		}
	}
}

func TestAliasesShareTheSameHandlerInstance(t *testing.T) {
	r := newTestRegistry(t)
	handlers := r.Handlers()

	groups := map[string][]string{
		ServicePrometheus: {
			"/server/metrics",
			"/server/metrics/prometheus",
			"/api/v1/server/metrics",
			"/api/v1/server/metrics/prometheus",
			"/api/metrics",
			"/api/metrics/prometheus",
			"/metrics",
			"/metrics/prometheus",
		},
		ServiceGrafana: {"/server/metrics/grafana", "/api/v1/server/metrics/grafana", "/api/metrics/grafana", "/metrics/grafana"},
		ServiceLoki:    {"/server/metrics/loki", "/api/v1/server/metrics/loki", "/api/metrics/loki", "/metrics/loki"},
	}

	for service, paths := range groups {
		first := handlers[paths[0]]

		for _, path := range paths[1:] {
			if handlers[path] != first {
				t.Fatalf("%s: %q is not the same handler instance as %q", service, path, paths[0])
			}
		}
	}

	if handlers["/server/metrics"] != r.Handler() {
		t.Fatal("Handler() must be the instance mounted at /server/metrics")
	}
}

func TestRootAliasGated(t *testing.T) {
	r := New(Options{APIVersion: "v1"})

	if _, ok := r.Handlers()["/metrics"]; ok {
		t.Fatal("root alias must be absent when RootAliasEnabled is false")
	}
}

func TestVersionedRouteOmittedWithoutAPIVersion(t *testing.T) {
	r := New(Options{})

	for path := range r.Handlers() {
		if strings.HasPrefix(path, "/api/v") {
			t.Fatalf("unexpected versioned route %q", path)
		}
	}
}

func TestGrafanaAndLokiServiceResponses(t *testing.T) {
	r := newTestRegistry(t)
	r.Log("info", "server started")

	grafana := get(r.GrafanaHandler(), "/server/metrics/grafana", "Bearer graf-token")
	if grafana.Code != http.StatusOK {
		t.Fatalf("grafana status = %d", grafana.Code)
	}

	if ct := grafana.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("grafana content type = %q", ct)
	}

	loki := get(r.LokiHandler(), "/server/metrics/loki", "Bearer loki-token")
	if loki.Code != http.StatusOK {
		t.Fatalf("loki status = %d", loki.Code)
	}

	if !strings.Contains(loki.Body.String(), `"streams"`) {
		t.Fatalf("loki body = %s", loki.Body.String())
	}
}
