package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// newRequest builds a request carrying an Accept header.
func newRequest(method, target, accept string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	return r
}

func TestAPIPathsUseConfiguredVersion(t *testing.T) {
	Configure(Config{Version: "v2"})
	defer Configure(Config{})

	if got := APIBasePath(); got != "/api/v2" {
		t.Fatalf("APIBasePath = %q, want /api/v2", got)
	}
	if got := APIPath("server", "healthz"); got != "/api/v2/server/healthz" {
		t.Fatalf("APIPath = %q", got)
	}
	if got := UnversionedPath("healthz"); got != "/api/healthz" {
		t.Fatalf("UnversionedPath = %q", got)
	}
}

func TestAPIPathDefaultsToDefaultVersion(t *testing.T) {
	Configure(Config{})
	if got := APIBasePath(); got != "/api/"+DefaultVersion {
		t.Fatalf("APIBasePath = %q", got)
	}
}

func TestNegotiateAPIPrefersTxtSuffixThenAccept(t *testing.T) {
	cases := []struct {
		name   string
		target string
		accept string
		agent  string
		want   Format
	}{
		{"txt suffix", "/api/v1/server/healthz.txt", "application/json", "", FormatText},
		{"json accept", "/api/v1/server/healthz", "application/json", "Mozilla/5.0", FormatJSON},
		{"text accept", "/api/v1/server/healthz", "text/plain", "Mozilla/5.0", FormatText},
		{"curl", "/api/v1/server/healthz", "*/*", "curl/8.5.0", FormatText},
		{"browser default", "/api/v1/server/healthz", "text/html", "Mozilla/5.0", FormatJSON},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRequest(http.MethodGet, tc.target, tc.accept)
			if tc.agent != "" {
				r.Header.Set("User-Agent", tc.agent)
			}
			if got := Negotiate(r); got != tc.want {
				t.Fatalf("Negotiate = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNegotiateFrontendPrefersHTML(t *testing.T) {
	r := newRequest(http.MethodGet, "/server/healthz", "text/html")
	r.Header.Set("User-Agent", "Mozilla/5.0")
	if got := Negotiate(r); got != FormatHTML {
		t.Fatalf("Negotiate = %q, want html", got)
	}

	cli := newRequest(http.MethodGet, "/server/healthz", "")
	cli.Header.Set("User-Agent", CLIUserAgentPrefix+"1.0.0")
	if got := Negotiate(cli); got != FormatJSON {
		t.Fatalf("our CLI Negotiate = %q, want json", got)
	}

	lynx := newRequest(http.MethodGet, "/server/healthz", "")
	lynx.Header.Set("User-Agent", "Lynx/2.9.0")
	if got := Negotiate(lynx); got != FormatHTML {
		t.Fatalf("text browser Negotiate = %q, want html", got)
	}

	tool := newRequest(http.MethodGet, "/server/healthz", "")
	if got := Negotiate(tool); got != FormatText {
		t.Fatalf("empty user agent Negotiate = %q, want text", got)
	}
}

func TestWriteSuccessEnvelopeAndTrailingNewline(t *testing.T) {
	w := httptest.NewRecorder()
	WriteSuccess(w, newRequest(http.MethodPost, "/api/v1/things", "application/json"), http.StatusCreated, map[string]any{"id": "abc"})

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.HasSuffix(body, "}\n") || strings.HasSuffix(body, "\n\n") {
		t.Fatalf("body must end with exactly one newline: %q", body)
	}
	if !strings.Contains(body, "\n  \"ok\": true") {
		t.Fatalf("body is not indented with two spaces: %q", body)
	}
	var decoded Success
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !decoded.OK {
		t.Fatal("ok must be true")
	}
}

func TestWriteItemIsBare(t *testing.T) {
	w := httptest.NewRecorder()
	WriteItem(w, newRequest(http.MethodGet, "/api/v1/things/1", "application/json"), http.StatusOK, map[string]any{"id": "1"})

	var decoded map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, wrapped := decoded["ok"]; wrapped {
		t.Fatal("a single item must not be wrapped in the ok envelope")
	}
}

func TestPaginationDefaultsAndClamp(t *testing.T) {
	page, limit := Paginate(newRequest(http.MethodGet, "/api/v1/things", ""))
	if page != 1 || limit != DefaultPageSize {
		t.Fatalf("defaults = %d/%d, want 1/%d", page, limit, DefaultPageSize)
	}

	_, limit = Paginate(newRequest(http.MethodGet, "/api/v1/things?limit=99999", ""))
	if limit != MaxPageSize {
		t.Fatalf("limit = %d, want clamp to %d", limit, MaxPageSize)
	}

	p := NewPagination(2, 10, 25)
	if p.Pages != 3 {
		t.Fatalf("pages = %d, want 3", p.Pages)
	}
}

func TestWritePageShape(t *testing.T) {
	w := httptest.NewRecorder()
	WritePage(w, newRequest(http.MethodGet, "/api/v1/things?page=2&limit=10", "application/json"), []string{"a"}, 25)

	var decoded PageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Pagination.Page != 2 || decoded.Pagination.Limit != 10 || decoded.Pagination.Total != 25 || decoded.Pagination.Pages != 3 {
		t.Fatalf("pagination = %+v", decoded.Pagination)
	}
}

func TestWriteErrorEnvelopeAndSanitisation(t *testing.T) {
	err := apperr.New(apperr.CodeValidation, http.StatusBadRequest, "the request failed validation").
		WithDetails(map[string]any{"field": "name", "password": "hunter2", "config_path": "/etc/cashp"}).
		WithCause(errors.New("dsn=postgres://user:pass@127.0.0.1/db"))

	w := httptest.NewRecorder()
	WriteError(w, newRequest(http.MethodPost, "/api/v1/things", "application/json"), err)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	for _, leak := range []string{"hunter2", "/etc/cashp", "postgres://", "127.0.0.1"} {
		if strings.Contains(body, leak) {
			t.Fatalf("response leaked %q: %s", leak, body)
		}
	}
	var failure Failure
	if err := json.Unmarshal([]byte(body), &failure); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if failure.OK || failure.Error != apperr.CodeValidation {
		t.Fatalf("failure = %+v", failure)
	}
	if failure.Details["field"] != "name" {
		t.Fatalf("non-sensitive detail was dropped: %+v", failure.Details)
	}
}

func TestWriteErrorRendersEveryFormat(t *testing.T) {
	for _, tc := range []struct {
		accept string
		want   string
	}{
		{"application/json", "application/json"},
		{"text/plain", "text/plain"},
		{"text/html", "text/html"},
	} {
		w := httptest.NewRecorder()
		r := newRequest(http.MethodGet, "/things", tc.accept)
		r.Header.Set("User-Agent", "Mozilla/5.0")
		WriteError(w, r, apperr.New(apperr.CodeNotFound, http.StatusNotFound, "not found"))
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, tc.want) {
			t.Fatalf("Content-Type = %q, want %q", ct, tc.want)
		}
		if w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("responses must be marked nosniff")
		}
	}
}

func TestClassifyVersion(t *testing.T) {
	cases := map[string]VersionKind{
		"1.0.0":               KindStable,
		"1.2.3-rc1":           KindStable,
		"20250101120000-beta": KindBeta,
		"a1b2c3d":             KindDaily,
		DevVersion:            KindDev,
	}
	for version, want := range cases {
		if got := ClassifyVersion(version); got != want {
			t.Fatalf("ClassifyVersion(%q) = %q, want %q", version, got, want)
		}
	}
}

func TestBuildNormalizeStripsVPrefix(t *testing.T) {
	b := Build{Version: "v1.2.3"}.Normalize()
	if b.Version != "1.2.3" {
		t.Fatalf("Version = %q, want 1.2.3", b.Version)
	}
	if (Build{}).Normalize().Version != DevVersion {
		t.Fatal("an empty version must fall back to dev")
	}
}

func TestVersionHandlerFormats(t *testing.T) {
	h := NewVersionHandler("cashp", Build{Version: "1.0.0", CommitID: "abcdef1", BuildEpoch: "1700000000"}, "go1.27.0")
	for _, accept := range []string{"application/json", "text/plain", "text/html"} {
		w := httptest.NewRecorder()
		r := newRequest(http.MethodGet, "/server/version", accept)
		r.Header.Set("User-Agent", "Mozilla/5.0")
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d", accept, w.Code)
		}
		if !strings.Contains(w.Body.String(), "1.0.0") {
			t.Fatalf("%s body missing version: %s", accept, w.Body.String())
		}
	}
}

// newTestHealth builds a health handler whose single probe always fails, so
// both the degraded and the unhealthy paths can be exercised.
func newTestHealth(t *testing.T, failCritical bool) *Health {
	t.Helper()
	return NewHealth(HealthOptions{
		Project: ProjectInfo{Name: "cashp", Tagline: "hosting control panel"},
		Build:   Build{Version: "1.0.0", CommitID: "abcdef1"},
		Mode:    "production",
		Started: time.Now().Add(-90 * time.Minute),
		Checks: []Check{{
			Name:     "database",
			Critical: failCritical,
			Probe: func(context.Context) error {
				return errors.New("connect tcp 10.0.0.5:5432: refused")
			},
		}},
	})
}

func TestHealthJSONIsBareAndOrdered(t *testing.T) {
	h := NewHealth(HealthOptions{Project: ProjectInfo{Name: "cashp"}, Build: Build{Version: "1.0.0"}, Mode: "production"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, newRequest(http.MethodGet, "/server/healthz", "application/json"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("health must not be cached")
	}
	body := w.Body.String()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, wrapped := decoded["ok"]; wrapped {
		t.Fatal("the health document must be a bare object")
	}
	if decoded["status"] != StatusHealthy {
		t.Fatalf("status = %v", decoded["status"])
	}
	if strings.Index(body, "\"project\"") > strings.Index(body, "\"status\"") {
		t.Fatal("project must precede status in the canonical order")
	}
}

func TestHealthStatusAndProbeErrorIsNeverExposed(t *testing.T) {
	critical := newTestHealth(t, true)
	w := httptest.NewRecorder()
	critical.ServeHTTP(w, newRequest(http.MethodGet, "/server/healthz", "application/json"))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("critical failure status = %d, want 503", w.Code)
	}
	if strings.Contains(w.Body.String(), "10.0.0.5") {
		t.Fatalf("the probe error leaked into the response: %s", w.Body.String())
	}

	degraded := newTestHealth(t, false)
	w = httptest.NewRecorder()
	degraded.ServeHTTP(w, newRequest(http.MethodGet, "/server/healthz", "application/json"))
	if w.Code != http.StatusOK {
		t.Fatalf("degraded status = %d, want 200", w.Code)
	}
	var decoded map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["status"] != StatusDegraded {
		t.Fatalf("status = %v, want degraded", decoded["status"])
	}
}

func TestHealthRendersEveryFormat(t *testing.T) {
	h := newTestHealth(t, false)
	for _, accept := range []string{"application/json", "text/plain", "text/html"} {
		w := httptest.NewRecorder()
		r := newRequest(http.MethodGet, "/server/healthz", accept)
		r.Header.Set("User-Agent", "Mozilla/5.0")
		h.ServeHTTP(w, r)
		body := w.Body.String()
		if !strings.HasSuffix(body, "\n") || strings.HasSuffix(body, "\n\n") {
			t.Fatalf("%s body must end with exactly one newline", accept)
		}
		if !strings.Contains(body, "degraded") {
			t.Fatalf("%s body missing status: %s", accept, body)
		}
	}
}

func TestReadyMirrorsHealth(t *testing.T) {
	ready := NewReady(newTestHealth(t, true))
	w := httptest.NewRecorder()
	ready.ServeHTTP(w, newRequest(http.MethodGet, "/server/readyz", "application/json"))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	var decoded map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["ready"] != false {
		t.Fatalf("ready = %v, want false", decoded["ready"])
	}
}

func TestFormatUptime(t *testing.T) {
	if got := FormatUptime(53*time.Hour + 30*time.Minute); got != "2d 5h 30m" {
		t.Fatalf("FormatUptime = %q, want 2d 5h 30m", got)
	}
}

func TestAutodiscoverBuildsPathsFromVersion(t *testing.T) {
	Configure(Config{Version: "v3"})
	defer Configure(Config{})

	h := NewAutodiscover(AutodiscoverOptions{Project: "cashp", BaseURL: "https://panel.example.com"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, newRequest(http.MethodGet, "/api/autodiscover", "application/json"))

	body := w.Body.String()
	if !strings.Contains(body, "/api/v3/server/healthz") {
		t.Fatalf("autodiscover did not use the configured version: %s", body)
	}
	if strings.Contains(body, "/api/v1/") {
		t.Fatalf("autodiscover hardcoded v1: %s", body)
	}
}

func TestRouteOperationIDAndSplitPattern(t *testing.T) {
	method, path := SplitPattern("GET /api/v1/things")
	if method != http.MethodGet || path != "/api/v1/things" {
		t.Fatalf("SplitPattern = %q %q", method, path)
	}
	rt := Route{Method: http.MethodGet, Pattern: "/server/healthz"}
	rt.Name = DeriveName(rt.Method, rt.Pattern)
	if rt.OperationID() == "" {
		t.Fatal("OperationID must not be empty")
	}
}

func TestTextOfRendersNestedDocuments(t *testing.T) {
	text := TextOf(map[string]any{"status": "healthy", "checks": map[string]any{"database": "ok"}})
	if !strings.Contains(text, "status") || !strings.Contains(text, "healthy") {
		t.Fatalf("TextOf = %q", text)
	}
}
