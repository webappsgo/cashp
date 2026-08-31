package service

import (
	"fmt"
	"os/user"
	"runtime"
	"strconv"
)

// System UID/GID search bounds. Linux uses the 200-899 safe range and macOS
// the narrower 200-399 system-services range (AI.md PART 24 "UID/GID
// Selection Logic" and "macOS UID/GID Ranges").
const (
	linuxIDRangeMin = 200
	linuxIDRangeMax = 899
	macIDRangeMin   = 200
	macIDRangeMax   = 399
)

// reservedIDs lists UIDs/GIDs used by well-known services across distros.
// They are never selected even when they look free on this host, because a
// later install of one of those services would collide (AI.md PART 24
// "Reserved/Well-Known UIDs").
var reservedIDs = map[int]bool{
	// nobody/nogroup
	65534: true,
	// docker, systemd-*
	999: true, 998: true, 997: true, 996: true, 995: true,
	// systemd-*, input, kvm, render
	994: true, 993: true, 992: true, 991: true, 990: true,
	// sgx, pipewire, colord, geoclue
	989: true, 988: true, 987: true, 986: true, 985: true,
	// avahi, rtkit, saned, usbmux, cups-pk-helper
	984: true, 983: true, 982: true, 981: true, 980: true,
	// sshd, postfix, dovecot and other common services
	101: true, 102: true, 103: true, 104: true, 105: true,
	106: true, 107: true, 108: true, 109: true, 110: true,
	// postgres, mysql and other database servers
	170: true, 171: true, 172: true, 173: true, 174: true,
	175: true, 176: true, 177: true, 178: true, 179: true,
}

// idProbe reports whether a numeric UID or GID is already taken on the
// host. Injecting it keeps ID selection testable without reading
// /etc/passwd or /etc/group.
type idProbe struct {
	uidTaken func(id int) bool
	gidTaken func(id int) bool
}

// systemIDProbe is the real probe, backed by the os/user lookups.
var systemIDProbe = idProbe{
	uidTaken: func(id int) bool {
		_, err := user.LookupId(strconv.Itoa(id))
		return err == nil
	},
	gidTaken: func(id int) bool {
		_, err := user.LookupGroupId(strconv.Itoa(id))
		return err == nil
	},
}

// idRange returns the platform-appropriate search bounds.
func idRange() (int, int) {
	if runtime.GOOS == "darwin" {
		return macIDRangeMin, macIDRangeMax
	}
	return linuxIDRangeMin, linuxIDRangeMax
}

// findAvailableIDInRange walks the range downwards and returns the first
// value that is neither reserved nor already used as a UID or a GID. The
// same value is used for both, because the service account requires
// UID == GID.
func findAvailableIDInRange(low, high int, probe idProbe) (int, error) {
	if low > high {
		return 0, fmt.Errorf("invalid UID/GID range %d-%d", low, high)
	}
	for id := high; id >= low; id-- {
		if reservedIDs[id] {
			continue
		}
		if probe.uidTaken(id) {
			continue
		}
		if probe.gidTaken(id) {
			continue
		}
		return id, nil
	}
	return 0, fmt.Errorf("no available UID/GID in safe range %d-%d", low, high)
}

// FindAvailableSystemID returns an unused, non-reserved ID usable as both
// the UID and the GID of the cashp service account on this host.
func FindAvailableSystemID() (int, error) {
	low, high := idRange()
	return findAvailableIDInRange(low, high, systemIDProbe)
}
