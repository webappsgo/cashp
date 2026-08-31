package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeDomain(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  Example.COM.  ", "example.com"},
		{"example.com", "example.com"},
		{"EXAMPLE.COM", "example.com"},
	}
	for _, c := range cases {
		if got := NormalizeDomain(c.in); got != c.want {
			t.Errorf("NormalizeDomain(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidateDomainValid(t *testing.T) {
	cases := []struct {
		name           string
		in             string
		wantApex       bool
		wantWildcard   bool
	}{
		{"apex", "example.com", true, false},
		{"subdomain", "sites.example.com", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			norm, isApex, isWildcard, err := ValidateDomain(c.in, false, nil)
			if err != nil {
				t.Fatalf("ValidateDomain(%q) unexpected error: %v", c.in, err)
			}
			if norm != strings.ToLower(c.in) {
				t.Errorf("normalized = %q, want %q", norm, strings.ToLower(c.in))
			}
			if isApex != c.wantApex {
				t.Errorf("isApex = %v, want %v", isApex, c.wantApex)
			}
			if isWildcard != c.wantWildcard {
				t.Errorf("isWildcard = %v, want %v", isWildcard, c.wantWildcard)
			}
		})
	}
}

func TestValidateDomainWildcard(t *testing.T) {
	norm, isApex, isWildcard, err := ValidateDomain("*.example.com", true, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isWildcard {
		t.Error("isWildcard = false, want true")
	}
	if isApex {
		t.Error("a wildcard domain must never be reported as apex")
	}
	if norm != "*.example.com" {
		t.Errorf("normalized = %q, want *.example.com", norm)
	}
}

func TestValidateDomainWildcardRejectedWhenDisallowed(t *testing.T) {
	_, _, _, err := ValidateDomain("*.example.com", false, nil)
	if !errors.Is(err, ErrDomainWildcard) {
		t.Errorf("err = %v, want ErrDomainWildcard", err)
	}
}

func TestValidateDomainRejectsInvalid(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr error
	}{
		{"empty", "", ErrDomainEmpty},
		{"scheme", "https://example.com", ErrDomainScheme},
		{"path", "example.com/path", ErrDomainPath},
		{"space", "exa mple.com", ErrDomainPath},
		{"single label", "localhost", ErrDomainTLD},
		{"empty label", "example..com", ErrDomainLabel},
		{"leading hyphen label", "-example.com", ErrDomainLabel},
		{"trailing hyphen label", "example-.com", ErrDomainLabel},
		{"short tld", "example.c", ErrDomainTLD},
		{"numeric tld", "example.123", ErrDomainTLD},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, _, err := ValidateDomain(c.in, true, nil)
			if !errors.Is(err, c.wantErr) {
				t.Errorf("ValidateDomain(%q) err = %v, want %v", c.in, err, c.wantErr)
			}
		})
	}
}

func TestValidateDomainTooLong(t *testing.T) {
	label := strings.Repeat("a", 63)
	long := label + "." + label + "." + label + "." + label + ".co" // 4*63 + 3 dots + 1 dot + 2 = 258 chars
	if len(long) <= 253 {
		t.Fatalf("test fixture is not actually over the 253 char limit: %d", len(long))
	}
	_, _, _, err := ValidateDomain(long, false, nil)
	if !errors.Is(err, ErrDomainTooLong) {
		t.Errorf("err = %v, want ErrDomainTooLong", err)
	}
}

func TestValidateDomainReserved(t *testing.T) {
	_, _, _, err := ValidateDomain("example.com", false, []string{"example.com"})
	if !errors.Is(err, ErrDomainReserved) {
		t.Errorf("err = %v, want ErrDomainReserved", err)
	}
	// Suffix match: a subdomain of a reserved domain is also reserved.
	_, _, _, err = ValidateDomain("sites.example.com", false, []string{"example.com"})
	if !errors.Is(err, ErrDomainReserved) {
		t.Errorf("subdomain of reserved domain: err = %v, want ErrDomainReserved", err)
	}
}

func TestValidateDomainOverlayHostRejected(t *testing.T) {
	_, _, _, err := ValidateDomain("abcdefghijklmnop.onion", false, nil)
	if !errors.Is(err, ErrDomainOverlay) {
		t.Errorf("err = %v, want ErrDomainOverlay", err)
	}
}

func TestNewVerificationTokenFormat(t *testing.T) {
	tok, err := NewVerificationToken()
	if err != nil {
		t.Fatalf("NewVerificationToken: %v", err)
	}
	if !strings.HasPrefix(tok, "cashp-verify=") {
		t.Errorf("token %q missing cashp-verify= prefix", tok)
	}
	tok2, err := NewVerificationToken()
	if err != nil {
		t.Fatalf("NewVerificationToken: %v", err)
	}
	if tok == tok2 {
		t.Error("two calls to NewVerificationToken produced the same token")
	}
}

func TestVerificationRecordName(t *testing.T) {
	cases := []struct{ prefix, domain, want string }{
		{"_cashp-verify", "example.com", "_cashp-verify.example.com"},
		{"_cashp-verify", "*.example.com", "_cashp-verify.example.com"},
	}
	for _, c := range cases {
		if got := VerificationRecordName(c.prefix, c.domain); got != c.want {
			t.Errorf("VerificationRecordName(%q, %q) = %q, want %q", c.prefix, c.domain, got, c.want)
		}
	}
}

func TestSelectChallenge(t *testing.T) {
	cases := []struct {
		name        string
		isWildcard  bool
		dnsProvider string
		httpOnly    bool
		want        string
	}{
		{"wildcard forces dns01", true, "", false, ChallengeDNS01},
		{"dns provider forces dns01", false, "cloudflare", false, ChallengeDNS01},
		{"http only", false, "", true, ChallengeHTTP01},
		{"default tls-alpn", false, "", false, ChallengeTLSALPN01},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SelectChallenge(c.isWildcard, c.dnsProvider, c.httpOnly); got != c.want {
				t.Errorf("SelectChallenge(%v,%q,%v) = %q, want %q", c.isWildcard, c.dnsProvider, c.httpOnly, got, c.want)
			}
		})
	}
}

func TestSSLEligible(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"example.com", true},
		{"sites.example.com", true},
		{"abcdefghijklmnop.onion", false},
		{"example.b32.i2p", false},
	}
	for _, c := range cases {
		if got := SSLEligible(c.in); got != c.want {
			t.Errorf("SSLEligible(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// fakeResolver lets domain-ownership tests substitute canned DNS answers
// instead of touching the network, per domain.go's documented seam.
type fakeResolver struct {
	txt []string
	err error
}

func (f fakeResolver) LookupTXT(_ context.Context, _ string) ([]string, error) {
	return f.txt, f.err
}

func newTestVerificationToken(t *testing.T) string {
	t.Helper()
	tok, err := NewVerificationToken()
	if err != nil {
		t.Fatalf("NewVerificationToken: %v", err)
	}
	return tok
}

func TestVerifyDomainOwnershipSuccess(t *testing.T) {
	token := newTestVerificationToken(t)
	resolver := fakeResolver{txt: []string{"unrelated-record", token}}
	err := VerifyDomainOwnership(context.Background(), resolver, "_cashp-verify", "example.com", token)
	if err != nil {
		t.Errorf("VerifyDomainOwnership() = %v, want nil", err)
	}
}

func TestVerifyDomainOwnershipMissingRecord(t *testing.T) {
	token := newTestVerificationToken(t)
	resolver := fakeResolver{txt: []string{"some-other-value"}}
	err := VerifyDomainOwnership(context.Background(), resolver, "_cashp-verify", "example.com", token)
	if !errors.Is(err, ErrDomainVerifyMiss) {
		t.Errorf("err = %v, want ErrDomainVerifyMiss", err)
	}
}

func TestVerifyDomainOwnershipNoRecords(t *testing.T) {
	token := newTestVerificationToken(t)
	resolver := fakeResolver{txt: nil}
	err := VerifyDomainOwnership(context.Background(), resolver, "_cashp-verify", "example.com", token)
	if !errors.Is(err, ErrDomainVerifyMiss) {
		t.Errorf("err = %v, want ErrDomainVerifyMiss", err)
	}
}

func TestVerifyDomainOwnershipResolverError(t *testing.T) {
	token := newTestVerificationToken(t)
	resolver := fakeResolver{err: errors.New("dns lookup timed out")}
	err := VerifyDomainOwnership(context.Background(), resolver, "_cashp-verify", "example.com", token)
	if err == nil {
		t.Error("VerifyDomainOwnership must return an error when the resolver fails")
	}
}
