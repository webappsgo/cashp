package display

import (
	"os"
	"strings"

	"github.com/webappsgo/cashp/src/common/terminal"
)

// DisplayEnv is the detected display environment.
type DisplayEnv struct {
	// Mode is the resolved display mode.
	Mode DisplayMode
	// HasDisplay reports an X11, Wayland, Windows, or macOS display.
	HasDisplay bool
	// DisplayType is "x11", "wayland", "windows", "windows-rdp", "macos", or "none".
	DisplayType string
	// IsTerminal reports whether stdout is a TTY.
	IsTerminal bool
	// IsSSH reports an SSH session.
	IsSSH bool
	// IsMosh reports a mosh session.
	IsMosh bool
	// IsScreen reports a screen or tmux session.
	IsScreen bool
	// TerminalType is the TERM value.
	TerminalType string
	// Cols is the terminal width. Falls back to DefaultCols when there is no
	// terminal or the size could not be queried, so callers always have a
	// usable value to lay out CLI/TUI output with.
	Cols int
	// Rows is the terminal height. Falls back to DefaultRows under the same
	// conditions as Cols.
	Rows int
}

// DefaultCols and DefaultRows are the classic 80x24 terminal size, used
// whenever the real size can't be determined (no TTY, ioctl failure, etc.).
const (
	DefaultCols = 80
	DefaultRows = 24
)

// ConfigColor is an optional hook supplying the config file's colour
// preference. It returns the configured value and whether it was set. The
// main package wires it after loading the config; while it is nil the
// config step of the precedence chain is skipped.
var ConfigColor func() (value bool, set bool)

// ConfigEmoji is an optional hook supplying the config file's emoji
// preference, wired the same way as ConfigColor.
var ConfigEmoji func() (value bool, set bool)

// IsTerminalFile reports whether the file is attached to a character
// device, which is the portable stdlib test for a TTY.
func IsTerminalFile(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// DetectDisplayEnv auto-detects the display environment.
func DetectDisplayEnv() DisplayEnv {
	env := DisplayEnv{}

	env.IsTerminal = IsTerminalFile(os.Stdout)
	if env.IsTerminal {
		if cols, rows, ok := terminal.RawSize(); ok {
			env.Cols, env.Rows = cols, rows
		}
	}
	if env.Cols <= 0 || env.Rows <= 0 {
		env.Cols, env.Rows = DefaultCols, DefaultRows
	}
	env.TerminalType = os.Getenv("TERM")

	env.IsSSH = os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != ""
	env.IsMosh = os.Getenv("MOSH") != "" || strings.Contains(os.Getenv("TERM"), "mosh")
	env.IsScreen = os.Getenv("STY") != "" || os.Getenv("TMUX") != ""

	env.detectPlatformDisplay()

	env.Mode = env.autoDetectDisplayMode()

	return env
}

// autoDetectDisplayMode determines the display mode from the environment.
func (e *DisplayEnv) autoDetectDisplayMode() DisplayMode {
	if !e.IsTerminal && !e.HasDisplay {
		return DisplayModeHeadless
	}
	// TERM=dumb forces CLI mode: no TUI and no ANSI escapes.
	if e.TerminalType == "dumb" {
		return DisplayModeCLI
	}
	if e.HasDisplay && !e.IsSSH && !e.IsMosh {
		return DisplayModeGUI
	}
	if e.IsTerminal {
		return DisplayModeTUI
	}
	return DisplayModeCLI
}

// IsDumbTerminal reports a terminal with no ANSI support.
func (e *DisplayEnv) IsDumbTerminal() bool {
	return e.TerminalType == "dumb"
}

// SupportsUnicode reports whether the terminal can render Unicode symbols.
// It answers terminal capability only: a dumb terminal cannot, and neither
// can a terminal whose locale is not UTF-8. NO_COLOR does not change the
// answer here because NO_COLOR never disables box drawing — see
// UseUnicodeSymbols for the status-symbol decision.
func (e DisplayEnv) SupportsUnicode() bool {
	if e.IsDumbTerminal() {
		return false
	}
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		value := strings.ToLower(os.Getenv(key))
		if value == "" {
			continue
		}
		return strings.Contains(value, "utf-8") || strings.Contains(value, "utf8")
	}
	// No locale set: assume a modern UTF-8 terminal unless it is dumb.
	return true
}

// UseUnicodeSymbols reports whether status symbols should use the Unicode
// set. Status symbols follow the emoji policy, so NO_COLOR and TERM=dumb
// both fall back to the ASCII set ([OK], [ERR], ...).
func (e DisplayEnv) UseUnicodeSymbols() bool {
	return e.SupportsUnicode() && EmojiEnabled()
}

// Helper methods with clear names.
func (e DisplayEnv) IsAutoDetectDisplayModeGUI() bool      { return e.Mode == DisplayModeGUI }
func (e DisplayEnv) IsAutoDetectDisplayModeTUI() bool      { return e.Mode == DisplayModeTUI }
func (e DisplayEnv) IsAutoDetectDisplayModeCLI() bool      { return e.Mode == DisplayModeCLI }
func (e DisplayEnv) IsAutoDetectDisplayModeHeadless() bool { return e.Mode == DisplayModeHeadless }

// CanUseANSI reports whether ANSI features (cursor movement, clearing,
// colour) may be emitted. NO_COLOR users want plain output, so it is
// respected here as well.
func CanUseANSI(env *DisplayEnv) bool {
	if env == nil {
		return false
	}
	if env.IsDumbTerminal() {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return env.IsTerminal
}

// ColorEnabled reports whether colour output should be used. Precedence,
// highest first: CLI flag, config file, NO_COLOR env var, auto-detect.
// forceColor is nil when the --color flag was not passed.
func ColorEnabled(forceColor *bool) bool {
	if forceColor != nil {
		return *forceColor
	}
	if ConfigColor != nil {
		if value, set := ConfigColor(); set {
			return value
		}
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if !IsTerminalFile(os.Stdout) {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return true
}

// EmojiEnabled reports whether emojis should be used. The config file may
// force emojis on even when NO_COLOR is set; otherwise NO_COLOR and
// TERM=dumb both disable them.
func EmojiEnabled() bool {
	if ConfigEmoji != nil {
		if value, set := ConfigEmoji(); set && value {
			return true
		}
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return true
}

// ParseColorFlag converts a --color value into the pointer ColorEnabled
// expects. "auto" and an empty value return nil so auto-detection runs.
func ParseColorFlag(value string) *bool {
	yes := true
	no := false
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes", "true", "on", "always", "1", "enable", "enabled":
		return &yes
	case "no", "false", "off", "never", "0", "disable", "disabled", "none":
		return &no
	default:
		return nil
	}
}
