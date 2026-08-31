package netinfo

import (
	"net"
	"reflect"
	"testing"
	"time"
)

// learningOptions returns settings with learning enabled and no configured
// domains, which is the state the learner is designed for.
func learningOptions() Options {
	return Options{
		ProjectName:  "cashp",
		Learning:     true,
		MinSamples:   3,
		SampleWindow: 5 * time.Minute,
		LogChanges:   true,
	}
}

// fakeClock pins the package clock and returns a function that advances it.
func fakeClock(t *testing.T) func(time.Duration) {
	t.Helper()

	current := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	original := now
	now = func() time.Time { return current }

	t.Cleanup(func() { now = original })

	return func(d time.Duration) { current = current.Add(d) }
}

// silenceLog captures the change log so the tests can assert on it without
// writing to the process logger.
func silenceLog(t *testing.T) *[]string {
	t.Helper()

	var lines []string
	original := Logf
	Logf = func(format string, args ...any) {
		lines = append(lines, format)
	}

	t.Cleanup(func() { Logf = original })

	return &lines
}

// TestLearningDisabledRecordsNothing checks the learning switch.
func TestLearningDisabledRecordsNothing(t *testing.T) {
	t.Setenv("DOMAIN", "")

	opts := learningOptions()
	opts.Learning = false
	testOptions(t, opts)

	Observe("app.myapp.com", "https")
	if hosts := ObservedHosts(); len(hosts) != 0 {
		t.Errorf("with learning disabled ObservedHosts = %v, want none", hosts)
	}
}

// TestLearnBaseDomainAndWildcard checks base domain inference, the
// MinSamples gate on the wildcard, and the non-www primary rule.
func TestLearnBaseDomainAndWildcard(t *testing.T) {
	t.Setenv("DOMAIN", "")
	testOptions(t, learningOptions())
	silenceLog(t)

	Observe("www.myapp.com", "https")
	Observe("app.myapp.com", "https")

	if got := GetBaseDomain(); got != "myapp.com" {
		t.Errorf("GetBaseDomain = %q, want myapp.com", got)
	}
	if got := GetWildcardDomain(); got != "" {
		t.Errorf("below MinSamples GetWildcardDomain = %q, want an empty string", got)
	}

	Observe("api.myapp.com", "https")

	if got := GetWildcardDomain(); got != "*.myapp.com" {
		t.Errorf("GetWildcardDomain = %q, want *.myapp.com", got)
	}
	if got := PrimaryFQDN(); got != "api.myapp.com" {
		t.Errorf("PrimaryFQDN = %q, want the most frequent non-www host", got)
	}

	want := []string{"api.myapp.com", "app.myapp.com", "www.myapp.com"}
	if got := ObservedHosts(); !reflect.DeepEqual(got, want) {
		t.Errorf("ObservedHosts = %v, want %v", got, want)
	}
}

// TestPrimaryFQDNPrefersFrequency checks that the most frequently observed
// non-www host wins.
func TestPrimaryFQDNPrefersFrequency(t *testing.T) {
	t.Setenv("DOMAIN", "")
	testOptions(t, learningOptions())
	silenceLog(t)

	Observe("app.myapp.com", "https")
	Observe("api.myapp.com", "https")
	Observe("app.myapp.com", "https")
	Observe("www.myapp.com", "https")

	if got := PrimaryFQDN(); got != "app.myapp.com" {
		t.Errorf("PrimaryFQDN = %q, want app.myapp.com", got)
	}
}

// TestSampleWindowExpiry checks that observations older than the window stop
// counting, so a name that is no longer used cannot pin the wildcard.
func TestSampleWindowExpiry(t *testing.T) {
	t.Setenv("DOMAIN", "")
	testOptions(t, learningOptions())
	silenceLog(t)
	advance := fakeClock(t)

	Observe("old.myapp.com", "https")
	Observe("stale.myapp.com", "https")

	advance(10 * time.Minute)

	Observe("app.newapp.com", "https")
	Observe("api.newapp.com", "https")
	Observe("web.newapp.com", "https")

	want := []string{"api.newapp.com", "app.newapp.com", "web.newapp.com"}
	if got := ObservedHosts(); !reflect.DeepEqual(got, want) {
		t.Errorf("ObservedHosts = %v, want only the recent hosts %v", got, want)
	}
	if got := GetBaseDomain(); got != "newapp.com" {
		t.Errorf("GetBaseDomain = %q, want newapp.com", got)
	}
	if got := GetWildcardDomain(); got != "*.newapp.com" {
		t.Errorf("GetWildcardDomain = %q, want *.newapp.com", got)
	}
}

// TestObserveLogsChanges checks that an address-to-domain upgrade and a
// conflicting domain are both reported.
func TestObserveLogsChanges(t *testing.T) {
	t.Setenv("DOMAIN", "")
	testOptions(t, learningOptions())
	lines := silenceLog(t)

	Observe("203.0.113.10", "http")
	Observe("app.myapp.com", "https")
	Observe("app.otherapp.com", "https")

	if len(*lines) < 3 {
		t.Fatalf("expected an upgrade, a conflict, and a proto change, got %v", *lines)
	}
}

// TestConfiguredDomainsWin checks that an explicit DOMAIN list disables
// learning and drives the base, wildcard, and primary values.
func TestConfiguredDomainsWin(t *testing.T) {
	opts := learningOptions()
	opts.Domains = []string{"myapp.com", "www.myapp.com", "api.myapp.com"}
	testOptions(t, opts)
	silenceLog(t)

	Observe("attacker.example.net", "https")
	if hosts := ObservedHosts(); len(hosts) != 0 {
		t.Errorf("an explicit domain list must disable learning, got %v", hosts)
	}

	if got := PrimaryDomain(); got != "myapp.com" {
		t.Errorf("PrimaryDomain = %q, want myapp.com", got)
	}
	if got := GetBaseDomain(); got != "myapp.com" {
		t.Errorf("GetBaseDomain = %q, want myapp.com", got)
	}
	if got := GetWildcardDomain(); got != "*.myapp.com" {
		t.Errorf("GetWildcardDomain = %q, want *.myapp.com", got)
	}
	if got := PrimaryFQDN(); got != "myapp.com" {
		t.Errorf("PrimaryFQDN = %q, want myapp.com", got)
	}
}

// TestResetLearning checks that a configuration reload starts from nothing.
func TestResetLearning(t *testing.T) {
	t.Setenv("DOMAIN", "")
	testOptions(t, learningOptions())
	silenceLog(t)

	Observe("app.myapp.com", "https")
	ResetLearning()

	if hosts := ObservedHosts(); len(hosts) != 0 {
		t.Errorf("after a reset ObservedHosts = %v, want none", hosts)
	}
}

// TestDomainListFromEnvironment checks the DOMAIN environment fallback and
// that invalid entries are skipped rather than fatal.
func TestDomainListFromEnvironment(t *testing.T) {
	testOptions(t, Options{ProjectName: "cashp"})

	t.Setenv("DOMAIN", " myapp.com , www.myapp.com ")

	want := []string{"myapp.com", "www.myapp.com"}
	if got := DomainList(); !reflect.DeepEqual(got, want) {
		t.Errorf("DomainList = %v, want %v", got, want)
	}

	t.Setenv("DOMAIN", "not-a-domain,api.example.com")
	if got := PrimaryDomain(); got != "api.example.com" {
		t.Errorf("PrimaryDomain = %q, want the first valid entry", got)
	}

	t.Setenv("DOMAIN", "")
	if got := DomainList(); len(got) != 0 {
		t.Errorf("with no DOMAIN set DomainList = %v, want none", got)
	}
}

// TestDetectFQDN checks the configured-domain shortcut and the guarantee
// that detection always yields a usable name.
func TestDetectFQDN(t *testing.T) {
	opts := Options{ProjectName: "cashp", Domains: []string{"api.example.com"}}
	testOptions(t, opts)

	if got := DetectFQDN(); got != "api.example.com" {
		t.Errorf("DetectFQDN = %q, want api.example.com", got)
	}

	testOptions(t, Options{ProjectName: "cashp", DevMode: true})
	t.Setenv("DOMAIN", "")
	if got := DetectFQDN(); got == "" {
		t.Error("DetectFQDN must never return an empty string")
	}
}

// TestIsPublicIP checks the globally routable classification.
func TestIsPublicIP(t *testing.T) {
	public := []string{"203.0.113.10", "8.8.8.8", "2606:4700:4700::1111"}
	for _, value := range public {
		if !IsPublicIP(net.ParseIP(value)) {
			t.Errorf("%s must be public", value)
		}
	}

	private := []string{
		"10.0.0.1", "172.16.0.1", "192.168.1.1", "127.0.0.1",
		"169.254.1.1", "::1", "fd00::1", "fe80::1", "224.0.0.1",
	}
	for _, value := range private {
		if IsPublicIP(net.ParseIP(value)) {
			t.Errorf("%s must not be public", value)
		}
	}

	if IsPublicIP(nil) {
		t.Error("a nil address is never public")
	}
}
