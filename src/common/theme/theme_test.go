package theme

import (
	"strings"
	"testing"
)

// TestResolveModeDefaultsToDark checks that dark is the default and that an
// unknown value never selects light.
func TestResolveModeDefaultsToDark(t *testing.T) {
	if ResolveMode(ModeLight) != ModeLight {
		t.Error("an explicit light mode must resolve to light")
	}
	if ResolveMode(ModeDark) != ModeDark {
		t.Error("an explicit dark mode must resolve to dark")
	}
	for _, value := range []string{"", "nonsense"} {
		if ResolveMode(value) != ModeDark {
			t.Errorf("ResolveMode(%q) must fall back to dark", value)
		}
	}
}

// TestPaletteSelection checks that each mode returns its own palette and
// that no palette entry is empty.
func TestPaletteSelection(t *testing.T) {
	dark := GetThemePalette(ModeDark)
	light := GetThemePalette(ModeLight)

	if dark == light {
		t.Fatal("the dark and light palettes must differ")
	}
	if dark != ThemePaletteDark {
		t.Error("the dark mode must return the dark palette")
	}
	if light != ThemePaletteLight {
		t.Error("the light mode must return the light palette")
	}

	for name, value := range map[string]string{
		"background": dark.Background,
		"foreground": dark.Foreground,
		"primary":    dark.Primary,
		"success":    dark.Success,
		"error":      dark.Error,
		"warning":    dark.Warning,
		"info":       dark.Info,
	} {
		if !strings.HasPrefix(value, "#") {
			t.Errorf("dark palette %s = %q, want a hex colour", name, value)
		}
	}
}

// TestTerminalPaletteUsesANSIIndices checks that terminal colours are ANSI
// indices, never hex literals: CLI and TUI output must respect the user's
// terminal theme.
func TestTerminalPaletteUsesANSIIndices(t *testing.T) {
	palette := GetTerminalPalette(ModeDark)

	for name, value := range map[string]string{
		"primary": palette.Primary,
		"success": palette.Success,
		"error":   palette.Error,
		"warning": palette.Warning,
		"info":    palette.Info,
		"muted":   palette.Muted,
	} {
		if value == "" {
			t.Errorf("terminal palette %s must be set", name)
		}
		if strings.HasPrefix(value, "#") {
			t.Errorf("terminal palette %s = %q, want an ANSI index", name, value)
		}
	}
}

// TestRGBA checks the alpha expression generation and the passthrough for
// values that are not hex colours.
func TestRGBA(t *testing.T) {
	if got := RGBA("#ffffff", 0.15); got != "rgba(255, 255, 255, 0.15)" {
		t.Errorf("RGBA(#ffffff, 0.15) = %q", got)
	}
	if got := RGBA("#000", 0.12); got != "rgba(0, 0, 0, 0.12)" {
		t.Errorf("RGBA(#000, 0.12) = %q", got)
	}
	if got := RGBA("rgba(0, 0, 0, 0.05)", 0.5); got != "rgba(0, 0, 0, 0.05)" {
		t.Errorf("a non-hex value must pass through unchanged, got %q", got)
	}
}

// TestVariablesGeneration checks that every custom property is named and
// filled, and that status tints become rgba expressions.
func TestVariablesGeneration(t *testing.T) {
	vars := Variables(ModeDark)
	if len(vars) == 0 {
		t.Fatal("no CSS variables were generated")
	}

	seen := map[string]bool{}
	for _, v := range vars {
		if !strings.HasPrefix(v.Name, "--color-") {
			t.Errorf("variable %q must be a --color- custom property", v.Name)
		}
		if v.Value == "" {
			t.Errorf("variable %q has no value", v.Name)
		}
		if seen[v.Name] {
			t.Errorf("variable %q is defined twice", v.Name)
		}
		seen[v.Name] = true
	}

	for _, required := range []string{"--color-bg", "--color-text", "--color-primary", "--color-border"} {
		if !seen[required] {
			t.Errorf("required variable %s is missing", required)
		}
	}

	lookup := VariableMap(ModeDark)
	if !strings.HasPrefix(lookup["--color-success-bg"], "rgba(") {
		t.Errorf("--color-success-bg = %q, want an rgba tint", lookup["--color-success-bg"])
	}
	if len(lookup) != len(vars) {
		t.Errorf("VariableMap has %d entries, want %d", len(lookup), len(vars))
	}
}

// TestVariablesDifferPerMode checks that the light mode really overrides
// the dark defaults.
func TestVariablesDifferPerMode(t *testing.T) {
	dark := VariableMap(ModeDark)
	light := VariableMap(ModeLight)

	if dark["--color-bg"] == light["--color-bg"] {
		t.Error("the light background must differ from the dark background")
	}
	if dark["--color-text"] == light["--color-text"] {
		t.Error("the light text colour must differ from the dark one")
	}
}

// TestRenderCSS checks the stylesheet contains every selector and both
// prefers-color-scheme branches needed for theme-auto.
func TestRenderCSS(t *testing.T) {
	css := RenderCSS()

	for _, fragment := range []string{
		"html.theme-dark {",
		"html.theme-light {",
		"html.theme-auto {",
		"@media (prefers-color-scheme: light)",
		"@media (prefers-color-scheme: dark)",
		"--color-bg:",
	} {
		if !strings.Contains(css, fragment) {
			t.Errorf("the stylesheet is missing %q", fragment)
		}
	}

	if strings.Count(css, "html.theme-auto {") != 2 {
		t.Error("theme-auto must be defined in both prefers-color-scheme branches")
	}
}

// TestRenderBlock checks a single rule renders with the given selector.
func TestRenderBlock(t *testing.T) {
	block := RenderBlock(":root", ModeLight)

	if !strings.HasPrefix(block, ":root {\n") {
		t.Errorf("RenderBlock produced %q", block)
	}
	if !strings.HasSuffix(block, "}\n") {
		t.Error("RenderBlock must close the rule")
	}
}
