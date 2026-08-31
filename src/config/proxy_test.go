package config

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestIsTrustedProxyAlwaysTrustedRanges(t *testing.T) {
	cfg := Defaults()
	Validate(cfg)

	for _, ip := range []string{"127.0.0.1", "::1", "10.1.2.3", "172.16.0.9", "192.168.5.5", "169.254.1.1", "fd00::1"} {
		if !cfg.IsTrustedProxy(net.ParseIP(ip)) {
			t.Errorf("IsTrustedProxy(%s) = false, want true", ip)
		}
	}

	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "2606:4700::1111"} {
		if cfg.IsTrustedProxy(net.ParseIP(ip)) {
			t.Errorf("IsTrustedProxy(%s) = true, want false", ip)
		}
	}
}

func TestIsTrustedProxyNilPeer(t *testing.T) {
	cfg := Defaults()

	if cfg.IsTrustedProxy(nil) {
		t.Error("a missing peer address is never a trusted proxy")
	}
}

func TestIsTrustedProxyWithoutValidate(t *testing.T) {
	cfg := Defaults()
	cfg.Server.TrustedProxy.Additional = []string{"203.0.113.7"}

	if !cfg.IsTrustedProxy(net.ParseIP("203.0.113.7")) {
		t.Error("a hand-built config must still answer trust questions")
	}
}

func TestIsTrustedProxyAdditionalCIDR(t *testing.T) {
	cfg := Defaults()
	cfg.Server.TrustedProxy.Additional = []string{"203.0.113.0/24", "2001:db8::/32"}
	Validate(cfg)

	if !cfg.IsTrustedProxy(net.ParseIP("203.0.113.42")) {
		t.Error("an address inside an additional CIDR must be trusted")
	}
	if !cfg.IsTrustedProxy(net.ParseIP("2001:db8::5")) {
		t.Error("an IPv6 address inside an additional CIDR must be trusted")
	}
	if cfg.IsTrustedProxy(net.ParseIP("203.0.114.42")) {
		t.Error("an address outside every CIDR must not be trusted")
	}
}

func TestListenSubnetTrustsSameSegment(t *testing.T) {
	cfg := Defaults()
	cfg.Server.Address = "203.0.113.10"
	Validate(cfg)

	if !cfg.IsTrustedProxy(net.ParseIP("203.0.113.200")) {
		t.Error("a peer on the listen address's own /24 must be trusted")
	}
	if cfg.IsTrustedProxy(net.ParseIP("203.0.114.200")) {
		t.Error("a peer outside the listen /24 must not be trusted")
	}
}

func TestListenSubnetIgnoresWildcard(t *testing.T) {
	for _, address := range []string{"", "[::]", "0.0.0.0", "::"} {
		if network := listenSubnet(address); network != nil {
			t.Errorf("listenSubnet(%q) = %v, want nil", address, network)
		}
	}
}

func TestProxyResolverDNSEntries(t *testing.T) {
	resolver := newProxyResolver([]string{"proxy.example.test"}, "")
	resolver.lookup = func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("198.51.100.4")}, nil
	}

	if !resolver.trusts(net.ParseIP("198.51.100.4")) {
		t.Error("a resolved DNS entry must be trusted")
	}
	if resolver.trusts(net.ParseIP("198.51.100.5")) {
		t.Error("an unrelated address must not be trusted")
	}
}

func TestProxyResolverKeepsCacheOnLookupFailure(t *testing.T) {
	resolver := newProxyResolver([]string{"proxy.example.test"}, "")
	resolver.lookup = func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("198.51.100.4")}, nil
	}

	if !resolver.trusts(net.ParseIP("198.51.100.4")) {
		t.Fatal("expected the first lookup to populate the cache")
	}

	resolver.lookup = func(string) ([]net.IP, error) {
		return nil, errors.New("dns down")
	}
	resolver.refreshed = time.Now().Add(-2 * proxyRefreshInterval)

	if !resolver.trusts(net.ParseIP("198.51.100.4")) {
		t.Error("a transient DNS failure must not shrink the trust set")
	}
}

func TestClientIPUsesOriginalPeerForTrust(t *testing.T) {
	cfg := Defaults()
	Validate(cfg)

	trustedPeer := net.ParseIP("127.0.0.1")
	if got := cfg.ClientIP(trustedPeer, "198.51.100.9, 10.0.0.1", ""); !got.Equal(net.ParseIP("198.51.100.9")) {
		t.Errorf("ClientIP() = %v, want the client from X-Forwarded-For", got)
	}

	untrustedPeer := net.ParseIP("8.8.8.8")
	if got := cfg.ClientIP(untrustedPeer, "198.51.100.9", "203.0.113.1"); !got.Equal(untrustedPeer) {
		t.Errorf("ClientIP() = %v, want the untouched peer %v", got, untrustedPeer)
	}
}

func TestClientIPFallsBackToRealIP(t *testing.T) {
	cfg := Defaults()
	Validate(cfg)

	peer := net.ParseIP("127.0.0.1")
	if got := cfg.ClientIP(peer, "", "203.0.113.1"); !got.Equal(net.ParseIP("203.0.113.1")) {
		t.Errorf("ClientIP() = %v, want 203.0.113.1", got)
	}
	if got := cfg.ClientIP(peer, "", "garbage"); !got.Equal(peer) {
		t.Errorf("ClientIP() = %v, want the peer when headers are unusable", got)
	}
}

func TestParseHeaderIP(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{" 203.0.113.1 ", "203.0.113.1"},
		{"203.0.113.1:1234", "203.0.113.1"},
		{"[2001:db8::1]:443", "2001:db8::1"},
		{"\"203.0.113.1\"", "203.0.113.1"},
	} {
		got := parseHeaderIP(tc.in)
		if got == nil || !got.Equal(net.ParseIP(tc.want)) {
			t.Errorf("parseHeaderIP(%q) = %v, want %s", tc.in, got, tc.want)
		}
	}

	if parseHeaderIP("") != nil || parseHeaderIP("unknown") != nil {
		t.Error("parseHeaderIP must return nil for unusable values")
	}
}

func TestLocalNodeID(t *testing.T) {
	if LocalNodeID() == "" {
		t.Error("LocalNodeID() must never be empty")
	}
}
