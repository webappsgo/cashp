package guard

import (
	"encoding/json"
	"log/slog"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/security"
)

// maxRedactDepth bounds how deep RedactPayload will walk. A payload nested
// beyond this is truncated rather than followed, so a cyclic or hostile
// structure cannot exhaust the stack on a logging path.
const maxRedactDepth = 12

// truncatedMarker replaces a value that exceeded the walk depth.
const truncatedMarker = "[truncated]"

// tokenPattern matches cashp's own token format: a registered prefix
// followed by the 32-character alphanumeric body. Matching the shape means
// a token pasted into a free-text message is scrubbed even when it is not
// sitting in a field with a sensitive name.
var tokenPattern = regexp.MustCompile(`\b(?:` + strings.Join(tokenPrefixAlternatives(), "|") + `)[A-Za-z0-9]{32}\b`)

// pairPattern matches a "key=value" or "key: value" fragment inside free
// text so an embedded credential is scrubbed along with the token shapes.
var pairPattern = regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(?:password|passwd|secret|token|api[_-]?key|apikey|auth|session|private[_-]?key)[a-z0-9_.-]*)\s*[:=]\s*("[^"]*"|'[^']*'|\S+)`)

// tokenPrefixAlternatives renders the registered token prefixes as regexp
// alternatives, escaping each so a prefix can never be read as a pattern.
func tokenPrefixAlternatives() []string {
	out := make([]string, 0, len(security.TokenPrefixes))
	for _, prefix := range security.TokenPrefixes {
		out = append(out, regexp.QuoteMeta(prefix))
	}
	return out
}

// Secret wraps a value that must never reach a log, an error string, or a
// serialized response. Every rendering path Go can take to a value —
// fmt's Stringer and GoStringer, encoding/json, encoding.TextMarshaler,
// and slog's LogValuer — is implemented to yield the mask, so leaking one
// requires an explicit Reveal call that shows up in review.
type Secret struct {
	value string
}

// NewSecret wraps a sensitive value.
func NewSecret(value string) Secret {
	return Secret{value: value}
}

// String renders the mask. It satisfies fmt.Stringer, which is what most
// accidental log lines reach for.
func (s Secret) String() string {
	return security.MaskedValue
}

// GoString renders the mask for the %#v verb.
func (s Secret) GoString() string {
	return security.MaskedValue
}

// MarshalJSON renders the mask, so a Secret embedded in an API response
// struct serializes as the mask rather than the value.
func (s Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(security.MaskedValue)
}

// MarshalText renders the mask for every encoding that consults
// encoding.TextMarshaler.
func (s Secret) MarshalText() ([]byte, error) {
	return []byte(security.MaskedValue), nil
}

// LogValue renders the mask for structured logging, so slog attributes
// holding a Secret emit the mask without the call site remembering to.
func (s Secret) LogValue() slog.Value {
	return slog.StringValue(security.MaskedValue)
}

// Reveal returns the underlying value. It is the single, deliberately
// conspicuous way to read a Secret, and belongs only where the value is
// about to be used — never where it is about to be recorded.
func (s Secret) Reveal() string {
	return s.value
}

// Empty reports whether the wrapped value is the empty string.
func (s Secret) Empty() bool {
	return s.value == ""
}

// Equal compares the wrapped value against a candidate in constant time,
// so a Secret can be checked without the comparison itself leaking the
// value through timing.
func (s Secret) Equal(candidate string) bool {
	return security.ConstantTimeEqualString(s.value, candidate)
}

// EqualSecret compares two Secrets in constant time.
func (s Secret) EqualSecret(other Secret) bool {
	return security.ConstantTimeEqualString(s.value, other.value)
}

// ScrubText masks credentials embedded in free text: cashp's own token
// format and any "key=value" pair whose key names a credential. It is the
// last line of defense for a message assembled from an upstream tool's
// output, where no field name is available to key on.
func ScrubText(s string) string {
	if s == "" {
		return ""
	}
	out := tokenPattern.ReplaceAllString(s, security.MaskedValue)
	return pairPattern.ReplaceAllString(out, "$1="+security.MaskedValue)
}

// RedactPayload returns a copy of v with every sensitive value masked,
// suitable for logging or for returning over the API. It walks maps,
// slices, arrays, pointers, and structs, keying on field, JSON tag, and
// map-key names via security.IsSensitiveName, and masks any Secret it
// encounters regardless of the name it sits under.
//
// The input is never mutated; a caller can log the result and still use
// the original.
func RedactPayload(v any) any {
	return redactValue(reflect.ValueOf(v), 0)
}

// RedactMap is the map-shaped convenience form of RedactPayload, for the
// common case of a decoded JSON request or response body.
func RedactMap(m map[string]any) map[string]any {
	out, ok := RedactPayload(m).(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return out
}

// RedactAttrs masks the value of every slog attribute whose key names a
// credential, and recursively redacts structured values, so an audit line
// assembled from request data cannot carry a secret.
func RedactAttrs(attrs []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if security.IsSensitiveName(attr.Key) {
			out = append(out, slog.String(attr.Key, security.MaskedValue))
			continue
		}
		switch attr.Value.Kind() {
		case slog.KindString:
			out = append(out, slog.String(attr.Key, ScrubText(attr.Value.String())))
		case slog.KindGroup:
			out = append(out, slog.Attr{Key: attr.Key, Value: slog.GroupValue(RedactAttrs(attr.Value.Group())...)})
		case slog.KindAny:
			out = append(out, slog.Any(attr.Key, RedactPayload(attr.Value.Any())))
		default:
			out = append(out, attr)
		}
	}
	return out
}

// secretType is the reflect type of Secret, used to mask a wrapped value
// wherever it appears in a payload.
var secretType = reflect.TypeOf(Secret{})

// timeType is the reflect type of time.Time. It is rendered through its
// own RFC 3339 form rather than walked, because walking it would explode
// an ordinary timestamp into an unreadable map of unexported fields.
var timeType = reflect.TypeOf(time.Time{})

// redactValue is RedactPayload's recursive worker. Anything past the depth
// ceiling collapses to the truncation marker rather than being followed.
func redactValue(v reflect.Value, depth int) any {
	if depth > maxRedactDepth {
		return truncatedMarker
	}
	if !v.IsValid() {
		return nil
	}
	if v.Type() == secretType {
		return security.MaskedValue
	}
	if v.Type() == timeType {
		ts, ok := v.Interface().(time.Time)
		if !ok {
			return truncatedMarker
		}
		return ts.Format(time.RFC3339)
	}

	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return redactValue(v.Elem(), depth+1)
	case reflect.String:
		return ScrubText(v.String())
	case reflect.Map:
		return redactMapValue(v, depth)
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return nil
		}
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			return security.MaskedValue
		}
		out := make([]any, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			out = append(out, redactValue(v.Index(i), depth+1))
		}
		return out
	case reflect.Struct:
		return redactStructValue(v, depth)
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return truncatedMarker
	default:
		return v.Interface()
	}
}

// redactMapValue redacts a map, masking any entry whose key names a
// credential. A non-string key is rendered through fmt-free reflection so
// no arbitrary Stringer runs on a redaction path.
func redactMapValue(v reflect.Value, depth int) any {
	out := make(map[string]any, v.Len())
	iter := v.MapRange()
	for iter.Next() {
		key := mapKeyString(iter.Key())
		if security.IsSensitiveName(key) {
			out[key] = security.MaskedValue
			continue
		}
		out[key] = redactValue(iter.Value(), depth+1)
	}
	return out
}

// redactStructValue redacts a struct, keying on the JSON tag name when one
// is present and on the Go field name otherwise. Unexported fields are
// skipped because they cannot be read without unsafe access, and skipping
// them is the safe direction.
func redactStructValue(v reflect.Value, depth int) any {
	t := v.Type()
	out := make(map[string]any, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name := field.Name
		if tag := field.Tag.Get("json"); tag != "" {
			if tag == "-" {
				continue
			}
			if comma := strings.IndexByte(tag, ','); comma >= 0 {
				tag = tag[:comma]
			}
			if tag != "" {
				name = tag
			}
		}
		if security.IsSensitiveName(name) || security.IsSensitiveName(field.Name) {
			out[name] = security.MaskedValue
			continue
		}
		out[name] = redactValue(v.Field(i), depth+1)
	}
	return out
}

// mapKeyString renders a map key as a string without invoking a Stringer
// the key's own type may define.
func mapKeyString(k reflect.Value) string {
	if k.Kind() == reflect.String {
		return k.String()
	}
	b, err := json.Marshal(k.Interface())
	if err != nil {
		return truncatedMarker
	}
	return strings.Trim(string(b), `"`)
}
