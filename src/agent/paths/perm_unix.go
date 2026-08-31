//go:build !windows

package paths

import (
	"fmt"
	"io/fs"
	"os"
)

// setDirPermissions tightens an existing directory to owner-only, which
// MkdirAll does not do when the directory already exists.
func setDirPermissions(path string) error {
	if err := os.Chmod(path, DirPerm); err != nil {
		return fmt.Errorf("set permissions on %s: %w", path, err)
	}
	return nil
}

// setFilePermissions tightens a file to owner-only.
func setFilePermissions(path string) error {
	if err := os.Chmod(path, FilePerm); err != nil {
		return fmt.Errorf("set permissions on %s: %w", path, err)
	}
	return nil
}

// permsAreSecure reports whether no group or other bits are set.
func permsAreSecure(mode fs.FileMode) bool {
	return mode.Perm()&0o077 == 0
}

// hasWindowsAdmin is never reached on Unix; the Unix build decides
// elevation from the effective UID.
func hasWindowsAdmin() bool {
	return false
}
