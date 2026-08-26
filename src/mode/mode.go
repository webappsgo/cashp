// Package mode resolves the application's operational mode (production,
// development, debug) from CLI flags, environment variables, and defaults,
// per AI.md PART 6.
package mode

import (
	"os"
	"strings"
)

// Mode is one of the three operational modes cashp can run in.
type Mode string

const (
	// Production is the default mode — no debug endpoints, no verbose logging.
	Production Mode = "production"
	// Development enables relaxed defaults for local iteration.
	Development Mode = "development"
	// Debug enables verbose logging and debug endpoints; never auto-enabled.
	Debug Mode = "debug"
)

// Resolve determines the active mode. Priority: flagMode (from --mode) >
// MODE env var > default production. flagMode may be empty when the flag
// was not passed.
func Resolve(flagMode string) Mode {
	if m := normalize(flagMode); m != "" {
		return m
	}
	if m := normalize(os.Getenv("MODE")); m != "" {
		return m
	}
	return Production
}

// ResolveDebug determines whether the debug flag is active. Priority:
// flagDebug (from --debug, nil when not passed) > DEBUG env var >
// mode-implied default > false. Debug is opt-in only and never bypasses
// authentication.
func ResolveDebug(flagDebug *bool, m Mode) bool {
	if flagDebug != nil {
		return *flagDebug
	}
	if v := os.Getenv("DEBUG"); v != "" {
		return strings.EqualFold(v, "true") || v == "1"
	}
	return m == Debug
}

func normalize(v string) Mode {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "production", "prod":
		return Production
	case "development", "dev":
		return Development
	case "debug":
		return Debug
	default:
		return ""
	}
}
