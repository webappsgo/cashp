package overlay

import "testing"

func TestKind(t *testing.T) {
	cases := []struct {
		host string
		want string
	}{
		{"expyuzz4wqqyqhjn.onion", KindTor},
		{"EXPYUZZ4WQQYQHJN.ONION", KindTor},
		{"expyuzz4wqqyqhjn.onion:80", KindTor},
		{"expyuzz4wqqyqhjn.onion.", KindTor},
		{" expyuzz4wqqyqhjn.onion ", KindTor},
		{"yhjvnyh4g35f3e4tsryfgb7zxosegmnslvxsfuby5hroxb25rmxa.b32.i2p", KindI2P},
		{"yhjvnyh4g35f3e4tsryfgb7zxosegmnslvxsfuby5hroxb25rmxa.b32.i2p:80", KindI2P},
		{"forum.i2p", KindI2P},
		{"example.com", KindClearnet},
		{"example.com:8080", KindClearnet},
		{"onion", KindClearnet},
		{".onion", KindClearnet},
		{".i2p", KindClearnet},
		{"notanonion.example.com", KindClearnet},
		{"onion.example.com", KindClearnet},
		{"127.0.0.1:64123", KindClearnet},
		{"[::1]:64123", KindClearnet},
		{"", KindClearnet},
	}

	for _, tc := range cases {
		if got := Kind(tc.host); got != tc.want {
			t.Errorf("Kind(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}

func TestIsOverlayRequest(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"expyuzz4wqqyqhjn.onion", true},
		{"yhjvnyh4g35f3e4tsryfgb7zxosegmnslvxsfuby5hroxb25rmxa.b32.i2p", true},
		{"example.com", false},
		{"onion.example.com", false},
		{"", false},
	}

	for _, tc := range cases {
		if got := IsOverlayRequest(tc.host); got != tc.want {
			t.Errorf("IsOverlayRequest(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := []struct {
		host string
		want string
	}{
		{"Example.COM:443", "example.com"},
		{"example.com.", "example.com"},
		{"[2001:db8::1]", "2001:db8::1"},
		{"[2001:db8::1]:8080", "2001:db8::1"},
		{"  example.com  ", "example.com"},
	}

	for _, tc := range cases {
		if got := NormalizeHost(tc.host); got != tc.want {
			t.Errorf("NormalizeHost(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}
