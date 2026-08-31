//go:build freebsd

package service

import (
	"context"
	"fmt"
	"os"
	"strconv"
)

// ensureServiceAccount creates the cashp system group and user through
// pw(8) with a matching UID/GID (AI.md PART 24 "FreeBSD").
func ensureServiceAccount(ctx context.Context, name, home string) (int, int, error) {
	if uid, gid, ok := lookupServiceAccount(name); ok {
		return uid, gid, nil
	}
	if !hasBinary("pw") {
		return 0, 0, fmt.Errorf("cannot create system account %q: pw(8) is not available", name)
	}
	id, err := FindAvailableSystemID()
	if err != nil {
		return 0, 0, err
	}
	if err := os.MkdirAll(home, stateDirMode); err != nil {
		return 0, 0, fmt.Errorf("create home directory %s: %w", home, err)
	}
	sid := strconv.Itoa(id)
	if err := run(ctx, "pw", "groupadd", "-n", name, "-g", sid); err != nil {
		return 0, 0, err
	}
	if err := run(ctx, "pw", "useradd", "-n", name, "-u", sid, "-g", sid,
		"-d", home, "-s", nologinShell(), "-c", ServiceAccountGecos); err != nil {
		return 0, 0, err
	}
	return id, id, nil
}

// removeServiceAccount deletes the system user and its group.
func removeServiceAccount(ctx context.Context, name string) error {
	if _, _, ok := lookupServiceAccount(name); !ok {
		return nil
	}
	if !hasBinary("pw") {
		return fmt.Errorf("cannot delete system account %q: pw(8) is not available", name)
	}
	if err := run(ctx, "pw", "userdel", "-n", name); err != nil {
		return err
	}
	// The group may already be gone with the user; that is not an error.
	_ = run(ctx, "pw", "groupdel", "-n", name)
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
