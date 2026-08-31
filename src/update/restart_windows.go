//go:build windows

package update

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

// startupGrace gives the spawned process time to take over before the
// current one exits.
const startupGrace = 100 * time.Millisecond

// Restart spawns the freshly installed binary and exits (AI.md PART 23
// "Update Flow" step 5). Windows has no exec() replacement, so the update
// completes in a new process.
func Restart() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("update: locate executable: %w", err)
	}

	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("update: restart: %w", err)
	}

	time.Sleep(startupGrace)
	os.Exit(0)

	return nil
}
