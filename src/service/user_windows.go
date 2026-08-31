//go:build windows

package service

import (
	"context"
	"fmt"
)

// virtualServiceAccount is the Windows Virtual Service Account the service
// runs as. Windows creates and manages it automatically when the service is
// installed, so no account provisioning happens here. It already carries
// minimal privileges, which is why no privilege drop is needed on Windows
// (AI.md PART 24 "Windows Service Account").
const virtualServiceAccount = `NT SERVICE\` + ServiceAccountName

// ensureServiceAccount is a no-op on Windows: the Virtual Service Account
// is created by the service control manager. The negative identity tells
// the directory pass to grant access through an ACL instead of chown.
func ensureServiceAccount(ctx context.Context, name, home string) (int, int, error) {
	return -1, -1, nil
}

// removeServiceAccount is a no-op on Windows: deleting the service also
// removes its Virtual Service Account.
func removeServiceAccount(ctx context.Context, name string) error {
	return nil
}

// applyOwnership grants the Virtual Service Account full control of a
// directory, the Windows equivalent of the Unix chown pass (AI.md PART 24
// "Directory Permissions").
func applyOwnership(path string, uid, gid int) error {
	if !IsElevated() {
		return nil
	}
	if !hasBinary("icacls") {
		return fmt.Errorf("cannot grant %s access to %s: icacls is not available", virtualServiceAccount, path)
	}
	ctx, cancel := context.WithTimeout(context.Background(), accountCommandTimeout)
	defer cancel()
	return run(ctx, "icacls", path, "/grant", virtualServiceAccount+":(OI)(CI)F", "/T")
}
