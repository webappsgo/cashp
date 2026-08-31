// Package terminal reports terminal dimensions, the responsive size mode
// derived from them, resize notifications, and the Unicode/ASCII symbol
// sets, per AI.md PART 7 "Terminal Package".
package terminal

import (
	"os"
	"strconv"
)

// DefaultCols and DefaultRows are assumed when the real size is unknown
// (piped output, no controlling terminal).
const (
	DefaultCols = 80
	DefaultRows = 24
)

// SizeMode is the responsive breakpoint the terminal falls into.
type SizeMode int

const (
	// SizeModeMicro is <40 cols or <10 rows.
	SizeModeMicro SizeMode = iota
	// SizeModeMinimal is 40-59 cols or 10-15 rows.
	SizeModeMinimal
	// SizeModeCompact is 60-79 cols or 16-23 rows.
	SizeModeCompact
	// SizeModeStandard is 80-119 cols and 24-39 rows.
	SizeModeStandard
	// SizeModeWide is 120-199 cols and 40-59 rows.
	SizeModeWide
	// SizeModeUltrawide is 200-399 cols and 60-79 rows.
	SizeModeUltrawide
	// SizeModeMassive is 400+ cols and 80+ rows.
	SizeModeMassive
)

// String returns the lowercase name of the size mode.
func (s SizeMode) String() string {
	switch s {
	case SizeModeMinimal:
		return "minimal"
	case SizeModeCompact:
		return "compact"
	case SizeModeStandard:
		return "standard"
	case SizeModeWide:
		return "wide"
	case SizeModeUltrawide:
		return "ultrawide"
	case SizeModeMassive:
		return "massive"
	default:
		return "micro"
	}
}

// TerminalSize is the current terminal geometry and its size mode.
type TerminalSize struct {
	Cols int
	Rows int
	Mode SizeMode
}

// GetTerminalSize returns the current terminal size, falling back to the
// 80x24 default when the size cannot be determined.
func GetTerminalSize() TerminalSize {
	cols, rows, ok := RawSize()
	if !ok || cols <= 0 {
		cols = DefaultCols
	}
	if !ok || rows <= 0 {
		rows = DefaultRows
	}

	return TerminalSize{
		Cols: cols,
		Rows: rows,
		Mode: ModeFor(cols, rows),
	}
}

// RawSize returns the real terminal geometry. The third result is false
// when no attached stream is a terminal and no COLUMNS/LINES override is
// present, which lets callers distinguish "unknown" from the default.
func RawSize() (cols, rows int, ok bool) {
	for _, f := range []*os.File{os.Stdout, os.Stderr, os.Stdin} {
		if f == nil {
			continue
		}
		if c, r, sized := termSize(f.Fd()); sized && c > 0 && r > 0 {
			return c, r, true
		}
	}

	envCols := envInt("COLUMNS")
	envRows := envInt("LINES")
	if envCols > 0 && envRows > 0 {
		return envCols, envRows, true
	}

	return 0, 0, false
}

// ModeFor maps a column and row count onto a size mode. A dimension that
// falls into a smaller bucket wins, so a wide but short terminal is still
// treated as small.
func ModeFor(cols, rows int) SizeMode {
	switch {
	case cols < 40 || rows < 10:
		return SizeModeMicro
	case cols < 60 || rows < 16:
		return SizeModeMinimal
	case cols < 80 || rows < 24:
		return SizeModeCompact
	case cols < 120 || rows < 40:
		return SizeModeStandard
	case cols < 200 || rows < 60:
		return SizeModeWide
	case cols < 400 || rows < 80:
		return SizeModeUltrawide
	default:
		return SizeModeMassive
	}
}

// ShowASCIIArt reports whether there is room for the ASCII art logo.
func (s SizeMode) ShowASCIIArt() bool { return s >= SizeModeStandard }

// ShowBorders reports whether boxes and borders should be drawn.
func (s SizeMode) ShowBorders() bool { return s >= SizeModeCompact }

// ShowSidebar reports whether a sidebar fits alongside the main content.
func (s SizeMode) ShowSidebar() bool { return s >= SizeModeWide }

// ShowIcons reports whether icons should be shown next to labels.
func (s SizeMode) ShowIcons() bool { return s >= SizeModeMinimal }

// MaxTableColumns returns how many table columns fit in this size mode.
func (s SizeMode) MaxTableColumns() int {
	switch s {
	case SizeModeMicro:
		return 2
	case SizeModeMinimal:
		return 3
	case SizeModeCompact:
		return 4
	case SizeModeStandard:
		return 6
	default:
		return 10
	}
}

// envInt reads a positive integer from an environment variable, returning
// 0 when it is unset or not a positive number.
func envInt(key string) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil || value <= 0 {
		return 0
	}
	return value
}
