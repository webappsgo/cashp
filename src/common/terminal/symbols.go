package terminal

// SymbolSet holds the status glyphs used in console output. Two sets
// exist: Unicode for capable terminals and ASCII for dumb terminals,
// NO_COLOR output, and non-UTF-8 locales.
type SymbolSet struct {
	Success string
	Error   string
	Warning string
	Info    string
	Arrow   string
	Check   string
	Cross   string
	Bullet  string
	Spinner []string
}

// SymbolsUnicode is the Unicode symbol set.
var SymbolsUnicode = SymbolSet{
	Success: "✓",
	Error:   "✗",
	Warning: "⚠",
	Info:    "ℹ",
	Arrow:   "→",
	Check:   "☑",
	Cross:   "☒",
	Bullet:  "•",
	Spinner: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
}

// SymbolsASCII is the fallback set for terminals without Unicode support.
var SymbolsASCII = SymbolSet{
	Success: "[OK]",
	Error:   "[ERR]",
	Warning: "[WARN]",
	Info:    "[INFO]",
	Arrow:   "->",
	Check:   "[x]",
	Cross:   "[ ]",
	Bullet:  "*",
	Spinner: []string{"|", "/", "-", "\\"},
}

// BoxSet holds the box-drawing characters used for tables and panels.
type BoxSet struct {
	TopLeft     string
	TopRight    string
	BottomLeft  string
	BottomRight string
	Horizontal  string
	Vertical    string
	Cross       string
	TeeDown     string
	TeeUp       string
	TeeRight    string
	TeeLeft     string
}

// BoxUnicode is the Unicode box-drawing set.
var BoxUnicode = BoxSet{
	TopLeft:     "┌",
	TopRight:    "┐",
	BottomLeft:  "└",
	BottomRight: "┘",
	Horizontal:  "─",
	Vertical:    "│",
	Cross:       "┼",
	TeeDown:     "┬",
	TeeUp:       "┴",
	TeeRight:    "├",
	TeeLeft:     "┤",
}

// BoxASCII is the ASCII fallback box set.
var BoxASCII = BoxSet{
	TopLeft:     "+",
	TopRight:    "+",
	BottomLeft:  "+",
	BottomRight: "+",
	Horizontal:  "-",
	Vertical:    "|",
	Cross:       "+",
	TeeDown:     "+",
	TeeUp:       "+",
	TeeRight:    "+",
	TeeLeft:     "+",
}

// Symbols returns the symbol set matching the terminal's capability.
func Symbols(unicode bool) SymbolSet {
	if unicode {
		return SymbolsUnicode
	}
	return SymbolsASCII
}

// Box returns the box-drawing set matching the terminal's capability.
func Box(unicode bool) BoxSet {
	if unicode {
		return BoxUnicode
	}
	return BoxASCII
}
