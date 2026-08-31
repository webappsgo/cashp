//go:build linux

package service

import (
	"context"
	"fmt"
	"os"
	"strconv"
)

// ensureServiceAccount creates the cashp system group and user with a
// matching UID/GID when they do not already exist, and returns the numeric
// identity to own the state directories (AI.md PART 24 "Platform-Specific
// Commands").
func ensureServiceAccount(ctx context.Context, name, home string) (int, int, error) {
	if uid, gid, ok := lookupServiceAccount(name); ok {
		return uid, gid, nil
	}
	id, err := FindAvailableSystemID()
	if err != nil {
		return 0, 0, err
	}
	if err := os.MkdirAll(home, stateDirMode); err != nil {
		return 0, 0, fmt.Errorf("create home directory %s: %w", home, err)
	}
	if err := createLinuxAccount(ctx, name, home, id); err != nil {
		return 0, 0, err
	}
	return id, id, nil
}

// createLinuxAccount uses shadow-utils when available and falls back to the
// BusyBox tools shipped by Alpine, which is a supported target.
func createLinuxAccount(ctx context.Context, name, home string, id int) error {
	sid := strconv.Itoa(id)
	shell := nologinShell()
	switch {
	case hasBinary("groupadd") && hasBinary("useradd"):
		if err := run(ctx, "groupadd", "--system", "--gid", sid, name); err != nil {
			return err
		}
		return run(ctx, "useradd", "--system", "--uid", sid, "--gid", sid,
			"--home-dir", home, "--shell", shell,
			"--comment", ServiceAccountGecos, name)
	case hasBinary("addgroup") && hasBinary("adduser"):
		if err := run(ctx, "addgroup", "-S", "-g", sid, name); err != nil {
			return err
		}
		return run(ctx, "adduser", "-S", "-D", "-H", "-h", home, "-s", shell,
			"-G", name, "-g", ServiceAccountGecos, "-u", sid, name)
	default:
		return fmt.Errorf("cannot create system account %q: neither useradd nor adduser is available", name)
	}
}

// removeServiceAccount deletes the system user and its group. Missing
// accounts are not an error, so an uninstall stays idempotent.
func removeServiceAccount(ctx context.Context, name string) error {
	if _, _, ok := lookupServiceAccount(name); !ok {
		return nil
	}
	switch {
	case hasBinary("userdel"):
		if err := run(ctx, "userdel", name); err != nil {
			return err
		}
	case hasBinary("deluser"):
		if err := run(ctx, "deluser", name); err != nil {
			return err
		}
	default:
		return fmt.Errorf("cannot delete system account %q: neither userdel nor deluser is available", name)
	}
	switch {
	case hasBinary("groupdel"):
		// A group already removed together with the user is not an error.
		_ = run(ctx, "groupdel", name)
	case hasBinary("delgroup"):
		_ = run(ctx, "delgroup", name)
	}
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
