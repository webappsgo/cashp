package i18n

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// contextKey is the unexported type used for the request-scoped locale so no
// other package can collide with it.
type contextKey struct{}

// localeContextKey stores the negotiated locale on a request context.
var localeContextKey = contextKey{}

// NewContext returns a copy of ctx carrying the negotiated locale.
func NewContext(ctx context.Context, locale string) context.Context {
	return context.WithValue(ctx, localeContextKey, locale)
}

// FromContext returns the locale stored on ctx, or the default locale when no
// language middleware has run.
func FromContext(ctx context.Context) string {
	if locale, ok := ctx.Value(localeContextKey).(string); ok && locale != "" {
		return locale
	}

	return DefaultLocale
}

// Match resolves a single language tag against the bundle, accepting region
// and script subtags, and reports whether a supported locale was found.
func (b *Bundle) Match(tag string) (string, bool) {
	if tag == "" {
		return "", false
	}

	normalized := normalizeTag(tag)
	if b.Has(normalized) {
		return normalized, true
	}

	if base, _, found := strings.Cut(normalized, "-"); found && b.Has(base) {
		return base, true
	}

	return "", false
}

// FromRequest resolves the locale for a request.
//
// Precedence follows AI.md PART 31: an explicit user preference (a stored
// account setting, or a value the caller already decided on) wins, then the
// ?lang= query parameter, then the lang cookie, then Accept-Language
// negotiation, and finally the default locale.
//
// A value that is present but names an unsupported language resolves to the
// default locale rather than falling through, so a bad ?lang= or a stale
// cookie produces a predictable English page instead of silently reverting to
// whatever the browser happens to send. The returned code is always supported.
func FromRequest(b *Bundle, r *http.Request, userPref string) string {
	if userPref != "" {
		if locale, ok := b.Match(userPref); ok {
			return locale
		}

		return DefaultLocale
	}

	if r == nil {
		return DefaultLocale
	}

	if q := r.URL.Query().Get(QueryParam); q != "" {
		if locale, ok := b.Match(q); ok {
			return locale
		}

		return DefaultLocale
	}

	if cookie, err := r.Cookie(CookieName); err == nil && cookie.Value != "" {
		if locale, ok := b.Match(cookie.Value); ok {
			return locale
		}

		return DefaultLocale
	}

	if locale, ok := b.Negotiate(r.Header.Get("Accept-Language")); ok {
		return locale
	}

	return DefaultLocale
}

// acceptedTag is one parsed Accept-Language entry.
type acceptedTag struct {
	tag     string
	quality float64
	order   int
}

// Negotiate selects the best supported locale from an Accept-Language header.
//
// Entries are ranked by q-value, descending, with the original header order
// breaking ties so the result is deterministic. A q-value of 0 explicitly
// rejects a tag and is skipped. The wildcard "*" matches the default locale.
// It reports false when the header names nothing the bundle supports.
func (b *Bundle) Negotiate(header string) (string, bool) {
	if strings.TrimSpace(header) == "" {
		return "", false
	}

	var accepted []acceptedTag

	for i, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		tag, params, _ := strings.Cut(part, ";")

		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}

		quality := parseQuality(params)
		if quality <= 0 {
			continue
		}

		accepted = append(accepted, acceptedTag{tag: tag, quality: quality, order: i})
	}

	sort.SliceStable(accepted, func(i, j int) bool {
		if accepted[i].quality != accepted[j].quality {
			return accepted[i].quality > accepted[j].quality
		}

		return accepted[i].order < accepted[j].order
	})

	for _, entry := range accepted {
		if entry.tag == "*" {
			return DefaultLocale, true
		}

		if locale, ok := b.Match(entry.tag); ok {
			return locale, true
		}
	}

	return "", false
}

// parseQuality extracts the q-value from an Accept-Language parameter list.
// A malformed or absent q-value means the default weight of 1.
func parseQuality(params string) float64 {
	for _, param := range strings.Split(params, ";") {
		name, value, found := strings.Cut(param, "=")
		if !found || strings.TrimSpace(strings.ToLower(name)) != "q" {
			continue
		}

		quality, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || quality < 0 || quality > 1 {
			return 1
		}

		return quality
	}

	return 1
}

// SetCookie persists a language choice for a year. The cookie is HttpOnly and
// SameSite=Lax, and is marked Secure whenever the request arrived over TLS.
func SetCookie(w http.ResponseWriter, r *http.Request, locale string) {
	secure := r != nil && r.TLS != nil

	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    locale,
		Path:     "/",
		MaxAge:   CookieMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// Middleware resolves the locale for every request, persists an explicit
// ?lang= choice in the language cookie, and stores the result on the request
// context for handlers and templates to read with FromContext.
//
// userPref returns a stored per-user language preference and may be nil when
// the caller has no account context; returning an empty string means "no
// stored preference".
func (b *Bundle) Middleware(userPref func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pref := ""
			if userPref != nil {
				pref = userPref(r)
			}

			locale := FromRequest(b, r, pref)

			if q := r.URL.Query().Get(QueryParam); q != "" {
				if _, ok := b.Match(q); ok {
					SetCookie(w, r, locale)
				}
			}

			w.Header().Add("Vary", "Accept-Language")
			w.Header().Add("Vary", "Cookie")

			next.ServeHTTP(w, r.WithContext(NewContext(r.Context(), locale)))
		})
	}
}
