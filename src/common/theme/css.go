package theme

import (
	"fmt"
	"strconv"
	"strings"
)

// Variable is one generated CSS custom property.
type Variable struct {
	// Name includes the leading double dash, for example "--color-bg".
	Name string
	// Value is the CSS value, a hex colour or an rgba() expression.
	Value string
}

// Variables returns the CSS custom properties for a theme mode, in a
// stable order suitable for direct rendering.
func Variables(themeMode string) []Variable {
	palette := GetThemePalette(themeMode)
	extras := GetPaletteExtras(themeMode)
	alpha := extras.StatusAlpha

	return []Variable{
		{"--color-bg", palette.Background},
		{"--color-bg-secondary", palette.SurfaceAlt},
		{"--color-bg-card", extras.BgCard},
		{"--color-bg-hover", extras.BgHover},
		{"--color-bg-active", extras.BgActive},
		{"--color-code-bg", extras.CodeBg},
		{"--color-text", palette.Foreground},
		{"--color-muted", palette.Muted},
		{"--color-surface", palette.Surface},
		{"--color-border", palette.Border},
		{"--color-border-hover", extras.BorderHover},
		{"--color-success", palette.Success},
		{"--color-success-bg", RGBA(palette.Success, alpha)},
		{"--color-error", palette.Error},
		{"--color-error-bg", RGBA(palette.Error, alpha)},
		{"--color-warning", palette.Warning},
		{"--color-warning-bg", RGBA(palette.Warning, alpha)},
		{"--color-info", palette.Info},
		{"--color-info-bg", RGBA(palette.Info, alpha)},
		{"--color-primary", palette.Primary},
		{"--color-primary-bg", RGBA(palette.Primary, alpha)},
		{"--color-secondary", palette.Secondary},
		{"--color-accent", palette.Accent},
	}
}

// VariableMap returns the same custom properties keyed by name, for
// template lookups.
func VariableMap(themeMode string) map[string]string {
	vars := Variables(themeMode)
	out := make(map[string]string, len(vars))
	for _, v := range vars {
		out[v.Name] = v.Value
	}
	return out
}

// RenderBlock renders one CSS rule containing the custom properties for a
// theme mode, using the given selector.
func RenderBlock(selector, themeMode string) string {
	var b strings.Builder
	b.WriteString(selector)
	b.WriteString(" {\n")
	for _, v := range Variables(themeMode) {
		fmt.Fprintf(&b, "  %s: %s;\n", v.Name, v.Value)
	}
	b.WriteString("}\n")
	return b.String()
}

// RenderCSS renders the complete theme stylesheet: the dark defaults, the
// light overrides, and the auto rule that follows the operating system
// through prefers-color-scheme so no JavaScript is needed.
func RenderCSS() string {
	var b strings.Builder
	b.WriteString(RenderBlock("html.theme-dark", ModeDark))
	b.WriteString("\n")
	b.WriteString(RenderBlock("html.theme-light", ModeLight))
	b.WriteString("\n")
	b.WriteString("@media (prefers-color-scheme: light) {\n")
	b.WriteString(indent(RenderBlock("html.theme-auto", ModeLight)))
	b.WriteString("}\n")
	b.WriteString("\n")
	b.WriteString("@media (prefers-color-scheme: dark) {\n")
	b.WriteString(indent(RenderBlock("html.theme-auto", ModeDark)))
	b.WriteString("}\n")
	return b.String()
}

// RGBA converts a #rrggbb or #rgb colour into an rgba() expression with
// the given alpha. A value that is not a hex colour is returned unchanged,
// which lets already-translucent palette entries pass through.
func RGBA(hex string, alpha float64) string {
	r, g, bl, ok := parseHex(hex)
	if !ok {
		return hex
	}
	return fmt.Sprintf("rgba(%d, %d, %d, %s)", r, g, bl, strconv.FormatFloat(alpha, 'g', -1, 64))
}

// parseHex decodes a #rgb or #rrggbb colour into its components.
func parseHex(hex string) (r, g, b int, ok bool) {
	value := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(value) == 3 {
		value = string([]byte{value[0], value[0], value[1], value[1], value[2], value[2]})
	}
	if len(value) != 6 {
		return 0, 0, 0, false
	}
	parsed, err := strconv.ParseUint(value, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(parsed >> 16 & 0xff), int(parsed >> 8 & 0xff), int(parsed & 0xff), true
}

// indent shifts every non-empty line of a CSS block by two spaces so
// nested media-query rules stay readable.
func indent(block string) string {
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = "  " + line
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
