// Package term provides the small amount of terminal awareness the CLI and
// agent need — TTY detection, terminal width and echo-free input — using
// only the standard library so both binaries stay pure Go and CGO-free.
package term

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
)

// DefaultWidth is used when the real terminal width cannot be determined.
const DefaultWidth = 80

// Screen size categories from AI.md PART 33 "Screen Size Categories".
const (
	SizeSmall  = "small"
	SizeMedium = "medium"
	SizeLarge  = "large"
)

// IsTTY reports whether f is attached to a character device.
func IsTTY(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// Interactive reports whether both stdin and stdout are terminals, which is
// the condition for prompting the user.
func Interactive() bool {
	return IsTTY(os.Stdin) && IsTTY(os.Stdout)
}

// Width returns the terminal width in columns, honouring COLUMNS before
// asking the operating system.
func Width() int {
	if value := strings.TrimSpace(os.Getenv("COLUMNS")); value != "" {
		if columns, err := strconv.Atoi(value); err == nil && columns > 0 {
			return columns
		}
	}
	if columns := osWidth(); columns > 0 {
		return columns
	}
	return DefaultWidth
}

// SizeCategory classifies a width into the layout breakpoints the CLI uses
// to decide how much detail to render.
func SizeCategory(width int) string {
	switch {
	case width < 80:
		return SizeSmall
	case width < 120:
		return SizeMedium
	default:
		return SizeLarge
	}
}

// ReadLine reads a single line from r, trimming the newline.
func ReadLine(r io.Reader) (string, error) {
	reader := bufio.NewReader(r)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// ReadSecret reads a line without echoing it when the platform supports
// disabling echo. EchoDisabled reports whether the input was actually
// hidden, so callers can warn the user when it was not.
func ReadSecret(r io.Reader) (secret string, echoDisabled bool, err error) {
	restore, disabled := disableEcho()
	if disabled {
		defer restore()
	}
	line, err := ReadLine(r)
	return line, disabled, err
}
