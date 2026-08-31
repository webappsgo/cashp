//go:build linux

package service

// Detect returns the manager for the init system running on this host,
// checked in the order AI.md PART 25 documents: systemd, OpenRC, runit,
// then SysVinit. When the caller is not root and systemd is present, the
// per-user fallback service is selected instead of the system service
// (AI.md PART 24 "Service Installation Logic").
func Detect() (Manager, error) {
	switch {
	case hasSystemd():
		return newSystemdManager(!IsElevated()), nil
	case hasOpenRC():
		return newOpenRCManager(), nil
	case hasRunit():
		return newRunitManager(), nil
	case hasSysVInit():
		return newSysVInitManager(), nil
	default:
		return nil, ErrUnsupportedPlatform
	}
}

// hasSystemd reports whether systemd is the running init system, not merely
// installed.
func hasSystemd() bool {
	return hasBinary("systemctl") && fileExists("/run/systemd/system")
}

// hasOpenRC reports whether OpenRC drives this host.
func hasOpenRC() bool {
	return fileExists("/sbin/openrc-run") || fileExists("/usr/sbin/openrc-run")
}

// hasRunit reports whether runit supervises services on this host.
func hasRunit() bool {
	if !hasBinary("sv") {
		return false
	}
	if fileExists("/etc/runit") {
		return true
	}
	for _, dir := range runitLinkDirs {
		if fileExists(dir) {
			return true
		}
	}
	return false
}

// hasSysVInit reports whether a classic init.d layout with a working boot
// registration tool is present.
func hasSysVInit() bool {
	if !fileExists(initScriptDir) {
		return false
	}
	return hasBinary("update-rc.d") || hasBinary("chkconfig")
}
