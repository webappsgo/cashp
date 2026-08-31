package netinfo

import (
	"net/http"
	"strings"
)

// Authorization and API key headers.
const (
	HeaderAuthorization = "Authorization"
	HeaderAPIKeyX       = "X-API-Key"
	HeaderAPIKey        = "API-Key"
	HeaderAPIKeyPlain   = "ApiKey"
)

// Custom token headers.
const (
	HeaderAuthToken   = "X-Auth-Token"
	HeaderAccessToken = "X-Access-Token"
	HeaderTokenX      = "X-Token"
	HeaderToken       = "Token"
)

// Session and CSRF headers.
const (
	HeaderCSRFToken = "X-CSRF-Token"
	HeaderXSRFToken = "X-XSRF-Token"
	HeaderSessionID = "X-Session-ID"
)

// Service-to-service headers.
const (
	HeaderServiceToken  = "X-Service-Token"
	HeaderInternalToken = "X-Internal-Token"
)

// Token sources, reported by ExtractToken so callers can log and rate-limit
// by credential channel.
const (
	SourceNone          = ""
	SourceAuthorization = "authorization"
	SourceAPIKey        = "api_key"
	SourceAuthToken     = "auth_token"
	SourceShortToken    = "token_header"
	SourceQuery         = "query"
)

// QueryTokenParam is the least preferred credential channel and should be
// avoided in production, where it leaks into logs and referrers.
const QueryTokenParam = "token"

// AuthorizationScheme splits the Authorization header into its scheme and
// credentials, for example ("Bearer", "abc123"). Both results are empty
// when the header is absent.
func AuthorizationScheme(r *http.Request) (scheme, credentials string) {
	value := headerValue(r, HeaderAuthorization)
	if value == "" {
		return "", ""
	}

	parts := strings.SplitN(value, " ", 2)
	if len(parts) != 2 {
		return "", strings.TrimSpace(value)
	}
	return parts[0], strings.TrimSpace(parts[1])
}

// ExtractToken returns the request credential and the channel it came from,
// following the documented priority: Authorization, then the API key
// headers, then the custom auth token headers, then the short token
// headers, and finally the query parameter.
func ExtractToken(r *http.Request) (token, source string) {
	if r == nil {
		return "", SourceNone
	}

	if _, credentials := AuthorizationScheme(r); credentials != "" {
		return credentials, SourceAuthorization
	}

	if value := headerValue(r, HeaderAPIKeyX, HeaderAPIKey, HeaderAPIKeyPlain); value != "" {
		return value, SourceAPIKey
	}

	if value := headerValue(r, HeaderAuthToken, HeaderAccessToken); value != "" {
		return value, SourceAuthToken
	}

	if value := headerValue(r, HeaderTokenX, HeaderToken); value != "" {
		return value, SourceShortToken
	}

	if r.URL != nil {
		if value := strings.TrimSpace(r.URL.Query().Get(QueryTokenParam)); value != "" {
			return value, SourceQuery
		}
	}

	return "", SourceNone
}

// BearerToken returns the credential only when it is a bearer token or came
// from a token header or query parameter. Basic and Digest credentials are
// rejected, so a caller expecting a bearer token never receives base64
// user credentials by accident.
func BearerToken(r *http.Request) string {
	scheme, credentials := AuthorizationScheme(r)
	if credentials != "" {
		if scheme == "" || strings.EqualFold(scheme, "Bearer") || strings.EqualFold(scheme, "Token") {
			return credentials
		}
		return ""
	}

	token, source := ExtractToken(r)
	if source == SourceNone || source == SourceAuthorization {
		return ""
	}
	return token
}

// APIKey returns the API key from any of its header spellings.
func APIKey(r *http.Request) string {
	return headerValue(r, HeaderAPIKeyX, HeaderAPIKey, HeaderAPIKeyPlain)
}

// CSRFToken returns the CSRF token, accepting the Angular spelling.
func CSRFToken(r *http.Request) string {
	return headerValue(r, HeaderCSRFToken, HeaderXSRFToken)
}

// SessionID returns the session identifier header.
func SessionID(r *http.Request) string {
	return headerValue(r, HeaderSessionID)
}

// ServiceToken returns the service-to-service token.
func ServiceToken(r *http.Request) string {
	return headerValue(r, HeaderServiceToken, HeaderInternalToken)
}
