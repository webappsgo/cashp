package guard

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// okHandler records that it was reached and answers 200.
func okHandler(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestBodyLimitRefusesAnOversizedDeclaredLength(t *testing.T) {
	reached := false
	handler := BodyLimit(64, nil)(okHandler(&reached))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", strings.NewReader(strings.Repeat("a", 4096)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if reached {
		t.Fatal("BodyLimit passed an oversized body to the handler")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("BodyLimit answered %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "ceiling") {
		t.Fatalf("BodyLimit leaked the log-only detail: %s", rec.Body.String())
	}
}

func TestBodyLimitTruncatesALyingContentLength(t *testing.T) {
	var readErr error
	handler := BodyLimit(64, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}))

	// A chunked request declares no length, so the ceiling has to be enforced
	// on the read itself rather than trusted from the header.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", strings.NewReader(strings.Repeat("a", 4096)))
	req.ContentLength = -1
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if readErr == nil {
		t.Fatal("BodyLimit let the handler read past the ceiling")
	}
}

func TestBodyLimitHasNoUnlimitedSetting(t *testing.T) {
	var readErr error
	handler := BodyLimit(0, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("a", int(DefaultMaxBodyBytes)+1)))
	req.ContentLength = -1
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if readErr == nil {
		t.Fatal("a zero limit was treated as unlimited")
	}
}

func TestRequireContentTypeRefusesUnnamedAndForeignMediaTypes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		wantPass    bool
	}{
		{"absent", "", false},
		{"form post", "application/x-www-form-urlencoded", false},
		{"multipart", "multipart/form-data; boundary=x", false},
		{"html", "text/html", false},
		{"unparseable", "application/json; charset", false},
		{"json", "application/json", true},
		{"json with charset", "application/json; charset=utf-8", true},
		{"json uppercase", "APPLICATION/JSON", true},
	} {
		reached := false
		handler := RequireContentType(nil, "application/json")(okHandler(&reached))
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", strings.NewReader(`{"name":"web"}`))
		if tc.contentType != "" {
			req.Header.Set("Content-Type", tc.contentType)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if reached != tc.wantPass {
			t.Fatalf("%s: reached=%v want %v (status %d)", tc.name, reached, tc.wantPass, rec.Code)
		}
		if !tc.wantPass && rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("%s: answered %d", tc.name, rec.Code)
		}
	}
}

func TestRequireContentTypeWithNoAllowlistAcceptsNothing(t *testing.T) {
	reached := false
	handler := RequireContentType(nil)(okHandler(&reached))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if reached {
		t.Fatal("an empty media-type allowlist accepted a request")
	}

	// A bodyless request has no media type to check and must pass.
	reached = false
	bodyless := httptest.NewRequest(http.MethodGet, "/", nil)
	RequireContentType(nil, "application/json")(okHandler(&reached)).ServeHTTP(httptest.NewRecorder(), bodyless)
	if !reached {
		t.Fatal("a bodyless request was refused for having no content type")
	}
}

func TestAllowedHostsFailsClosedAndNormalizes(t *testing.T) {
	reached := false
	empty := AllowedHosts(nil, nil)(okHandler(&reached))
	empty.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://panel.example.com/", nil))
	if reached {
		t.Fatal("an empty host allowlist accepted a request")
	}

	handler := AllowedHosts([]string{"panel.example.com", " ", ""}, nil)
	for _, tc := range []struct {
		host     string
		wantPass bool
	}{
		{"panel.example.com", true},
		{"PANEL.Example.COM:8443", true},
		{"panel.example.com.", true},
		{"attacker.example.net", false},
		{"panel.example.com.attacker.example.net", false},
		{"", false},
	} {
		reached = false
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = tc.host
		rec := httptest.NewRecorder()
		handler(okHandler(&reached)).ServeHTTP(rec, req)
		if reached != tc.wantPass {
			t.Fatalf("host %q: reached=%v want %v", tc.host, reached, tc.wantPass)
		}
		if !tc.wantPass && rec.Code != http.StatusBadRequest {
			t.Fatalf("host %q answered %d", tc.host, rec.Code)
		}
	}

	// The wildcard is the documented development escape hatch and must be
	// the only thing that opens the check.
	reached = false
	wild := AllowedHosts([]string{"*"}, nil)(okHandler(&reached))
	wildReq := httptest.NewRequest(http.MethodGet, "/", nil)
	wildReq.Host = "anything.example.net"
	wild.ServeHTTP(httptest.NewRecorder(), wildReq)
	if !reached {
		t.Fatal("the wildcard entry did not disable the host check")
	}
}

func TestRequireOriginRefusesAForeignStateChange(t *testing.T) {
	handler := RequireOrigin([]string{"https://panel.example.com"}, nil)

	cases := []struct {
		name     string
		method   string
		header   string
		value    string
		wantPass bool
	}{
		{"safe method with foreign origin", http.MethodGet, "Origin", "https://attacker.example.net", true},
		{"no origin at all", http.MethodPost, "", "", true},
		{"matching origin", http.MethodPost, "Origin", "https://panel.example.com", true},
		{"matching referer with path", http.MethodPost, "Referer", "https://panel.example.com/sites/1", true},
		{"foreign origin", http.MethodPost, "Origin", "https://attacker.example.net", false},
		{"scheme downgrade", http.MethodPost, "Origin", "http://panel.example.com", false},
		{"suffix confusion", http.MethodPost, "Origin", "https://panel.example.com.attacker.example.net", false},
		{"path smuggling", http.MethodPost, "Referer", "https://attacker.example.net/https://panel.example.com", false},
		{"null origin", http.MethodPost, "Origin", "null", false},
		{"delete with foreign origin", http.MethodDelete, "Origin", "https://attacker.example.net", false},
	}
	for _, tc := range cases {
		reached := false
		req := httptest.NewRequest(tc.method, "/api/v1/sites", nil)
		if tc.header != "" {
			req.Header.Set(tc.header, tc.value)
		}
		rec := httptest.NewRecorder()
		handler(okHandler(&reached)).ServeHTTP(rec, req)
		if reached != tc.wantPass {
			t.Fatalf("%s: reached=%v want %v", tc.name, reached, tc.wantPass)
		}
		if !tc.wantPass && rec.Code != http.StatusForbidden {
			t.Fatalf("%s: answered %d", tc.name, rec.Code)
		}
	}
}

func TestChainRunsTheFirstNamedMiddlewareOutermost(t *testing.T) {
	order := []string{}
	mark := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	reached := false
	Chain(mark("outer"), nil, mark("inner"))(okHandler(&reached)).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if !reached {
		t.Fatal("Chain did not reach the handler")
	}
	if len(order) != 2 || order[0] != "outer" || order[1] != "inner" {
		t.Fatalf("Chain ran middlewares in the order %v", order)
	}
}
