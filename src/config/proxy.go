package config

import (
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// proxyRefreshInterval is how long a resolved DNS name in
// server.trusted_proxies.additional stays cached. Load balancers change
// addresses, so the list is refreshed rather than resolved once at startup.
const proxyRefreshInterval = 5 * time.Minute

// alwaysTrustedCIDRs are trusted without configuration: loopback, the
// RFC 1918 and unique-local ranges, and link-local addressing. A reverse
// proxy on the same host or the same private network is the normal
// deployment, so these need no operator action.
var alwaysTrustedCIDRs = []string{
	"127.0.0.0/8",
	"::1/128",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"fc00::/7",
	"169.254.0.0/16",
	"fe80::/10",
}

// proxyResolver answers trusted-proxy questions from a fixed set of
// networks plus a periodically re-resolved set of DNS names.
type proxyResolver struct {
	mu sync.Mutex
	// networks holds every CIDR and literal IP known at construction time.
	networks []*net.IPNet
	// names holds the DNS entries that must be re-resolved periodically.
	names []string
	// resolved caches the addresses the names last resolved to.
	resolved []net.IP
	// refreshed is when resolved was last rebuilt.
	refreshed time.Time
	// lookup resolves a host name; it is a field so tests can substitute it.
	lookup func(string) ([]net.IP, error)
}

// newProxyResolver builds a resolver from the always-trusted ranges, the
// /24 surrounding an explicit listen address, and the operator's additional
// entries, which may be IPs, CIDRs, or DNS names.
func newProxyResolver(additional []string, listenAddress string) *proxyResolver {
	r := &proxyResolver{lookup: net.LookupIP}

	for _, cidr := range alwaysTrustedCIDRs {
		if _, network, err := net.ParseCIDR(cidr); err == nil {
			r.networks = append(r.networks, network)
		}
	}

	if network := listenSubnet(listenAddress); network != nil {
		r.networks = append(r.networks, network)
	}

	for _, entry := range additional {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		if _, network, err := net.ParseCIDR(entry); err == nil {
			r.networks = append(r.networks, network)
			continue
		}

		if ip := net.ParseIP(entry); ip != nil {
			r.networks = append(r.networks, hostNetwork(ip))
			continue
		}

		r.names = append(r.names, entry)
	}

	return r
}

// listenSubnet returns the /24 (or /64 for IPv6) containing an explicit
// listen address, so sibling machines on the same segment are trusted. A
// wildcard or unset address yields no extra network.
func listenSubnet(address string) *net.IPNet {
	address = strings.TrimSpace(address)
	if host, _, err := net.SplitHostPort(address); err == nil {
		address = host
	}

	ip := net.ParseIP(strings.Trim(address, "[]"))
	if ip == nil || ip.IsUnspecified() {
		return nil
	}

	if v4 := ip.To4(); v4 != nil {
		return &net.IPNet{IP: v4.Mask(net.CIDRMask(24, 32)), Mask: net.CIDRMask(24, 32)}
	}

	return &net.IPNet{IP: ip.Mask(net.CIDRMask(64, 128)), Mask: net.CIDRMask(64, 128)}
}

// hostNetwork wraps a single IP as a full-length network.
func hostNetwork(ip net.IP) *net.IPNet {
	if v4 := ip.To4(); v4 != nil {
		return &net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)}
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)}
}

// trusts reports whether peer matches any trusted network or DNS entry,
// refreshing stale DNS results first.
func (r *proxyResolver) trusts(peer net.IP) bool {
	if peer == nil {
		return false
	}

	for _, network := range r.networks {
		if network.Contains(peer) {
			return true
		}
	}

	if len(r.names) == 0 {
		return false
	}

	for _, ip := range r.refresh() {
		if ip.Equal(peer) {
			return true
		}
	}

	return false
}

// refresh returns the addresses of the configured DNS entries, re-resolving
// them when the cache has expired.
func (r *proxyResolver) refresh() []net.IP {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.refreshed.IsZero() && time.Since(r.refreshed) < proxyRefreshInterval {
		return r.resolved
	}

	var resolved []net.IP
	for _, name := range r.names {
		ips, err := r.lookup(name)
		if err != nil {
			continue
		}
		resolved = append(resolved, ips...)
	}

	// A failed lookup keeps the previous answers rather than silently
	// shrinking the trust set during a transient DNS outage.
	if len(resolved) > 0 || len(r.resolved) == 0 {
		r.resolved = resolved
	}
	r.refreshed = time.Now()

	return r.resolved
}

// IsTrustedProxy reports whether peer may set forwarding headers. The
// argument must be the original TCP peer address: once real-IP middleware
// has rewritten the remote address, the answer is meaningless and any
// client could promote itself to a proxy.
func (c *Config) IsTrustedProxy(peer net.IP) bool {
	if c == nil || peer == nil {
		return false
	}

	resolver := c.proxies
	if resolver == nil {
		resolver = newProxyResolver(c.Server.TrustedProxy.Additional, c.Server.Address)
	}

	return resolver.trusts(peer)
}

// ClientIP returns the address to attribute a request to. The trust
// decision is made against peer, the untouched TCP peer, and forwarding
// headers are consulted only when that peer is trusted. peer is returned
// unchanged for every untrusted caller, so spoofed headers cannot move a
// rate limit or an audit entry onto someone else's address.
func (c *Config) ClientIP(peer net.IP, forwardedFor, realIP string) net.IP {
	if !c.IsTrustedProxy(peer) {
		return peer
	}

	if ip := parseHeaderIP(firstValue(forwardedFor)); ip != nil {
		return ip
	}

	if ip := parseHeaderIP(realIP); ip != nil {
		return ip
	}

	return peer
}

// parseHeaderIP parses one address out of a forwarding header, tolerating
// the surrounding whitespace, brackets, and port that proxies add.
func parseHeaderIP(value string) net.IP {
	value = strings.Trim(strings.TrimSpace(value), "\"")
	if value == "" {
		return nil
	}

	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}

	return net.ParseIP(strings.Trim(value, "[]"))
}

// LocalNodeID returns a stable identifier for this cluster member: the
// system hostname, falling back to the listen address and process ID when
// the hostname is unavailable.
func LocalNodeID() string {
	if name, err := os.Hostname(); err == nil && name != "" {
		return name
	}

	return "node-" + strconv.Itoa(os.Getpid())
}
