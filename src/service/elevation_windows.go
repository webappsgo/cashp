//go:build windows

package service

import (
	"context"
	"os"
	"os/user"
	"strings"
	"time"
)

// elevationProbePath is a device path only an elevated token may open. It is
// the standard, dependency-free way to detect an elevated process without
// linking golang.org/x/sys.
const elevationProbePath = `\\.\PHYSICALDRIVE0`

// administratorsSID is the well-known SID of the local Administrators
// group, matched in `whoami /groups` output.
const administratorsSID = "S-1-5-32-544"

// whoamiTimeout bounds the group-membership probe.
const whoamiTimeout = 5 * time.Second

// IsElevated reports whether the process runs with an elevated token.
func IsElevated() bool {
	f, err := os.Open(elevationProbePath)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// CanEscalate reports whether the current Windows account can raise itself
// through UAC or runas. It never triggers a UAC prompt: membership is read
// from the process token instead. When escalation is impossible the second
// value explains why (AI.md PART 24 "Windows").
func CanEscalate() (bool, string) {
	elevated := IsElevated()
	return evaluateWindowsEscalation(elevated, inAdministratorsGroup(), currentAccountName())
}

// inAdministratorsGroup reports whether the process token carries the local
// Administrators group SID.
func inAdministratorsGroup() bool {
	ctx, cancel := context.WithTimeout(context.Background(), whoamiTimeout)
	defer cancel()
	out, err := output(ctx, "whoami", "/groups")
	if err != nil {
		return false
	}
	return strings.Contains(out, administratorsSID)
}

// currentAccountName returns the current Windows account name, or a
// placeholder when it cannot be resolved.
func currentAccountName() string {
	u, err := user.Current()
	if err != nil {
		return "unknown"
	}
	return u.Username
}
