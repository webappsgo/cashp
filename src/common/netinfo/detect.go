package netinfo

import (
	"net"
	"os"
	"strings"
)

// DomainList returns the configured DOMAIN list, falling back to the DOMAIN
// environment variable when the caller supplied none. The first entry is
// the primary FQDN.
func DomainList() []string {
	opts := Settings()
	if len(opts.Domains) > 0 {
		return opts.Domains
	}
	return cleanDomains(strings.Split(os.Getenv("DOMAIN"), ","))
}

// PrimaryDomain returns the first configured domain that passes host
// validation for the current mode, or an empty string when there is none.
// Invalid entries are skipped silently and detection continues.
func PrimaryDomain() string {
	opts := Settings()
	for _, domain := range DomainList() {
		if IsValidHost(domain, opts.DevMode, opts.ProjectName) {
			return domain
		}
	}
	return ""
}

// DetectFQDN resolves the FQDN without a request, for startup banners and
// background jobs. The order is DOMAIN, os.Hostname(), $HOSTNAME, the first
// public IPv6, the first public IPv4, and finally localhost.
func DetectFQDN() string {
	opts := Settings()

	if domain := PrimaryDomain(); domain != "" {
		return domain
	}

	if hostname, err := os.Hostname(); err == nil {
		if IsValidHost(hostname, opts.DevMode, opts.ProjectName) {
			return strings.ToLower(hostname)
		}
	}

	if hostname := strings.TrimSpace(os.Getenv("HOSTNAME")); hostname != "" {
		if IsValidHost(hostname, opts.DevMode, opts.ProjectName) {
			return strings.ToLower(hostname)
		}
	}

	if ip := PublicIPv6(); ip != "" {
		return ip
	}

	if ip := PublicIPv4(); ip != "" {
		return ip
	}

	return "localhost"
}

// PublicIPv6 returns the first globally routable IPv6 address on this host,
// excluding loopback, link-local, and unique local addresses.
func PublicIPv6() string {
	return firstPublicIP(false)
}

// PublicIPv4 returns the first globally routable IPv4 address on this host,
// excluding loopback, link-local, and the RFC 1918 private ranges.
func PublicIPv4() string {
	return firstPublicIP(true)
}

// firstPublicIP walks the interface addresses and returns the first public
// address of the requested family.
func firstPublicIP(wantIPv4 bool) string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}

	for _, addr := range addrs {
		network, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := network.IP
		isIPv4 := ip.To4() != nil
		if isIPv4 != wantIPv4 {
			continue
		}
		if !IsPublicIP(ip) {
			continue
		}
		return ip.String()
	}
	return ""
}

// IsPublicIP reports whether an address is globally routable.
func IsPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if !ip.IsGlobalUnicast() {
		return false
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	// fc00::/7 unique local addresses are not covered by IsPrivate for
	// every Go release, so they are excluded explicitly.
	if ip.To4() == nil && len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc {
		return false
	}
	return true
}
