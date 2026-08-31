//go:build windows

package paths

import "os"

// setDirPermissions is a no-op on Windows: %APPDATA% and %LOCALAPPDATA%
// already grant access to the owning user only, and child objects inherit
// that ACL.
func setDirPermissions(dir string) error {
	return nil
}

// setFilePermissions is a no-op on Windows for the same inheritance reason
// as setDirPermissions.
func setFilePermissions(path string) error {
	return nil
}

// permsAreSecure always reports true on Windows because the Unix mode bits
// are not the access-control mechanism there; ACL inheritance from the
// user profile directory is.
func permsAreSecure(mode os.FileMode) bool {
	return true
}
