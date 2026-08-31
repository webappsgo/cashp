package geoip

import (
	"net"
	"testing"
)

func TestCountryAllowedAllowlistWins(t *testing.T) {
	d := newTestDB(t, Options{
		Enabled:        true,
		AllowCountries: []string{"US", "CA"},
		DenyCountries:  []string{"US", "CN"},
	})

	// US is on both lists; the allowlist takes precedence.
	if !d.CountryAllowed("US") {
		t.Fatal("US denied even though it is on the allowlist")
	}

	if !d.CountryAllowed("CA") {
		t.Fatal("CA denied even though it is on the allowlist")
	}

	// CN is only on the deny list, but allowlist mode blocks everything
	// that is not explicitly listed.
	if d.CountryAllowed("CN") {
		t.Fatal("CN allowed in allowlist mode")
	}

	if d.CountryAllowed("GB") {
		t.Fatal("GB allowed in allowlist mode")
	}
}

func TestCountryAllowedDenylist(t *testing.T) {
	d := newTestDB(t, Options{Enabled: true, DenyCountries: []string{"CN", "RU"}})

	if d.CountryAllowed("cn") {
		t.Fatal("CN allowed while on the deny list")
	}

	if !d.CountryAllowed("US") {
		t.Fatal("US denied while only CN and RU are on the deny list")
	}
}

func TestCountryAllowedOpenPolicy(t *testing.T) {
	d := newTestDB(t, Options{Enabled: true})

	for _, code := range []string{"US", "CN", "RU", "KP"} {
		if !d.CountryAllowed(code) {
			t.Fatalf("%s denied under an open policy", code)
		}
	}
}

func TestCountryAllowedUnknownCodeFailsOpen(t *testing.T) {
	d := newTestDB(t, Options{Enabled: true, AllowCountries: []string{"US"}})

	for _, code := range []string{"", "ZZZ", "?"} {
		if !d.CountryAllowed(code) {
			t.Fatalf("unrecognized code %q was denied; policy must fail open", code)
		}
	}
}

func TestAllowedFailsOpenWithoutDatabases(t *testing.T) {
	cases := []Options{
		{Enabled: true},
		{Enabled: true, DenyCountries: []string{"US", "CN"}},
		{Enabled: true, AllowCountries: []string{"US"}},
		{Enabled: false, AllowCountries: []string{"US"}},
	}

	for _, opts := range cases {
		d := newTestDB(t, opts)

		for _, s := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
			if !d.Allowed(net.ParseIP(s)) {
				t.Fatalf("Allowed(%s) = false with no country database; GeoIP must fail open", s)
			}
		}
	}
}

func TestAllowedNeverBlocksInternalAddresses(t *testing.T) {
	d := newTestDB(t, Options{Enabled: true, AllowCountries: []string{"AQ"}})

	for _, s := range []string{"127.0.0.1", "10.0.0.1", "192.168.5.5", "172.20.0.1", "::1", "fd00::1"} {
		if !d.Allowed(net.ParseIP(s)) {
			t.Fatalf("Allowed(%s) = false; internal addresses are never country-blocked", s)
		}
	}

	if !d.Allowed(nil) {
		t.Fatal("Allowed(nil) = false; an unknown address must fail open")
	}
}

func TestAllowedWhenDisabled(t *testing.T) {
	d := newTestDB(t, Options{Enabled: false, AllowCountries: []string{"AQ"}})

	if !d.Allowed(net.ParseIP("8.8.8.8")) {
		t.Fatal("Allowed = false while GeoIP is disabled")
	}
}
