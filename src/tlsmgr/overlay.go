package tlsmgr

import (
	"net"
	"strings"
)

// overlaySuffixes are the hostname suffixes that identify an overlay network
// address. Per AI.md PART 15 and PART 32 these are always served over plain
// http:// with no TLS, no HTTP->HTTPS redirect, and no HSTS.
var overlaySuffixes = []string{".onion", ".b32.i2p", ".i2p", ".exit"}

// NormalizeHost lowercases a host, strips any port and any trailing root dot
// so overlay and certificate lookups compare apples to apples.
func NormalizeHost(host string) string {
	h := strings.TrimSpace(host)
	if h == "" {
		return ""
	}

	// Strip an IPv6 zone/bracket form or a trailing :port when present.
	if stripped, _, err := net.SplitHostPort(h); err == nil {
		h = stripped
	}

	h = strings.Trim(h, "[]")
	h = strings.ToLower(h)
	h = strings.TrimSuffix(h, ".")

	return h
}

// IsOverlayHost reports whether host is a Tor or I2P address. Overlay
// networks terminate encryption in their own transport layer, so cashp never
// issues, self-signs, or serves a certificate for one.
func IsOverlayHost(host string) bool {
	h := NormalizeHost(host)
	if h == "" {
		return false
	}

	for _, suffix := range overlaySuffixes {
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}

	return false
}

// ShouldSendHSTS reports whether Strict-Transport-Security may be sent for
// this host. It is always false for .onion/.b32.i2p (and any other overlay)
// requests — an HSTS header there would break the hidden service.
func ShouldSendHSTS(host string) bool {
	h := NormalizeHost(host)
	if h == "" {
		return false
	}

	return !IsOverlayHost(h)
}
