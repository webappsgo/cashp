package hosting

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// Length ceilings applied before any value reaches a template.
const (
	maxDomainLen    = 253
	maxLabelLen     = 63
	maxIDLen        = 64
	maxNameLen      = 63
	maxTXTLen       = 4096
	maxEnvKeyLen    = 64
	maxEnvValueLen  = 4096
	maxLocalPartLen = 64
	maxTTL          = 604800
	minTTL          = 60

	maxGitRemoteLen    = 512
	maxImageRefLen     = 255
	maxCommandTokenLen = 256
)

// PHPVersionNone marks a site that runs no PHP-FPM pool at all.
const PHPVersionNone = "none"

// phpVersions is the allowlist of PHP-FPM versions IDEA.md requires
// ("multiple concurrent PHP versions (5.6 through latest)"). Availability per
// distro is a separate host concern; this list bounds what may be requested.
var phpVersions = map[string]bool{
	PHPVersionNone: true,
	"5.6":          true,
	"7.0":          true,
	"7.1":          true,
	"7.2":          true,
	"7.3":          true,
	"7.4":          true,
	"8.0":          true,
	"8.1":          true,
	"8.2":          true,
	"8.3":          true,
	"8.4":          true,
	"8.5":          true,
}

// Runtimes recognised by the PaaS auto-detector, exactly the languages named
// in IDEA.md: Node.js, Python, Ruby, PHP, Go, Java, .NET, and Rust.
const (
	RuntimeNode   = "node"
	RuntimePython = "python"
	RuntimeRuby   = "ruby"
	RuntimePHP    = "php"
	RuntimeGo     = "go"
	RuntimeJava   = "java"
	RuntimeDotNet = "dotnet"
	RuntimeRust   = "rust"
	RuntimeStatic = "static"
)

// runtimes is the allowlist backing ValidateRuntime.
var runtimes = map[string]bool{
	RuntimeNode:   true,
	RuntimePython: true,
	RuntimeRuby:   true,
	RuntimePHP:    true,
	RuntimeGo:     true,
	RuntimeJava:   true,
	RuntimeDotNet: true,
	RuntimeRust:   true,
	RuntimeStatic: true,
}

// DNS record types cashp manages in a BIND zone.
const (
	RecordA     = "A"
	RecordAAAA  = "AAAA"
	RecordCNAME = "CNAME"
	RecordMX    = "MX"
	RecordTXT   = "TXT"
	RecordSRV   = "SRV"
	RecordNS    = "NS"
	RecordCAA   = "CAA"
	RecordPTR   = "PTR"
)

// recordTypes is the allowlist backing ValidateRecordType.
var recordTypes = map[string]bool{
	RecordA:     true,
	RecordAAAA:  true,
	RecordCNAME: true,
	RecordMX:    true,
	RecordTXT:   true,
	RecordSRV:   true,
	RecordNS:    true,
	RecordCAA:   true,
	RecordPTR:   true,
}

// caaTags is the allowlist of CAA property tags (RFC 8659).
var caaTags = map[string]bool{"issue": true, "issuewild": true, "iodef": true}

// invalid builds the single validation error shape used across the package.
// The field name is safe to return; the offending value never is, because a
// hostile value echoed back into an API response is an injection vector of
// its own.
func invalid(field, reason string) *apperr.Error {
	return apperr.New(apperr.CodeValidation, 422, fmt.Sprintf("%s is not valid: %s", field, reason)).
		WithDetails(map[string]any{"field": field})
}

// ValidateID accepts an internal identifier: ASCII alphanumerics, dash, and
// underscore only, so an id can never traverse a path or open a directive.
func ValidateID(field, v string) error {
	if v == "" {
		return invalid(field, "must not be empty")
	}
	if len(v) > maxIDLen {
		return invalid(field, "is too long")
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-' || c == '_':
			if i == 0 {
				return invalid(field, "must start with a letter or digit")
			}
		default:
			return invalid(field, "contains an unsupported character")
		}
	}
	return nil
}

// ValidateName accepts a tenant-chosen short name for a site or an app:
// lowercase alphanumerics and single dashes, never leading or trailing.
func ValidateName(field, v string) error {
	if len(v) < 2 || len(v) > maxNameLen {
		return invalid(field, "must be between 2 and 63 characters")
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-':
			if i == 0 || i == len(v)-1 || v[i-1] == '-' {
				return invalid(field, "has a misplaced dash")
			}
		default:
			return invalid(field, "may only contain lowercase letters, digits, and dashes")
		}
	}
	return nil
}

// ValidateDomain normalises and validates a fully qualified domain name. It
// returns the lowercase form without a trailing dot. Only LDH labels (plus
// the xn-- ACE prefix) survive, which is exactly what a zone file, an nginx
// server_name, and a Postfix map can hold without escaping.
func ValidateDomain(raw string) (string, error) {
	d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if d == "" {
		return "", invalid("domain", "must not be empty")
	}
	if len(d) > maxDomainLen {
		return "", invalid("domain", "is too long")
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return "", invalid("domain", "must contain at least two labels")
	}
	for _, l := range labels {
		if err := validateLabel("domain", l, false); err != nil {
			return "", err
		}
	}
	if _, err := strconv.Atoi(strings.ReplaceAll(d, ".", "")); err == nil {
		return "", invalid("domain", "must not be numeric")
	}
	return d, nil
}

// validateLabel checks a single DNS label. Underscore-prefixed labels are
// permitted only where allowUnderscore is set (service labels such as
// _dmarc or _domainkey inside a record name).
func validateLabel(field, l string, allowUnderscore bool) error {
	if l == "" {
		return invalid(field, "has an empty label")
	}
	if len(l) > maxLabelLen {
		return invalid(field, "has a label longer than 63 characters")
	}
	for i := 0; i < len(l); i++ {
		c := l[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-':
			if i == 0 || i == len(l)-1 {
				return invalid(field, "has a label starting or ending with a dash")
			}
		case c == '_':
			if !allowUnderscore || i != 0 {
				return invalid(field, "has a misplaced underscore")
			}
		default:
			return invalid(field, "has a label with an unsupported character")
		}
	}
	return nil
}

// ValidateRecordName normalises the owner name of a DNS record relative to
// its zone. "@" means the zone apex; a single leading "*" label is a
// wildcard; underscore-prefixed service labels are allowed.
func ValidateRecordName(raw string) (string, error) {
	n := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if n == "" || n == "@" {
		return "@", nil
	}
	if len(n) > maxDomainLen {
		return "", invalid("record name", "is too long")
	}
	labels := strings.Split(n, ".")
	for i, l := range labels {
		if l == "*" && i == 0 {
			continue
		}
		if err := validateLabel("record name", l, true); err != nil {
			return "", err
		}
	}
	return n, nil
}

// ValidateHostmaster checks the SOA responsible-party local part.
func ValidateHostmaster(v string) error {
	if v == "" {
		return invalid("hostmaster", "must not be empty")
	}
	if strings.Contains(v, "@") {
		local, domain, _ := strings.Cut(v, "@")
		if err := ValidateLocalPart(local); err != nil {
			return err
		}
		if _, err := ValidateDomain(domain); err != nil {
			return err
		}
		return nil
	}
	return ValidateLocalPart(v)
}

// ValidateLocalPart checks the local part of a mailbox address. The allowed
// set is deliberately narrower than RFC 5321 so a local part can be written
// into a Postfix map or a Dovecot passwd-file line without escaping.
func ValidateLocalPart(raw string) error {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" || len(v) > maxLocalPartLen {
		return invalid("mailbox", "must be between 1 and 64 characters")
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '.' || c == '-' || c == '_' || c == '+':
			if i == 0 || i == len(v)-1 {
				return invalid("mailbox", "must start and end with a letter or digit")
			}
			if c == '.' && v[i-1] == '.' {
				return invalid("mailbox", "must not contain consecutive dots")
			}
		default:
			return invalid("mailbox", "contains an unsupported character")
		}
	}
	return nil
}

// ValidatePHPVersion checks a requested PHP-FPM version against the allowlist.
func ValidatePHPVersion(v string) error {
	if !phpVersions[v] {
		return invalid("php version", "is not a supported version")
	}
	return nil
}

// ValidateRuntime checks a PaaS runtime against the allowlist.
func ValidateRuntime(v string) error {
	if !runtimes[v] {
		return invalid("runtime", "is not a supported runtime")
	}
	return nil
}

// ValidateRecordType checks a DNS record type against the allowlist.
func ValidateRecordType(v string) error {
	if !recordTypes[strings.ToUpper(v)] {
		return invalid("record type", "is not a supported type")
	}
	return nil
}

// ValidateTTL bounds a record TTL.
func ValidateTTL(ttl int64) error {
	if ttl < minTTL || ttl > maxTTL {
		return invalid("ttl", "must be between 60 and 604800 seconds")
	}
	return nil
}

// ValidateEnvKey checks a PaaS environment variable name. Only upper-case
// POSIX names are accepted, so a key can never carry an equals sign, a quote,
// a newline, or a shell token into a process environment.
func ValidateEnvKey(k string) error {
	if k == "" || len(k) > maxEnvKeyLen {
		return invalid("env key", "must be between 1 and 64 characters")
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c == '_':
		case c >= '0' && c <= '9':
			if i == 0 {
				return invalid("env key", "must not start with a digit")
			}
		default:
			return invalid("env key", "may only contain A-Z, digits, and underscore")
		}
	}
	return nil
}

// ValidateEnvValue rejects control characters and over-long values so a value
// cannot break out of a single environment entry.
func ValidateEnvValue(v string) error {
	if len(v) > maxEnvValueLen {
		return invalid("env value", "is too long")
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] == 0x7f {
			return invalid("env value", "must not contain control characters")
		}
	}
	return nil
}

// ValidateTXTValue restricts a TXT payload to printable ASCII. Quotes and
// backslashes survive validation and are escaped by zoneTXT at render time.
func ValidateTXTValue(v string) error {
	if v == "" {
		return invalid("record value", "must not be empty")
	}
	if len(v) > maxTXTLen {
		return invalid("record value", "is too long")
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] > 0x7e {
			return invalid("record value", "must contain only printable ASCII")
		}
	}
	return nil
}

// ValidateIPv4 checks that v is a dotted-quad IPv4 address.
func ValidateIPv4(v string) error {
	ip := net.ParseIP(strings.TrimSpace(v))
	if ip == nil || ip.To4() == nil || strings.Contains(v, ":") {
		return invalid("record value", "must be an IPv4 address")
	}
	return nil
}

// ValidateIPv6 checks that v is an IPv6 address.
func ValidateIPv6(v string) error {
	ip := net.ParseIP(strings.TrimSpace(v))
	if ip == nil || ip.To4() != nil || !strings.Contains(v, ":") {
		return invalid("record value", "must be an IPv6 address")
	}
	return nil
}

// ValidateCAAValue checks the tag and value halves of a CAA record, supplied
// as `tag value` (the flags byte lives on the record's Priority field).
func ValidateCAAValue(v string) error {
	tag, value, ok := strings.Cut(strings.TrimSpace(v), " ")
	if !ok {
		return invalid("record value", "must be a CAA tag followed by a value")
	}
	if !caaTags[strings.ToLower(tag)] {
		return invalid("record value", "has an unsupported CAA tag")
	}
	return ValidateTXTValue(value)
}

// ValidateGitRemote accepts an https or ssh repository URL. A value starting
// with a dash is refused because it would reach git as an option, and the
// character set is narrow enough that no argument can be split.
func ValidateGitRemote(v string) error {
	if v == "" {
		return nil
	}
	if len(v) > maxGitRemoteLen {
		return invalid("git remote", "must be at most 512 characters")
	}
	if !strings.HasPrefix(v, "https://") && !strings.HasPrefix(v, "ssh://") {
		return invalid("git remote", "must be an https:// or ssh:// URL")
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte(":/._-~%@+", c) >= 0:
		default:
			return invalid("git remote", "contains an unsupported character")
		}
	}
	return nil
}

// ValidateGitBranch accepts a branch or tag name. Leading dashes, whitespace,
// and the ref characters git itself forbids are all rejected.
func ValidateGitBranch(v string) error {
	if v == "" {
		return nil
	}
	if len(v) > maxNameLen {
		return invalid("git branch", "must be at most 63 characters")
	}
	if strings.HasPrefix(v, "-") || strings.HasPrefix(v, ".") || strings.HasSuffix(v, ".") {
		return invalid("git branch", "must not start with a dash or dot")
	}
	if strings.Contains(v, "..") {
		return invalid("git branch", "must not contain two consecutive dots")
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == '-' || c == '/':
		default:
			return invalid("git branch", "may only contain letters, digits, dot, dash, underscore, and slash")
		}
	}
	return nil
}

// ValidateImageRef accepts a container image reference. The character set is
// the one a registry reference can legally use and nothing more.
func ValidateImageRef(v string) error {
	if v == "" {
		return nil
	}
	if len(v) > maxImageRefLen {
		return invalid("image", "must be at most 255 characters")
	}
	if strings.HasPrefix(v, "-") {
		return invalid("image", "must not start with a dash")
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == '-' || c == '/' || c == ':' || c == '@':
		default:
			return invalid("image", "contains an unsupported character")
		}
	}
	return nil
}

// ValidateCommandToken accepts one argv entry of a start command. Whitespace
// and control characters are refused so a token can never split into two.
func ValidateCommandToken(v string) error {
	if v == "" {
		return invalid("command", "must not contain an empty argument")
	}
	if len(v) > maxCommandTokenLen {
		return invalid("command", "argument is too long")
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c <= 0x20 || c == 0x7f {
			return invalid("command", "argument contains whitespace or a control character")
		}
	}
	return nil
}

// ValidateQuotaMB bounds a storage quota in megabytes; zero means unlimited.
func ValidateQuotaMB(field string, mb int64) error {
	if mb < 0 || mb > 1<<24 {
		return invalid(field, "must be between 0 and 16777216 megabytes")
	}
	return nil
}
