package guard

import (
	"net"
	"net/netip"
	"strings"

	"github.com/webappsgo/cashp/src/security"
)

// Resolver resolves a hostname to its addresses. It is an injected
// dependency so every destination guard is unit-testable without DNS, and
// so a caller that already pinned an address can supply it directly rather
// than resolving twice and inviting a rebinding race.
type Resolver func(host string) ([]net.IP, error)

// SystemResolver resolves through the host's configured resolver.
func SystemResolver(host string) ([]net.IP, error) {
	return net.LookupIP(host)
}

// FixedResolver returns a Resolver that always answers with the supplied
// addresses. It is how a caller that has already resolved and pinned an
// address gets it vetted without a second lookup.
func FixedResolver(ips ...net.IP) Resolver {
	pinned := append([]net.IP(nil), ips...)
	return func(string) ([]net.IP, error) {
		return append([]net.IP(nil), pinned...), nil
	}
}

// deniedHostSuffixes name the internal namespaces a tenant-supplied
// destination may never address, whatever they resolve to. They cover the
// container and orchestrator service namespaces where a name resolves
// inside the trust boundary rather than on the public internet.
var deniedHostSuffixes = []string{
	".cluster.local",
	".consul",
	".internal",
	".local",
	".localdomain",
	".localhost",
	".svc",
}

// deniedHostNames are exact hostnames that address the host itself or a
// cloud instance-metadata endpoint.
var deniedHostNames = map[string]struct{}{
	"100.100.100.200":              {},
	"169.254.169.254":              {},
	"169.254.170.2":                {},
	"fd00:ec2::254":                {},
	"gateway.docker.internal":      {},
	"host.containers.internal":     {},
	"host.docker.internal":         {},
	"host.lima.internal":           {},
	"instance-data":                {},
	"kubernetes":                   {},
	"kubernetes.default":           {},
	"kubernetes.default.svc":       {},
	"localhost":                    {},
	"localhost.localdomain":        {},
	"metadata":                     {},
	"metadata.google.internal":     {},
	"metadata.oraclecloud.com":     {},
	"metadata.packet.net":          {},
	"metadata.platformequinix.com": {},
	"metadata.tencentyun.com":      {},
}

// CheckOutboundHost vets a destination host for any request cashp or a
// tenant workload initiates. It refuses internal namespaces by name and
// then refuses the destination unless every address it resolves to is
// public, so a name that answers with one public and one private address —
// the shape a DNS-rebinding attack takes — is denied rather than raced.
//
// The caller should connect to a pinned address from the same resolution
// this function checked; resolving again afterwards reopens the race.
func CheckOutboundHost(host string, resolve Resolver) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return Deny(ReasonOutboundBlocked, "destination host is empty")
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	host = strings.TrimSuffix(host, ".")
	lower := strings.ToLower(host)

	if _, denied := deniedHostNames[lower]; denied {
		return Deny(ReasonOutboundBlocked, "destination "+host+" addresses internal infrastructure")
	}
	for _, suffix := range deniedHostSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return Deny(ReasonOutboundBlocked, "destination "+host+" is in an internal namespace")
		}
	}

	// A literal address needs no resolution, and must not be handed to one:
	// a resolver call on a literal is a needless lookup and, for some
	// resolvers, a needless network round trip.
	if addr, err := netip.ParseAddr(lower); err == nil {
		return checkAddr(host, net.IP(addr.AsSlice()))
	}

	if err := ValidateHostname(lower); err != nil {
		return Deny(ReasonOutboundBlocked, "destination "+host+" is not a valid hostname")
	}
	if resolve == nil {
		return Deny(ReasonOutboundBlocked, "destination "+host+" cannot be vetted without a resolver")
	}

	ips, err := resolve(lower)
	if err != nil {
		return Deny(ReasonOutboundBlocked, "destination "+host+" did not resolve")
	}
	if len(ips) == 0 {
		return Deny(ReasonOutboundBlocked, "destination "+host+" resolved to no addresses")
	}
	for _, ip := range ips {
		if err := checkAddr(host, ip); err != nil {
			return err
		}
	}
	return nil
}

// checkAddr refuses any address that is not a routable public unicast
// address, which covers loopback, RFC 1918, carrier-grade NAT, link-local
// and the cloud metadata address that lives inside it, multicast, and the
// unspecified address.
func checkAddr(host string, ip net.IP) error {
	if ip == nil {
		return Deny(ReasonOutboundBlocked, "destination "+host+" resolved to an unparsable address")
	}
	if security.IsPrivateOrLoopbackIP(ip) {
		return Deny(ReasonOutboundBlocked, "destination "+host+" resolves inside the trust boundary")
	}
	if !ip.IsGlobalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
		return Deny(ReasonOutboundBlocked, "destination "+host+" is not a public unicast address")
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return Deny(ReasonOutboundBlocked, "destination "+host+" resolved to an unparsable address")
	}
	addr = addr.Unmap()
	if addr.Is4() {
		octets := addr.As4()
		// 100.64.0.0/10 is carrier-grade NAT: not private by the stdlib's
		// reckoning, but never a legitimate public destination here.
		if octets[0] == 100 && octets[1] >= 64 && octets[1] <= 127 {
			return Deny(ReasonOutboundBlocked, "destination "+host+" is a carrier-grade NAT address")
		}
		// 192.0.0.0/24 is the IETF protocol assignments block, which
		// includes the NAT64 and DS-Lite service addresses.
		if octets[0] == 192 && octets[1] == 0 && octets[2] == 0 {
			return Deny(ReasonOutboundBlocked, "destination "+host+" is in a reserved protocol block")
		}
	}
	return nil
}

// ValidateListenAddress checks that a listener a tenant asked for binds
// somewhere it is allowed to. A console or admin listener must stay on
// loopback; exposing it on a wildcard address publishes an unauthenticated
// control channel to every network the host is attached to.
func ValidateListenAddress(addr string, loopbackOnly bool) error {
	if addr == "" {
		return Deny(ReasonInvalidInput, "listen address is empty")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return Deny(ReasonInvalidInput, "listen address "+addr+" is not host:port")
	}
	if err := ValidatePort(port); err != nil {
		return err
	}
	if !loopbackOnly {
		return nil
	}
	if !isLoopbackHost(host) {
		return Deny(ReasonInvalidInput, "listen address "+addr+" must bind to loopback")
	}
	return nil
}

// isLoopbackHost reports whether a host portion is a loopback literal. A
// name other than "localhost" is rejected rather than resolved, because a
// name that resolves to loopback today can resolve elsewhere tomorrow.
func isLoopbackHost(host string) bool {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]")) {
	case "127.0.0.1", "::1", "localhost":
		return true
	default:
		return false
	}
}

// ValidatePort checks a port string is a decimal number in range. It
// refuses port 0, which asks the kernel for an arbitrary port and so
// cannot be reasoned about by any firewall rule written in advance.
func ValidatePort(port string) error {
	if port == "" {
		return Deny(ReasonInvalidInput, "port is empty")
	}
	if len(port) > 5 {
		return Deny(ReasonInvalidInput, "port "+port+" is out of range")
	}
	value := 0
	for _, r := range port {
		if r < '0' || r > '9' {
			return Deny(ReasonInvalidInput, "port "+port+" is not numeric")
		}
		value = value*10 + int(r-'0')
	}
	if value < 1 || value > 65535 {
		return Deny(ReasonInvalidInput, "port "+port+" is out of range")
	}
	return nil
}
