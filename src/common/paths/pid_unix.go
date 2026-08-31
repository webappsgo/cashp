//go:build !windows

package paths

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// isProcessRunning reports whether a process with this PID exists. Signal 0
// performs the permission and existence check without delivering anything;
// EPERM means the process exists but belongs to another user.
func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return err == syscall.EPERM
}

// isOurProcess reports whether the PID belongs to an instance of this
// binary rather than an unrelated process that reused the number. The
// comparison is an exact base-name match: a substring match would treat
// cashp-cli or cashp-agent as the server.
func isOurProcess(pid int) bool {
	if pid <= 0 {
		return false
	}

	if exe, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe")); err == nil {
		return filepath.Base(exe) == Name
	}

	// macOS and the BSDs have no /proc/{pid}/exe.
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return false
	}
	return filepath.Base(strings.TrimSpace(string(output))) == Name
}
