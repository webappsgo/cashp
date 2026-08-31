package banner

import (
	"strings"
	"testing"

	"github.com/webappsgo/cashp/src/common/terminal"
)

// sampleConfig is the banner content shared by the tests.
func sampleConfig() BannerConfig {
	return BannerConfig{
		AppName:    "cashp",
		Version:    "1.2.3",
		AppMode:    "production",
		URLs:       []string{"https://app.example.com/", "http://192.0.2.10:64500/"},
		ShowSetup:  true,
		SetupToken: "a1b2c3d4",
	}
}

// sizeFor builds a terminal size for a column and row count.
func sizeFor(cols, rows int) terminal.TerminalSize {
	return terminal.TerminalSize{Cols: cols, Rows: rows, Mode: terminal.ModeFor(cols, rows)}
}

// TestRenderPlainWithoutEmojis checks the NO_COLOR, TERM=dumb, and piped
// form: no emojis, no ANSI escapes, no ASCII art.
func TestRenderPlainWithoutEmojis(t *testing.T) {
	out := Render(sampleConfig(), sizeFor(200, 60), false, false)

	if strings.Contains(out, "\033[") {
		t.Error("the plain banner must not contain ANSI escapes")
	}
	if strings.Contains(out, iconApp) || strings.Contains(out, iconURL) {
		t.Error("the plain banner must not contain emojis")
	}
	if strings.Contains(out, "#") {
		t.Error("the plain banner must not contain ASCII art")
	}
	if !strings.Contains(out, "cashp v1.2.3") {
		t.Errorf("the plain banner is missing the name and version: %q", out)
	}
	if !strings.Contains(out, "https://app.example.com/") {
		t.Error("the plain banner must list the URLs")
	}
	if !strings.Contains(out, "a1b2c3d4") {
		t.Error("the plain banner must show the setup token")
	}
	if !strings.HasSuffix(out, "\n") {
		t.Error("the banner must end with a newline")
	}
}

// TestRenderColorsOnlyWhenEnabled checks that colour is opt-in even when
// emojis are allowed.
func TestRenderColorsOnlyWhenEnabled(t *testing.T) {
	uncolored := Render(sampleConfig(), sizeFor(120, 40), true, false)
	if strings.Contains(uncolored, "\033[") {
		t.Error("colour was emitted with colours disabled")
	}

	colored := Render(sampleConfig(), sizeFor(120, 40), true, true)
	if !strings.Contains(colored, "\033[38;5;") {
		t.Error("no ANSI colour was emitted with colours enabled")
	}
}

// TestRenderResponsiveSizes checks that the banner shrinks with the
// terminal, dropping ASCII art, then icons, then everything but one line.
func TestRenderResponsiveSizes(t *testing.T) {
	cfg := sampleConfig()

	full := Render(cfg, sizeFor(100, 30), true, false)
	if !strings.Contains(full, "#") {
		t.Error("the standard size mode must include ASCII art")
	}

	compact := Render(cfg, sizeFor(70, 20), true, false)
	if strings.Contains(compact, "#") {
		t.Error("the compact size mode must not include ASCII art")
	}
	if !strings.Contains(compact, iconApp) {
		t.Error("the compact size mode keeps icons")
	}

	minimal := Render(cfg, sizeFor(45, 12), true, false)
	if strings.Contains(minimal, iconApp) {
		t.Error("the minimal size mode must drop icons")
	}
	if !strings.Contains(minimal, "app.example.com") {
		t.Error("the minimal size mode keeps the host")
	}
	if strings.Contains(minimal, "https://") {
		t.Error("the minimal size mode shows the host only, not the full URL")
	}

	micro := Render(cfg, sizeFor(30, 8), true, false)
	if strings.Count(micro, "\n") != 1 {
		t.Errorf("the micro size mode must be a single line, got %q", micro)
	}
}

// TestRenderDebugMode checks the debug marker and icon.
func TestRenderDebugMode(t *testing.T) {
	cfg := sampleConfig()
	cfg.Debug = true

	out := Render(cfg, sizeFor(70, 20), true, false)
	if !strings.Contains(out, "production (debug)") {
		t.Errorf("the debug marker is missing: %q", out)
	}
	if !strings.Contains(out, iconDebug) {
		t.Error("the debug icon is missing")
	}
}

// TestRenderWithoutSetupToken checks the token line is omitted when the
// server is already configured.
func TestRenderWithoutSetupToken(t *testing.T) {
	cfg := sampleConfig()
	cfg.ShowSetup = false

	out := Render(cfg, sizeFor(100, 30), true, false)
	if strings.Contains(out, "a1b2c3d4") {
		t.Error("the setup token must not be shown once setup is complete")
	}
}

// TestASCIIArt checks the block font renders and folds back to plain text
// when the terminal is too narrow.
func TestASCIIArt(t *testing.T) {
	art := ASCIIArt("cashp")
	if strings.Count(art, "\n") != glyphRows-1 {
		t.Fatalf("ASCII art must have %d rows, got %q", glyphRows, art)
	}
	if ASCIIArt("") != "" {
		t.Error("an empty name must produce no art")
	}

	if width := ASCIIArtWidth("cashp"); width != 5*(glyphWidth+1)-1 {
		t.Errorf("ASCIIArtWidth = %d", width)
	}

	if got := ASCIIArtFit("cashp", 10); got != "CASHP" {
		t.Errorf("a narrow terminal must fall back to plain text, got %q", got)
	}
	if got := ASCIIArtFit("cashp", 200); !strings.Contains(got, "#") {
		t.Error("a wide terminal must keep the block art")
	}
}

// TestASCIIArtUnknownRunes checks that characters outside the font do not
// break the layout.
func TestASCIIArtUnknownRunes(t *testing.T) {
	art := ASCIIArt("a+b")
	if strings.Count(art, "\n") != glyphRows-1 {
		t.Fatalf("unknown runes must still render %d rows, got %q", glyphRows, art)
	}
}
