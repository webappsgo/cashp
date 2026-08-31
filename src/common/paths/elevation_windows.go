//go:build windows

package paths

import (
	"os"
	"path/filepath"
)

// isElevated reports whether the process runs with Administrator rights.
// Opening a raw physical drive handle requires an elevated token, so a
// successful open is a reliable stdlib-only elevation probe. When the drive
// is absent the check falls back to writing inside the system directory,
// which is likewise only permitted to an administrator.
func isElevated() bool {
	if f, err := os.Open(`\\.\PHYSICALDRIVE0`); err == nil {
		f.Close()
		return true
	}

	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}

	probe := filepath.Join(systemRoot, "System32", "config", ".elevation-probe")
	f, err := os.Create(probe)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(probe)
	return true
}
