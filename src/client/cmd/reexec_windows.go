//go:build windows

package cmd

import (
	"fmt"
	"os"
	"os/exec"
)

// reexec starts the updated binary as a child process, because Windows has
// no exec-replace equivalent.
func reexec(target string, argv []string) error {
	command := exec.Command(target, argv...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("restart after update: %w", err)
	}
	return nil
}
