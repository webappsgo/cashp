//go:build !linux && !darwin && !freebsd && !windows

package service

import (
	"context"
	"fmt"
	"os"
	"runtime"
)

// ensureServiceAccount reports that this build target has no supported
// account-management tooling. The server still runs, using the directory
// set of the invoking user.
func ensureServiceAccount(ctx context.Context, name, home string) (int, int, error) {
	return 0, 0, fmt.Errorf("system account provisioning is not supported on %s", runtime.GOOS)
}

// removeServiceAccount reports that this build target has no supported
// account-management tooling.
func removeServiceAccount(ctx context.Context, name string) error {
	return fmt.Errorf("system account removal is not supported on %s", runtime.GOOS)
}

// applyOwnership assigns the service account as owner of a directory. A
// negative identity means the caller is unprivileged and ownership stays
// with the invoking user.
func applyOwnership(path string, uid, gid int) error {
	if uid < 0 || gid < 0 {
		return nil
	}
	return os.Chown(path, uid, gid)
}
