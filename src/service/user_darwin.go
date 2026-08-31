//go:build darwin

package service

import (
	"context"
	"fmt"
	"os"
	"strconv"
)

// ensureServiceAccount creates the cashp service account through dscl with
// a matching UID/GID in the macOS 200-399 system range, hidden from the
// login window (AI.md PART 24 "macOS Service Account").
func ensureServiceAccount(ctx context.Context, name, home string) (int, int, error) {
	if uid, gid, ok := lookupServiceAccount(name); ok {
		return uid, gid, nil
	}
	if !hasBinary("dscl") {
		return 0, 0, fmt.Errorf("cannot create system account %q: dscl is not available", name)
	}
	id, err := FindAvailableSystemID()
	if err != nil {
		return 0, 0, err
	}
	if err := os.MkdirAll(home, stateDirMode); err != nil {
		return 0, 0, fmt.Errorf("create home directory %s: %w", home, err)
	}
	sid := strconv.Itoa(id)
	commands := [][]string{
		{".", "-create", "/Groups/" + name},
		{".", "-create", "/Groups/" + name, "PrimaryGroupID", sid},
		{".", "-create", "/Groups/" + name, "Password", "*"},
		{".", "-create", "/Users/" + name},
		{".", "-create", "/Users/" + name, "UniqueID", sid},
		{".", "-create", "/Users/" + name, "PrimaryGroupID", sid},
		{".", "-create", "/Users/" + name, "UserShell", "/usr/bin/false"},
		{".", "-create", "/Users/" + name, "RealName", ServiceAccountGecos},
		{".", "-create", "/Users/" + name, "NFSHomeDirectory", home},
		{".", "-create", "/Users/" + name, "Password", "*"},
		{".", "-create", "/Users/" + name, "IsHidden", "1"},
	}
	for _, args := range commands {
		if err := run(ctx, "dscl", args...); err != nil {
			return 0, 0, err
		}
	}
	return id, id, nil
}

// removeServiceAccount deletes the dscl user and group records.
func removeServiceAccount(ctx context.Context, name string) error {
	if _, _, ok := lookupServiceAccount(name); !ok {
		return nil
	}
	if !hasBinary("dscl") {
		return fmt.Errorf("cannot delete system account %q: dscl is not available", name)
	}
	if err := run(ctx, "dscl", ".", "-delete", "/Users/"+name); err != nil {
		return err
	}
	// The group record may already be gone; that is not an error.
	_ = run(ctx, "dscl", ".", "-delete", "/Groups/"+name)
	return nil
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
