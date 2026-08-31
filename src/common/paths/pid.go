package paths

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// processRunning and processIsOurs are indirections over the platform
// process checks so tests can exercise the stale and reused-PID branches
// without spawning real processes.
var (
	processRunning = isProcessRunning
	processIsOurs  = isOurProcess
)

// CheckPIDFile reports whether the PID file names a live instance of this
// binary. A corrupt, stale, or reused PID file is removed and reported as
// not running. Containers never use a PID file, so the answer there is
// always "not running".
func CheckPIDFile(pidPath string) (bool, int, error) {
	if inContainer() {
		return false, 0, nil
	}

	data, err := os.ReadFile(pidPath)
	if os.IsNotExist(err) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("reading pid file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		// Corrupt PID file.
		os.Remove(pidPath)
		return false, 0, nil
	}

	if !processRunning(pid) {
		// Stale PID file.
		os.Remove(pidPath)
		return false, 0, nil
	}

	if !processIsOurs(pid) {
		// The PID was reused by an unrelated process.
		os.Remove(pidPath)
		return false, 0, nil
	}

	return true, pid, nil
}

// WritePIDFile records the current PID after confirming no other instance
// is running. It is a no-op inside a container.
func WritePIDFile(pidPath string) error {
	if inContainer() {
		return nil
	}

	running, existingPID, err := CheckPIDFile(pidPath)
	if err != nil {
		return err
	}
	if running {
		return fmt.Errorf("already running (pid %d)", existingPID)
	}

	pid := os.Getpid()
	return os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), PIDPerm())
}

// RemovePIDFile deletes the PID file during shutdown. A missing file is
// not an error, and containers never created one.
func RemovePIDFile(pidPath string) error {
	if inContainer() {
		return nil
	}
	if err := os.Remove(pidPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
