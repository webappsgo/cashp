package service

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"time"

	"github.com/webappsgo/cashp/src/config"
)

// ServiceAccountName is the name of the dedicated system user and group.
// The user and the group always share the same numeric ID (AI.md PART 24
// "System User Requirements").
const ServiceAccountName = config.InternalName

// ServiceAccountGecos is the GECOS/RealName field of the service account.
const ServiceAccountGecos = config.InternalName + " service account"

// accountCommandTimeout bounds the account-management helper commands.
const accountCommandTimeout = 30 * time.Second

// Directory permissions applied to the service state directories. The run
// directory is world-readable so process supervisors can read the pid file.
const (
	stateDirMode = 0o750
	runDirMode   = 0o755
)

// EnsureSystemUser provisions the identity, directories and permissions the
// server needs. It is called by the server binary during normal startup, not
// by `--service --install` (AI.md PART 24 "Service Installation Logic").
//
// The cashp server process itself runs permanently as root and never drops
// to this account (IDEA.md "Security decisions & exceptions"): the account
// exists so state directories have a stable owning identity and so
// unprivileged helper processes spawned by the server have a non-root
// identity to run under. Creating it is not, and must not be mistaken for,
// a privilege drop.
//
// When the process is not elevated the function provisions the per-user
// directory set instead and creates no account, because account creation
// requires privileges the caller does not have.
func EnsureSystemUser() error {
	elevated := IsElevated()
	data := DefaultTemplateData(!elevated)
	if !elevated {
		return ensureDirectories(data, -1, -1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), accountCommandTimeout)
	defer cancel()

	uid, gid, err := ensureServiceAccount(ctx, ServiceAccountName, data.ConfigDir)
	if err != nil {
		return err
	}
	return ensureDirectories(data, uid, gid)
}

// ensureDirectories creates every directory the service uses and applies
// ownership when a numeric identity is available.
func ensureDirectories(data TemplateData, uid, gid int) error {
	type managedDir struct {
		path string
		mode os.FileMode
	}
	dirs := []managedDir{
		{data.ConfigDir, stateDirMode},
		{data.DataDir, stateDirMode},
		{data.CacheDir, stateDirMode},
		{data.LogDir, stateDirMode},
		{data.RunDir, runDirMode},
	}
	// AI.md PART 8 "Directory Flags" requires every directory flag target to
	// be created; the backup directory is skipped only when unresolved.
	if data.BackupDir != "" {
		dirs = append(dirs, managedDir{data.BackupDir, stateDirMode})
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir.path, dir.mode); err != nil {
			return fmt.Errorf("create %s: %w", dir.path, err)
		}
		if err := os.Chmod(dir.path, dir.mode); err != nil {
			return fmt.Errorf("set permissions on %s: %w", dir.path, err)
		}
		if err := applyOwnership(dir.path, uid, gid); err != nil {
			return fmt.Errorf("set ownership on %s: %w", dir.path, err)
		}
	}
	return nil
}

// lookupServiceAccount returns the numeric UID and GID of an existing
// account, reporting whether it was found.
func lookupServiceAccount(name string) (int, int, bool) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, 0, false
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, false
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return 0, 0, false
	}
	return uid, gid, true
}

// nologinShells are the non-login shells tried in order when creating the
// service account.
var nologinShells = []string{"/sbin/nologin", "/usr/sbin/nologin", "/bin/false"}

// nologinShell returns the first non-login shell present on the host,
// falling back to /bin/false which exists everywhere.
func nologinShell() string {
	for _, shell := range nologinShells {
		if _, err := os.Stat(shell); err == nil {
			return shell
		}
	}
	return "/bin/false"
}
