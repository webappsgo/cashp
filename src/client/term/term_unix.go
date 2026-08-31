//go:build !windows

package term

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// osWidth asks stty for the terminal geometry. stty is invoked with an
// argv slice and constant arguments only — no user input reaches it and no
// shell is involved.
func osWidth() int {
	if !IsTTY(os.Stdout) {
		return 0
	}
	command := exec.Command("stty", "size")
	command.Stdin = os.Stdin
	out, err := command.Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0
	}
	columns, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0
	}
	return columns
}

// disableEcho turns terminal echo off and returns a function that restores
// it. It reports false when echo could not be disabled, so the caller can
// tell the user their input will be visible.
func disableEcho() (restore func(), disabled bool) {
	if !IsTTY(os.Stdin) {
		return func() {}, false
	}
	if err := stty("-echo"); err != nil {
		return func() {}, false
	}
	return func() {
		_ = stty("echo")
	}, true
}

// stty runs a single stty flag against the controlling terminal.
func stty(flag string) error {
	command := exec.Command("stty", flag)
	command.Stdin = os.Stdin
	return command.Run()
}
