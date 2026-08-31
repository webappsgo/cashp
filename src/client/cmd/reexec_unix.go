//go:build !windows

package cmd

import (
	"fmt"
	"os"
	"syscall"
)

// reexec replaces the current process with the freshly installed binary so
// the interrupted command continues transparently.
func reexec(target string, argv []string) error {
	args := append([]string{target}, argv...)
	if err := syscall.Exec(target, args, os.Environ()); err != nil {
		return fmt.Errorf("restart after update: %w", err)
	}
	return nil
}
