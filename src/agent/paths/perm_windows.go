//go:build windows

package paths

import (
	"io/fs"
	"os"
)

// setDirPermissions is a no-op on Windows, where access is controlled by
// inherited ACLs rather than Unix mode bits.
func setDirPermissions(path string) error {
	return nil
}

// setFilePermissions is a no-op on Windows for the same reason.
func setFilePermissions(path string) error {
	return nil
}

// permsAreSecure always reports true on Windows: the mode bits Go reports
// are synthesised and say nothing about the real ACL.
func permsAreSecure(mode fs.FileMode) bool {
	return true
}

// hasWindowsAdmin reports whether the process can open a raw device, which
// only an elevated token permits.
func hasWindowsAdmin() bool {
	handle, err := os.Open("\\\\.\\PHYSICALDRIVE0")
	if err != nil {
		return false
	}
	_ = handle.Close()
	return true
}
