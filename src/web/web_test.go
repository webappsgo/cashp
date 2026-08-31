package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestRenderer builds a renderer with deterministic options for tests.
func newTestRenderer(t *testing.T) *Renderer {
	t.Helper()
	r, err := New(Options{
		BaseURL:   "https://panel.example.com/",
		AppName:   "CasHp",
		Version:   "1.2.3",
		BuildDate: "2026-01-02",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

// pageData returns page data suitable for each embedded page template.
func pageData(name string) any {
	switch name {
	case "contact":
		return contactData{
			Values:   map[string]string{"name": "", "email": "", "subject": "", "message": ""},
			Errors:   map[string]string{},
			Question: "What is 3 plus 7?",
			Token:    "10.0.testtoken",
		}
	case "privacy":
		return privacyData{Consent: ConsentState{Essential: true, Preferences: true}}
	case "help":
		return helpData{
			TorAddress: "cashpexampleaddressonionv3serviceidentifier0000000000000.onion",
			I2PAddress: "cashpexample.b32.i2p",
		}
	case "error":
		return errorPayload{
			Status:  http.StatusNotFound,
			Code:    "not_found",
			Message: "That page does not exist.",
			Title:   errorTitle(http.StatusNotFound),
			Hint:    errorHint(http.StatusNotFound),
		}
	default:
		return nil
	}
}

func TestNewDefaults(t *testing.T) {
	r, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	opts := r.Options()
	if opts.AppName == "" {
		t.Error("AppName should default to a non-empty value")
	}
	if opts.DefaultTheme != ThemeDark {
		t.Errorf("DefaultTheme = %q, want %q", opts.DefaultTheme, ThemeDark)
	}
	if opts.Version != "dev" {
		t.Errorf("Version = %q, want dev", opts.Version)
	}
}

func TestNewRejectsInvalidTheme(t *testing.T) {
	if _, err := New(Options{DefaultTheme: "sepia"}); err == nil {
		t.Fatal("New accepted an invalid default theme")
	}
}

func TestNewTrimsBaseURL(t *testing.T) {
	r := newTestRenderer(t)
	if got := r.Options().BaseURL; got != "https://panel.example.com" {
		t.Errorf("BaseURL = %q, want the trailing slash removed", got)
	}
}

func TestRenderEveryPage(t *testing.T) {
	r := newTestRenderer(t)
	names := r.PageNames()
	if len(names) == 0 {
		t.Fatal("no pages parsed")
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/server/about", nil)
			if err := r.Render(rec, req, name, pageData(name)); err != nil {
				t.Fatalf("Render(%s): %v", name, err)
			}
			body := rec.Body.String()
			if !strings.HasPrefix(body, "<!doctype html>") {
				t.Errorf("page %s does not start with a doctype", name)
			}
			for _, want := range []string{"<html lang=\"en\"", "id=\"main\"", "</html>"} {
				if !strings.Contains(body, want) {
					t.Errorf("page %s is missing %q", name, want)
				}
			}
			if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
				t.Errorf("Content-Type = %q", got)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q", got)
			}
		})
	}
}

func TestRenderUnknownPage(t *testing.T) {
	r := newTestRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := r.Render(rec, req, "does-not-exist", nil); err == nil {
		t.Fatal("Render accepted an unknown page name")
	}
}

func TestRenderErrorHTML(t *testing.T) {
	r := newTestRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Firefox/128.0")

	r.RenderError(rec, req, http.StatusNotFound, "", "")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Page not found") {
		t.Error("themed error page is missing its title")
	}
	if !strings.Contains(body, "not_found") {
		t.Error("themed error page is missing the reference code")
	}
	if !strings.Contains(body, "class=\"nav-list\"") {
		t.Error("themed error page must include navigation so the visitor can leave")
	}
}

func TestRenderErrorJSON(t *testing.T) {
	r := newTestRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil)
	req.Header.Set("Accept", "application/json")

	r.RenderError(rec, req, http.StatusForbidden, "", "Not allowed.")

	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"ok":false`) || !strings.Contains(body, `"error":"forbidden"`) {
		t.Errorf("unexpected JSON error body: %s", body)
	}
	if !strings.HasSuffix(body, "\n") {
		t.Error("JSON error body must end with a newline")
	}
}

func TestRenderErrorText(t *testing.T) {
	r := newTestRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/server/about", nil)
	req.Header.Set("User-Agent", "curl/8.6.0")

	r.RenderError(rec, req, http.StatusServiceUnavailable, "", "Starting up.")

	if got := rec.Body.String(); got != "ERROR: SERVICE_UNAVAILABLE: Starting up.\n" {
		t.Errorf("text error body = %q", got)
	}
}

func TestRenderErrorNormalizesStatus(t *testing.T) {
	r := newTestRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("User-Agent", "curl/8.6.0")

	r.RenderError(rec, req, 200, "", "")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 for an out-of-range error status", rec.Code)
	}
}

func TestPublicNavHasNoAdminLink(t *testing.T) {
	for _, item := range publicNav() {
		if strings.Contains(item.Href, "admin") {
			t.Errorf("public navigation exposes an admin route: %s", item.Href)
		}
	}
}

func TestHandlersRouteTable(t *testing.T) {
	r := newTestRenderer(t)
	handlers := r.Handlers()

	for _, route := range []string{
		"/server", "/server/about", "/server/privacy", "/server/contact",
		"/server/help", "/server/terms", "/server/theme", "/server/consent",
		"/server/ccpa", "/offline.html", "/manifest.json", "/sw.js", "/static/",
	} {
		if handlers[route] == nil {
			t.Errorf("route %s is not exposed by Handlers()", route)
		}
	}

	// The router and the health endpoint belong to the server package; the
	// renderer must not claim them.
	for _, route := range []string{"/", "/server/healthz"} {
		if _, ok := handlers[route]; ok {
			t.Errorf("Handlers() must not claim %s", route)
		}
	}
}

func TestServerRootRedirects(t *testing.T) {
	r := newTestRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/server", nil)

	r.Handlers()["/server"].ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Errorf("status = %d, want 301", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/server/about" {
		t.Errorf("Location = %q, want /server/about", got)
	}
}

func TestAboutPageServesRealContent(t *testing.T) {
	r := newTestRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/server/about", nil)

	r.Handlers()["/server/about"].ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, banned := range []string{"Your app name", "Feature 1", "Lorem ipsum", "coming soon", "TODO"} {
		if strings.Contains(body, banned) {
			t.Errorf("about page contains placeholder copy: %q", banned)
		}
	}
	if !strings.Contains(body, "control panel") {
		t.Error("about page does not describe the product")
	}
}

func TestContactFormRejectsMissingCSRF(t *testing.T) {
	r := newTestRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/server/contact", strings.NewReader("name=Ada"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "curl/8.6.0")

	r.Handlers()["/server/contact"].ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 without a CSRF token", rec.Code)
	}
}

func TestContactGetIssuesCaptchaAndToken(t *testing.T) {
	r := newTestRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/server/contact", nil)

	r.Handlers()["/server/contact"].ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `name="captcha_token"`) {
		t.Error("contact form is missing the captcha token")
	}
	if !strings.Contains(body, `name="csrf_token"`) {
		t.Error("contact form is missing the CSRF token")
	}
	if !strings.Contains(body, `name="website"`) {
		t.Error("contact form is missing the honeypot field")
	}
	if !strings.Contains(body, "/server/security") {
		t.Error("contact page must link the security policy for vulnerability reports")
	}
	if strings.Contains(body, "/.well-known/security.txt") {
		t.Error("contact page must not link security.txt directly")
	}
}

func TestOptionsIsACopy(t *testing.T) {
	r := newTestRenderer(t)
	opts := r.Options()
	opts.AppName = "changed"
	if r.Options().AppName == "changed" {
		t.Error("Options() leaked a mutable reference to the renderer configuration")
	}
}

func TestFuncsIsACopy(t *testing.T) {
	r := newTestRenderer(t)
	funcs := r.Funcs()
	if len(funcs) == 0 {
		t.Fatal("Funcs() returned an empty map")
	}
	delete(funcs, "dict")
	if _, ok := r.Funcs()["dict"]; !ok {
		t.Error("Funcs() leaked the renderer's own function map")
	}
}

func TestStaticHandlerServesEmbeddedAssets(t *testing.T) {
	r := newTestRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/static/css/common.css", nil)

	r.StaticHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "--color-bg") {
		t.Error("common.css does not define the colour tokens")
	}
}

func TestStaticHandlerRejectsWrites(t *testing.T) {
	r := newTestRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/static/css/common.css", nil)

	r.StaticHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestUsageMeterLevels(t *testing.T) {
	cases := []struct {
		used  float64
		total float64
		want  string
		pct   int
	}{
		{used: 1, total: 10, want: "normal", pct: 10},
		{used: 8, total: 10, want: "warning", pct: 80},
		{used: 95, total: 100, want: "critical", pct: 95},
		{used: 5, total: 0, want: "normal", pct: 0},
	}
	for _, tc := range cases {
		meter := UsageMeter{Label: "Disk", Used: tc.used, Total: tc.total}
		if got := meter.Level(); got != tc.want {
			t.Errorf("Level(%v/%v) = %q, want %q", tc.used, tc.total, got, tc.want)
		}
		if got := meter.UsedPercent(); got != tc.pct {
			t.Errorf("UsedPercent(%v/%v) = %d, want %d", tc.used, tc.total, got, tc.pct)
		}
	}
}

func TestAssetAccessors(t *testing.T) {
	templates, err := TemplatesFS()
	if err != nil {
		t.Fatalf("TemplatesFS: %v", err)
	}
	if _, err := templates.Open("layout/public.tmpl"); err != nil {
		t.Errorf("public layout not reachable through TemplatesFS: %v", err)
	}

	static, err := StaticFS()
	if err != nil {
		t.Fatalf("StaticFS: %v", err)
	}
	if _, err := static.Open("manifest.json"); err != nil {
		t.Errorf("manifest not reachable through StaticFS: %v", err)
	}
}
