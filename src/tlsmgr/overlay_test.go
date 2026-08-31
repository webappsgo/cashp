package tlsmgr

import "testing"

func TestNormalizeHost(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"  ", ""},
		{"Example.COM", "example.com"},
		{"example.com.", "example.com"},
		{"example.com:8443", "example.com"},
		{"[2001:db8::1]:443", "2001:db8::1"},
		{"ABC.onion:80", "abc.onion"},
		{"panel.example.com:80", "panel.example.com"},
	}

	for _, tc := range cases {
		if got := NormalizeHost(tc.in); got != tc.want {
			t.Errorf("NormalizeHost(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsOverlayHost(t *testing.T) {
	overlay := []string{
		"abcdefghij.onion",
		"ABCDEFGHIJ.ONION",
		"site.onion:8080",
		"xyz.b32.i2p",
		"xyz.i2p",
		"host.exit",
	}
	for _, host := range overlay {
		if !IsOverlayHost(host) {
			t.Errorf("IsOverlayHost(%q) = false, want true", host)
		}
	}

	clearnet := []string{"example.com", "panel.example.com", "onion.example.com", "i2p.example.org", ""}
	for _, host := range clearnet {
		if IsOverlayHost(host) {
			t.Errorf("IsOverlayHost(%q) = true, want false", host)
		}
	}
}

func TestShouldSendHSTSSuppressedOnOverlayHosts(t *testing.T) {
	suppressed := []string{
		"abcdefghij.onion",
		"abcdefghij.onion:80",
		"xyz.b32.i2p",
		"xyz.i2p",
		"",
	}
	for _, host := range suppressed {
		if ShouldSendHSTS(host) {
			t.Errorf("ShouldSendHSTS(%q) = true, want false", host)
		}
	}

	allowed := []string{"example.com", "panel.example.com:443", "Example.COM."}
	for _, host := range allowed {
		if !ShouldSendHSTS(host) {
			t.Errorf("ShouldSendHSTS(%q) = false, want true", host)
		}
	}
}
