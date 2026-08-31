//go:build windows

package paths

import (
	"os/exec"
	"strconv"
	"strings"
)

// processImageName returns the image name of a PID using tasklist, which is
// present on every supported Windows release and needs no cgo.
func processImageName(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}

	output, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH", "/FO", "CSV").Output()
	if err != nil {
		return "", false
	}

	line := strings.TrimSpace(string(output))
	if line == "" || !strings.HasPrefix(line, "\"") {
		return "", false
	}

	fields := strings.Split(line, "\",\"")
	if len(fields) == 0 {
		return "", false
	}
	return strings.Trim(fields[0], "\""), true
}

// isProcessRunning reports whether a process with this PID exists.
func isProcessRunning(pid int) bool {
	_, ok := processImageName(pid)
	return ok
}

// isOurProcess reports whether the PID belongs to an instance of this
// binary rather than an unrelated process that reused the number.
func isOurProcess(pid int) bool {
	image, ok := processImageName(pid)
	if !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSuffix(image, ".exe"), Name)
}
