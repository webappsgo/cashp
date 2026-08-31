package i18n

import (
	"net/http"
	"sync"
	"time"
)

// The process-wide bundle. Callers that thread a *Bundle explicitly should
// keep doing so; these helpers exist for the CLI, agent and handler call
// sites that AI.md PART 31 spells as bare Translate/IsSupported calls.
var (
	defaultOnce   sync.Once
	defaultBundle *Bundle
	defaultErr    error
)

// Default returns the process-wide bundle, parsing the embedded catalogs on
// first use. The error is cached, so a malformed catalog is reported to every
// caller rather than being retried on each request.
func Default() (*Bundle, error) {
	defaultOnce.Do(func() {
		defaultBundle, defaultErr = New()
	})

	return defaultBundle, defaultErr
}

// mustDefault returns the process-wide bundle or nil when it failed to load.
// Every helper below degrades to a safe, non-panicking result when it is nil.
func mustDefault() *Bundle {
	bundle, _ := Default()

	return bundle
}

// IsSupported reports whether a language tag resolves to an embedded locale.
func IsSupported(lang string) bool {
	bundle := mustDefault()
	if bundle == nil {
		return lang == DefaultLocale
	}

	_, ok := bundle.Match(lang)

	return ok
}

// SupportedLanguages returns every embedded locale code.
func SupportedLanguages() []string {
	bundle := mustDefault()
	if bundle == nil {
		return []string{DefaultLocale}
	}

	return bundle.Locales()
}

// Translate returns the message for key in lang, falling back to the default
// locale and finally to the key itself.
func Translate(lang, key string) string {
	bundle := mustDefault()
	if bundle == nil {
		return key
	}

	return bundle.T(lang, key)
}

// TranslateFormat returns the message for key with its named {token}
// placeholders replaced by literal string substitution.
func TranslateFormat(lang, key string, args map[string]string) string {
	bundle := mustDefault()
	if bundle == nil {
		return interpolate(key, args)
	}

	return bundle.T(lang, key, args)
}

// TranslatePlural returns the plural form of key appropriate for count.
func TranslatePlural(lang, key string, count int) string {
	bundle := mustDefault()
	if bundle == nil {
		return key
	}

	return bundle.N(lang, key, count)
}

// Direction returns "ltr" or "rtl" for lang.
func Direction(lang string) string {
	bundle := mustDefault()
	if bundle == nil {
		return DirLTR
	}

	return bundle.Dir(lang)
}

// LangFromRequest resolves the request language using the PART 31 precedence:
// ?lang= query parameter, lang cookie, Accept-Language header, then English.
func LangFromRequest(r *http.Request) string {
	bundle := mustDefault()
	if bundle == nil {
		return DefaultLocale
	}

	return FromRequest(bundle, r, "")
}

// FormatNumber renders a decimal number for lang with two fraction digits.
func FormatNumber(lang string, v float64) string {
	bundle := mustDefault()
	if bundle == nil {
		return formats[DefaultLocale].number(v, 2)
	}

	return bundle.FormatNumber(lang, v)
}

// FormatDate renders a date in lang's short date order.
func FormatDate(lang string, t time.Time) string {
	bundle := mustDefault()
	if bundle == nil {
		return t.Format(formats[DefaultLocale].dateLayout)
	}

	return bundle.FormatDate(lang, t)
}
