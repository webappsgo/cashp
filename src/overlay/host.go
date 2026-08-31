package overlay

import (
	"net"
	"strings"
)

// Overlay kinds returned by Kind. They are the values the HTTP layer switches
// on to apply Tor parity rules (no Onion-Location, no HSTS, no HTTPS
// redirect, no clearnet FQDN / contact email / local timezone leak).
const (
	KindTor      = "tor"
	KindI2P      = "i2p"
	KindClearnet = "clearnet"
)

// onionSuffix is the Tor hidden-service suffix; i2pSuffix covers every I2P
// eepsite host, including the .b32.i2p base32 form.
const (
	onionSuffix = ".onion"
	i2pSuffix   = ".i2p"
)

// IsOverlayRequest reports whether host is served over an overlay network —
// true for any *.onion and any *.b32.i2p (or other *.i2p) host, false for
// every clearnet host. host may carry a port and/or a trailing dot.
func IsOverlayRequest(host string) bool {
	return Kind(host) != KindClearnet
}

// Kind classifies host as "tor", "i2p" or "clearnet". An empty label before
// the suffix (".onion", ".i2p") is not a valid overlay host and classifies as
// clearnet.
func Kind(host string) string {
	h := NormalizeHost(host)
	switch {
	case hasLabeledSuffix(h, onionSuffix):
		return KindTor
	case hasLabeledSuffix(h, i2pSuffix):
		return KindI2P
	default:
		return KindClearnet
	}
}

// NormalizeHost lowercases host, strips any port, IPv6 brackets, surrounding
// whitespace and the trailing root dot, so overlay suffixes can be matched
// exactly.
func NormalizeHost(host string) string {
	h := strings.TrimSpace(host)
	if hostOnly, _, err := net.SplitHostPort(h); err == nil {
		h = hostOnly
	}
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	h = strings.TrimSuffix(h, ".")
	return strings.ToLower(h)
}

// hasLabeledSuffix reports whether h ends in suffix with at least one
// character of label in front of it.
func hasLabeledSuffix(h, suffix string) bool {
	return len(h) > len(suffix) && strings.HasSuffix(h, suffix)
}
