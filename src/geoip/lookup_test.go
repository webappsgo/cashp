package geoip

import (
	"net"
	"testing"
)

func TestIsInternal(t *testing.T) {
	internal := []string{
		"127.0.0.1",
		"::1",
		"10.1.2.3",
		"172.16.0.1",
		"192.168.1.1",
		"169.254.10.10",
		"fe80::1",
		"fd00::1",
		"fc00::abcd",
		"0.0.0.0",
		"::",
		"100.64.0.1",
		"0.1.2.3",
		"224.0.0.1",
		"ff02::1",
	}

	for _, s := range internal {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("could not parse %q", s)
		}

		if !IsInternal(ip) {
			t.Fatalf("IsInternal(%s) = false, want true", s)
		}
	}

	public := []string{"8.8.8.8", "1.1.1.1", "203.0.113.7", "2606:4700:4700::1111"}
	for _, s := range public {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("could not parse %q", s)
		}

		if IsInternal(ip) {
			t.Fatalf("IsInternal(%s) = true, want false", s)
		}
	}

	if !IsInternal(nil) {
		t.Fatal("IsInternal(nil) = false, want true")
	}
}

func TestLookupShortCircuitsPrivateAddresses(t *testing.T) {
	d := newTestDB(t, Options{Enabled: true, DenyCountries: []string{"US"}})

	for _, s := range []string{"127.0.0.1", "10.0.0.5", "::1", "fd00::5"} {
		res, ok := d.Lookup(net.ParseIP(s))
		if ok {
			t.Fatalf("Lookup(%s) reported a hit for an internal address", s)
		}

		if res != (Result{}) {
			t.Fatalf("Lookup(%s) returned data for an internal address: %+v", s, res)
		}

		if code, ok := d.Country(net.ParseIP(s)); ok || code != "" {
			t.Fatalf("Country(%s) = %q, %v; want empty, false", s, code, ok)
		}
	}
}

func TestLookupWithoutDatabasesMisses(t *testing.T) {
	d := newTestDB(t, Options{Enabled: true})

	res, ok := d.Lookup(net.ParseIP("8.8.8.8"))
	if ok {
		t.Fatal("Lookup reported a hit with no databases present")
	}

	if res != (Result{}) {
		t.Fatalf("Lookup returned data with no databases present: %+v", res)
	}
}

func TestLookupWhenDisabled(t *testing.T) {
	d := newTestDB(t, Options{Enabled: false})

	if _, ok := d.Lookup(net.ParseIP("8.8.8.8")); ok {
		t.Fatal("Lookup reported a hit while GeoIP is disabled")
	}

	if _, ok := d.Country(net.ParseIP("8.8.8.8")); ok {
		t.Fatal("Country reported a hit while GeoIP is disabled")
	}
}

func TestLookupNilIP(t *testing.T) {
	d := newTestDB(t, Options{Enabled: true})

	if _, ok := d.Lookup(nil); ok {
		t.Fatal("Lookup(nil) reported a hit")
	}
}

func TestCityFileMatchesAddressFamily(t *testing.T) {
	if got := cityFile(net.ParseIP("8.8.8.8")); got != fileCity4 {
		t.Fatalf("cityFile(IPv4) = %q, want %q", got, fileCity4)
	}

	if got := cityFile(net.ParseIP("2606:4700:4700::1111")); got != fileCity6 {
		t.Fatalf("cityFile(IPv6) = %q, want %q", got, fileCity6)
	}
}

func TestNormalizeCode(t *testing.T) {
	if got := normalizeCode(" us "); got != "US" {
		t.Fatalf("normalizeCode(\" us \") = %q, want US", got)
	}

	for _, in := range []string{"", "U", "USA", "u1"} {
		if got := normalizeCode(in); got != "" {
			t.Fatalf("normalizeCode(%q) = %q, want empty", in, got)
		}
	}
}

func TestValueCoercion(t *testing.T) {
	if got := asString("x"); got != "x" {
		t.Fatalf("asString = %q, want x", got)
	}

	if got := asString(42); got != "" {
		t.Fatalf("asString(42) = %q, want empty", got)
	}

	floats := map[any]float64{
		float64(1.5):  1.5,
		float32(2):    2,
		int(3):        3,
		int32(4):      4,
		int64(5):      5,
		uint(6):       6,
		uint16(7):     7,
		uint32(8):     8,
		uint64(9):     9,
		" -10.25 ":    -10.25,
		"not-a-float": 0,
		true:          0,
	}

	for in, want := range floats {
		if got := asFloat(in); got != want {
			t.Fatalf("asFloat(%v) = %v, want %v", in, got, want)
		}
	}

	uints := map[any]uint{
		uint(1):     1,
		uint16(2):   2,
		uint32(3):   3,
		uint64(4):   4,
		int(5):      5,
		int32(6):    6,
		int64(7):    7,
		float64(8):  8,
		" 9 ":       9,
		int(-1):     0,
		int32(-1):   0,
		int64(-1):   0,
		float64(-1): 0,
		"abc":       0,
		true:        0,
	}

	for in, want := range uints {
		if got := asUint(in); got != want {
			t.Fatalf("asUint(%v) = %v, want %v", in, got, want)
		}
	}
}
