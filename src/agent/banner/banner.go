// Package banner renders the agent's responsive startup banner. The shared
// src/common/banner package supplies the ASCII art, name, version and mode
// line so all three binaries look alike; this package adds the lines that
// only make sense for a managed node — the panel it reports to, this node's
// identity, and whether the connection succeeded.
package banner

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	commonbanner "github.com/webappsgo/cashp/src/common/banner"
	"github.com/webappsgo/cashp/src/common/display"
	"github.com/webappsgo/cashp/src/common/terminal"
)

// Icons used in the agent lines. They are only emitted when emojis are on.
const (
	iconServer    = "📡"
	iconTag       = "🏷️ "
	iconConnected = "✅"
	iconOffline   = "⚠️ "
)

// Config is the content of the agent startup banner.
type Config struct {
	// AppName is the display name, e.g. "cashp-agent".
	AppName string
	// Version is the version string, printed without a leading "v".
	Version string
	// AppMode is production, development, or debug.
	AppMode string
	// Debug reports whether debug output is active.
	Debug bool
	// Server is the panel URL this node reports to.
	Server string
	// Hostname is how this node identifies itself to the panel.
	Hostname string
	// Tags are the operator-assigned labels for this node.
	Tags []string
	// Connected reports whether the panel answered during startup.
	Connected bool
}

// Print writes the banner to stdout, detecting the terminal size, colour
// and emoji policy automatically.
func Print(cfg Config) {
	PrintTo(os.Stdout, cfg, nil)
}

// PrintTo writes the banner to w. forceColor carries the --color flag and
// is nil when the flag was not passed.
func PrintTo(w io.Writer, cfg Config, forceColor *bool) {
	size := terminal.GetTerminalSize()
	fmt.Fprint(w, Render(cfg, size, display.EmojiEnabled(), display.ColorEnabled(forceColor)))
}

// Render builds the banner text for an explicit size and output policy. It
// always ends with a trailing newline.
func Render(cfg Config, size terminal.TerminalSize, useEmojis, useColors bool) string {
	shared := commonbanner.Render(commonbanner.BannerConfig{
		AppName: cfg.AppName,
		Version: cfg.Version,
		AppMode: cfg.AppMode,
		Debug:   cfg.Debug,
	}, size, useEmojis, useColors)

	builder := &strings.Builder{}
	builder.WriteString(strings.TrimRight(shared, "\n"))
	builder.WriteString("\n")

	switch {
	case !useEmojis:
		writePlainLines(builder, cfg)
	case size.Mode >= terminal.SizeModeStandard:
		writeFullLines(builder, cfg)
	case size.Mode >= terminal.SizeModeCompact:
		writeCompactLines(builder, cfg)
	case size.Mode >= terminal.SizeModeMinimal:
		writeMinimalLines(builder, cfg)
	default:
		writeMicroLine(builder, cfg)
	}
	return builder.String()
}

// writePlainLines is the NO_COLOR and TERM=dumb form: labelled plain text.
func writePlainLines(builder *strings.Builder, cfg Config) {
	if cfg.Server != "" {
		fmt.Fprintf(builder, "Server: %s\n", cfg.Server)
	}
	if cfg.Hostname != "" {
		fmt.Fprintf(builder, "Hostname: %s\n", cfg.Hostname)
	}
	if len(cfg.Tags) > 0 {
		fmt.Fprintf(builder, "Tags: %s\n", strings.Join(cfg.Tags, ", "))
	}
	if cfg.Connected {
		builder.WriteString("[OK] Connected to server\n")
		return
	}
	builder.WriteString("[WARN] Not connected to server\n")
}

// writeFullLines is the >=80 column form.
func writeFullLines(builder *strings.Builder, cfg Config) {
	builder.WriteString("\n")
	if cfg.Server != "" {
		fmt.Fprintf(builder, "%s Server: %s\n", iconServer, cfg.Server)
	}
	if cfg.Hostname != "" {
		fmt.Fprintf(builder, "%s Hostname: %s\n", iconTag, cfg.Hostname)
	}
	if len(cfg.Tags) > 0 {
		fmt.Fprintf(builder, "%s Tags: %s\n", iconTag, strings.Join(cfg.Tags, ", "))
	}
	builder.WriteString("\n")
	if cfg.Connected {
		fmt.Fprintf(builder, "%s Connected to server\n", iconConnected)
		return
	}
	fmt.Fprintf(builder, "%s Not connected to server\n", iconOffline)
}

// writeCompactLines is the 60-79 column form.
func writeCompactLines(builder *strings.Builder, cfg Config) {
	if cfg.Server != "" {
		fmt.Fprintf(builder, "%s %s\n", iconServer, cfg.Server)
	}
	if cfg.Hostname != "" {
		fmt.Fprintf(builder, "%s %s\n", iconTag, cfg.Hostname)
	}
	if cfg.Connected {
		fmt.Fprintf(builder, "%s Connected\n", iconConnected)
		return
	}
	fmt.Fprintf(builder, "%s Offline\n", iconOffline)
}

// writeMinimalLines is the 40-59 column form: no icons, host only.
func writeMinimalLines(builder *strings.Builder, cfg Config) {
	switch {
	case cfg.Hostname != "" && cfg.Server != "":
		fmt.Fprintf(builder, "%s → %s\n", cfg.Hostname, hostOf(cfg.Server))
	case cfg.Server != "":
		fmt.Fprintf(builder, "%s\n", hostOf(cfg.Server))
	case cfg.Hostname != "":
		fmt.Fprintf(builder, "%s\n", cfg.Hostname)
	}
	if cfg.Connected {
		builder.WriteString("Connected\n")
		return
	}
	builder.WriteString("Offline\n")
}

// writeMicroLine is the <40 column form: a single line.
func writeMicroLine(builder *strings.Builder, cfg Config) {
	state := "offline"
	if cfg.Connected {
		state = "connected"
	}
	if cfg.Server == "" {
		fmt.Fprintf(builder, "%s\n", state)
		return
	}
	fmt.Fprintf(builder, "%s %s\n", hostOf(cfg.Server), state)
}

// hostOf reduces a URL to its host for narrow terminals.
func hostOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw
	}
	return parsed.Host
}
