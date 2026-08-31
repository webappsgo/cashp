//go:build !windows

package paths

import "os"

// isElevated reports whether the process runs as root. It is called once at
// package initialisation, before any privilege drop.
func isElevated() bool {
	return os.Geteuid() == 0
}
