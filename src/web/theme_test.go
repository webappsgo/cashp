package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ThemeFromRequest reports the client's expressed preference only; the
// renderer applies the configured default when there is none.
func TestThemeFromRequestWithoutPreference(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := ThemeFromRequest(req); got != "" {
		t.Errorf("ThemeFromRequest = %q, want an empty string", got)
	}
	if got := ThemeFromRequest(nil); got != "" {
		t.Errorf("ThemeFromRequest(nil) = %q, want an empty string", got)
	}
}

// The renderer resolves an absent preference to the dark default, which is
// what actually reaches the template.
func TestRendererDefaultsToDark(t *testing.T) {
	r := newTestRenderer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := r.themeFor(req); got != ThemeDark {
		t.Errorf("themeFor = %q, want %q", got, ThemeDark)
	}
}

func TestThemeFromRequestReadsCookie(t *testing.T) {
	for _, theme := range []string{ThemeLight, ThemeDark, ThemeAuto} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "theme", Value: theme})
		if got := ThemeFromRequest(req); got != theme {
			t.Errorf("ThemeFromRequest with cookie %q = %q", theme, got)
		}
	}
}

func TestThemeFromRequestIgnoresUnknownCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "theme", Value: "solarized"})
	if got := ThemeFromRequest(req); got != "" {
		t.Errorf("ThemeFromRequest = %q, want no preference for an unknown value", got)
	}
}

func TestWithThemeOverridesCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "theme", Value: ThemeDark})

	// A signed-in user's stored preference is carried on the context and must
	// win over the guest cookie.
	req = WithTheme(req, ThemeLight)

	if got := ThemeFromRequest(req); got != ThemeLight {
		t.Errorf("ThemeFromRequest = %q, want the context override %q", got, ThemeLight)
	}
}

func TestWithThemeRejectsUnknownValue(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = WithTheme(req, "neon")
	if got := ThemeFromRequest(req); got != "" {
		t.Errorf("ThemeFromRequest = %q, want no preference", got)
	}
}

func TestSetThemeCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	SetThemeCookie(rec, ThemeLight)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "theme" || cookie.Value != ThemeLight {
		t.Errorf("cookie = %s=%s, want theme=%s", cookie.Name, cookie.Value, ThemeLight)
	}
	if cookie.Path != "/" {
		t.Errorf("cookie path = %q, want /", cookie.Path)
	}
	if cookie.MaxAge != themeCookieMaxAge {
		t.Errorf("cookie MaxAge = %d, want %d", cookie.MaxAge, themeCookieMaxAge)
	}
}

func TestRenderedThemeClassMatchesPreference(t *testing.T) {
	r := newTestRenderer(t)

	for _, theme := range []string{ThemeDark, ThemeLight, ThemeAuto} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/server/about", nil)
		req.AddCookie(&http.Cookie{Name: "theme", Value: theme})

		if err := r.Render(rec, req, "about", nil); err != nil {
			t.Fatalf("Render: %v", err)
		}

		body := rec.Body.String()
		// The theme is baked into the first paint by the server, so there is no
		// flash of the wrong theme and no JavaScript is involved.
		if !strings.Contains(body, "class=\"theme-"+theme+" no-js\"") {
			t.Errorf("theme %s: root element class is wrong", theme)
		}
		if !strings.Contains(body, "data-theme=\""+theme+"\"") {
			t.Errorf("theme %s: data-theme attribute is wrong", theme)
		}
	}
}

func TestThemeHandlerStoresChoiceAndRedirects(t *testing.T) {
	r := newTestRenderer(t)
	rec := httptest.NewRecorder()
	body := strings.NewReader("theme=light&csrf_token=token-value&return_to=/server/help")
	req := httptest.NewRequest(http.MethodPost, "/server/theme", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "token-value"})

	r.Handlers()["/server/theme"].ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/server/help" {
		t.Errorf("Location = %q, want /server/help", got)
	}
	var found bool
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "theme" && cookie.Value == ThemeLight {
			found = true
		}
	}
	if !found {
		t.Error("theme cookie was not written")
	}
}

func TestConsentRoundTrip(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	SetConsentCookie(rec, req, ConsentState{Essential: true, Preferences: true, Analytics: false})

	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no consent cookie written")
	}

	next := httptest.NewRequest(http.MethodGet, "/", nil)
	next.AddCookie(cookies[0])

	if !hasConsentCookie(next) {
		t.Error("hasConsentCookie = false after the cookie was set")
	}
	state := ConsentFromRequest(next)
	if !state.Essential || !state.Preferences || state.Analytics {
		t.Errorf("ConsentFromRequest = %+v, want essential+preferences only", state)
	}
}

func TestCCPAOptOutRoundTrip(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	SetCCPAOptOut(rec, req, true)

	next := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, cookie := range rec.Result().Cookies() {
		next.AddCookie(cookie)
	}
	if !CCPAOptedOut(next) {
		t.Error("CCPAOptedOut = false after opting out")
	}
}

func TestFlashRoundTrip(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	AddFlash(rec, req, "success", "Your message has been sent.")

	next := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, cookie := range rec.Result().Cookies() {
		next.AddCookie(cookie)
	}

	clear := httptest.NewRecorder()
	flashes := takeFlashes(clear, next)
	if len(flashes) != 1 {
		t.Fatalf("got %d flashes, want 1", len(flashes))
	}
	if flashes[0].Level != "success" || flashes[0].Message != "Your message has been sent." {
		t.Errorf("flash = %+v", flashes[0])
	}
}

func TestValidateCSRF(t *testing.T) {
	body := strings.NewReader("csrf_token=matching-value")
	req := httptest.NewRequest(http.MethodPost, "/server/contact", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "matching-value"})
	if !ValidateCSRF(req) {
		t.Error("ValidateCSRF = false for a matching double-submit token")
	}

	mismatch := httptest.NewRequest(http.MethodPost, "/server/contact", strings.NewReader("csrf_token=other"))
	mismatch.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mismatch.AddCookie(&http.Cookie{Name: "csrf_token", Value: "matching-value"})
	if ValidateCSRF(mismatch) {
		t.Error("ValidateCSRF = true for a mismatched token")
	}

	if ValidateCSRF(nil) {
		t.Error("ValidateCSRF(nil) = true")
	}
}

func TestCaptchaVerification(t *testing.T) {
	question, token := newCaptcha()
	if question == "" || token == "" {
		t.Fatal("newCaptcha returned an empty question or token")
	}
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		t.Fatalf("token = %q, want answer.expiry.signature", token)
	}
	if !verifyCaptcha(token, parts[0]) {
		t.Error("verifyCaptcha rejected the correct answer")
	}
	if verifyCaptcha(token, "not-the-answer") {
		t.Error("verifyCaptcha accepted a wrong answer")
	}
	if verifyCaptcha("forged.9999999999.deadbeef", "forged") {
		t.Error("verifyCaptcha accepted a forged token")
	}
}
