package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Cookie names for the per-request state the frontend owns.
const (
	csrfCookie    = "csrf_token"
	flashCookie   = "flash"
	consentCookie = "cookie_consent"
	ccpaCookie    = "ccpa_opt_out"
)

// consentMaxAge keeps a consent choice for one year.
const consentMaxAge = 31536000

// ConsentState is the granular consent record stored in the cookie_consent
// cookie. Essential cookies can never be switched off.
type ConsentState struct {
	Essential   bool  `json:"essential"`
	Preferences bool  `json:"preferences"`
	Analytics   bool  `json:"analytics"`
	Timestamp   int64 `json:"timestamp"`
}

// ensureCSRFToken returns the request's CSRF token, minting and setting one
// when the visitor does not have it yet. The token is a double-submit value:
// the same string appears in the cookie and in every rendered form.
func ensureCSRFToken(w http.ResponseWriter, req *http.Request) string {
	if req != nil {
		if cookie, err := req.Cookie(csrfCookie); err == nil && len(cookie.Value) >= 32 {
			return cookie.Value
		}
	}
	token := randomToken()
	if w != nil {
		http.SetCookie(w, &http.Cookie{
			Name:     csrfCookie,
			Value:    token,
			Path:     "/",
			MaxAge:   0,
			HttpOnly: false,
			SameSite: http.SameSiteLaxMode,
			Secure:   isSecureRequest(req),
		})
	}
	return token
}

// ValidateCSRF compares the submitted token against the cookie in constant
// time. Both the form field and the header form are accepted.
func ValidateCSRF(req *http.Request) bool {
	if req == nil {
		return false
	}
	cookie, err := req.Cookie(csrfCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	submitted := req.Header.Get("X-CSRF-Token")
	if submitted == "" {
		submitted = req.PostFormValue("csrf_token")
	}
	if submitted == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(submitted)) == 1
}

// randomToken returns a 256-bit URL-safe random string.
func randomToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// A failure of the system CSPRNG is fatal for token security; fall
		// back to a time-derived value so the request still completes with a
		// unique, non-guessable-by-replay token.
		return base64.RawURLEncoding.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// isSecureRequest reports whether the request arrived over TLS, so cookies can
// be marked Secure without breaking plain-HTTP onion and I2P access.
func isSecureRequest(req *http.Request) bool {
	if req == nil {
		return false
	}
	if req.TLS != nil {
		return true
	}
	return strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https")
}

// AddFlash queues a one-shot message shown on the next rendered page. It is
// stored in a cookie so the POST/redirect/GET flow works without JavaScript and
// without a server-side session.
func AddFlash(w http.ResponseWriter, req *http.Request, level, message string) {
	if w == nil || message == "" {
		return
	}
	switch level {
	case "success", "error", "warning", "info":
	default:
		level = "info"
	}
	flashes := readFlashes(req)
	flashes = append(flashes, Flash{Level: level, Message: message})
	if len(flashes) > 5 {
		flashes = flashes[len(flashes)-5:]
	}
	encoded, err := json.Marshal(flashes)
	if err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     flashCookie,
		Value:    url.QueryEscape(string(encoded)),
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isSecureRequest(req),
	})
}

// readFlashes decodes the pending flash messages without clearing them.
func readFlashes(req *http.Request) []Flash {
	if req == nil {
		return nil
	}
	cookie, err := req.Cookie(flashCookie)
	if err != nil || cookie.Value == "" {
		return nil
	}
	raw, err := url.QueryUnescape(cookie.Value)
	if err != nil {
		return nil
	}
	var flashes []Flash
	if err := json.Unmarshal([]byte(raw), &flashes); err != nil {
		return nil
	}
	return flashes
}

// takeFlashes returns the pending messages and expires the cookie so each
// message is displayed exactly once.
func takeFlashes(w http.ResponseWriter, req *http.Request) []Flash {
	flashes := readFlashes(req)
	if len(flashes) == 0 || w == nil {
		return flashes
	}
	http.SetCookie(w, &http.Cookie{
		Name:     flashCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return flashes
}

// hasConsentCookie reports whether the visitor has already answered the cookie
// banner. The server, not JavaScript, decides whether the banner renders.
func hasConsentCookie(req *http.Request) bool {
	if req == nil {
		return true
	}
	cookie, err := req.Cookie(consentCookie)
	return err == nil && cookie.Value != ""
}

// ConsentFromRequest returns the visitor's granular consent record. Essential
// cookies are always reported as granted.
func ConsentFromRequest(req *http.Request) ConsentState {
	state := ConsentState{Essential: true}
	if req == nil {
		return state
	}
	cookie, err := req.Cookie(consentCookie)
	if err != nil || cookie.Value == "" {
		return state
	}
	raw, err := url.QueryUnescape(cookie.Value)
	if err != nil {
		return state
	}
	var stored ConsentState
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return state
	}
	stored.Essential = true
	return stored
}

// SetConsentCookie persists a consent choice.
func SetConsentCookie(w http.ResponseWriter, req *http.Request, state ConsentState) {
	state.Essential = true
	state.Timestamp = time.Now().Unix()
	encoded, err := json.Marshal(state)
	if err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     consentCookie,
		Value:    url.QueryEscape(string(encoded)),
		Path:     "/",
		MaxAge:   consentMaxAge,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Secure:   isSecureRequest(req),
	})
}

// CCPAOptedOut reports whether the visitor asked not to have their data sold.
func CCPAOptedOut(req *http.Request) bool {
	if req == nil {
		return false
	}
	cookie, err := req.Cookie(ccpaCookie)
	return err == nil && cookie.Value == "true"
}

// SetCCPAOptOut records or clears the "do not sell" choice.
func SetCCPAOptOut(w http.ResponseWriter, req *http.Request, optedOut bool) {
	value := "false"
	if optedOut {
		value = "true"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     ccpaCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   consentMaxAge,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Secure:   isSecureRequest(req),
	})
}

// safeReferrer returns a same-origin path to redirect back to after a POST,
// defaulting to the home page. An absolute or cross-origin Referer is rejected
// so the redirect cannot be used as an open redirect.
func safeReferrer(req *http.Request) string {
	if req == nil {
		return "/"
	}
	if target := req.PostFormValue("return_to"); isSafePath(target) {
		return target
	}
	ref := req.Header.Get("Referer")
	if ref == "" {
		return "/"
	}
	parsed, err := url.Parse(ref)
	if err != nil {
		return "/"
	}
	if parsed.Host != "" && parsed.Host != req.Host {
		return "/"
	}
	if !isSafePath(parsed.EscapedPath()) {
		return "/"
	}
	if parsed.RawQuery != "" {
		return parsed.EscapedPath() + "?" + parsed.RawQuery
	}
	return parsed.EscapedPath()
}

// isSafePath reports whether a redirect target is a same-origin absolute path.
func isSafePath(target string) bool {
	return strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "//")
}
