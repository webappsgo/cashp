package guard

import (
	"strconv"
	"strings"
)

// Length ceilings for the strict allowlist validators. They are deliberate
// upper bounds, not protocol maxima, so an oversized value is rejected
// before it reaches a shell argv, a filesystem call, or an SQL identifier.
const (
	// MaxIdentifierLen bounds a generic internal identifier.
	MaxIdentifierLen = 64
	// MaxUsernameLen bounds a cluster-unique username.
	MaxUsernameLen = 32
	// MinUsernameLen is the shortest acceptable username.
	MinUsernameLen = 3
	// MaxHostnameLen is the DNS name ceiling from RFC 1035.
	MaxHostnameLen = 253
	// MaxLabelLen is the DNS label ceiling from RFC 1035.
	MaxLabelLen = 63
	// MaxFilenameLen bounds a single path component.
	MaxFilenameLen = 255
	// MaxSQLIdentifierLen bounds a database, schema, table, or column name.
	MaxSQLIdentifierLen = 63
	// MaxEnvNameLen bounds an environment variable name.
	MaxEnvNameLen = 128
	// MaxEnvValueLen bounds an environment variable value.
	MaxEnvValueLen = 32768
	// MaxExecArgLen bounds a single argv element.
	MaxExecArgLen = 4096
	// MaxImageRefLen bounds an OCI image reference.
	MaxImageRefLen = 512
)

// shellMeta are the bytes that carry meaning to a shell, an argument
// parser, or a filter chain. They are never legitimate inside any value
// this package validates, so their presence alone is a rejection.
const shellMeta = "`$&;|<>()[]{}!*?~\"'\\\n\r\t #"

// deniedEnvNames are environment variables that alter how a child process
// loads code or splits words. A tenant-supplied environment may never set
// one, at any value, because doing so turns a benign exec into arbitrary
// code execution inside cashp's own root-privileged process tree.
var deniedEnvNames = map[string]struct{}{
	"BASH_ENV":              {},
	"BASH_FUNC_":            {},
	"CDPATH":                {},
	"DYLD_INSERT_LIBRARIES": {},
	"ENV":                   {},
	"GCONV_PATH":            {},
	"GLIBC_TUNABLES":        {},
	"IFS":                   {},
	"LD_AUDIT":              {},
	"LD_LIBRARY_PATH":       {},
	"LD_PRELOAD":            {},
	"NODE_OPTIONS":          {},
	"PATH":                  {},
	"PERL5LIB":              {},
	"PERL5OPT":              {},
	"PYTHONPATH":            {},
	"PYTHONSTARTUP":         {},
	"SHELL":                 {},
	"SHELLOPTS":             {},
	"ZDOTDIR":               {},
}

// reservedFilenames are path components that are never a tenant data file
// on any host cashp supports, including the Windows device names that a
// client binary could round-trip into a server-side path.
var reservedFilenames = map[string]struct{}{
	".":    {},
	"..":   {},
	"aux":  {},
	"com1": {},
	"con":  {},
	"lpt1": {},
	"nul":  {},
	"prn":  {},
}

// isASCIIPrintable reports whether every byte of s is a printable ASCII
// character. Rejecting everything else is what makes these validators
// immune to unicode-normalization and homoglyph tricks: a value that
// normalizes into an ASCII metacharacter cannot survive this check,
// because it never contained an ASCII byte to begin with.
func isASCIIPrintable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// hasShellMeta reports whether s contains any shell-significant byte.
func hasShellMeta(s string) bool {
	return strings.ContainsAny(s, shellMeta)
}

// isLowerAlnum reports whether b is a lowercase letter or a digit.
func isLowerAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

// isAlnum reports whether b is an ASCII letter or digit in either case.
func isAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// invalid builds the standard rejection for a validator, naming the field
// and the offending value in the log-only detail.
func invalid(field, value, why string) *DenyError {
	return Deny(ReasonInvalidInput, field+" "+strconv.Quote(value)+": "+why)
}

// checkCommon applies the checks every allowlist validator shares: a
// non-empty value, a length ceiling, printable ASCII only, and no embedded
// NUL. It runs before any charset-specific rule.
func checkCommon(field, value string, maxLen int) *DenyError {
	if value == "" {
		return invalid(field, value, "must not be empty")
	}
	if len(value) > maxLen {
		return invalid(field, "", "exceeds "+strconv.Itoa(maxLen)+" bytes")
	}
	if strings.ContainsRune(value, 0) {
		return invalid(field, "", "contains a null byte")
	}
	if !isASCIIPrintable(value) {
		return invalid(field, "", "contains non-printable or non-ASCII bytes")
	}
	return nil
}

// ValidateIdentifier accepts a generic internal identifier: lowercase
// alphanumerics separated by single hyphens or underscores, starting and
// ending with an alphanumeric. It is the base rule for tenant ids, network
// names, workload names, and anything else that reaches a system tool.
func ValidateIdentifier(field, value string) error {
	if err := checkCommon(field, value, MaxIdentifierLen); err != nil {
		return err
	}
	if !isLowerAlnum(value[0]) || !isLowerAlnum(value[len(value)-1]) {
		return invalid(field, value, "must start and end with a lowercase alphanumeric")
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if isLowerAlnum(c) {
			continue
		}
		if c != '-' && c != '_' {
			return invalid(field, value, "contains a character outside [a-z0-9_-]")
		}
		if value[i-1] == '-' || value[i-1] == '_' {
			return invalid(field, value, "contains consecutive separators")
		}
	}
	return nil
}

// ValidateUsername applies the identifier rule with the username length
// bounds. It enforces format only; reservation, tombstone, and
// availability decisions belong to CheckNameAvailability.
func ValidateUsername(value string) error {
	if err := checkCommon("username", value, MaxUsernameLen); err != nil {
		return err
	}
	if len(value) < MinUsernameLen {
		return invalid("username", value, "shorter than "+strconv.Itoa(MinUsernameLen)+" characters")
	}
	return ValidateIdentifier("username", value)
}

// ValidateHostname accepts a DNS hostname of one or more LDH labels. A
// trailing root dot is tolerated and stripped; every other form of
// punctuation, whitespace, or escaping is rejected.
func ValidateHostname(value string) error {
	trimmed := strings.TrimSuffix(value, ".")
	if err := checkCommon("hostname", trimmed, MaxHostnameLen); err != nil {
		return err
	}
	for _, label := range strings.Split(trimmed, ".") {
		if label == "" {
			return invalid("hostname", value, "contains an empty label")
		}
		if len(label) > MaxLabelLen {
			return invalid("hostname", value, "label exceeds "+strconv.Itoa(MaxLabelLen)+" bytes")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return invalid("hostname", value, "label starts or ends with a hyphen")
		}
		for i := 0; i < len(label); i++ {
			if !isAlnum(label[i]) && label[i] != '-' {
				return invalid("hostname", value, "label contains a character outside [A-Za-z0-9-]")
			}
		}
	}
	return nil
}

// ValidateFQDN accepts a fully qualified domain name: a valid hostname of
// at least two labels whose last label is alphabetic or an IDNA A-label.
// A bare hostname, an IP literal, or a wildcard is rejected.
func ValidateFQDN(value string) error {
	if err := ValidateHostname(value); err != nil {
		return err
	}
	trimmed := strings.TrimSuffix(value, ".")
	labels := strings.Split(trimmed, ".")
	if len(labels) < 2 {
		return invalid("fqdn", value, "needs at least two labels")
	}
	tld := strings.ToLower(labels[len(labels)-1])
	if strings.HasPrefix(tld, "xn--") {
		return nil
	}
	if len(tld) < 2 {
		return invalid("fqdn", value, "top-level label is too short")
	}
	for i := 0; i < len(tld); i++ {
		if tld[i] < 'a' || tld[i] > 'z' {
			return invalid("fqdn", value, "top-level label is not alphabetic")
		}
	}
	return nil
}

// ValidateDomainName accepts a domain a tenant may host mail, DNS, or a
// vhost for. It is an FQDN that is additionally not an overlay-network or
// reserved special-use name, so a tenant cannot claim a name whose
// resolution cashp does not actually control.
func ValidateDomainName(value string) error {
	if err := ValidateFQDN(value); err != nil {
		return err
	}
	lower := strings.ToLower(strings.TrimSuffix(value, "."))
	for _, suffix := range reservedDomainSuffixes {
		if lower == strings.TrimPrefix(suffix, ".") || strings.HasSuffix(lower, suffix) {
			return invalid("domain", value, "is a reserved or overlay-network name")
		}
	}
	return nil
}

// reservedDomainSuffixes are special-use and overlay-network names a
// tenant may never register as a hosted domain.
var reservedDomainSuffixes = []string{
	".alt",
	".b32.i2p",
	".example",
	".i2p",
	".internal",
	".invalid",
	".local",
	".localhost",
	".onion",
	".test",
}

// ValidateFilename accepts a single path component. It rejects every
// separator, the traversal components, reserved device names, and the
// leading or trailing whitespace and dots that let a name masquerade as a
// different file once a tool normalizes it.
func ValidateFilename(value string) error {
	if err := checkCommon("filename", value, MaxFilenameLen); err != nil {
		return err
	}
	if strings.ContainsAny(value, `/\`) {
		return invalid("filename", value, "contains a path separator")
	}
	if strings.Contains(value, "..") {
		return invalid("filename", value, "contains a traversal sequence")
	}
	if strings.TrimSpace(value) != value {
		return invalid("filename", value, "has leading or trailing whitespace")
	}
	if strings.HasSuffix(value, ".") {
		return invalid("filename", value, "ends with a dot")
	}
	if hasShellMeta(value) {
		return invalid("filename", value, "contains a shell metacharacter")
	}
	base := strings.ToLower(value)
	if idx := strings.IndexByte(base, '.'); idx > 0 {
		base = base[:idx]
	}
	if _, bad := reservedFilenames[base]; bad {
		return invalid("filename", value, "is a reserved name")
	}
	if _, bad := reservedFilenames[strings.ToLower(value)]; bad {
		return invalid("filename", value, "is a reserved name")
	}
	return nil
}

// ValidateSQLIdentifier accepts a database, schema, table, or column name
// for the rare statement position that cannot be parameterized. It is not
// a substitute for parameterized queries: values always bind, and only a
// structural identifier is ever routed through here.
func ValidateSQLIdentifier(field, value string) error {
	if err := checkCommon(field, value, MaxSQLIdentifierLen); err != nil {
		return err
	}
	first := value[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || first == '_') {
		return invalid(field, value, "must start with a letter or underscore")
	}
	for i := 0; i < len(value); i++ {
		if !isAlnum(value[i]) && value[i] != '_' {
			return invalid(field, value, "contains a character outside [A-Za-z0-9_]")
		}
	}
	return nil
}

// ValidateEnvVarName accepts an environment variable name for a tenant
// workload: uppercase letters, digits, and underscores, not starting with
// a digit, and never one of the loader or word-splitting variables that
// would turn a child process into arbitrary code execution.
func ValidateEnvVarName(value string) error {
	if err := checkCommon("env name", value, MaxEnvNameLen); err != nil {
		return err
	}
	first := value[0]
	if !((first >= 'A' && first <= 'Z') || first == '_') {
		return invalid("env name", value, "must start with an uppercase letter or underscore")
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			continue
		}
		return invalid("env name", value, "contains a character outside [A-Z0-9_]")
	}
	if _, bad := deniedEnvNames[value]; bad {
		return invalid("env name", value, "is a loader or word-splitting variable")
	}
	if strings.HasPrefix(value, "BASH_FUNC_") || strings.HasPrefix(value, "LD_") {
		return invalid("env name", value, "is a loader or exported-function variable")
	}
	return nil
}

// ValidateEnvVarValue accepts an environment variable value. Values are
// opaque to cashp, so the rule is structural only: no NUL, no control
// characters, no newline, and a length ceiling.
func ValidateEnvVarValue(value string) error {
	if len(value) > MaxEnvValueLen {
		return invalid("env value", "", "exceeds "+strconv.Itoa(MaxEnvValueLen)+" bytes")
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == 0 {
			return invalid("env value", "", "contains a null byte")
		}
		if c < 0x20 || c == 0x7f {
			return invalid("env value", "", "contains a control character")
		}
	}
	return nil
}

// ValidateEnvVars validates a whole environment map and returns the
// KEY=VALUE slice an exec call consumes. A single bad entry rejects the
// entire environment rather than silently dropping it.
func ValidateEnvVars(env map[string]string) ([]string, error) {
	out := make([]string, 0, len(env))
	for name, value := range env {
		if err := ValidateEnvVarName(name); err != nil {
			return nil, err
		}
		if err := ValidateEnvVarValue(value); err != nil {
			return nil, err
		}
		out = append(out, name+"="+value)
	}
	return out, nil
}

// ValidateExecArg accepts a single argv element. Because NewCommand never
// invokes a shell, metacharacters are inert here and are therefore
// permitted inside an argument body; what is rejected is anything that can
// terminate or split an argument at the syscall or option-parser layer.
func ValidateExecArg(value string) error {
	if len(value) > MaxExecArgLen {
		return invalid("argument", "", "exceeds "+strconv.Itoa(MaxExecArgLen)+" bytes")
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == 0 {
			return invalid("argument", "", "contains a null byte")
		}
		if c < 0x20 || c == 0x7f {
			return invalid("argument", "", "contains a control character")
		}
	}
	return nil
}

// ValidateImageReference accepts an OCI image reference. Registry host,
// path, tag, and digest characters are allowed; everything a shell or an
// argument parser could act on is not.
func ValidateImageReference(value string) error {
	if err := checkCommon("image", value, MaxImageRefLen); err != nil {
		return err
	}
	if strings.HasPrefix(value, "-") {
		return invalid("image", value, "starts with an option marker")
	}
	if strings.Contains(value, "..") {
		return invalid("image", value, "contains a traversal sequence")
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if isAlnum(c) {
			continue
		}
		switch c {
		case '.', '-', '_', '/', ':', '@':
		default:
			return invalid("image", value, "contains a character outside the OCI reference set")
		}
	}
	return nil
}
