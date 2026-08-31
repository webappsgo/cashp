package netinfo

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClientIPPriority checks the documented header priority chain for a
// request that arrived from a trusted proxy.
func TestClientIPPriority(t *testing.T) {
	all := map[string]string{
		HeaderCFConnectingIP: "198.51.100.1",
		HeaderTrueClientIP:   "198.51.100.2",
		HeaderRealIP:         "198.51.100.3",
		HeaderForwardedFor:   "198.51.100.4, 10.0.0.9",
		HeaderClientIP:       "198.51.100.5",
	}
	order := []string{
		HeaderCFConnectingIP,
		HeaderTrueClientIP,
		HeaderRealIP,
		HeaderForwardedFor,
		HeaderClientIP,
	}
	want := []string{
		"198.51.100.1",
		"198.51.100.2",
		"198.51.100.3",
		"198.51.100.4",
		"198.51.100.5",
	}

	for i := range order {
		headers := map[string]string{}
		for _, name := range order[i:] {
			headers[name] = all[name]
		}
		if got := ClientIP(proxyRequest(headers)); got != want[i] {
			t.Errorf("with %s as the highest header ClientIP = %q, want %q", order[i], got, want[i])
		}
	}

	if got := ClientIP(proxyRequest(nil)); got != "10.1.2.3" {
		t.Errorf("with no headers ClientIP = %q, want the peer address", got)
	}
}

// TestClientIPIgnoresUntrustedHeaders checks that a direct client cannot
// spoof its own address, which would defeat rate limiting and blocklists.
func TestClientIPIgnoresUntrustedHeaders(t *testing.T) {
	r := directRequest(map[string]string{
		HeaderCFConnectingIP: "10.0.0.1",
		HeaderForwardedFor:   "10.0.0.2",
		HeaderRealIP:         "10.0.0.3",
	})

	if got := ClientIP(r); got != "203.0.113.7" {
		t.Errorf("ClientIP = %q, want the real peer address 203.0.113.7", got)
	}
}

// TestClientIPNilRequest checks the background-job case.
func TestClientIPNilRequest(t *testing.T) {
	if got := ClientIP(nil); got != "" {
		t.Errorf("ClientIP(nil) = %q, want an empty string", got)
	}
}

// TestOriginalPeerIsUsedForTrust checks the middleware-ordering rule: once
// real-IP middleware has rewritten RemoteAddr, the trust decision must still
// use the preserved TCP peer.
func TestOriginalPeerIsUsedForTrust(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil)
	r.Header.Set(HeaderForwardedHost, "app.example.com")
	// The address a real-IP middleware would have written back.
	r.RemoteAddr = "198.51.100.20:33333"
	r = r.WithContext(WithOriginalPeer(r.Context(), "10.1.2.3:54321"))

	if !TrustedRequest(r) {
		t.Fatal("the preserved peer 10.1.2.3 is a trusted proxy")
	}
	if got := ProxyFQDN(r); got != "app.example.com" {
		t.Errorf("ProxyFQDN = %q, want app.example.com", got)
	}

	spoofed := httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil)
	spoofed.Header.Set(HeaderForwardedHost, "evil.example.net")
	// A rewritten RemoteAddr must not be able to buy trust either.
	spoofed.RemoteAddr = "10.1.2.3:54321"
	spoofed = spoofed.WithContext(WithOriginalPeer(spoofed.Context(), "203.0.113.7:44321"))

	if TrustedRequest(spoofed) {
		t.Error("the preserved peer 203.0.113.7 is not a trusted proxy")
	}
	if got := ProxyFQDN(spoofed); got != "" {
		t.Errorf("ProxyFQDN = %q, want an empty string for an untrusted peer", got)
	}
}

// TestOriginalPeerFallback checks that RemoteAddr is used when no middleware
// stored a peer.
func TestOriginalPeerFallback(t *testing.T) {
	if got := OriginalPeer(proxyRequest(nil)); got != "10.1.2.3:54321" {
		t.Errorf("OriginalPeer = %q, want the RemoteAddr", got)
	}
	if got := OriginalPeer(nil); got != "" {
		t.Errorf("OriginalPeer(nil) = %q, want an empty string", got)
	}
}

// TestDefaultTrustedCIDRs checks the private and loopback ranges trusted out
// of the box, and that public addresses are not trusted.
func TestDefaultTrustedCIDRs(t *testing.T) {
	t.Cleanup(func() {
		if err := SetTrustedProxies(nil); err != nil {
			t.Fatalf("restoring the defaults failed: %v", err)
		}
	})

	if err := SetTrustedProxies(nil); err != nil {
		t.Fatalf("SetTrustedProxies(nil) failed: %v", err)
	}

	trusted := []string{
		"10.1.2.3:1", "172.16.0.9:1", "172.31.255.254:1", "192.168.1.1:1",
		"127.0.0.1:1", "169.254.10.10:1", "[::1]:1", "[fd00::1]:1", "[fe80::1]:1",
	}
	for _, addr := range trusted {
		if !IsTrustedPeer(addr) {
			t.Errorf("%s must be trusted by default", addr)
		}
	}

	untrusted := []string{
		"203.0.113.7:1", "8.8.8.8:1", "172.32.0.1:1", "[2606:4700::1]:1", "not-an-address",
	}
	for _, addr := range untrusted {
		if IsTrustedPeer(addr) {
			t.Errorf("%s must not be trusted by default", addr)
		}
	}

	if len(DefaultTrustedCIDRs()) == 0 {
		t.Error("DefaultTrustedCIDRs() must list the built-in ranges")
	}
}

// TestSetTrustedProxies checks explicit configuration, bare IP entries, and
// rejection of invalid input.
func TestSetTrustedProxies(t *testing.T) {
	t.Cleanup(func() {
		if err := SetTrustedProxies(nil); err != nil {
			t.Fatalf("restoring the defaults failed: %v", err)
		}
	})

	if err := SetTrustedProxies([]string{"203.0.113.0/24", "198.51.100.7", "2001:db8::1"}); err != nil {
		t.Fatalf("SetTrustedProxies failed: %v", err)
	}

	if !IsTrustedPeer("203.0.113.7:1") {
		t.Error("an address inside the configured CIDR must be trusted")
	}
	if !IsTrustedPeer("198.51.100.7:1") {
		t.Error("a bare IPv4 entry must be treated as a /32")
	}
	if !IsTrustedPeer("[2001:db8::1]:1") {
		t.Error("a bare IPv6 entry must be treated as a /128")
	}
	// AI.md PART 12 "Trusted Proxies": the private ranges are always trusted
	// in addition to the `additional` allow-list — configuring an explicit
	// list extends the defaults, it never replaces them.
	if !IsTrustedPeer("10.1.2.3:1") {
		t.Error("the default private ranges must stay trusted alongside an explicit allow-list")
	}

	if err := SetTrustedProxies([]string{"not-a-cidr"}); err == nil {
		t.Error("an invalid entry must be rejected")
	}
	if !IsTrustedPeer("203.0.113.7:1") {
		t.Error("a rejected update must leave the previous list in place")
	}
}

// TestNormalizeBasePath checks the helper in isolation.
func TestNormalizeBasePath(t *testing.T) {
	cases := map[string]string{
		"":            "",
		"/":           "",
		"/app":        "/app",
		"app":         "/app",
		"/app/":       "/app",
		"  /app/  ":   "/app",
		"/deep/path/": "/deep/path",
	}

	for input, want := range cases {
		if got := NormalizeBasePath(input); got != want {
			t.Errorf("NormalizeBasePath(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestProxyGettersRequireTrust checks that every proxy getter is gated.
func TestProxyGettersRequireTrust(t *testing.T) {
	headers := map[string]string{
		HeaderForwardedHost:   "evil.example.net",
		HeaderForwardedProto:  "https",
		HeaderForwardedPort:   "8443",
		HeaderForwardedPrefix: "/evil",
	}
	r := directRequest(headers)

	if got := ProxyFQDN(r); got != "" {
		t.Errorf("ProxyFQDN = %q, want an empty string", got)
	}
	if got := ProxyProto(r); got != "" {
		t.Errorf("ProxyProto = %q, want an empty string", got)
	}
	if got := ProxyPort(r); got != "" {
		t.Errorf("ProxyPort = %q, want an empty string", got)
	}
	if got := ProxyBasePath(r); got != "" {
		t.Errorf("ProxyBasePath = %q, want an empty string", got)
	}
}
