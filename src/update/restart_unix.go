//go:build !windows

package update

import (
	"fmt"
	"os"
	"syscall"
)

// Restart re-executes the freshly installed binary in place (AI.md PART 23
// "Update Flow" step 5). On Unix syscall.Exec replaces the running process
// image, so the caller never returns on success.
func Restart() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("update: locate executable: %w", err)
	}

	if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
		return fmt.Errorf("update: restart: %w", err)
	}

	return nil
}
