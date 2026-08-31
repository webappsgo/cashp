package netinfo

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// authRequest builds a request carrying the given headers and query string.
func authRequest(query string, headers map[string]string) *http.Request {
	target := "http://app.example.com/api"
	if query != "" {
		target += "?" + query
	}

	r := httptest.NewRequest(http.MethodGet, target, nil)
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	return r
}

// TestExtractTokenPriority checks the documented credential priority by
// removing the highest-priority channel one step at a time.
func TestExtractTokenPriority(t *testing.T) {
	steps := []struct {
		headers map[string]string
		token   string
		source  string
	}{
		{
			map[string]string{
				HeaderAuthorization: "Bearer authorization-token",
				HeaderAPIKeyX:       "api-key-token",
				HeaderAuthToken:     "auth-header-token",
				HeaderTokenX:        "short-header-token",
			},
			"authorization-token", SourceAuthorization,
		},
		{
			map[string]string{
				HeaderAPIKeyX:   "api-key-token",
				HeaderAuthToken: "auth-header-token",
				HeaderTokenX:    "short-header-token",
			},
			"api-key-token", SourceAPIKey,
		},
		{
			map[string]string{
				HeaderAuthToken: "auth-header-token",
				HeaderTokenX:    "short-header-token",
			},
			"auth-header-token", SourceAuthToken,
		},
		{
			map[string]string{HeaderTokenX: "short-header-token"},
			"short-header-token", SourceShortToken,
		},
		{
			nil,
			"query-token", SourceQuery,
		},
	}

	for _, step := range steps {
		token, source := ExtractToken(authRequest("token=query-token", step.headers))
		if token != step.token || source != step.source {
			t.Errorf("ExtractToken = (%q, %q), want (%q, %q)", token, source, step.token, step.source)
		}
	}

	token, source := ExtractToken(authRequest("", nil))
	if token != "" || source != SourceNone {
		t.Errorf("with no credential ExtractToken = (%q, %q), want empty", token, source)
	}

	if token, source := ExtractToken(nil); token != "" || source != SourceNone {
		t.Errorf("ExtractToken(nil) = (%q, %q), want empty", token, source)
	}
}

// TestExtractTokenHeaderSpellings checks every accepted alias.
func TestExtractTokenHeaderSpellings(t *testing.T) {
	cases := []struct {
		header string
		source string
	}{
		{HeaderAPIKeyX, SourceAPIKey},
		{HeaderAPIKey, SourceAPIKey},
		{HeaderAPIKeyPlain, SourceAPIKey},
		{HeaderAuthToken, SourceAuthToken},
		{HeaderAccessToken, SourceAuthToken},
		{HeaderTokenX, SourceShortToken},
		{HeaderToken, SourceShortToken},
	}

	for _, tc := range cases {
		token, source := ExtractToken(authRequest("", map[string]string{tc.header: "value"}))
		if token != "value" || source != tc.source {
			t.Errorf("%s produced (%q, %q), want (\"value\", %q)", tc.header, token, source, tc.source)
		}
	}
}

// TestAuthorizationScheme checks the scheme split, including a bare
// credential with no scheme.
func TestAuthorizationScheme(t *testing.T) {
	scheme, credentials := AuthorizationScheme(authRequest("", map[string]string{HeaderAuthorization: "Bearer abc123"}))
	if scheme != "Bearer" || credentials != "abc123" {
		t.Errorf("AuthorizationScheme = (%q, %q), want (Bearer, abc123)", scheme, credentials)
	}

	scheme, credentials = AuthorizationScheme(authRequest("", map[string]string{HeaderAuthorization: "abc123"}))
	if scheme != "" || credentials != "abc123" {
		t.Errorf("a bare credential produced (%q, %q), want (\"\", abc123)", scheme, credentials)
	}

	scheme, credentials = AuthorizationScheme(authRequest("", nil))
	if scheme != "" || credentials != "" {
		t.Errorf("a missing header produced (%q, %q), want two empty strings", scheme, credentials)
	}
}

// TestBearerTokenRejectsOtherSchemes checks that Basic and Digest
// credentials are never handed back as bearer tokens.
func TestBearerTokenRejectsOtherSchemes(t *testing.T) {
	if got := BearerToken(authRequest("", map[string]string{HeaderAuthorization: "Bearer abc123"})); got != "abc123" {
		t.Errorf("BearerToken = %q, want abc123", got)
	}
	if got := BearerToken(authRequest("", map[string]string{HeaderAuthorization: "Token abc123"})); got != "abc123" {
		t.Errorf("a Token scheme must be accepted, got %q", got)
	}

	for _, value := range []string{"Basic dXNlcjpwYXNz", "Digest username=\"user\""} {
		if got := BearerToken(authRequest("", map[string]string{HeaderAuthorization: value})); got != "" {
			t.Errorf("BearerToken(%q) = %q, want an empty string", value, got)
		}
	}

	if got := BearerToken(authRequest("", map[string]string{HeaderAPIKeyX: "api-key-token"})); got != "api-key-token" {
		t.Errorf("BearerToken must fall back to the api key header, got %q", got)
	}
	if got := BearerToken(authRequest("", nil)); got != "" {
		t.Errorf("BearerToken with no credential = %q, want an empty string", got)
	}
}

// TestCredentialHelpers checks the single-purpose header accessors.
func TestCredentialHelpers(t *testing.T) {
	if got := APIKey(authRequest("", map[string]string{HeaderAPIKey: "key"})); got != "key" {
		t.Errorf("APIKey = %q, want key", got)
	}
	if got := CSRFToken(authRequest("", map[string]string{HeaderXSRFToken: "csrf"})); got != "csrf" {
		t.Errorf("CSRFToken = %q, want csrf", got)
	}
	if got := CSRFToken(authRequest("", map[string]string{HeaderCSRFToken: "csrf"})); got != "csrf" {
		t.Errorf("CSRFToken = %q, want csrf", got)
	}
	if got := SessionID(authRequest("", map[string]string{HeaderSessionID: "session"})); got != "session" {
		t.Errorf("SessionID = %q, want session", got)
	}
	if got := ServiceToken(authRequest("", map[string]string{HeaderInternalToken: "internal"})); got != "internal" {
		t.Errorf("ServiceToken = %q, want internal", got)
	}
	if got := ServiceToken(authRequest("", nil)); got != "" {
		t.Errorf("ServiceToken with no header = %q, want an empty string", got)
	}
}
