package banner

import "strings"

// glyphRows is the height of every glyph in the block font.
const glyphRows = 5

// glyphWidth is the width of one glyph, excluding the spacer column added
// between glyphs.
const glyphWidth = 5

// blockFont maps a character onto its five-row block representation. Any
// character missing from the table renders as blank space.
var blockFont = map[rune][glyphRows]string{
	'A': {" ### ", "#   #", "#####", "#   #", "#   #"},
	'B': {"#### ", "#   #", "#### ", "#   #", "#### "},
	'C': {" ####", "#    ", "#    ", "#    ", " ####"},
	'D': {"#### ", "#   #", "#   #", "#   #", "#### "},
	'E': {"#####", "#    ", "#### ", "#    ", "#####"},
	'F': {"#####", "#    ", "#### ", "#    ", "#    "},
	'G': {" ####", "#    ", "#  ##", "#   #", " ####"},
	'H': {"#   #", "#   #", "#####", "#   #", "#   #"},
	'I': {"#####", "  #  ", "  #  ", "  #  ", "#####"},
	'J': {"#####", "    #", "    #", "#   #", " ### "},
	'K': {"#   #", "#  # ", "###  ", "#  # ", "#   #"},
	'L': {"#    ", "#    ", "#    ", "#    ", "#####"},
	'M': {"#   #", "## ##", "# # #", "#   #", "#   #"},
	'N': {"#   #", "##  #", "# # #", "#  ##", "#   #"},
	'O': {" ### ", "#   #", "#   #", "#   #", " ### "},
	'P': {"#### ", "#   #", "#### ", "#    ", "#    "},
	'Q': {" ### ", "#   #", "#   #", "#  # ", " ## #"},
	'R': {"#### ", "#   #", "#### ", "#  # ", "#   #"},
	'S': {" ####", "#    ", " ### ", "    #", "#### "},
	'T': {"#####", "  #  ", "  #  ", "  #  ", "  #  "},
	'U': {"#   #", "#   #", "#   #", "#   #", " ### "},
	'V': {"#   #", "#   #", "#   #", " # # ", "  #  "},
	'W': {"#   #", "#   #", "# # #", "## ##", "#   #"},
	'X': {"#   #", " # # ", "  #  ", " # # ", "#   #"},
	'Y': {"#   #", " # # ", "  #  ", "  #  ", "  #  "},
	'Z': {"#####", "   # ", "  #  ", " #   ", "#####"},
	'0': {" ### ", "#  ##", "# # #", "##  #", " ### "},
	'1': {"  #  ", " ##  ", "  #  ", "  #  ", "#####"},
	'2': {" ### ", "#   #", "  ## ", " #   ", "#####"},
	'3': {"#### ", "    #", " ### ", "    #", "#### "},
	'4': {"#   #", "#   #", "#####", "    #", "    #"},
	'5': {"#####", "#    ", "#### ", "    #", "#### "},
	'6': {" ### ", "#    ", "#### ", "#   #", " ### "},
	'7': {"#####", "    #", "   # ", "  #  ", "  #  "},
	'8': {" ### ", "#   #", " ### ", "#   #", " ### "},
	'9': {" ### ", "#   #", " ####", "    #", " ### "},
	'-': {"     ", "     ", "#####", "     ", "     "},
	'_': {"     ", "     ", "     ", "     ", "#####"},
	'.': {"     ", "     ", "     ", "     ", "  #  "},
	' ': {"     ", "     ", "     ", "     ", "     "},
}

// blankGlyph renders characters the font does not cover.
var blankGlyph = blockFont[' ']

// ASCIIArtWidth returns the rendered width of the name in the block font.
func ASCIIArtWidth(name string) int {
	count := len([]rune(name))
	if count == 0 {
		return 0
	}
	return count*(glyphWidth+1) - 1
}

// ASCIIArt renders the name as five rows of block letters. An empty name
// yields an empty string.
func ASCIIArt(name string) string {
	runes := []rune(strings.ToUpper(name))
	if len(runes) == 0 {
		return ""
	}

	rows := make([]strings.Builder, glyphRows)
	for i, r := range runes {
		glyph, known := blockFont[r]
		if !known {
			glyph = blankGlyph
		}
		for row := 0; row < glyphRows; row++ {
			if i > 0 {
				rows[row].WriteString(" ")
			}
			rows[row].WriteString(glyph[row])
		}
	}

	lines := make([]string, glyphRows)
	for row := 0; row < glyphRows; row++ {
		lines[row] = strings.TrimRight(rows[row].String(), " ")
	}
	return strings.Join(lines, "\n")
}

// ASCIIArtFit renders the name as block letters when it fits the given
// width, and returns the plain uppercase name when it does not.
func ASCIIArtFit(name string, width int) string {
	if width > 0 && ASCIIArtWidth(name) > width {
		return strings.ToUpper(name)
	}
	return ASCIIArt(name)
}
