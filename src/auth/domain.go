package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/tlsmgr"
)

// Domain validation errors.
var (
	ErrDomainEmpty      = errors.New("enter a domain name")
	ErrDomainScheme     = errors.New("enter the domain only, without http:// or https://")
	ErrDomainPath       = errors.New("enter the domain only, without a path or port")
	ErrDomainTooLong    = errors.New("domain name is too long")
	ErrDomainLabel      = errors.New("each part of the domain must be 1-63 characters of letters, digits, or hyphen")
	ErrDomainTLD        = errors.New("domain must include a top-level domain, for example example.com")
	ErrDomainWildcard   = errors.New("wildcard domains are not permitted on this server")
	ErrDomainOverlay    = errors.New("overlay network addresses cannot be added as custom domains")
	ErrDomainReserved   = errors.New("that domain is reserved")
	ErrDomainVerifyMiss = errors.New("verification record not found")
)

// labelRunes reports whether a DNS label uses only permitted characters.
func labelRunes(label string) bool {
	for i, r := range label {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i != 0 && i != len(label)-1:
		default:
			return false
		}
	}
	return true
}

// NormalizeDomain lowercases, trims, and strips a trailing dot from a domain name.
func NormalizeDomain(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	return strings.TrimSuffix(domain, ".")
}

// ValidateDomain checks a candidate custom domain and reports whether it is an apex
// name and whether it is a wildcard. Overlay hosts are rejected outright: they resolve
// through Tor or I2P rather than DNS, so ownership cannot be proven with a TXT record
// and no public CA will issue for them.
func ValidateDomain(domain string, allowWildcards bool, reserved []string) (normalized string, isApex, isWildcard bool, err error) {
	normalized = NormalizeDomain(domain)
	if normalized == "" {
		return "", false, false, ErrDomainEmpty
	}
	if strings.Contains(normalized, "://") {
		return "", false, false, ErrDomainScheme
	}
	if strings.ContainsAny(normalized, "/?#@ ") || strings.Contains(normalized, ":") {
		return "", false, false, ErrDomainPath
	}
	if len(normalized) > 253 {
		return "", false, false, ErrDomainTooLong
	}

	isWildcard = strings.HasPrefix(normalized, "*.")
	if isWildcard {
		if !allowWildcards {
			return "", false, false, ErrDomainWildcard
		}
		normalized = strings.TrimPrefix(normalized, "*.")
	}

	labels := strings.Split(normalized, ".")
	if len(labels) < 2 {
		return "", false, false, ErrDomainTLD
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || !labelRunes(label) {
			return "", false, false, ErrDomainLabel
		}
	}
	tld := labels[len(labels)-1]
	if len(tld) < 2 {
		return "", false, false, ErrDomainTLD
	}
	for _, r := range tld {
		if r < 'a' || r > 'z' {
			return "", false, false, ErrDomainTLD
		}
	}

	if tlsmgr.IsOverlayHost(normalized) {
		return "", false, false, ErrDomainOverlay
	}

	full := normalized
	if isWildcard {
		full = "*." + normalized
	}
	for _, r := range reserved {
		r = NormalizeDomain(r)
		if r == "" {
			continue
		}
		if normalized == r || strings.HasSuffix(normalized, "."+r) {
			return "", false, false, ErrDomainReserved
		}
	}

	isApex = len(labels) == 2 && !isWildcard
	return full, isApex, isWildcard, nil
}

// NewVerificationToken generates the random value the owner must publish in DNS.
func NewVerificationToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate verification token: %w", err)
	}
	return "cashp-verify=" + hex.EncodeToString(buf), nil
}

// VerificationRecordName returns the fully qualified name of the ownership TXT record.
// Ownership is proven only with a TXT record: a CNAME, A or AAAA record proves that
// traffic points here, not that the requester controls the zone, so those are never
// accepted as proof.
func VerificationRecordName(prefix, domain string) string {
	domain = NormalizeDomain(domain)
	domain = strings.TrimPrefix(domain, "*.")
	return prefix + "." + domain
}

// DNSResolver is the lookup surface the verifier needs. Tests substitute a fake.
type DNSResolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// defaultResolver uses the system resolver.
type defaultResolver struct{}

func (defaultResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, name)
}

// VerifyDomainOwnership looks up the ownership TXT record and compares it to the
// expected token in constant time. The lookup is bounded so a hostile or slow
// nameserver cannot hold a request handler or a scheduler worker open.
func VerifyDomainOwnership(ctx context.Context, resolver DNSResolver, prefix, domain, token string) error {
	if resolver == nil {
		resolver = defaultResolver{}
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	records, err := resolver.LookupTXT(lookupCtx, VerificationRecordName(prefix, domain))
	if err != nil {
		return ErrDomainVerifyMiss
	}
	matched := 0
	want := []byte(token)
	for _, rec := range records {
		got := []byte(strings.TrimSpace(rec))
		if len(got) != len(want) {
			continue
		}
		matched |= subtle.ConstantTimeCompare(got, want)
	}
	if matched != 1 {
		return ErrDomainVerifyMiss
	}
	return nil
}

// SelectChallenge picks the ACME challenge for a domain, per AI.md PART 36:
// a wildcard or a configured DNS provider forces dns-01, otherwise tls-alpn-01 is
// preferred and http-01 is the fallback when port 80 is the only reachable path.
func SelectChallenge(isWildcard bool, dnsProvider string, httpOnly bool) string {
	if isWildcard || strings.TrimSpace(dnsProvider) != "" {
		return ChallengeDNS01
	}
	if httpOnly {
		return ChallengeHTTP01
	}
	return ChallengeTLSALPN01
}

// SSLEligible reports whether a domain may be issued a certificate and served HSTS.
// Overlay hosts are excluded from both: no public CA can validate them, and pinning
// HSTS on a .onion or .b32.i2p address would strand the site behind a header it can
// never satisfy.
func SSLEligible(domain string) bool {
	host := tlsmgr.NormalizeHost(NormalizeDomain(strings.TrimPrefix(domain, "*.")))
	if host == "" {
		return false
	}
	return !tlsmgr.IsOverlayHost(host) && tlsmgr.ShouldSendHSTS(host)
}
