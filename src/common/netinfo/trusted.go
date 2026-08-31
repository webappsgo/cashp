package netinfo

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
)

// defaultTrustedCIDRs are the ranges always treated as reverse proxies: the
// RFC 1918 private space, loopback, link-local, and their IPv6 equivalents.
// This is the canonical list referenced by AI.md PART 12 "Trusted Proxies".
var defaultTrustedCIDRs = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
}

// trustedMu guards the additional allow-list, which config reload replaces.
var (
	trustedMu         sync.RWMutex
	trustedNets       = parseCIDRs(defaultTrustedCIDRs)
	additionalTrusted []*net.IPNet
)

// DefaultTrustedCIDRs returns a copy of the always-trusted ranges.
func DefaultTrustedCIDRs() []string {
	out := make([]string, len(defaultTrustedCIDRs))
	copy(out, defaultTrustedCIDRs)
	return out
}

// SetTrustedProxies replaces the additional trusted proxy allow-list. The
// default private ranges always remain trusted. An invalid entry is
// reported and leaves the previous allow-list untouched.
func SetTrustedProxies(cidrs []string) error {
	parsed := make([]*net.IPNet, 0, len(cidrs))
	for _, entry := range cidrs {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		// A bare address is accepted as a single-host range.
		if !strings.Contains(entry, "/") {
			ip := net.ParseIP(entry)
			if ip == nil {
				return fmt.Errorf("invalid trusted proxy address %q", entry)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			parsed = append(parsed, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			return fmt.Errorf("invalid trusted proxy range %q: %w", entry, err)
		}
		parsed = append(parsed, network)
	}

	trustedMu.Lock()
	additionalTrusted = parsed
	trustedMu.Unlock()
	return nil
}

// IsTrustedPeer reports whether an address (with or without a port) belongs
// to a trusted reverse proxy.
func IsTrustedPeer(remoteAddr string) bool {
	host, _ := splitHostPort(strings.TrimSpace(remoteAddr))
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	trustedMu.RLock()
	defer trustedMu.RUnlock()

	for _, network := range trustedNets {
		if network.Contains(ip) {
			return true
		}
	}
	for _, network := range additionalTrusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// TrustedRequest reports whether a request arrived through a trusted proxy.
// It evaluates the preserved original TCP peer, never a rewritten
// RemoteAddr, so a real-IP middleware cannot widen the trust boundary.
func TrustedRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return IsTrustedPeer(OriginalPeer(r))
}

// parseCIDRs converts a static list of ranges, skipping anything malformed.
func parseCIDRs(entries []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(entries))
	for _, entry := range entries {
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			continue
		}
		out = append(out, network)
	}
	return out
}
