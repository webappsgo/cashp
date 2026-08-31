package middleware

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/webappsgo/cashp/src/api"
	apperr "github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/security"
)

// okHandler answers 200 with a short body.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
})

// mustCIDR parses a network for the trusted-proxy list.
func mustCIDR(t *testing.T, cidr string) net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("parse %s: %v", cidr, err)
	}
	return *n
}

func TestRequestIDReusesAWellFormedInboundValue(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = api.RequestIDFrom(r.Context())
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(RequestIDHeader, "trace_ID-42")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if seen != "trace_ID-42" {
		t.Fatalf("context id = %q", seen)
	}
	if got := w.Header().Get(RequestIDHeader); got != "trace_ID-42" {
		t.Fatalf("response id = %q", got)
	}
}

func TestRequestIDReplacesAHostileInboundValue(t *testing.T) {
	h := RequestID(okHandler)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(RequestIDHeader, "line\nbreak level=ERROR")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	got := w.Header().Get(RequestIDHeader)
	if strings.Contains(got, "\n") || strings.Contains(got, " ") || got == "" {
		t.Fatalf("a forged identifier reached the response: %q", got)
	}
}

func TestRealIPTrustsAForwardedHeaderOnlyFromATrustedPeer(t *testing.T) {
	opts := RealIPOptions{TrustedProxies: []net.IPNet{mustCIDR(t, "10.0.0.0/8")}}

	var peer, client string
	var trusted bool
	var forwarded string
	h := RealIP(opts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer = PeerAddrFrom(r.Context())
		client = ClientIPFrom(r.Context())
		trusted = FromTrustedProxy(r.Context())
		forwarded = r.Header.Get("X-Forwarded-For")
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.1.2.3:5555"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	h.ServeHTTP(httptest.NewRecorder(), r)

	if !trusted {
		t.Fatal("10.1.2.3 is inside the trusted network")
	}
	if client != "203.0.113.9" {
		t.Fatalf("client ip = %q, want the forwarded address", client)
	}
	if peer != "10.1.2.3:5555" {
		t.Fatalf("peer = %q, the original TCP peer must survive the rewrite", peer)
	}
	if forwarded != "203.0.113.9" {
		t.Fatalf("a trusted proxy's header must be preserved, got %q", forwarded)
	}
}

func TestRealIPStripsForwardedHeadersFromAnUntrustedPeer(t *testing.T) {
	opts := RealIPOptions{TrustedProxies: []net.IPNet{mustCIDR(t, "10.0.0.0/8")}}

	var peer, client string
	var trusted bool
	var leftovers []string
	h := RealIP(opts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer = PeerAddrFrom(r.Context())
		client = ClientIPFrom(r.Context())
		trusted = FromTrustedProxy(r.Context())
		for _, name := range forwardedHeaders {
			if r.Header.Get(name) != "" {
				leftovers = append(leftovers, name)
			}
		}
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "198.51.100.7:44444"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	r.Header.Set("X-Real-IP", "127.0.0.1")
	r.Header.Set("X-Forwarded-Proto", "https")
	h.ServeHTTP(httptest.NewRecorder(), r)

	if trusted {
		t.Fatal("an unlisted peer must never be treated as a proxy")
	}
	if client != "198.51.100.7" {
		t.Fatalf("client ip = %q, want the TCP peer", client)
	}
	if peer != "198.51.100.7:44444" {
		t.Fatalf("peer = %q", peer)
	}
	if len(leftovers) > 0 {
		t.Fatalf("forged proxy headers reached the handler: %v", leftovers)
	}
}

func TestRealIPResolvesTheBasePathAndOverlayFlag(t *testing.T) {
	opts := RealIPOptions{
		TrustedProxies: []net.IPNet{mustCIDR(t, "10.0.0.0/8")},
		BasePath:       "/panel/",
	}

	var basePath string
	var overlay bool
	h := RealIP(opts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		basePath = BasePathFrom(r.Context())
		overlay = IsOverlayRequest(r.Context())
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.9.9.9:1111"
	r.Host = "abcdefghij.b32.i2p"
	r.Header.Set("X-Forwarded-Prefix", "/mounted/")
	h.ServeHTTP(httptest.NewRecorder(), r)

	if basePath != "/mounted" {
		t.Fatalf("base path = %q", basePath)
	}
	if !overlay {
		t.Fatal("a .b32.i2p host is an overlay request")
	}
}

func TestIsOverlayHost(t *testing.T) {
	cases := map[string]bool{
		"example.onion":            true,
		"example.onion:8080":       true,
		"abcdefghij.b32.i2p":       true,
		"panel.example.com":        false,
		"onion.example.com":        false,
		"notanonion.example.onion.": true,
	}
	for host, want := range cases {
		if got := IsOverlayHost(host); got != want {
			t.Fatalf("IsOverlayHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestSecurityHeadersOnAClearnetRequest(t *testing.T) {
	h := SecurityHeaders(HeaderOptions{TLS: true, HSTS: true, OnionAddress: "cashp.onion"})(okHandler)

	r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	r.Host = "panel.example.com"
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if got := w.Header().Get("Strict-Transport-Security"); !strings.HasPrefix(got, "max-age=") {
		t.Fatalf("HSTS = %q", got)
	}
	if got := w.Header().Get("Onion-Location"); got != "http://cashp.onion/dashboard" {
		t.Fatalf("Onion-Location = %q", got)
	}
	if got := w.Header().Get("Content-Security-Policy"); got != DefaultCSP {
		t.Fatalf("CSP = %q", got)
	}
}

func TestSecurityHeadersOnAnOverlayRequest(t *testing.T) {
	h := SecurityHeaders(HeaderOptions{TLS: true, HSTS: true, OnionAddress: "cashp.onion"})(okHandler)

	for _, host := range []string{"cashp.onion", "abcdefghij.b32.i2p"} {
		r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
		r.Host = host
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if got := w.Header().Get("Strict-Transport-Security"); got != "" {
			t.Fatalf("%s received HSTS %q", host, got)
		}
		if got := w.Header().Get("Onion-Location"); got != "" {
			t.Fatalf("%s received Onion-Location %q", host, got)
		}
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("%s lost the shared headers", host)
		}
	}
}

func TestOnionLocationIsNotAdvertisedOnAPIResponses(t *testing.T) {
	h := SecurityHeaders(HeaderOptions{OnionAddress: "cashp.onion"})(okHandler)

	r := httptest.NewRequest(http.MethodGet, api.APIPath("server", "healthz"), nil)
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if got := w.Header().Get("Onion-Location"); got != "" {
		t.Fatalf("Onion-Location = %q on an API response", got)
	}
}

func TestCSRFAllowsSafeMethodsAndSameOriginWrites(t *testing.T) {
	h := CSRF(CSRFOptions{})(okHandler)

	get := httptest.NewRequest(http.MethodGet, "/", nil)
	get.Host = "panel.example.com"
	get.Header.Set("Origin", "https://evil.example.net")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, get)
	if w.Code != http.StatusOK {
		t.Fatalf("a GET is never a CSRF risk, got %d", w.Code)
	}

	post := httptest.NewRequest(http.MethodPost, "/", nil)
	post.Host = "panel.example.com"
	post.Header.Set("Origin", "https://panel.example.com")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, post)
	if w.Code != http.StatusOK {
		t.Fatalf("a same-origin write must pass, got %d", w.Code)
	}
}

func TestCSRFRejectsAForeignOriginWithoutDisclosingTheCheck(t *testing.T) {
	h := CSRF(CSRFOptions{})(okHandler)

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Host = "panel.example.com"
	r.Header.Set("Origin", "https://evil.example.net")
	// AI.md PART 14 "Content Negotiation Priority": an empty User-Agent is a
	// non-interactive HTTP tool and gets plain text unless Accept:
	// application/json wins first — set it explicitly since this test
	// exercises the JSON envelope shape.
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	var failure api.Failure
	if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if failure.OK || failure.Error != apperr.CodeForbidden {
		t.Fatalf("failure = %+v", failure)
	}
	if failure.Details != nil {
		t.Fatalf("the failing check is debug-only detail: %+v", failure.Details)
	}
}

func TestCSRFRequiresABoundTokenForACookieSession(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	opts := CSRFOptions{
		Secret:    secret,
		SessionID: func(*http.Request) string { return "session-1" },
	}
	h := CSRF(opts)(okHandler)

	missing := httptest.NewRequest(http.MethodPost, "/", nil)
	missing.Host = "panel.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, missing)
	if w.Code != http.StatusForbidden {
		t.Fatalf("a cookie session without a token must be rejected, got %d", w.Code)
	}

	wrong := httptest.NewRequest(http.MethodPost, "/", nil)
	wrong.Host = "panel.example.com"
	wrong.Header.Set(CSRFHeader, security.NewCSRFToken(secret, "another-session"))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, wrong)
	if w.Code != http.StatusForbidden {
		t.Fatalf("a token bound to another session must be rejected, got %d", w.Code)
	}

	good := httptest.NewRequest(http.MethodPost, "/", nil)
	good.Host = "panel.example.com"
	good.Header.Set(CSRFHeader, security.NewCSRFToken(secret, "session-1"))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, good)
	if w.Code != http.StatusOK {
		t.Fatalf("a valid bound token must pass, got %d", w.Code)
	}
}

func TestCSRFSkipsTheTokenForBearerAuth(t *testing.T) {
	opts := CSRFOptions{
		Secret:    []byte("0123456789abcdef0123456789abcdef"),
		SessionID: func(*http.Request) string { return "session-1" },
	}
	h := CSRF(opts)(okHandler)

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Host = "panel.example.com"
	r.Header.Set("Authorization", "Bearer abc123")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("a bearer-authenticated write cannot be forged by a third-party page, got %d", w.Code)
	}
}

func TestRateLimitRejectsWithGenericDetail(t *testing.T) {
	limiter := NewLimiter()
	opts := RateLimitOptions{Read: Rule{Limit: 2, Window: time.Minute}}
	h := RateLimit(limiter, opts)(okHandler)

	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, api.APIPath("things"), nil)
		// AI.md PART 14 "Content Negotiation Priority": an empty
		// User-Agent is a non-interactive HTTP tool and gets plain text
		// unless Accept: application/json wins first — set it explicitly
		// since this test exercises the JSON envelope shape.
		r.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	for i := 0; i < 2; i++ {
		if got := call().Code; got != http.StatusOK {
			t.Fatalf("request %d status = %d", i+1, got)
		}
	}
	w := call()
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("a 429 must carry Retry-After")
	}
	var failure api.Failure
	if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if failure.Error != apperr.CodeRateLimited {
		t.Fatalf("error = %q", failure.Error)
	}
	if failure.Details != nil {
		t.Fatalf("the threshold must stay private: %+v", failure.Details)
	}
}

func TestRateLimitSeparatesWriteAndLoginBuckets(t *testing.T) {
	limiter := NewLimiter()
	opts := RateLimitOptions{
		Read:       Rule{Limit: 5, Window: time.Minute},
		Write:      Rule{Limit: 1, Window: time.Minute},
		Login:      Rule{Limit: 1, Window: 15 * time.Minute},
		LoginPaths: []string{"/auth/login"},
	}
	h := RateLimit(limiter, opts)(okHandler)

	send := func(method, path string) int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w.Code
	}

	if got := send(http.MethodPost, "/auth/login"); got != http.StatusOK {
		t.Fatalf("first login status = %d", got)
	}
	if got := send(http.MethodPost, "/auth/login"); got != http.StatusTooManyRequests {
		t.Fatalf("second login status = %d, want 429", got)
	}
	if got := send(http.MethodPost, api.APIPath("things")); got != http.StatusOK {
		t.Fatalf("the write bucket must be independent of the login bucket, got %d", got)
	}
	if got := send(http.MethodGet, api.APIPath("things")); got != http.StatusOK {
		t.Fatalf("the read bucket must be independent of the write bucket, got %d", got)
	}
}

func TestRateLimitHonoursDisabledAndExempt(t *testing.T) {
	limiter := NewLimiter()
	h := RateLimit(limiter, RateLimitOptions{Disabled: true, Read: Rule{Limit: 1, Window: time.Minute}})(okHandler)
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("a disabled limiter must never reject, got %d", w.Code)
		}
	}

	exempt := RateLimit(NewLimiter(), RateLimitOptions{
		Read:   Rule{Limit: 1, Window: time.Minute},
		Exempt: func(*http.Request) bool { return true },
	})(okHandler)
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		exempt.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("an exempt request must never be rejected, got %d", w.Code)
		}
	}
}

func TestRecoveryReturnsTheEnvelopeWithoutAStackTrace(t *testing.T) {
	h := Recovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("dsn postgres://cashp:hunter2@10.0.0.5:5432/cashp")
	}))

	r := httptest.NewRequest(http.MethodGet, api.APIPath("things"), nil)
	// AI.md PART 14 "Content Negotiation Priority": an empty User-Agent is
	// a non-interactive HTTP tool and gets plain text unless Accept:
	// application/json wins first — set it explicitly since this test
	// exercises the JSON envelope shape.
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	for _, leak := range []string{"hunter2", "postgres://", "10.0.0.5", "goroutine", "middleware_test.go", "runtime."} {
		if strings.Contains(body, leak) {
			t.Fatalf("the response leaked %q: %s", leak, body)
		}
	}
	var failure api.Failure
	if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if failure.Error != apperr.CodeInternal {
		t.Fatalf("error = %q", failure.Error)
	}
}

func TestRecoveryKeepsAnAlreadyWrittenResponse(t *testing.T) {
	h := Recovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("late failure")
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, a written response cannot be rewritten", w.Code)
	}
	if w.Body.String() != "partial" {
		t.Fatalf("body = %q", w.Body.String())
	}
}

func TestNegotiateStripsTheTxtSuffixBeforeRouting(t *testing.T) {
	var path string
	var format api.Format
	h := Negotiate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		format, _ = api.FormatFrom(r.Context())
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, api.APIPath("server", "healthz")+".txt", nil))
	if path != api.APIPath("server", "healthz") {
		t.Fatalf("path = %q, the suffix must be stripped before routing", path)
	}
	if format != api.FormatText {
		t.Fatalf("format = %q, want text", format)
	}
}

func TestNegotiateRecordsTheResolvedFormat(t *testing.T) {
	var format api.Format
	h := Negotiate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		format, _ = api.FormatFrom(r.Context())
	}))

	r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	r.Header.Set("Accept", "text/html")
	r.Header.Set("User-Agent", "Mozilla/5.0")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if format != api.FormatHTML {
		t.Fatalf("format = %q, want html", format)
	}
}

func TestCORSPreflightAndRejection(t *testing.T) {
	h := CORS(CORSOptions{AllowedOrigins: []string{"https://app.example.com"}, MaxAge: 600})(okHandler)

	pre := httptest.NewRequest(http.MethodOptions, api.APIPath("things"), nil)
	pre.Header.Set("Origin", "https://app.example.com")
	pre.Header.Set("Access-Control-Request-Method", http.MethodPost)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, pre)

	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("allow-origin = %q", got)
	}
	if got := w.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Fatalf("max-age = %q", got)
	}

	denied := httptest.NewRequest(http.MethodOptions, api.APIPath("things"), nil)
	denied.Header.Set("Origin", "https://evil.example.net")
	denied.Header.Set("Access-Control-Request-Method", http.MethodPost)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, denied)
	if w.Code != http.StatusForbidden {
		t.Fatalf("an unlisted origin preflight status = %d, want 403", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("an unlisted origin was granted access: %q", got)
	}
}

func TestCORSNeverCombinesWildcardWithCredentials(t *testing.T) {
	h := CORS(CORSOptions{AllowedOrigins: []string{"*"}, AllowCredentials: true})(okHandler)

	r := httptest.NewRequest(http.MethodGet, api.APIPath("things"), nil)
	r.Header.Set("Origin", "https://app.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("a credentialed wildcard must not grant access, got %q", got)
	}
}

func TestLoggingPassesTheResponseThrough(t *testing.T) {
	h := RequestID(Logging(okHandler))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusOK || w.Body.String() != "ok\n" {
		t.Fatalf("status = %d body = %q", w.Code, w.Body.String())
	}
}

func TestLevelForStatus(t *testing.T) {
	if levelFor(http.StatusInternalServerError) <= levelFor(http.StatusBadRequest) {
		t.Fatal("a 5xx must log louder than a 4xx")
	}
	if levelFor(http.StatusBadRequest) <= levelFor(http.StatusOK) {
		t.Fatal("a 4xx must log louder than a 2xx")
	}
}
