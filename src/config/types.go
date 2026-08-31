package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Bool is a YAML boolean that accepts the full truthy/falsy word list from
// bool.go. An unrecognized value never fails the decode: the field keeps the
// default it was initialized with and is flagged so Validate can warn.
type Bool struct {
	// Value is the effective boolean, defaulted when the input was invalid.
	Value bool
	// Raw is the literal YAML scalar as written by the operator.
	Raw string
	// Invalid reports that Raw could not be parsed as a boolean.
	Invalid bool
}

// NewBool returns a Bool carrying v as its effective value.
func NewBool(v bool) Bool {
	return Bool{Value: v}
}

// UnmarshalYAML decodes a scalar into a Bool via ParseBool, preserving the
// pre-populated default when the scalar is not a recognized boolean word.
func (b *Bool) UnmarshalYAML(value *yaml.Node) error {
	b.Raw = value.Value
	b.Invalid = false

	parsed, err := ParseBool(value.Value, b.Value)
	if err != nil {
		b.Invalid = true
		return nil
	}

	b.Value = parsed
	return nil
}

// MarshalYAML emits the effective boolean so a rewritten server.yml always
// contains a canonical true/false rather than the operator's original word.
func (b Bool) MarshalYAML() (any, error) {
	return b.Value, nil
}

// Duration is a YAML duration accepting Go duration syntax plus the d/w/y
// suffixes the spec uses (30d, 7d, 1h). Invalid input keeps the default.
type Duration struct {
	// Value is the effective duration, defaulted when the input was invalid.
	Value time.Duration
	// Raw is the literal YAML scalar as written by the operator.
	Raw string
	// Invalid reports that Raw could not be parsed as a duration.
	Invalid bool
}

// NewDuration returns a Duration carrying d as its effective value.
func NewDuration(d time.Duration) Duration {
	return Duration{Value: d, Raw: FormatDuration(d)}
}

// UnmarshalYAML decodes a scalar into a Duration, preserving the
// pre-populated default when the scalar cannot be parsed.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	d.Raw = value.Value
	d.Invalid = false

	parsed, err := ParseDuration(value.Value)
	if err != nil {
		d.Invalid = true
		return nil
	}

	d.Value = parsed
	return nil
}

// MarshalYAML emits the effective duration in canonical form.
func (d Duration) MarshalYAML() (any, error) {
	return FormatDuration(d.Value), nil
}

// durationUnits maps the spec's calendar-ish duration suffixes to their
// fixed-length equivalents. Days/weeks/years are exact multiples here — the
// spec uses them for cookie lifetimes and retention windows, not calendars.
var durationUnits = map[string]time.Duration{
	"ns": time.Nanosecond,
	"us": time.Microsecond,
	"ms": time.Millisecond,
	"s":  time.Second,
	"m":  time.Minute,
	"h":  time.Hour,
	"d":  24 * time.Hour,
	"w":  7 * 24 * time.Hour,
	"y":  365 * 24 * time.Hour,
}

// ParseDuration parses a duration string in the forms the spec uses: a bare
// number (seconds), a Go duration, or a number with an s/m/h/d/w/y suffix.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("config: empty duration")
	}

	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Duration(n * float64(time.Second)), nil
	}

	for _, unit := range []string{"ns", "us", "ms", "d", "w", "y"} {
		if strings.HasSuffix(s, unit) {
			n, err := strconv.ParseFloat(strings.TrimSuffix(s, unit), 64)
			if err != nil {
				break
			}
			return time.Duration(n * float64(durationUnits[unit])), nil
		}
	}

	parsed, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("config: invalid duration %q", s)
	}
	return parsed, nil
}

// FormatDuration renders d using the largest whole spec unit that divides it
// evenly, so 30 days round-trips as "30d" instead of "720h0m0s".
func FormatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}

	for _, unit := range []struct {
		suffix string
		size   time.Duration
		// max caps this unit to values that read naturally in it (e.g.
		// nobody writes "90m" for what is really "1h30m0s") — zero means
		// no cap.
		max time.Duration
	}{
		{"y", durationUnits["y"], 0},
		{"w", durationUnits["w"], 0},
		{"d", durationUnits["d"], 0},
		{"h", time.Hour, 0},
		{"m", time.Minute, time.Hour},
		{"s", time.Second, time.Minute},
	} {
		if unit.max != 0 && d >= unit.max {
			continue
		}
		if d%unit.size == 0 {
			return strconv.FormatInt(int64(d/unit.size), 10) + unit.suffix
		}
	}

	return d.String()
}

// Size is a YAML byte size accepting plain bytes or a KB/MB/GB/TB suffix
// (case-insensitive, with or without the trailing B). Invalid input keeps
// the default.
type Size struct {
	// Value is the effective size in bytes, defaulted when input was invalid.
	Value int64
	// Raw is the literal YAML scalar as written by the operator.
	Raw string
	// Invalid reports that Raw could not be parsed as a byte size.
	Invalid bool
}

// NewSize returns a Size carrying n bytes as its effective value.
func NewSize(n int64) Size {
	return Size{Value: n, Raw: FormatSize(n)}
}

// UnmarshalYAML decodes a scalar into a Size, preserving the pre-populated
// default when the scalar cannot be parsed.
func (s *Size) UnmarshalYAML(value *yaml.Node) error {
	s.Raw = value.Value
	s.Invalid = false

	parsed, err := ParseSize(value.Value)
	if err != nil {
		s.Invalid = true
		return nil
	}

	s.Value = parsed
	return nil
}

// MarshalYAML emits the effective size in canonical form.
func (s Size) MarshalYAML() (any, error) {
	return FormatSize(s.Value), nil
}

// sizeUnits maps size suffixes to their byte multipliers. Binary multiples
// are used throughout because that is what operators mean by "10MB" for a
// request body cap.
var sizeUnits = []struct {
	suffix string
	mult   int64
}{
	{"tb", 1 << 40},
	{"gb", 1 << 30},
	{"mb", 1 << 20},
	{"kb", 1 << 10},
	{"t", 1 << 40},
	{"g", 1 << 30},
	{"m", 1 << 20},
	{"k", 1 << 10},
	{"b", 1},
}

// ParseSize parses a byte size such as "10MB", "512k", or "1048576".
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("config: empty size")
	}

	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("config: negative size %q", s)
		}
		return n, nil
	}

	for _, unit := range sizeUnits {
		if !strings.HasSuffix(s, unit.suffix) {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, unit.suffix)), 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("config: invalid size %q", s)
		}
		return int64(n * float64(unit.mult)), nil
	}

	return 0, fmt.Errorf("config: invalid size %q", s)
}

// FormatSize renders n using the largest binary unit that divides it evenly.
func FormatSize(n int64) string {
	for _, unit := range []struct {
		suffix string
		mult   int64
	}{
		{"TB", 1 << 40},
		{"GB", 1 << 30},
		{"MB", 1 << 20},
		{"KB", 1 << 10},
	} {
		if n >= unit.mult && n%unit.mult == 0 {
			return strconv.FormatInt(n/unit.mult, 10) + unit.suffix
		}
	}

	return strconv.FormatInt(n, 10)
}

// PortSpec is the server.port value. It accepts a single port ("64580", or a
// bare YAML integer) or the dual "http,https" form ("8090,8443"). Invalid
// input keeps the default so startup never fails on a bad port.
type PortSpec struct {
	// HTTP is the plain-HTTP listener port (0 when only HTTPS is configured).
	HTTP int
	// HTTPS is the TLS listener port, 0 when no dedicated TLS port was given.
	HTTPS int
	// Raw is the literal YAML scalar as written by the operator.
	Raw string
	// Invalid reports that Raw could not be parsed as a port specification.
	Invalid bool
}

// NewPortSpec returns a single-port PortSpec for http.
func NewPortSpec(http int) PortSpec {
	return PortSpec{HTTP: http, Raw: strconv.Itoa(http)}
}

// UnmarshalYAML decodes a scalar into a PortSpec, preserving the
// pre-populated default when the scalar cannot be parsed.
func (p *PortSpec) UnmarshalYAML(value *yaml.Node) error {
	p.Raw = value.Value
	p.Invalid = false

	parsed, err := ParsePortSpec(value.Value)
	if err != nil {
		p.Invalid = true
		return nil
	}

	p.HTTP = parsed.HTTP
	p.HTTPS = parsed.HTTPS
	return nil
}

// MarshalYAML emits a bare integer for the single-port form and the
// "http,https" string for the dual form.
func (p PortSpec) MarshalYAML() (any, error) {
	if p.HTTPS == 0 {
		return p.HTTP, nil
	}
	return strconv.Itoa(p.HTTP) + "," + strconv.Itoa(p.HTTPS), nil
}

// String renders the PortSpec in the same form MarshalYAML uses.
func (p PortSpec) String() string {
	if p.HTTPS == 0 {
		return strconv.Itoa(p.HTTP)
	}
	return strconv.Itoa(p.HTTP) + "," + strconv.Itoa(p.HTTPS)
}

// ParsePortSpec parses the single or dual port form used by server.port.
func ParsePortSpec(s string) (PortSpec, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return PortSpec{}, fmt.Errorf("config: empty port")
	}

	parts := strings.Split(s, ",")
	if len(parts) > 2 {
		return PortSpec{}, fmt.Errorf("config: invalid port specification %q", s)
	}

	http, err := parsePort(strings.TrimSpace(parts[0]))
	if err != nil {
		return PortSpec{}, err
	}

	spec := PortSpec{HTTP: http, Raw: s}
	if len(parts) == 2 {
		https, err := parsePort(strings.TrimSpace(parts[1]))
		if err != nil {
			return PortSpec{}, err
		}
		spec.HTTPS = https
	}

	return spec, nil
}

// parsePort parses and range-checks a port string, rejecting anything
// outside 0-65535.
func parsePort(s string) (int, error) {
	p, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("config: invalid port %q", s)
	}
	if p < 0 || p > 65535 {
		return 0, fmt.Errorf("config: port %q out of range", s)
	}
	return p, nil
}
