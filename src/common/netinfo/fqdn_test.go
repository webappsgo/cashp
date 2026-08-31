package netinfo

import "testing"

// TestIsValidHostTable checks the documented validation matrix for a
// project named cashp.
func TestIsValidHostTable(t *testing.T) {
	cases := []struct {
		host       string
		production bool
		developmen bool
	}{
		{"my.server.domain.co.uk", true, true},
		{"api.example.com", true, true},
		{"app.company.com.au", true, true},
		{"server.company.io", true, true},
		{"dev.local", false, true},
		{"app.test", false, true},
		{"staging.internal", false, true},
		{"app.cashp", false, true},
		{"my.app.cashp", false, true},
		{"localhost", false, true},
		{"co.uk", false, false},
		{"192.168.1.1", false, false},
		{"203.0.113.10", false, false},
		{"::1", false, false},
		{"myhost", false, false},
		{"devbox", false, false},
		{"", false, false},
	}

	for _, tc := range cases {
		if got := IsValidHost(tc.host, false, "cashp"); got != tc.production {
			t.Errorf("IsValidHost(%q, production) = %v, want %v", tc.host, got, tc.production)
		}
		if got := IsValidHost(tc.host, true, "cashp"); got != tc.developmen {
			t.Errorf("IsValidHost(%q, development) = %v, want %v", tc.host, got, tc.developmen)
		}
	}
}

// TestOverlayTLDsAlwaysValid checks that app-managed overlay addresses pass
// in both modes.
func TestOverlayTLDsAlwaysValid(t *testing.T) {
	hosts := []string{
		"abcdefghijklmnop.onion",
		"abcdefghijklmnop.b32.i2p",
		"relay.exit",
	}

	for _, host := range hosts {
		if !IsValidHost(host, false, "cashp") {
			t.Errorf("%q must be valid in production", host)
		}
		if !IsValidHost(host, true, "cashp") {
			t.Errorf("%q must be valid in development", host)
		}
	}
}

// TestIsValidSSLHost checks the Let's Encrypt gate: production validation
// and no .onion addresses.
func TestIsValidSSLHost(t *testing.T) {
	if !IsValidSSLHost("api.example.com") {
		t.Error("a public domain must be valid for Let's Encrypt")
	}
	if IsValidSSLHost("abcdefghijklmnop.onion") {
		t.Error(".onion addresses cannot use Let's Encrypt")
	}
	if IsValidSSLHost("dev.local") {
		t.Error("a dev-only TLD must never be valid for Let's Encrypt")
	}
	if IsValidSSLHost("localhost") {
		t.Error("localhost must never be valid for Let's Encrypt")
	}
}

// TestPublicSuffix checks the built-in suffix table, including multi-label
// suffixes and the ICANN flag.
func TestPublicSuffix(t *testing.T) {
	cases := []struct {
		host      string
		suffix    string
		wantICANN bool
	}{
		{"my.server.domain.co.uk", "co.uk", true},
		{"example.com", "com", true},
		{"company.com.au", "com.au", true},
		{"dev.local", "local", false},
		{"box.test", "test", false},
		{"site.github.io", "github.io", true},
	}

	for _, tc := range cases {
		suffix, icann := PublicSuffixFunc(tc.host)
		if suffix != tc.suffix {
			t.Errorf("PublicSuffix(%q) = %q, want %q", tc.host, suffix, tc.suffix)
		}
		if icann != tc.wantICANN {
			t.Errorf("PublicSuffix(%q) icann = %v, want %v", tc.host, icann, tc.wantICANN)
		}
	}
}

// TestEffectiveTLDPlusOne checks registrable domain extraction.
func TestEffectiveTLDPlusOne(t *testing.T) {
	cases := map[string]string{
		"my.server.domain.co.uk": "domain.co.uk",
		"www.example.com":        "example.com",
		"example.com":            "example.com",
		"app.company.com.au":     "company.com.au",
	}

	for host, want := range cases {
		got, err := EffectiveTLDPlusOneFunc(host)
		if err != nil {
			t.Errorf("EffectiveTLDPlusOne(%q) failed: %v", host, err)
			continue
		}
		if got != want {
			t.Errorf("EffectiveTLDPlusOne(%q) = %q, want %q", host, got, want)
		}
	}

	if _, err := EffectiveTLDPlusOneFunc("co.uk"); err == nil {
		t.Error("a bare public suffix has no registrable domain")
	}
	if _, err := EffectiveTLDPlusOneFunc(""); err == nil {
		t.Error("an empty host has no registrable domain")
	}
}

// TestBaseDomainOf checks the helper used by domain learning.
func TestBaseDomainOf(t *testing.T) {
	if got := BaseDomainOf("www.myapp.com"); got != "myapp.com" {
		t.Errorf("BaseDomainOf(www.myapp.com) = %q", got)
	}
	if got := BaseDomainOf("myhost"); got != "" {
		t.Errorf("BaseDomainOf(myhost) = %q, want an empty string", got)
	}
}
