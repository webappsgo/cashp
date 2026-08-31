package netinfo

import (
	"errors"
	"net"
	"strings"
)

// devOnlyTLDs are the internal suffixes that are valid in development mode
// and rejected in production (RFC 6761 plus the common internal names).
var devOnlyTLDs = map[string]bool{
	"localhost": true, "test": true, "example": true, "invalid": true,
	"local": true, "lan": true, "internal": true, "home": true,
	"localdomain": true, "home.arpa": true, "intranet": true,
	"corp": true, "private": true,
}

// overlayTLDs are app-managed overlay network suffixes. They are always
// valid and are never set through the DOMAIN environment variable.
var overlayTLDs = []string{".onion", ".i2p", ".exit"}

// multiLabelSuffixes are the public suffixes made of more than one label
// that this project needs to resolve correctly (co.uk, com.au, and the
// like). golang.org/x/net/publicsuffix carries the full Mozilla list; this
// table is the dependency-free subset covering the common cases, and
// PublicSuffixFunc below lets a caller substitute the full implementation.
var multiLabelSuffixes = map[string]bool{
	"co.uk": true, "org.uk": true, "me.uk": true, "ac.uk": true,
	"gov.uk": true, "net.uk": true, "sch.uk": true, "ltd.uk": true,
	"com.au": true, "net.au": true, "org.au": true, "edu.au": true,
	"gov.au": true, "id.au": true, "asn.au": true,
	"co.nz": true, "net.nz": true, "org.nz": true, "govt.nz": true,
	"co.za": true, "org.za": true, "net.za": true, "gov.za": true,
	"com.br": true, "net.br": true, "org.br": true, "gov.br": true,
	"co.jp": true, "or.jp": true, "ne.jp": true, "ac.jp": true, "go.jp": true,
	"com.cn": true, "net.cn": true, "org.cn": true, "gov.cn": true,
	"co.in": true, "net.in": true, "org.in": true, "gov.in": true,
	"com.mx": true, "org.mx": true, "gob.mx": true,
	"co.kr": true, "or.kr": true, "ne.kr": true,
	"com.tr": true, "net.tr": true, "org.tr": true,
	"com.sg": true, "com.hk": true, "com.tw": true, "com.ar": true,
	"com.pl": true, "com.ua": true, "com.ru": true, "co.il": true,
	"home.arpa": true, "in-addr.arpa": true, "ip6.arpa": true,
	"github.io": true, "gitlab.io": true, "pages.dev": true,
	"workers.dev": true, "herokuapp.com": true,
}

// ErrNoEffectiveTLDPlusOne is returned when a host is only a public suffix
// and therefore carries no registrable domain.
var ErrNoEffectiveTLDPlusOne = errors.New("host has no effective TLD plus one")

// PublicSuffixFunc returns the public suffix of a domain and whether that
// suffix is managed by ICANN. It defaults to the built-in table and can be
// replaced with publicsuffix.PublicSuffix when golang.org/x/net is present.
var PublicSuffixFunc = builtinPublicSuffix

// EffectiveTLDPlusOneFunc returns the registrable domain. It defaults to
// the built-in implementation and can be replaced with
// publicsuffix.EffectiveTLDPlusOne.
var EffectiveTLDPlusOneFunc = builtinEffectiveTLDPlusOne

// IsValidHost reports whether a host may be used as the FQDN. IP addresses
// and single-label names are always rejected; localhost, dev-only TLDs, and
// the project-name TLD are accepted in development mode only; production
// additionally requires an ICANN suffix and a full eTLD+1.
func IsValidHost(host string, devMode bool, projectName string) bool {
	lower := strings.ToLower(strings.TrimSpace(host))

	if lower == "" {
		return false
	}

	// Reject IP addresses always, bracketed IPv6 literals included.
	if net.ParseIP(strings.Trim(lower, "[]")) != nil {
		return false
	}

	if lower == "localhost" {
		return devMode
	}

	// Must contain at least one dot: single-label names are never valid.
	if !strings.Contains(lower, ".") {
		return false
	}

	// Overlay network suffixes are app-managed and always valid.
	for _, suffix := range overlayTLDs {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}

	// The project name doubles as a dev-only TLD (for example app.cashp).
	if projectName != "" && strings.HasSuffix(lower, "."+strings.ToLower(projectName)) {
		return devMode
	}

	suffix, icann := PublicSuffixFunc(lower)

	if devOnlyTLDs[suffix] {
		return devMode
	}

	if !devMode && !icann {
		return false
	}

	etldPlusOne, err := EffectiveTLDPlusOneFunc(lower)
	if err != nil {
		return false
	}
	return etldPlusOne != ""
}

// IsValidSSLHost reports whether a host may be used for a Let's Encrypt
// certificate. The host must be publicly resolvable, so production
// validation applies and .onion addresses are rejected: Tor already
// provides end-to-end encryption and is not publicly resolvable.
func IsValidSSLHost(host string) bool {
	lower := strings.ToLower(strings.TrimSpace(host))

	if strings.HasSuffix(lower, ".onion") {
		return false
	}

	return IsValidHost(host, false, "")
}

// builtinPublicSuffix resolves the public suffix using the built-in table.
// A suffix is treated as ICANN-managed when it is not a documented
// dev-only or overlay suffix and it looks like a real TLD: at least two
// characters, letters only, or an IDN punycode label.
func builtinPublicSuffix(domain string) (string, bool) {
	lower := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if lower == "" {
		return "", false
	}

	labels := strings.Split(lower, ".")

	// Longest match first so home.arpa wins over arpa.
	for size := 3; size >= 2; size-- {
		if len(labels) < size {
			continue
		}
		candidate := strings.Join(labels[len(labels)-size:], ".")
		if multiLabelSuffixes[candidate] || devOnlyTLDs[candidate] {
			return candidate, isICANNSuffix(candidate)
		}
	}

	last := labels[len(labels)-1]
	return last, isICANNSuffix(last)
}

// isICANNSuffix reports whether a suffix belongs to the ICANN section of
// the public suffix list.
func isICANNSuffix(suffix string) bool {
	if devOnlyTLDs[suffix] {
		return false
	}
	for _, overlay := range overlayTLDs {
		if suffix == strings.TrimPrefix(overlay, ".") {
			return false
		}
	}

	last := suffix
	if idx := strings.LastIndexByte(suffix, '.'); idx >= 0 {
		last = suffix[idx+1:]
	}
	if len(last) < 2 {
		return false
	}
	if strings.HasPrefix(last, "xn--") {
		return true
	}
	for _, r := range last {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// builtinEffectiveTLDPlusOne returns the registrable domain: the label
// immediately left of the public suffix, plus the suffix itself.
func builtinEffectiveTLDPlusOne(domain string) (string, error) {
	lower := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if lower == "" {
		return "", ErrNoEffectiveTLDPlusOne
	}

	suffix, _ := PublicSuffixFunc(lower)
	if suffix == "" || lower == suffix {
		return "", ErrNoEffectiveTLDPlusOne
	}

	remainder := strings.TrimSuffix(lower, "."+suffix)
	if remainder == "" {
		return "", ErrNoEffectiveTLDPlusOne
	}

	labels := strings.Split(remainder, ".")
	return labels[len(labels)-1] + "." + suffix, nil
}

// BaseDomainOf returns the registrable domain of a host, or an empty string
// when the host has none.
func BaseDomainOf(host string) string {
	etldPlusOne, err := EffectiveTLDPlusOneFunc(strings.ToLower(strings.TrimSpace(host)))
	if err != nil {
		return ""
	}
	return etldPlusOne
}
