package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// ctxKey is the private context key type of this package.
type ctxKey int

const (
	// peerKey holds the original TCP peer address, before any rewrite.
	peerKey ctxKey = iota
	// clientIPKey holds the resolved client IP.
	clientIPKey
	// trustedKey records whether the TCP peer was a trusted proxy.
	trustedKey
	// basePathKey holds the resolved mount prefix of this deployment.
	basePathKey
	// overlayKey records whether the request arrived over Tor or I2P.
	overlayKey
)

// forwardedHeaders are the proxy headers that may only be believed when the
// TCP peer itself is a trusted proxy. From any other peer they are attacker
// input and are removed before any handler can read them.
var forwardedHeaders = []string{
	"X-Forwarded-For",
	"X-Forwarded-Proto",
	"X-Forwarded-Host",
	"X-Forwarded-Port",
	"X-Forwarded-Prefix",
	"X-Forwarded-Path",
	"X-Real-IP",
	"X-Script-Name",
	"Forwarded",
}

// RealIPOptions configures proxy-aware client address resolution.
type RealIPOptions struct {
	// TrustedProxies lists the networks whose forwarded headers are believed.
	// An empty list means no proxy is trusted.
	TrustedProxies []net.IPNet
	// BasePath is the configured mount prefix, used when no trusted proxy
	// supplied one.
	BasePath string
}

// RealIP resolves the client address behind trusted proxies. The original
// TCP peer is captured on the context before anything is rewritten, and the
// trust decision is made against that original peer — never against a value
// a header claimed.
func RealIP(opts RealIPOptions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer := r.RemoteAddr
			peerHost, peerPort := splitHostPort(peer)
			trusted := ipInNets(net.ParseIP(peerHost), opts.TrustedProxies)

			ctx := context.WithValue(r.Context(), peerKey, peer)
			ctx = context.WithValue(ctx, trustedKey, trusted)

			clientIP := peerHost
			basePath := normalizeBasePath(opts.BasePath)
			if !trusted {
				for _, h := range forwardedHeaders {
					r.Header.Del(h)
				}
			} else {
				if resolved := forwardedClientIP(r, opts.TrustedProxies); resolved != "" {
					clientIP = resolved
					r.RemoteAddr = net.JoinHostPort(clientIP, peerPort)
				}
				if p := forwardedBasePath(r); p != "" {
					basePath = normalizeBasePath(p)
				}
			}

			ctx = context.WithValue(ctx, clientIPKey, clientIP)
			ctx = context.WithValue(ctx, basePathKey, basePath)
			ctx = context.WithValue(ctx, overlayKey, IsOverlayHost(r.Host))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// forwardedClientIP returns the rightmost address in X-Forwarded-For that is
// not itself a trusted proxy, falling back to X-Real-IP.
func forwardedClientIP(r *http.Request, trusted []net.IPNet) string {
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			candidate := strings.TrimSpace(parts[i])
			ip := net.ParseIP(candidate)
			if ip == nil {
				continue
			}
			if ipInNets(ip, trusted) {
				continue
			}
			return candidate
		}
	}
	if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
		if net.ParseIP(real) != nil {
			return real
		}
	}
	return ""
}

// forwardedBasePath resolves the mount prefix from the proxy headers in the
// documented priority order: X-Forwarded-Prefix, X-Forwarded-Path, then
// X-Script-Name.
func forwardedBasePath(r *http.Request) string {
	for _, h := range []string{"X-Forwarded-Prefix", "X-Forwarded-Path", "X-Script-Name"} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return v
		}
	}
	return ""
}

// normalizeBasePath reduces a prefix to a leading slash with no trailing
// slash, or "/" when it is empty.
func normalizeBasePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return strings.TrimSuffix(p, "/")
}

// splitHostPort splits an address that may or may not carry a port.
func splitHostPort(addr string) (host, port string) {
	if h, p, err := net.SplitHostPort(addr); err == nil {
		return h, p
	}
	return addr, "0"
}

// ipInNets reports whether an address falls inside any of the networks.
func ipInNets(ip net.IP, nets []net.IPNet) bool {
	if ip == nil {
		return false
	}
	for i := range nets {
		if nets[i].Contains(ip) {
			return true
		}
	}
	return false
}

// PeerAddrFrom returns the original TCP peer address of the request, which
// is preserved even after the client address has been rewritten.
func PeerAddrFrom(ctx context.Context) string {
	addr, _ := ctx.Value(peerKey).(string)
	return addr
}

// ClientIPFrom returns the resolved client IP.
func ClientIPFrom(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPKey).(string)
	return ip
}

// FromTrustedProxy reports whether the request arrived from a trusted proxy.
func FromTrustedProxy(ctx context.Context) bool {
	trusted, _ := ctx.Value(trustedKey).(bool)
	return trusted
}

// BasePathFrom returns the resolved mount prefix of this deployment.
func BasePathFrom(ctx context.Context) string {
	p, _ := ctx.Value(basePathKey).(string)
	if p == "" {
		return "/"
	}
	return p
}

// IsOverlayRequest reports whether the request arrived over Tor or I2P.
func IsOverlayRequest(ctx context.Context) bool {
	overlay, _ := ctx.Value(overlayKey).(bool)
	return overlay
}

// IsOverlayHost reports whether a Host header names a Tor hidden service or
// an I2P eepsite. The suffixes are matched locally so this package does not
// depend on the overlay implementation.
func IsOverlayHost(host string) bool {
	h := strings.ToLower(host)
	if i := strings.LastIndexByte(h, ':'); i > 0 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	h = strings.TrimSuffix(h, ".")
	return strings.HasSuffix(h, ".onion") || strings.HasSuffix(h, ".b32.i2p") || strings.HasSuffix(h, ".i2p")
}
