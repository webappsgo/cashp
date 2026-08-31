//go:build !windows

package paths

import "os"

// setDirPermissions enforces 0700 on a CLI directory.
func setDirPermissions(dir string) error {
	return os.Chmod(dir, DirPerm)
}

// setFilePermissions enforces 0600 on a CLI file.
func setFilePermissions(path string) error {
	return os.Chmod(path, FilePerm)
}

// permsAreSecure reports whether a file mode denies all group and other
// access, which is the Unix requirement for credential-bearing files.
func permsAreSecure(mode os.FileMode) bool {
	return mode&0o077 == 0
}
