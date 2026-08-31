// Package api implements cashp's HTTP response layer per AI.md PART 14:
// the {ok:true,data} success envelope, the {ok:false,error,message,details}
// error envelope, pagination, content negotiation, and the PART 13 health
// and version endpoints. Route patterns are always derived from
// APIBasePath() so the configured api_version is never hardcoded.
package api

import (
	"strings"
	"sync/atomic"
)

// DefaultVersion is the api_version used when the process has not called
// Configure yet. It is the single place a version literal appears; every
// other path is built from APIBasePath().
const DefaultVersion = "v1"

// DefaultPageSize is the pagination page size when the client does not ask
// for one (AI.md PART 14 § Pagination).
const DefaultPageSize = 250

// MaxPageSize caps a client-supplied ?limit so a single request cannot ask
// the database for an unbounded result set.
const MaxPageSize = 1000

// CLIUserAgentPrefix identifies cashp's own CLI client. That client is
// interactive and renders its own output, so it receives JSON rather than
// pre-formatted text.
const CLIUserAgentPrefix = "cashp-cli/"

// Config holds the process-wide response-layer settings injected once at
// startup by main. Debug gates the debug-only detail described in AI.md
// PART 11; it never unlocks stack traces, DSNs, internal addresses, or
// filesystem paths, which are forbidden in every mode.
type Config struct {
	// Version is the configured api_version segment, without slashes.
	Version string
	// Debug is true when --debug/DEBUG resolved to true (AI.md PART 6).
	Debug bool
	// TextWidth is the column width used when rendering text responses.
	TextWidth int
}

var current atomic.Pointer[Config]

// Configure installs the process-wide response-layer settings. It is safe
// to call from multiple goroutines and takes effect for later requests.
func Configure(c Config) {
	c.Version = normalizeVersion(c.Version)
	if c.TextWidth <= 0 {
		c.TextWidth = 80
	}
	current.Store(&c)
}

// Current returns the active response-layer settings, falling back to the
// built-in defaults when Configure has not run.
func Current() Config {
	if c := current.Load(); c != nil {
		return *c
	}
	return Config{Version: DefaultVersion, TextWidth: 80}
}

// APIBasePath returns the versioned API root ("/api/{api_version}") for the
// configured version. Handlers and route tables MUST build paths from this
// helper instead of writing a version literal.
func APIBasePath() string {
	return "/api/" + Current().Version
}

// APIPath joins segments onto APIBasePath, producing a clean, lowercase,
// slash-separated route with no trailing slash.
func APIPath(segments ...string) string {
	return joinPath(APIBasePath(), segments...)
}

// UnversionedPath joins segments onto "/api", used for the small set of
// unversioned aliases (swagger, graphql, healthz, autodiscover) that mount
// the same handler as their versioned canonical route.
func UnversionedPath(segments ...string) string {
	return joinPath("/api", segments...)
}

// joinPath appends segments to base, trimming stray slashes so the result
// never contains an empty segment or a trailing slash.
func joinPath(base string, segments ...string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSuffix(base, "/"))
	for _, seg := range segments {
		seg = strings.Trim(seg, "/")
		if seg == "" {
			continue
		}
		b.WriteString("/")
		b.WriteString(seg)
	}
	return b.String()
}

// normalizeVersion strips slashes and whitespace from a configured version
// and falls back to DefaultVersion when the value is unusable.
func normalizeVersion(v string) string {
	v = strings.Trim(strings.TrimSpace(v), "/")
	if v == "" {
		return DefaultVersion
	}
	return v
}
