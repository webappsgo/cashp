// Package netinfo resolves the URL variables ({proto}, {fqdn}, {port},
// {baseurl}), the client IP, the request ID, and the auth token from an
// incoming HTTP request, following AI.md PART 8 "URL & FQDN Detection".
//
// The package prefers reverse proxy headers, which are only honoured when
// the immediate TCP peer passes the trusted_proxies gate. Nothing here ever
// hardcodes a host, an IP, or a port.
package netinfo

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// Reverse proxy headers providing the FQDN.
const (
	HeaderForwardedHost = "X-Forwarded-Host"
	HeaderRealHost      = "X-Real-Host"
	HeaderOriginalHost  = "X-Original-Host"
)

// Reverse proxy headers providing the protocol.
const (
	HeaderForwardedProto = "X-Forwarded-Proto"
	HeaderForwardedSSL   = "X-Forwarded-Ssl"
	HeaderURLScheme      = "X-Url-Scheme"
	HeaderFrontEndHTTPS  = "Front-End-Https"
)

// Reverse proxy headers providing the port.
const (
	HeaderForwardedPort = "X-Forwarded-Port"
	HeaderRealPort      = "X-Real-Port"
)

// Reverse proxy headers providing the base path.
const (
	HeaderForwardedPrefix = "X-Forwarded-Prefix"
	HeaderForwardedPath   = "X-Forwarded-Path"
	HeaderScriptName      = "X-Script-Name"
)

// Reverse proxy headers providing the client IP.
const (
	HeaderCFConnectingIP = "CF-Connecting-IP"
	HeaderTrueClientIP   = "True-Client-IP"
	HeaderRealIP         = "X-Real-IP"
	HeaderForwardedFor   = "X-Forwarded-For"
	HeaderClientIP       = "X-Client-IP"
)

// contextKey is the private type for every value this package stores in a
// request context.
type contextKey string

// originalPeerKey holds the original TCP peer address, stored before any
// real-IP middleware rewrites RemoteAddr. Every trust decision reads this
// value, never the rewritten address.
const originalPeerKey contextKey = "original_peer"

// WithOriginalPeer stores the original TCP peer address in the context.
func WithOriginalPeer(ctx context.Context, remoteAddr string) context.Context {
	return context.WithValue(ctx, originalPeerKey, remoteAddr)
}

// OriginalPeer returns the preserved TCP peer address, falling back to the
// request's current RemoteAddr when no middleware stored one.
func OriginalPeer(r *http.Request) string {
	if r == nil {
		return ""
	}
	if value, ok := r.Context().Value(originalPeerKey).(string); ok && value != "" {
		return value
	}
	return r.RemoteAddr
}

// headerValue returns the first non-empty value among the named headers.
func headerValue(r *http.Request, names ...string) string {
	if r == nil {
		return ""
	}
	for _, name := range names {
		if value := strings.TrimSpace(r.Header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

// ProxyFQDN returns the FQDN advertised by a trusted reverse proxy, with any
// port suffix removed. It returns an empty string when the peer is not
// trusted or no header is present.
func ProxyFQDN(r *http.Request) string {
	if !TrustedRequest(r) {
		return ""
	}
	value := headerValue(r, HeaderForwardedHost, HeaderRealHost, HeaderOriginalHost)
	if value == "" {
		return ""
	}
	// A proxy may forward a comma-separated chain; the first entry is the
	// client-facing name.
	if idx := strings.IndexByte(value, ','); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	host, _ := splitHostPort(value)
	return host
}

// ProxyProto returns the protocol advertised by a trusted reverse proxy, or
// an empty string when none is present.
func ProxyProto(r *http.Request) string {
	if !TrustedRequest(r) {
		return ""
	}

	if value := headerValue(r, HeaderForwardedProto); value != "" {
		if idx := strings.IndexByte(value, ','); idx >= 0 {
			value = strings.TrimSpace(value[:idx])
		}
		return strings.ToLower(value)
	}
	if value := headerValue(r, HeaderForwardedSSL); value != "" {
		if strings.EqualFold(value, "on") {
			return "https"
		}
		return "http"
	}
	if value := headerValue(r, HeaderURLScheme); value != "" {
		return strings.ToLower(value)
	}
	if value := headerValue(r, HeaderFrontEndHTTPS); value != "" && strings.EqualFold(value, "on") {
		return "https"
	}
	return ""
}

// ProxyPort returns the port advertised by a trusted reverse proxy, or an
// empty string when none is present.
func ProxyPort(r *http.Request) string {
	if !TrustedRequest(r) {
		return ""
	}
	value := headerValue(r, HeaderForwardedPort, HeaderRealPort)
	if idx := strings.IndexByte(value, ','); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	return value
}

// ProxyBasePath returns the URL prefix the proxy mounts the app under,
// normalised to start with a slash and to have no trailing slash. It
// returns an empty string when no header is present.
func ProxyBasePath(r *http.Request) string {
	if !TrustedRequest(r) {
		return ""
	}
	value := headerValue(r, HeaderForwardedPrefix, HeaderForwardedPath, HeaderScriptName)
	return NormalizeBasePath(value)
}

// NormalizeBasePath turns a raw prefix into a leading-slash, no-trailing-
// slash path. The root prefix normalises to an empty string so callers can
// concatenate it with an absolute path.
func NormalizeBasePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" {
		return ""
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return strings.TrimRight(value, "/")
}

// ClientIP resolves the end client's IP for logging, rate limiting,
// blocklists, and GeoIP. Proxy headers are consulted only when the original
// TCP peer is a trusted proxy; otherwise the peer address is used directly.
func ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}

	if TrustedRequest(r) {
		if value := headerValue(r, HeaderCFConnectingIP); value != "" {
			return value
		}
		if value := headerValue(r, HeaderTrueClientIP); value != "" {
			return value
		}
		if value := headerValue(r, HeaderRealIP); value != "" {
			return value
		}
		if value := headerValue(r, HeaderForwardedFor); value != "" {
			// Standard chain "client, proxy1, proxy2": the leftmost entry
			// is the original client.
			parts := strings.Split(value, ",")
			if first := strings.TrimSpace(parts[0]); first != "" {
				return first
			}
		}
		if value := headerValue(r, HeaderClientIP); value != "" {
			return value
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// splitHostPort separates an optional port from a host value without
// failing on a bare host or a bracketed IPv6 literal.
func splitHostPort(value string) (host, port string) {
	if value == "" {
		return "", ""
	}
	if h, p, err := net.SplitHostPort(value); err == nil {
		return h, p
	}
	return strings.Trim(value, "[]"), ""
}
