// Package display detects the display environment every binary runs in
// (GUI, TUI, CLI, headless) and answers the colour, emoji, and ANSI
// capability questions derived from it, per AI.md PART 7 "Display
// Environment Detection" and PART 8 "NO_COLOR Support".
package display

import "strings"

// DisplayMode is the UI display mode. It is NOT the application mode
// (production/development/debug) — see src/mode for that.
type DisplayMode int

const (
	// DisplayModeHeadless means no display and no TTY (daemon, service, cron).
	DisplayModeHeadless DisplayMode = iota
	// DisplayModeCLI means command-line only output (piped, or a command was given).
	DisplayModeCLI
	// DisplayModeTUI means an interactive terminal UI is possible.
	DisplayModeTUI
	// DisplayModeGUI means a native graphical display is available.
	DisplayModeGUI
)

// String returns the lowercase name of the display mode.
func (m DisplayMode) String() string {
	switch m {
	case DisplayModeGUI:
		return "gui"
	case DisplayModeTUI:
		return "tui"
	case DisplayModeCLI:
		return "cli"
	default:
		return "headless"
	}
}

// ParseDisplayMode converts a configured display.mode value into a
// DisplayMode. The second result is false when the value is unknown, which
// keeps auto-detection in charge.
func ParseDisplayMode(value string) (DisplayMode, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "gui":
		return DisplayModeGUI, true
	case "tui":
		return DisplayModeTUI, true
	case "cli":
		return DisplayModeCLI, true
	case "headless":
		return DisplayModeHeadless, true
	default:
		return DisplayModeHeadless, false
	}
}
