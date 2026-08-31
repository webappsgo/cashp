package overlay

import (
	"context"
	"errors"
	"testing"
)

// newTestService builds a service that can never start a provider: the tor
// binary path points at a file that does not exist and I2P is left opted out,
// so no test touches the network or spawns a process.
func newTestService(t *testing.T, allowed ...string) *Service {
	t.Helper()

	torCfg := DefaultTorConfig()
	torCfg.Binary = t.TempDir() + "/no-such-tor"

	return New(Options{
		DataDir:             t.TempDir(),
		Port:                64080,
		AllowedDestinations: allowed,
		Tor:                 &torCfg,
	})
}

func TestDialContextRejectsNonAllowlistedDestination(t *testing.T) {
	svc := newTestService(t, "api.example.com:443")

	cases := []string{
		"evil.example.net:443",
		"api.example.com:8443",
		"127.0.0.1:22",
		"expyuzz4wqqyqhjn.onion:80",
	}

	for _, addr := range cases {
		conn, err := svc.DialContext(context.Background(), "tcp", addr)
		if conn != nil {
			conn.Close()
			t.Fatalf("DialContext(%q) returned a connection, want refusal", addr)
		}
		if !errors.Is(err, ErrDestinationNotAllowed) {
			t.Errorf("DialContext(%q) error = %v, want ErrDestinationNotAllowed", addr, err)
		}
	}
}

func TestDialContextEmptyAllowlistFailsClosed(t *testing.T) {
	svc := newTestService(t)

	if _, err := svc.DialContext(context.Background(), "tcp", "api.example.com:443"); !errors.Is(err, ErrDestinationNotAllowed) {
		t.Fatalf("empty allowlist error = %v, want ErrDestinationNotAllowed", err)
	}
}

// An inbound overlay request must never be able to choose an outbound
// destination, even one that is on the allowlist (AI.md PART 32.1
// "App-Scoped Only").
func TestDialContextRejectsInboundOverlayOrigin(t *testing.T) {
	svc := newTestService(t, "api.example.com:443")

	for _, kind := range []string{KindTor, KindI2P} {
		ctx := WithInboundOverlay(context.Background(), kind)

		if _, err := svc.DialContext(ctx, "tcp", "api.example.com:443"); !errors.Is(err, ErrOverlayScopedOutbound) {
			t.Errorf("DialContext with inbound %s error = %v, want ErrOverlayScopedOutbound", kind, err)
		}
		if _, err := svc.DialDirectContext(ctx, "tcp", "api.example.com:443"); !errors.Is(err, ErrOverlayScopedOutbound) {
			t.Errorf("DialDirectContext with inbound %s error = %v, want ErrOverlayScopedOutbound", kind, err)
		}
	}
}

func TestInboundOverlayContext(t *testing.T) {
	if _, ok := InboundOverlay(context.Background()); ok {
		t.Fatal("clearnet context reported as an inbound overlay request")
	}

	kind, ok := InboundOverlay(WithInboundOverlay(context.Background(), KindTor))
	if !ok || kind != KindTor {
		t.Fatalf("InboundOverlay = (%q, %v), want (%q, true)", kind, ok, KindTor)
	}
}

func TestDestinationAllowed(t *testing.T) {
	svc := newTestService(t, "api.example.com:443", "Peers.Example.NET", " ", "10.0.0.5:9000")

	cases := []struct {
		addr string
		want bool
	}{
		{"api.example.com:443", true},
		{"API.EXAMPLE.COM:443", true},
		{"peers.example.net:443", true},
		{"peers.example.net:80", true},
		{"10.0.0.5:9000", true},
		{"10.0.0.5:9001", false},
		{"api.example.com:80", false},
		{"other.example.com:443", false},
		{"", false},
		{":443", false},
	}

	for _, tc := range cases {
		if got := svc.destinationAllowed(tc.addr); got != tc.want {
			t.Errorf("destinationAllowed(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

func TestShouldUseTor(t *testing.T) {
	yes, no := true, false

	cases := []struct {
		serverUseNetwork    bool
		allowUserPreference bool
		userPref            *bool
		want                bool
	}{
		{false, false, nil, false},
		{true, false, &no, true},
		{false, true, nil, false},
		{false, true, &yes, true},
		{true, true, &no, false},
		{true, true, nil, true},
	}

	for _, tc := range cases {
		if got := ShouldUseTor(tc.serverUseNetwork, tc.allowUserPreference, tc.userPref); got != tc.want {
			t.Errorf("ShouldUseTor(%v, %v, %v) = %v, want %v",
				tc.serverUseNetwork, tc.allowUserPreference, tc.userPref, got, tc.want)
		}
	}
}

func TestHTTPClientUsesScopedTransport(t *testing.T) {
	svc := newTestService(t, "api.example.com:443")

	client := svc.HTTPClient(true)
	if client.Transport == nil {
		t.Fatal("HTTPClient returned a client without a transport")
	}
	if client.Timeout != torDialTimeout {
		t.Errorf("tor client timeout = %s, want %s", client.Timeout, torDialTimeout)
	}
	if direct := svc.HTTPClient(false); direct.Timeout != directDialTimeout {
		t.Errorf("direct client timeout = %s, want %s", direct.Timeout, directDialTimeout)
	}

	// The transport must refuse a destination that is not on the allowlist
	// rather than dialing it.
	if _, err := client.Get("https://evil.example.net/"); !errors.Is(err, ErrDestinationNotAllowed) {
		t.Errorf("client.Get error = %v, want ErrDestinationNotAllowed", err)
	}
}
