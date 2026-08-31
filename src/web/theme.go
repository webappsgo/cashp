package web

import (
	"context"
	"net/http"
)

// Theme values. Dark is the product default; auto defers to the operating
// system preference through the prefers-color-scheme media query.
const (
	ThemeDark  = "dark"
	ThemeLight = "light"
	ThemeAuto  = "auto"
)

// themeCookie is the cookie name holding a visitor's theme choice. The value is
// read server-side so the correct theme class is present in the first painted
// byte, which is why it lives in a cookie and never in localStorage.
const themeCookie = "theme"

// themeCookieMaxAge keeps a theme choice for one year.
const themeCookieMaxAge = 31536000

// validTheme reports whether the value is one of the three supported themes.
func validTheme(theme string) bool {
	switch theme {
	case ThemeDark, ThemeLight, ThemeAuto:
		return true
	default:
		return false
	}
}

// ThemeFromRequest returns the theme requested by the client, or an empty
// string when the request expresses no preference. An unknown cookie value is
// treated as no preference rather than as an error.
func ThemeFromRequest(req *http.Request) string {
	if req == nil {
		return ""
	}
	// A logged-in user's stored preference is applied upstream by setting the
	// request context value; the cookie is the guest path.
	if theme, ok := req.Context().Value(ThemeContextKey).(string); ok && validTheme(theme) {
		return theme
	}
	cookie, err := req.Cookie(themeCookie)
	if err != nil || cookie == nil {
		return ""
	}
	if !validTheme(cookie.Value) {
		return ""
	}
	return cookie.Value
}

// contextKey is a private type so context keys from this package cannot
// collide with keys defined elsewhere.
type contextKey string

// ThemeContextKey is the request-context key other packages use to inject a
// signed-in user's stored theme preference, which wins over the guest cookie.
const ThemeContextKey = contextKey("web.theme")

// WithTheme returns a request carrying an explicit theme preference, used by
// the session middleware to apply a stored user or admin preference.
func WithTheme(req *http.Request, theme string) *http.Request {
	if req == nil || !validTheme(theme) {
		return req
	}
	return req.WithContext(context.WithValue(req.Context(), ThemeContextKey, theme))
}

// themeFor resolves the theme for a request, falling back to the configured
// default and finally to dark.
func (r *Renderer) themeFor(req *http.Request) string {
	if theme := ThemeFromRequest(req); theme != "" {
		return theme
	}
	if validTheme(r.opts.DefaultTheme) {
		return r.opts.DefaultTheme
	}
	return ThemeDark
}

// SetThemeCookie persists a theme choice for the visitor.
func SetThemeCookie(w http.ResponseWriter, theme string) {
	if !validTheme(theme) {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     themeCookie,
		Value:    theme,
		Path:     "/",
		MaxAge:   themeCookieMaxAge,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
	})
}
