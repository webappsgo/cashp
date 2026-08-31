// Package i18n implements the internationalization and accessibility layer
// described in AI.md PART 31: embedded locale catalogs, message lookup with
// deterministic fallback, CLDR plural selection, locale-aware number/date/
// currency formatting, RTL direction resolution and WCAG 2.1 AA helpers.
//
// Every human-readable string in the application resolves through this
// package. Locale catalogs are compiled into the binary with go:embed, so no
// filesystem access is required at runtime and every binary (server, CLI,
// agent) carries the identical message set.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// localeFS embeds every locale catalog shipped with the binary.
//
//go:embed locales/*.json
var localeFS embed.FS

const (
	// DefaultLocale is the fallback language used whenever no supported
	// language can be resolved from the request or from an explicit setting.
	DefaultLocale = "en"

	// CookieName is the cookie that persists the visitor's language choice.
	CookieName = "lang"

	// QueryParam is the URL query parameter that selects a language and
	// persists it via CookieName.
	QueryParam = "lang"

	// CookieMaxAge is the lifetime of the language cookie, in seconds (1 year).
	CookieMaxAge = 365 * 24 * 60 * 60

	// DirLTR is the HTML dir attribute value for left-to-right locales.
	DirLTR = "ltr"

	// DirRTL is the HTML dir attribute value for right-to-left locales.
	DirRTL = "rtl"
)

// Meta describes a locale catalog. It mirrors the "meta" object that every
// locale JSON file carries as its first member.
type Meta struct {
	Language   string `json:"language"`
	Name       string `json:"name"`
	NativeName string `json:"native_name"`
	Direction  string `json:"direction"`
	Version    string `json:"version"`
}

// Info is the locale summary handed to templates for the language selector.
type Info struct {
	Code       string
	Name       string
	NativeName string
	Direction  string
}

// Locale is one parsed catalog: flat dotted-key messages plus plural groups.
type Locale struct {
	meta     Meta
	messages map[string]string
	plurals  map[string]map[string]string
}

// Meta returns a copy of the locale's metadata block.
func (l *Locale) Meta() Meta { return l.meta }

// Bundle holds every embedded locale catalog and is safe for concurrent use
// once constructed: it is never mutated after New returns.
type Bundle struct {
	locales map[string]*Locale
	codes   []string
}

// New parses every embedded locale catalog and returns the resulting bundle.
// It fails if a catalog is malformed or if the default locale is absent,
// because a missing default locale would leave every fallback path broken.
func New() (*Bundle, error) {
	return newFromFS(localeFS, "locales")
}

// newFromFS builds a bundle from any filesystem holding "<code>.json" files
// under dir. It exists so tests can exercise malformed catalogs without
// touching the embedded set.
func newFromFS(fsys fs.FS, dir string) (*Bundle, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("i18n: reading %s: %w", dir, err)
	}

	b := &Bundle{locales: make(map[string]*Locale, len(entries))}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}

		code := strings.TrimSuffix(name, ".json")

		raw, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("i18n: reading %s: %w", name, err)
		}

		locale, err := parseLocale(code, raw)
		if err != nil {
			return nil, err
		}

		b.locales[code] = locale
		b.codes = append(b.codes, code)
	}

	if _, ok := b.locales[DefaultLocale]; !ok {
		return nil, fmt.Errorf("i18n: default locale %q is missing from %s", DefaultLocale, dir)
	}

	sort.Strings(b.codes)

	return b, nil
}

// parseLocale decodes one catalog and flattens it into dotted keys.
func parseLocale(code string, raw []byte) (*Locale, error) {
	var tree map[string]any

	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()

	if err := dec.Decode(&tree); err != nil {
		return nil, fmt.Errorf("i18n: parsing locale %q: %w", code, err)
	}

	locale := &Locale{
		messages: make(map[string]string),
		plurals:  make(map[string]map[string]string),
	}

	if err := flatten(code, "", tree, locale); err != nil {
		return nil, err
	}

	metaRaw, ok := tree["meta"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("i18n: locale %q has no meta object", code)
	}

	locale.meta = Meta{
		Language:   stringField(metaRaw, "language"),
		Name:       stringField(metaRaw, "name"),
		NativeName: stringField(metaRaw, "native_name"),
		Direction:  stringField(metaRaw, "direction"),
		Version:    stringField(metaRaw, "version"),
	}

	if locale.meta.Language == "" {
		locale.meta.Language = code
	}

	if locale.meta.Language != code {
		return nil, fmt.Errorf("i18n: locale %q declares meta.language %q", code, locale.meta.Language)
	}

	switch locale.meta.Direction {
	case DirLTR, DirRTL:
	case "":
		locale.meta.Direction = DirLTR
	default:
		return nil, fmt.Errorf("i18n: locale %q has invalid meta.direction %q", code, locale.meta.Direction)
	}

	if locale.meta.NativeName == "" {
		locale.meta.NativeName = code
	}

	return locale, nil
}

// stringField reads a string member from a decoded JSON object.
func stringField(obj map[string]any, key string) string {
	if v, ok := obj[key].(string); ok {
		return v
	}

	return ""
}

// flatten walks the catalog tree, recording leaf strings under their dotted
// path and recognising plural groups (objects whose members are all CLDR
// plural category names) as a unit rather than as individual messages.
func flatten(code, prefix string, node map[string]any, into *Locale) error {
	for key, value := range node {
		full := key
		if prefix != "" {
			full = prefix + "." + key
		}

		switch typed := value.(type) {
		case string:
			into.messages[full] = typed
		case map[string]any:
			if forms, ok := pluralForms(typed); ok {
				into.plurals[full] = forms
				continue
			}

			if err := flatten(code, full, typed, into); err != nil {
				return err
			}
		default:
			return fmt.Errorf("i18n: locale %q key %q must be a string or an object", code, full)
		}
	}

	return nil
}

// pluralForms reports whether obj is a plural group and returns its forms.
// A group qualifies when every member is a string, every member name is a
// CLDR plural category and the mandatory "other" category is present.
func pluralForms(obj map[string]any) (map[string]string, bool) {
	if len(obj) == 0 {
		return nil, false
	}

	forms := make(map[string]string, len(obj))

	for key, value := range obj {
		text, ok := value.(string)
		if !ok || !isPluralCategory(key) {
			return nil, false
		}

		forms[key] = text
	}

	if _, ok := forms[CategoryOther]; !ok {
		return nil, false
	}

	return forms, true
}

// Locales returns every embedded locale code, sorted, so callers get a stable
// ordering across runs.
func (b *Bundle) Locales() []string {
	out := make([]string, len(b.codes))
	copy(out, b.codes)

	return out
}

// Available returns the locale summaries used to render a language selector.
// The default locale sorts first, the remainder alphabetically.
func (b *Bundle) Available() []Info {
	out := make([]Info, 0, len(b.codes))

	for _, code := range b.codes {
		meta := b.locales[code].meta
		out = append(out, Info{
			Code:       code,
			Name:       meta.Name,
			NativeName: meta.NativeName,
			Direction:  meta.Direction,
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Code == DefaultLocale) != (out[j].Code == DefaultLocale) {
			return out[i].Code == DefaultLocale
		}

		return out[i].Code < out[j].Code
	})

	return out
}

// Has reports whether the exact locale code is embedded in the bundle.
func (b *Bundle) Has(locale string) bool {
	_, ok := b.locales[locale]

	return ok
}

// Resolve maps an arbitrary language tag onto a supported locale code,
// accepting region and script subtags ("es-MX" resolves to "es") and falling
// back to the default locale. It never returns an unsupported code.
func (b *Bundle) Resolve(locale string) string {
	if locale == "" {
		return DefaultLocale
	}

	normalized := normalizeTag(locale)
	if b.Has(normalized) {
		return normalized
	}

	if base, _, found := strings.Cut(normalized, "-"); found && b.Has(base) {
		return base
	}

	return DefaultLocale
}

// normalizeTag lowercases a BCP 47 tag and converts "_" separators to "-".
func normalizeTag(tag string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(tag), "_", "-"))
}

// Meta returns the metadata of the resolved locale.
func (b *Bundle) Meta(locale string) Meta {
	return b.locales[b.Resolve(locale)].meta
}

// Dir returns the HTML dir attribute value for the resolved locale, either
// "ltr" or "rtl". Unknown locales resolve to the default locale's direction.
func (b *Bundle) Dir(locale string) string {
	if dir := b.Meta(locale).Direction; dir == DirRTL {
		return DirRTL
	}

	return DirLTR
}

// IsRTL reports whether the resolved locale is written right-to-left.
func (b *Bundle) IsRTL(locale string) bool {
	return b.Dir(locale) == DirRTL
}

// lookup returns the raw message for key in the resolved locale, falling back
// to the default locale. An empty stored value counts as missing so a blank
// translation can never silently erase user-visible text.
func (b *Bundle) lookup(locale, key string) (string, bool) {
	code := b.Resolve(locale)

	if msg, ok := b.locales[code].messages[key]; ok && msg != "" {
		return msg, true
	}

	if code != DefaultLocale {
		if msg, ok := b.locales[DefaultLocale].messages[key]; ok && msg != "" {
			return msg, true
		}
	}

	return "", false
}

// T returns the translated message for key.
//
// Resolution order is: the requested locale, then the default locale, then the
// key itself. Returning the key is deliberate — a visible key is a bug report,
// whereas an empty string silently deletes content from the page.
//
// args are interpolation values supplied either as alternating token/value
// pairs ("name", "Ada") or as a single map. Substitution is literal {token}
// replacement, never fmt formatting, so a stray '%' in a translation cannot
// corrupt the output and tokens with no value stay visible as-is.
func (b *Bundle) T(locale, key string, args ...any) string {
	msg, ok := b.lookup(locale, key)
	if !ok {
		msg = key
	}

	return interpolate(msg, argsToMap(args))
}

// N returns the plural form of key appropriate for count in the resolved
// locale, with {count} pre-filled.
//
// Form selection is: an explicit "zero" form when count is zero, then the
// locale's CLDR cardinal category, then "other". If the locale has no plural
// group for key, the default locale's group is used; if neither has one, T is
// consulted so a non-plural message under the same key still renders.
func (b *Bundle) N(locale, key string, count int, args ...any) string {
	code := b.Resolve(locale)

	forms, ok := b.locales[code].plurals[key]
	if !ok && code != DefaultLocale {
		forms, ok = b.locales[DefaultLocale].plurals[key]
		code = DefaultLocale
	}

	if !ok {
		values := argsToMap(args)
		if _, exists := values["count"]; !exists {
			values["count"] = formatCount(count)
		}

		msg, found := b.lookup(locale, key)
		if !found {
			msg = key
		}

		return interpolate(msg, values)
	}

	msg := selectForm(forms, code, count)

	values := argsToMap(args)
	if _, exists := values["count"]; !exists {
		values["count"] = formatCount(count)
	}

	return interpolate(msg, values)
}

// Keys returns every non-plural message key of the resolved locale, sorted.
// It backs the catalog-completeness test and the build-time key validation
// AI.md PART 31 requires.
func (b *Bundle) Keys(locale string) []string {
	locale = b.Resolve(locale)

	out := make([]string, 0, len(b.locales[locale].messages))
	for key := range b.locales[locale].messages {
		out = append(out, key)
	}

	sort.Strings(out)

	return out
}

// PluralKeys returns every plural group key of the resolved locale, sorted.
func (b *Bundle) PluralKeys(locale string) []string {
	locale = b.Resolve(locale)

	out := make([]string, 0, len(b.locales[locale].plurals))
	for key := range b.locales[locale].plurals {
		out = append(out, key)
	}

	sort.Strings(out)

	return out
}

// PluralFormsFor returns the form names defined for a plural group in the
// resolved locale, sorted. It reports false when the group does not exist.
func (b *Bundle) PluralFormsFor(locale, key string) ([]string, bool) {
	forms, ok := b.locales[b.Resolve(locale)].plurals[key]
	if !ok {
		return nil, false
	}

	out := make([]string, 0, len(forms))
	for name := range forms {
		out = append(out, name)
	}

	sort.Strings(out)

	return out, true
}

// Raw returns the embedded catalog bytes for a locale, for serving
// /locales/{lang}.json to the browser without a second copy on disk.
func (b *Bundle) Raw(locale string) ([]byte, error) {
	code := b.Resolve(locale)

	data, err := localeFS.ReadFile(path.Join("locales", code+".json"))
	if err != nil {
		return nil, fmt.Errorf("i18n: reading catalog %q: %w", code, err)
	}

	return data, nil
}

// argsToMap normalizes template and handler arguments into a token map.
// It accepts a single map argument or alternating token/value pairs; a
// trailing odd argument is ignored rather than panicking.
func argsToMap(args []any) map[string]string {
	values := make(map[string]string, len(args)/2+1)

	if len(args) == 1 {
		switch typed := args[0].(type) {
		case map[string]string:
			for k, v := range typed {
				values[k] = v
			}

			return values
		case map[string]any:
			for k, v := range typed {
				values[k] = fmt.Sprint(v)
			}

			return values
		}
	}

	for i := 0; i+1 < len(args); i += 2 {
		values[fmt.Sprint(args[i])] = fmt.Sprint(args[i+1])
	}

	return values
}

// interpolate performs literal {token} replacement. The message is never
// treated as a format string, and unresolved tokens are left in place.
func interpolate(msg string, values map[string]string) string {
	if len(values) == 0 || !strings.ContainsRune(msg, '{') {
		return msg
	}

	replacements := make([]string, 0, len(values)*2)
	for token, value := range values {
		replacements = append(replacements, "{"+token+"}", value)
	}

	return strings.NewReplacer(replacements...).Replace(msg)
}

// formatCount renders a plural count with plain ASCII digits. Locale-aware
// digit shaping is applied by the formatting helpers, not by plural selection.
func formatCount(count int) string {
	return fmt.Sprintf("%d", count)
}
