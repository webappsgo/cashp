package security

import (
	"errors"
	"fmt"
	"html"
	"net"
	"net/url"
	"path/filepath"
	"strings"
)

// Input guard errors.
var (
	// ErrPathEscapesBase is returned when an untrusted path resolves outside its base directory.
	ErrPathEscapesBase = errors.New("security: path escapes base directory")
	// ErrPathNotRelative is returned when an untrusted path is absolute.
	ErrPathNotRelative = errors.New("security: path must be relative")
	// ErrPathNullByte is returned when a path contains a NUL byte.
	ErrPathNullByte = errors.New("security: path contains null byte")
	// ErrEmptyBase is returned when SafeJoin is called without a base directory.
	ErrEmptyBase = errors.New("security: base directory must not be empty")
	// ErrSchemeNotAllowed is returned when an outbound URL uses a scheme other than http or https.
	ErrSchemeNotAllowed = errors.New("security: outbound URL scheme not allowed")
	// ErrHostNotAllowed is returned when an outbound URL targets a private, loopback, or internal host.
	ErrHostNotAllowed = errors.New("security: outbound URL host not allowed")
	// ErrInvalidURL is returned when an outbound URL cannot be parsed or has no host.
	ErrInvalidURL = errors.New("security: invalid outbound URL")
)

// MaskedValue is the replacement written in place of any secret value.
const MaskedValue = "xxxxx"

// SensitiveParamNames are the query-parameter and field names whose values
// are redacted before a URL is logged or returned, per the Output
// Sanitization Pipeline in AI.md PART 11.
var SensitiveParamNames = []string{
	"access_token",
	"api_key",
	"apikey",
	"auth",
	"code",
	"key",
	"password",
	"pwd",
	"refresh_token",
	"secret",
	"session",
	"token",
}

// internalHostSuffixes are hostname suffixes that always denote an
// internal or overlay-network destination and are never a valid outbound
// target for visitor-influenced requests.
var internalHostSuffixes = []string{
	".b32.i2p",
	".i2p",
	".internal",
	".local",
	".localhost",
	".onion",
}

// SafeJoin joins an untrusted relative path onto a trusted base directory,
// guaranteeing the result stays inside base. It rejects absolute paths,
// NUL bytes, and any traversal that would escape base after cleaning.
func SafeJoin(base, untrusted string) (string, error) {
	if base == "" {
		return "", ErrEmptyBase
	}
	if strings.ContainsRune(untrusted, 0) || strings.ContainsRune(base, 0) {
		return "", ErrPathNullByte
	}
	if filepath.IsAbs(untrusted) || strings.HasPrefix(untrusted, "/") || strings.HasPrefix(untrusted, `\`) {
		return "", ErrPathNotRelative
	}

	cleanBase := filepath.Clean(base)
	joined := filepath.Clean(filepath.Join(cleanBase, untrusted))

	if joined != cleanBase && !strings.HasPrefix(joined, cleanBase+string(filepath.Separator)) {
		return "", ErrPathEscapesBase
	}

	return joined, nil
}

// IsPrivateOrLoopbackIP reports whether ip is anything other than a
// globally routable public address — loopback, RFC1918 and equivalent
// private ranges, link-local, multicast, unspecified, or the IPv4
// broadcast address all return true.
func IsPrivateOrLoopbackIP(ip net.IP) bool {
	if ip == nil {
		return true
	}

	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}

	if ip4 := ip.To4(); ip4 != nil {
		// 100.64.0.0/10 carrier-grade NAT, 192.0.0.0/24 IETF protocol assignments, 255.255.255.255 broadcast.
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
		if ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 0 {
			return true
		}
		if ip4.Equal(net.IPv4bcast) {
			return true
		}
		return false
	}

	// fc00::/7 unique local addresses.
	if len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc {
		return true
	}

	return false
}

// ValidateOutboundURL is the SSRF guard applied before cashp fetches any
// URL that a request could have influenced. It enforces an http/https
// scheme, rejects internal and overlay-network hostnames, and rejects any
// host that resolves to a private or loopback address.
func ValidateOutboundURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidURL, err)
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return ErrSchemeNotAllowed
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return ErrInvalidURL
	}
	if host == "localhost" {
		return ErrHostNotAllowed
	}
	for _, suffix := range internalHostSuffixes {
		if strings.HasSuffix(host, suffix) {
			return ErrHostNotAllowed
		}
	}

	if ip := net.ParseIP(host); ip != nil {
		if IsPrivateOrLoopbackIP(ip) {
			return ErrHostNotAllowed
		}
		return nil
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("%w: dns lookup failed", ErrHostNotAllowed)
	}
	if len(ips) == 0 {
		return ErrHostNotAllowed
	}
	for _, ip := range ips {
		if IsPrivateOrLoopbackIP(ip) {
			return ErrHostNotAllowed
		}
	}

	return nil
}

// MaskSecret replaces a secret value with MaskedValue while preserving the
// key name of a "key=value" or "key: value" pair, so masked output stays
// diagnosable. A bare value with no key is replaced entirely.
func MaskSecret(s string) string {
	if s == "" {
		return ""
	}

	if i := strings.Index(s, "="); i > 0 {
		return s[:i+1] + MaskedValue
	}
	if i := strings.Index(s, ": "); i > 0 {
		return s[:i+2] + MaskedValue
	}
	if i := strings.Index(s, ":"); i > 0 {
		return s[:i+1] + MaskedValue
	}

	return MaskedValue
}

// IsSensitiveName reports whether a field or parameter name should have
// its value redacted before logging or serialization. Matching is
// case-insensitive and substring-based so "user_api_key" is caught too.
func IsSensitiveName(name string) bool {
	lower := strings.ToLower(name)
	for _, s := range SensitiveParamNames {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// RedactURL parses raw and replaces the value of every sensitive query
// parameter with MaskedValue. A URL that cannot be parsed is replaced
// entirely rather than passed through.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return MaskedValue
	}

	q := u.Query()
	if len(q) == 0 {
		return u.String()
	}

	for name := range q {
		if IsSensitiveName(name) {
			q.Set(name, MaskedValue)
		}
	}
	u.RawQuery = q.Encode()

	return u.String()
}

// EscapeHTML escapes untrusted text for safe interpolation into an HTML
// document and strips the C0 control characters that break parsers and
// log lines. It is never a substitute for template auto-escaping — it is
// for the paths that build strings outside a template.
func EscapeHTML(s string) string {
	return html.EscapeString(StripControlChars(s))
}

// StripControlChars removes C0 control characters and DEL from s, keeping
// tab, newline, and carriage return.
func StripControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return r
		case r < 0x20 || r == 0x7f:
			return -1
		default:
			return r
		}
	}, s)
}
