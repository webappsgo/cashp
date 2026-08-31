// Package theme owns the single source of truth for the application colour
// palettes (AI.md PART 16 "Unified Color Palette"). Every hex literal in the
// project lives in this file: web CSS variables, Swagger, and GraphiQL are
// generated from it, while CLI, TUI, and GUI map the same semantic roles onto
// ANSI colours instead of consuming the hex values.
package theme

// Theme mode names accepted by GetThemePalette and friends. Dark is the
// product default.
const (
	ModeDark  = "dark"
	ModeLight = "light"
	ModeAuto  = "auto"
)

// ThemePalette is the semantic colour palette rendered by web surfaces.
type ThemePalette struct {
	Background string `json:"background"`
	Foreground string `json:"foreground"`
	Primary    string `json:"primary"`
	Secondary  string `json:"secondary"`
	Accent     string `json:"accent"`
	Success    string `json:"success"`
	Warning    string `json:"warning"`
	Error      string `json:"error"`
	Info       string `json:"info"`
	Surface    string `json:"surface"`
	SurfaceAlt string `json:"surface_alt"`
	Border     string `json:"border"`
	Muted      string `json:"muted"`
}

// ThemePaletteDark is the default palette.
var ThemePaletteDark = ThemePalette{
	Background: "#282a36", Foreground: "#f8f8f2",
	Primary: "#bd93f9", Secondary: "#50fa7b", Accent: "#ff79c6",
	Success: "#50fa7b", Warning: "#ffb86c", Error: "#ff5555", Info: "#8be9fd",
	Surface: "#2b2d3a", SurfaceAlt: "#21222c", Border: "#44475a", Muted: "#6272a4",
}

// ThemePaletteLight is the opt-in light palette, based on GitHub Light.
var ThemePaletteLight = ThemePalette{
	Background: "#ffffff", Foreground: "#1f2328",
	Primary: "#0969da", Secondary: "#1a7f37", Accent: "#8250df",
	Success: "#1a7f37", Warning: "#9a6700", Error: "#d1242f", Info: "#0969da",
	Surface: "#f6f8fa", SurfaceAlt: "#eff2f5", Border: "#d1d9e0", Muted: "#59636e",
}

// PaletteExtras holds the interaction-state colours the CSS variable
// reference defines beyond the semantic palette, plus the alpha used for
// translucent status backgrounds.
type PaletteExtras struct {
	// BgCard is the raised card/panel background.
	BgCard string `json:"bg_card"`
	// BgHover is the hover background for rows, buttons, and links.
	BgHover string `json:"bg_hover"`
	// BgActive is the pressed/selected background.
	BgActive string `json:"bg_active"`
	// BorderHover is the border colour on hover.
	BorderHover string `json:"border_hover"`
	// CodeBg is the inline/code block background overlay.
	CodeBg string `json:"code_bg"`
	// StatusAlpha is the opacity applied to status background tints.
	StatusAlpha float64 `json:"status_alpha"`
}

// PaletteExtrasDark completes the dark palette.
var PaletteExtrasDark = PaletteExtras{
	BgCard:      "#2b2d3a",
	BgHover:     "#343746",
	BgActive:    "#44475a",
	BorderHover: "#6272a4",
	CodeBg:      "rgba(255, 255, 255, 0.1)",
	StatusAlpha: 0.15,
}

// PaletteExtrasLight completes the light palette.
var PaletteExtrasLight = PaletteExtras{
	BgCard:      "#ffffff",
	BgHover:     "#eff2f5",
	BgActive:    "#e6eaef",
	BorderHover: "#818b98",
	CodeBg:      "rgba(0, 0, 0, 0.05)",
	StatusAlpha: 0.12,
}

// TerminalPalette holds ANSI 16-colour indices (0-15) for CLI and TUI
// output. Terminals render a user-configured colour set, so they never
// consume the literal hex palette above.
type TerminalPalette struct {
	Foreground string `json:"foreground"`
	Muted      string `json:"muted"`
	Primary    string `json:"primary"`
	Success    string `json:"success"`
	Warning    string `json:"warning"`
	Error      string `json:"error"`
	Info       string `json:"info"`
	Border     string `json:"border"`
}

// TerminalPaletteDark maps the semantic roles onto bright ANSI colours.
var TerminalPaletteDark = TerminalPalette{
	Foreground: "15", Muted: "7", Primary: "13",
	Success: "10", Warning: "11", Error: "9", Info: "12", Border: "13",
}

// TerminalPaletteLight maps the semantic roles onto normal ANSI colours.
var TerminalPaletteLight = TerminalPalette{
	Foreground: "0", Muted: "8", Primary: "4",
	Success: "2", Warning: "3", Error: "1", Info: "4", Border: "4",
}

// GetThemePalette returns the palette for a theme mode. Anything other
// than "light" or a system-light "auto" resolves to the dark default.
func GetThemePalette(themeMode string) ThemePalette {
	if ResolveMode(themeMode) == ModeLight {
		return ThemePaletteLight
	}
	return ThemePaletteDark
}

// GetPaletteExtras returns the interaction-state colours for a theme mode.
func GetPaletteExtras(themeMode string) PaletteExtras {
	if ResolveMode(themeMode) == ModeLight {
		return PaletteExtrasLight
	}
	return PaletteExtrasDark
}

// GetTerminalPalette returns the ANSI palette for a theme mode.
func GetTerminalPalette(themeMode string) TerminalPalette {
	if ResolveMode(themeMode) == ModeLight {
		return TerminalPaletteLight
	}
	return TerminalPaletteDark
}

// ResolveMode collapses a theme mode into ModeDark or ModeLight, asking
// the operating system when the mode is "auto".
func ResolveMode(themeMode string) string {
	switch themeMode {
	case ModeLight:
		return ModeLight
	case ModeAuto:
		if IsSystemDarkTheme() {
			return ModeDark
		}
		return ModeLight
	default:
		return ModeDark
	}
}
