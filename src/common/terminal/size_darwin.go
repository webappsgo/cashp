//go:build darwin

package terminal

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// termSize reads the window size on macOS. Go's syscall package no longer
// exposes the ioctl trap numbers on darwin, so the size comes from stty
// reading the controlling terminal directly. The fd argument is accepted
// for signature parity with the ioctl implementations.
func termSize(fd uintptr) (cols, rows int, ok bool) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return 0, 0, false
	}
	defer tty.Close()

	cmd := exec.Command("stty", "size")
	cmd.Stdin = tty
	output, err := cmd.Output()
	if err != nil {
		return 0, 0, false
	}

	fields := strings.Fields(string(output))
	if len(fields) != 2 {
		return 0, 0, false
	}
	rows, err = strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, false
	}
	cols, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, false
	}
	return cols, rows, cols > 0 && rows > 0
}
