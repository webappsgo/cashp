package guard

import (
	"errors"
	"net"
	"testing"
)

// publicIP is a routable address used as the "safe" answer in resolver
// fixtures.
var publicIP = net.ParseIP("93.184.216.34")

func TestCheckOutboundHostRefusesInternalLiterals(t *testing.T) {
	for _, host := range []string{
		"",
		"127.0.0.1",
		"127.1.2.3",
		"::1",
		"[::1]",
		"::ffff:127.0.0.1",
		"10.0.0.1",
		"172.16.5.5",
		"192.168.1.1",
		"169.254.169.254",
		"169.254.170.2",
		"fd00::1",
		"fe80::1",
		"100.64.1.1",
		"100.127.255.255",
		"192.0.0.171",
		"0.0.0.0",
		"::",
		"224.0.0.1",
		"ff02::1",
	} {
		if err := CheckOutboundHost(host, SystemResolver); err == nil {
			t.Fatalf("CheckOutboundHost accepted the internal destination %q", host)
		} else if DenialReason(err) != ReasonOutboundBlocked {
			t.Fatalf("CheckOutboundHost(%q) denied with %q", host, DenialReason(err))
		}
	}
}

func TestCheckOutboundHostRefusesInternalNames(t *testing.T) {
	// These must be refused on the name alone, before any resolution, so a
	// resolver that would happily answer them is never consulted.
	poison := func(string) ([]net.IP, error) { return []net.IP{publicIP}, nil }

	for _, host := range []string{
		"localhost",
		"localhost.",
		"LOCALHOST",
		"metadata",
		"metadata.google.internal",
		"169.254.169.254",
		"host.docker.internal",
		"kubernetes.default.svc",
		"instance-data",
		"api.internal",
		"printer.local",
		"db.cluster.local",
		"vault.consul",
		"svc.example.svc",
	} {
		if err := CheckOutboundHost(host, poison); err == nil {
			t.Fatalf("CheckOutboundHost accepted the internal name %q", host)
		}
	}
}

func TestCheckOutboundHostDeniesDNSRebindingShapes(t *testing.T) {
	// One public and one internal answer is the classic rebinding setup: the
	// guard must refuse the host outright rather than pick the safe answer.
	mixed := FixedResolver(publicIP, net.ParseIP("127.0.0.1"))
	if err := CheckOutboundHost("rebind.example.com", mixed); err == nil {
		t.Fatal("CheckOutboundHost accepted a host resolving to both public and loopback addresses")
	}

	metadata := FixedResolver(net.ParseIP("169.254.169.254"))
	if err := CheckOutboundHost("harmless.example.com", metadata); err == nil {
		t.Fatal("CheckOutboundHost accepted a host resolving to the metadata address")
	}
}

func TestCheckOutboundHostFailsClosedWithoutAUsableResolution(t *testing.T) {
	if err := CheckOutboundHost("example.com", nil); err == nil {
		t.Fatal("CheckOutboundHost accepted a name with no resolver")
	}
	failing := func(string) ([]net.IP, error) { return nil, errors.New("nxdomain") }
	if err := CheckOutboundHost("example.com", failing); err == nil {
		t.Fatal("CheckOutboundHost accepted a name that did not resolve")
	}
	if err := CheckOutboundHost("example.com", FixedResolver()); err == nil {
		t.Fatal("CheckOutboundHost accepted a name resolving to no addresses")
	}
	if err := CheckOutboundHost("example.com", FixedResolver(nil)); err == nil {
		t.Fatal("CheckOutboundHost accepted a nil address")
	}
	for _, host := range []string{"exa mple.com", "example.com/../etc", "example.com;id", "example.com\x00"} {
		if err := CheckOutboundHost(host, FixedResolver(publicIP)); err == nil {
			t.Fatalf("CheckOutboundHost accepted the malformed host %q", host)
		}
	}
}

func TestCheckOutboundHostAllowsPublicDestinations(t *testing.T) {
	if err := CheckOutboundHost("example.com", FixedResolver(publicIP)); err != nil {
		t.Fatalf("CheckOutboundHost denied a public destination: %v", err)
	}
	if err := CheckOutboundHost("93.184.216.34", nil); err != nil {
		t.Fatalf("CheckOutboundHost denied a public literal: %v", err)
	}
	if err := CheckOutboundHost("2606:2800:220:1:248:1893:25c8:1946", nil); err != nil {
		t.Fatalf("CheckOutboundHost denied a public IPv6 literal: %v", err)
	}
}

func TestValidateListenAddressKeepsConsolesOnLoopback(t *testing.T) {
	for _, addr := range []string{
		"0.0.0.0:5900",
		"[::]:5900",
		"192.168.1.10:5900",
		"console.example.com:5900",
		"5900",
		"",
		"127.0.0.1:0",
		"127.0.0.1:70000",
	} {
		if err := ValidateListenAddress(addr, true); err == nil {
			t.Fatalf("ValidateListenAddress accepted %q for a loopback-only listener", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:5900", "[::1]:5900", "localhost:5900"} {
		if err := ValidateListenAddress(addr, true); err != nil {
			t.Fatalf("ValidateListenAddress rejected the loopback listener %q: %v", addr, err)
		}
	}
	if err := ValidateListenAddress("0.0.0.0:8080", false); err != nil {
		t.Fatalf("ValidateListenAddress rejected a public listener: %v", err)
	}
}

func TestValidatePortRejectsUnusableValues(t *testing.T) {
	for _, port := range []string{"", "0", "-1", "70000", "999999", "80x", "8 0", "０８０", "0x50"} {
		if err := ValidatePort(port); err == nil {
			t.Fatalf("ValidatePort accepted %q", port)
		}
	}
	for _, port := range []string{"1", "443", "65535"} {
		if err := ValidatePort(port); err != nil {
			t.Fatalf("ValidatePort rejected %q: %v", port, err)
		}
	}
}

func TestFixedResolverIgnoresTheHostItIsAsked(t *testing.T) {
	resolve := FixedResolver(publicIP)
	ips, err := resolve("anything.example.com")
	if err != nil {
		t.Fatalf("FixedResolver returned an error: %v", err)
	}
	if len(ips) != 1 || !ips[0].Equal(publicIP) {
		t.Fatalf("FixedResolver returned %v", ips)
	}
}
