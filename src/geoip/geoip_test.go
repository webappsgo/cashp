package geoip

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestDB builds a DB rooted at a fresh temporary data directory with
// no databases downloaded, which is the state every test relies on to
// exercise fail-open behaviour without network access.
func newTestDB(t *testing.T, opts Options) *DB {
	t.Helper()

	if opts.DataDir == "" {
		opts.DataDir = t.TempDir()
	}

	d, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	return d
}

func TestNewCreatesDatabaseDirectory(t *testing.T) {
	dataDir := t.TempDir()

	d := newTestDB(t, Options{DataDir: dataDir, Enabled: true})

	want := filepath.Join(dataDir, "security", "geoip")
	if d.Dir() != want {
		t.Fatalf("Dir() = %q, want %q", d.Dir(), want)
	}

	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("stat %q: %v", want, err)
	}

	if !info.IsDir() {
		t.Fatalf("%q is not a directory", want)
	}
}

func TestNewWithoutDataDirIsNotAnError(t *testing.T) {
	empty, err := New(Options{Enabled: true})
	if err != nil {
		t.Fatalf("New with empty DataDir: %v", err)
	}

	defer func() {
		if err := empty.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	if empty.Dir() != "" {
		t.Fatalf("Dir() = %q, want empty", empty.Dir())
	}
}

func TestNewNormalizesCountryLists(t *testing.T) {
	d := newTestDB(t, Options{
		Enabled:        true,
		AllowCountries: []string{" us ", "ca", "US", "USA", "", "1A"},
		DenyCountries:  []string{"cn", "RU", "russia"},
	})

	allow := d.AllowCountries()
	if strings.Join(allow, ",") != "CA,US" {
		t.Fatalf("AllowCountries() = %v, want [CA US]", allow)
	}

	deny := d.DenyCountries()
	if strings.Join(deny, ",") != "CN,RU" {
		t.Fatalf("DenyCountries() = %v, want [CN RU]", deny)
	}
}

func TestPolicyMode(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		want string
	}{
		{name: "open", opts: Options{Enabled: true}, want: "open"},
		{name: "denylist", opts: Options{Enabled: true, DenyCountries: []string{"CN"}}, want: "denylist"},
		{name: "allowlist", opts: Options{Enabled: true, AllowCountries: []string{"US"}}, want: "allowlist"},
		{
			name: "both lists set means allowlist",
			opts: Options{Enabled: true, AllowCountries: []string{"US"}, DenyCountries: []string{"US"}},
			want: "allowlist",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDB(t, tc.opts)

			if got := d.PolicyMode(); got != tc.want {
				t.Fatalf("PolicyMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNilDBIsInert(t *testing.T) {
	var d *DB

	if d.Enabled() {
		t.Fatal("nil DB reports enabled")
	}

	if d.Dir() != "" {
		t.Fatal("nil DB reports a directory")
	}

	if err := d.Close(); err != nil {
		t.Fatalf("Close on nil DB: %v", err)
	}

	if !d.CountryAllowed("CN") {
		t.Fatal("nil DB must fail open")
	}

	if d.PolicyMode() != "open" {
		t.Fatalf("PolicyMode() = %q, want open", d.PolicyMode())
	}

	if d.AllowCountries() != nil || d.DenyCountries() != nil {
		t.Fatal("nil DB returned country lists")
	}
}

func TestValidCountryCode(t *testing.T) {
	valid := []string{"US", "CA", "ZW"}
	for _, code := range valid {
		if !validCountryCode(code) {
			t.Fatalf("validCountryCode(%q) = false, want true", code)
		}
	}

	invalid := []string{"", "U", "USA", "u s", "u1", "us"}
	for _, code := range invalid {
		if validCountryCode(code) {
			t.Fatalf("validCountryCode(%q) = true, want false", code)
		}
	}
}

func TestCountryName(t *testing.T) {
	if got := CountryName("us"); got != "United States" {
		t.Fatalf("CountryName(us) = %q, want United States", got)
	}

	if got := CountryName("ZZ"); got != "" {
		t.Fatalf("CountryName(ZZ) = %q, want empty", got)
	}

	if got := CountryName(""); got != "" {
		t.Fatalf("CountryName(empty) = %q, want empty", got)
	}

	if !KnownCountry("de") {
		t.Fatal("KnownCountry(de) = false, want true")
	}

	if KnownCountry("QQ") {
		t.Fatal("KnownCountry(QQ) = true, want false")
	}
}

func TestAttributionIsVerbatim(t *testing.T) {
	if !strings.Contains(Attribution, `<a href="https://db-ip.com/">IP Geolocation by DB-IP</a>`) {
		t.Fatal("Attribution is missing the verbatim DB-IP notice")
	}

	const nro = "Country and ASN data licensed CC BY 4.0 by the Number Resource Organization (NRO)."
	if !strings.Contains(Attribution, nro) {
		t.Fatal("Attribution is missing the verbatim NRO notice")
	}

	if AttributionHTML == "" || AttributionText != nro {
		t.Fatal("split attribution constants do not match Attribution")
	}
}
