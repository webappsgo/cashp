package netinfo

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// testOptions installs settings for a test and restores the defaults.
func testOptions(t *testing.T, opts Options) {
	t.Helper()

	original := Settings()
	Configure(opts)
	ResetLearning()

	t.Cleanup(func() {
		Configure(original)
		ResetLearning()
	})
}

// proxyRequest builds a request that arrived from a trusted proxy.
func proxyRequest(headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://backend.internal/path", nil)
	r.RemoteAddr = "10.1.2.3:54321"
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	return r
}

// directRequest builds a request that arrived straight from the internet.
func directRequest(headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://app.example.com/path", nil)
	r.RemoteAddr = "203.0.113.7:44321"
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	return r
}

// TestGetURLVarsFromProxyHeaders checks that trusted proxy headers win.
func TestGetURLVarsFromProxyHeaders(t *testing.T) {
	testOptions(t, Options{ProjectName: "cashp", ListenPort: "64500"})

	r := proxyRequest(map[string]string{
		HeaderForwardedHost:  "app.example.com",
		HeaderForwardedProto: "https",
		HeaderForwardedPort:  "443",
	})

	proto, fqdn, port := GetURLVars(r)
	if proto != "https" {
		t.Errorf("proto = %q, want https", proto)
	}
	if fqdn != "app.example.com" {
		t.Errorf("fqdn = %q, want app.example.com", fqdn)
	}
	if port != "" {
		t.Errorf("port = %q, want an empty string because :443 is stripped", port)
	}
}

// TestProxyHeadersIgnoredFromUntrustedPeer checks the trusted_proxies gate:
// a random internet peer cannot forge the FQDN or protocol.
func TestProxyHeadersIgnoredFromUntrustedPeer(t *testing.T) {
	testOptions(t, Options{ProjectName: "cashp", ListenPort: "64500"})

	r := directRequest(map[string]string{
		HeaderForwardedHost:  "evil.example.net",
		HeaderForwardedProto: "https",
		HeaderForwardedPort:  "8443",
	})

	proto, fqdn, port := GetURLVars(r)
	if fqdn == "evil.example.net" {
		t.Error("an untrusted peer must not be able to set the fqdn")
	}
	if fqdn != "app.example.com" {
		t.Errorf("fqdn = %q, want the Host header value", fqdn)
	}
	if proto != "http" {
		t.Errorf("proto = %q, want http", proto)
	}
	if port != "64500" {
		t.Errorf("port = %q, want the listen port", port)
	}
}

// TestProtoResolutionOrder checks each protocol header in priority order.
func TestProtoResolutionOrder(t *testing.T) {
	testOptions(t, Options{ProjectName: "cashp"})

	cases := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"forwarded proto wins", map[string]string{HeaderForwardedProto: "https", HeaderForwardedSSL: "off"}, "https"},
		{"forwarded ssl on", map[string]string{HeaderForwardedSSL: "on"}, "https"},
		{"url scheme", map[string]string{HeaderURLScheme: "https"}, "https"},
		{"front end https", map[string]string{HeaderFrontEndHTTPS: "on"}, "https"},
		{"no headers", map[string]string{}, "http"},
		{"forwarded chain", map[string]string{HeaderForwardedProto: "https, http"}, "https"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proto, _, _ := GetURLVars(proxyRequest(tc.headers))
			if proto != tc.want {
				t.Fatalf("proto = %q, want %q", proto, tc.want)
			}
		})
	}
}

// TestFQDNHeaderFallbacks checks the X-Real-Host and X-Original-Host
// fallbacks and that a port on the header is stripped.
func TestFQDNHeaderFallbacks(t *testing.T) {
	testOptions(t, Options{ProjectName: "cashp"})

	if got := ProxyFQDN(proxyRequest(map[string]string{HeaderRealHost: "real.example.com"})); got != "real.example.com" {
		t.Errorf("X-Real-Host was not used, got %q", got)
	}
	if got := ProxyFQDN(proxyRequest(map[string]string{HeaderOriginalHost: "orig.example.com"})); got != "orig.example.com" {
		t.Errorf("X-Original-Host was not used, got %q", got)
	}

	headers := map[string]string{
		HeaderForwardedHost: "primary.example.com",
		HeaderRealHost:      "secondary.example.com",
	}
	if got := ProxyFQDN(proxyRequest(headers)); got != "primary.example.com" {
		t.Errorf("X-Forwarded-Host must win, got %q", got)
	}

	if got := ProxyFQDN(proxyRequest(map[string]string{HeaderForwardedHost: "app.example.com:8080"})); got != "app.example.com" {
		t.Errorf("the port must be stripped from the header, got %q", got)
	}
}

// TestPortResolution checks the port priority chain and the :80 and :443
// stripping rule.
func TestPortResolution(t *testing.T) {
	testOptions(t, Options{ProjectName: "cashp", ListenPort: "64500"})

	_, _, port := GetURLVars(proxyRequest(map[string]string{
		HeaderForwardedHost: "app.example.com:9000",
		HeaderForwardedPort: "8443",
	}))
	if port != "8443" {
		t.Errorf("X-Forwarded-Port must win, got %q", port)
	}

	r := httptest.NewRequest(http.MethodGet, "http://app.example.com:9000/", nil)
	r.RemoteAddr = "10.0.0.5:1234"
	_, _, port = GetURLVars(r)
	if port != "9000" {
		t.Errorf("the Host header port must be used, got %q", port)
	}

	for _, stripped := range []string{"80", "443"} {
		_, _, port = GetURLVars(proxyRequest(map[string]string{
			HeaderForwardedHost:  "app.example.com",
			HeaderForwardedProto: map[string]string{"80": "http", "443": "https"}[stripped],
			HeaderForwardedPort:  stripped,
		}))
		if port != "" {
			t.Errorf(":%s must never appear in a URL, got %q", stripped, port)
		}
	}
}

// TestStripDefaultPort checks the helper directly.
func TestStripDefaultPort(t *testing.T) {
	if StripDefaultPort("http", "80") != "" {
		t.Error(":80 must be stripped for http")
	}
	if StripDefaultPort("https", "443") != "" {
		t.Error(":443 must be stripped for https")
	}
	if StripDefaultPort("https", "80") != "80" {
		t.Error("a non-default port must be kept")
	}
	if StripDefaultPort("http", "8080") != "8080" {
		t.Error("a non-default port must be kept")
	}
}

// TestBuildURL checks the assembled URL, including the proxy base path.
func TestBuildURL(t *testing.T) {
	testOptions(t, Options{ProjectName: "cashp", ListenPort: "64500"})

	r := proxyRequest(map[string]string{
		HeaderForwardedHost:  "app.example.com",
		HeaderForwardedProto: "https",
		HeaderForwardedPort:  "443",
	})
	if got := BuildURL(r, "/server/healthz"); got != "https://app.example.com/server/healthz" {
		t.Errorf("BuildURL = %q", got)
	}
	if got := BuildURL(r, "server/healthz"); got != "https://app.example.com/server/healthz" {
		t.Errorf("a relative path must be normalised, got %q", got)
	}
	if got := Origin(r); got != "https://app.example.com" {
		t.Errorf("Origin = %q", got)
	}

	r = proxyRequest(map[string]string{
		HeaderForwardedHost:   "app.example.com",
		HeaderForwardedProto:  "https",
		HeaderForwardedPort:   "8443",
		HeaderForwardedPrefix: "/cashp/",
	})
	if got := BuildURL(r, "/api"); got != "https://app.example.com:8443/cashp/api" {
		t.Errorf("BuildURL with a prefix = %q", got)
	}
}

// TestBasePathNormalisation checks every prefix header and the root case.
func TestBasePathNormalisation(t *testing.T) {
	testOptions(t, Options{ProjectName: "cashp"})

	cases := []struct {
		header string
		value  string
		want   string
	}{
		{HeaderForwardedPrefix, "/app", "/app"},
		{HeaderForwardedPrefix, "app/", "/app"},
		{HeaderForwardedPrefix, "/", ""},
		{HeaderForwardedPath, "/alt", "/alt"},
		{HeaderScriptName, "/script", "/script"},
	}

	for _, tc := range cases {
		if got := BasePath(proxyRequest(map[string]string{tc.header: tc.value})); got != tc.want {
			t.Errorf("BasePath(%s: %q) = %q, want %q", tc.header, tc.value, got, tc.want)
		}
	}

	if got := BasePath(proxyRequest(nil)); got != "" {
		t.Errorf("with no prefix header BasePath = %q, want an empty string", got)
	}
}

// TestOnionRequestBypassesProxyHeaders checks that a Tor request resolves
// entirely from the tor configuration.
func TestOnionRequestBypassesProxyHeaders(t *testing.T) {
	const onion = "abcdefghijklmnopqrstuvwxyz234567.onion"
	testOptions(t, Options{ProjectName: "cashp", OnionAddress: onion, ListenPort: "64500"})

	r := proxyRequest(map[string]string{
		HeaderForwardedHost:  "clearnet.example.com",
		HeaderForwardedProto: "https",
	})
	r.Host = onion

	proto, fqdn, port := GetURLVars(r)
	if fqdn != onion {
		t.Errorf("fqdn = %q, want the onion address", fqdn)
	}
	if proto != "http" {
		t.Errorf("proto = %q, want http for a hidden service", proto)
	}
	if port != "" {
		t.Errorf("port = %q, want an empty string for a hidden service", port)
	}
}

// TestGetURLVarsWithoutRequest checks that detection still produces usable
// values for a nil request, as background jobs need.
func TestGetURLVarsWithoutRequest(t *testing.T) {
	testOptions(t, Options{ProjectName: "cashp", DevMode: true, ListenPort: "64500"})

	proto, fqdn, port := GetURLVars(nil)
	if proto != "http" {
		t.Errorf("proto = %q, want http", proto)
	}
	if fqdn == "" {
		t.Error("the fqdn must never be empty")
	}
	if port != "64500" {
		t.Errorf("port = %q, want the listen port", port)
	}
}
