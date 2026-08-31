// Package banner prints the responsive startup banner shared by the
// server, CLI, and agent binaries (AI.md PART 7 "Banner Package" and the
// "Responsive Startup Banner" rules). The banner adapts to the terminal
// size mode and degrades to plain text under NO_COLOR, TERM=dumb, and
// non-interactive output.
package banner

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/webappsgo/cashp/src/common/display"
	"github.com/webappsgo/cashp/src/common/terminal"
	"github.com/webappsgo/cashp/src/common/theme"
)

// Icons used in the banner. They are only emitted when emojis are enabled.
const (
	iconApp         = "🚀"
	iconVersion     = "📦"
	iconProduction  = "🔒"
	iconDevelopment = "🔧"
	iconDebug       = "🐛"
	iconURL         = "🌐"
	iconSetup       = "🔑"
)

// BannerConfig is the content of the startup banner.
type BannerConfig struct {
	// AppName is the display name shown in the banner.
	AppName string
	// Version is the version string, printed without a leading "v".
	Version string
	// AppMode is production, development, or debug.
	AppMode string
	// Debug reports whether debug output is active.
	Debug bool
	// URLs are the addresses the service is reachable on.
	URLs []string
	// ShowSetup enables the first-run setup token line (server only).
	ShowSetup bool
	// SetupToken is the one-time first-run token.
	SetupToken string
}

// PrintStartupBanner writes the banner to stdout, detecting the terminal
// size, colour, and emoji policy automatically.
func PrintStartupBanner(cfg BannerConfig) {
	PrintStartupBannerTo(os.Stdout, cfg, nil)
}

// PrintStartupBannerTo writes the banner to w. forceColor carries the
// --color flag and is nil when the flag was not passed.
func PrintStartupBannerTo(w io.Writer, cfg BannerConfig, forceColor *bool) {
	size := terminal.GetTerminalSize()
	fmt.Fprint(w, Render(cfg, size, display.EmojiEnabled(), display.ColorEnabled(forceColor)))
}

// Render builds the banner text for an explicit size and output policy.
// It always ends with a trailing newline.
func Render(cfg BannerConfig, size terminal.TerminalSize, useEmojis, useColors bool) string {
	if !useEmojis {
		return renderPlain(cfg)
	}
	switch {
	case size.Mode >= terminal.SizeModeStandard:
		return renderFull(cfg, size, useColors)
	case size.Mode >= terminal.SizeModeCompact:
		return renderCompact(cfg, useColors)
	case size.Mode >= terminal.SizeModeMinimal:
		return renderMinimal(cfg)
	default:
		return renderMicro(cfg)
	}
}

// renderPlain is the NO_COLOR, TERM=dumb, and piped-output form: plain
// text, no emojis, no ANSI escapes, no ASCII art.
func renderPlain(cfg BannerConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s v%s\n", cfg.AppName, cfg.Version)
	fmt.Fprintf(&b, "Mode: %s\n", modeLabel(cfg))
	for _, u := range cfg.URLs {
		fmt.Fprintf(&b, "  %s\n", u)
	}
	if cfg.ShowSetup && cfg.SetupToken != "" {
		fmt.Fprintf(&b, "Setup token: %s\n", cfg.SetupToken)
	}
	return b.String()
}

// renderFull is the >=80 column form: ASCII art, icons, and full URLs.
func renderFull(cfg BannerConfig, size terminal.TerminalSize, useColors bool) string {
	palette := theme.GetTerminalPalette(theme.ModeDark)

	var b strings.Builder
	art := ASCIIArtFit(cfg.AppName, size.Cols)
	if art != "" {
		b.WriteString(colorize(art, palette.Primary, useColors))
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "%s %s %s %s\n",
		iconApp,
		colorize(cfg.AppName, palette.Primary, useColors),
		iconVersion,
		colorize("v"+cfg.Version, palette.Muted, useColors),
	)
	fmt.Fprintf(&b, "%s %s\n", modeIcon(cfg), colorize(modeLabel(cfg), modeColor(cfg, palette), useColors))
	b.WriteString("\n")
	for _, u := range cfg.URLs {
		fmt.Fprintf(&b, "  %s %s\n", iconURL, colorize(u, palette.Info, useColors))
	}
	if cfg.ShowSetup && cfg.SetupToken != "" {
		fmt.Fprintf(&b, "\n  %s Setup token: %s\n", iconSetup, colorize(cfg.SetupToken, palette.Warning, useColors))
	}
	b.WriteString("\n")
	return b.String()
}

// renderCompact is the 60-79 column form: icons and text, no ASCII art.
func renderCompact(cfg BannerConfig, useColors bool) string {
	palette := theme.GetTerminalPalette(theme.ModeDark)

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s v%s\n", iconApp, colorize(cfg.AppName, palette.Primary, useColors), cfg.Version)
	fmt.Fprintf(&b, "%s %s\n", modeIcon(cfg), colorize(modeLabel(cfg), modeColor(cfg, palette), useColors))
	for _, u := range cfg.URLs {
		fmt.Fprintf(&b, "%s %s\n", iconURL, colorize(u, palette.Info, useColors))
	}
	if cfg.ShowSetup && cfg.SetupToken != "" {
		fmt.Fprintf(&b, "%s %s\n", iconSetup, colorize(cfg.SetupToken, palette.Warning, useColors))
	}
	return b.String()
}

// renderMinimal is the 40-59 column form: abbreviated, no icons.
func renderMinimal(cfg BannerConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", cfg.AppName, cfg.Version)
	for _, u := range cfg.URLs {
		fmt.Fprintf(&b, "%s\n", hostPort(u))
	}
	if cfg.ShowSetup && cfg.SetupToken != "" {
		fmt.Fprintf(&b, "%s\n", cfg.SetupToken)
	}
	return b.String()
}

// renderMicro is the <40 column form: a single line.
func renderMicro(cfg BannerConfig) string {
	if len(cfg.URLs) > 0 {
		return fmt.Sprintf("%s %s\n", cfg.AppName, hostPort(cfg.URLs[0]))
	}
	return cfg.AppName + "\n"
}

// modeLabel returns the mode text, marking debug output when active.
func modeLabel(cfg BannerConfig) string {
	if cfg.Debug && cfg.AppMode != "debug" {
		return cfg.AppMode + " (debug)"
	}
	return cfg.AppMode
}

// modeIcon returns the icon matching the application mode.
func modeIcon(cfg BannerConfig) string {
	if cfg.Debug || cfg.AppMode == "debug" {
		return iconDebug
	}
	if cfg.AppMode == "development" {
		return iconDevelopment
	}
	return iconProduction
}

// modeColor returns the ANSI palette entry matching the application mode.
func modeColor(cfg BannerConfig, palette theme.TerminalPalette) string {
	if cfg.Debug || cfg.AppMode == "debug" {
		return palette.Error
	}
	if cfg.AppMode == "development" {
		return palette.Warning
	}
	return palette.Success
}

// colorize wraps text in a 256-colour ANSI escape when colour is enabled.
// The colour index comes from the shared terminal palette, never a literal.
func colorize(text, ansiIndex string, useColors bool) string {
	if !useColors || ansiIndex == "" {
		return text
	}
	return "\033[38;5;" + ansiIndex + "m" + text + "\033[0m"
}

// hostPort reduces a URL to its host and port for narrow terminals.
func hostPort(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw
	}
	return parsed.Host
}
