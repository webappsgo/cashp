package api

import (
	"context"
	"net/http"
	"strings"
)

// Format is a negotiated response format.
type Format string

const (
	// FormatJSON is application/json.
	FormatJSON Format = "json"
	// FormatText is text/plain.
	FormatText Format = "text"
	// FormatHTML is text/html.
	FormatHTML Format = "html"
)

// ContentType returns the full Content-Type header value for a format.
func (f Format) ContentType() string {
	switch f {
	case FormatText:
		return "text/plain; charset=utf-8"
	case FormatHTML:
		return "text/html; charset=utf-8"
	default:
		return "application/json; charset=utf-8"
	}
}

type contextKey int

const (
	formatKey contextKey = iota
	errorRecorderKey
	requestIDKey
)

// WithFormat stores an already negotiated format on the request context so
// downstream handlers do not repeat the detection work.
func WithFormat(ctx context.Context, f Format) context.Context {
	return context.WithValue(ctx, formatKey, f)
}

// FormatFrom returns a format previously stored by WithFormat.
func FormatFrom(ctx context.Context) (Format, bool) {
	f, ok := ctx.Value(formatKey).(Format)
	return f, ok
}

// WithRequestID stores the per-request identifier on the context so error
// envelopes and log records can reference the same value.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFrom returns the per-request identifier, or an empty string when
// no request-ID middleware ran.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// IsAPIPath reports whether a request path belongs to the backend API tree,
// which negotiates JSON by default rather than HTML.
func IsAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

// IsOurCLIClient detects cashp's own CLI binary. It is interactive and
// renders its own TUI, so it always receives JSON.
func IsOurCLIClient(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("User-Agent"), CLIUserAgentPrefix)
}

// textBrowsers are interactive but have no JavaScript engine; they receive
// server-rendered HTML (AI.md PART 14 § Client Type Detection).
var textBrowsers = []string{
	"lynx/",
	"w3m/",
	"links ",
	"links/",
	"elinks/",
	"browsh/",
	"carbonyl/",
	"netsurf",
}

// IsTextBrowser detects text-mode browsers, which are interactive and get
// HTML that works without JavaScript.
func IsTextBrowser(r *http.Request) bool {
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	for _, b := range textBrowsers {
		if strings.Contains(ua, b) {
			return true
		}
	}
	return false
}

// httpTools are non-interactive fetch-and-dump clients; they receive
// pre-formatted plain text.
var httpTools = []string{
	"curl/",
	"libcurl/",
	"wget/",
	"httpie/",
	"python-requests/",
	"go-http-client/",
	"axios/",
	"node-fetch/",
}

// IsHTTPTool detects non-interactive HTTP tools. A missing User-Agent is
// treated as a tool as well.
func IsHTTPTool(r *http.Request) bool {
	ua := strings.ToLower(strings.TrimSpace(r.Header.Get("User-Agent")))
	if ua == "" {
		return true
	}
	for _, tool := range httpTools {
		if strings.Contains(ua, tool) {
			return true
		}
	}
	return false
}

// IsNonInteractiveClient reports whether the client needs pre-formatted
// text. Only HTTP tools qualify: our CLI and text browsers are interactive
// and render responses themselves.
func IsNonInteractiveClient(r *http.Request) bool {
	if IsOurCLIClient(r) {
		return false
	}
	if IsTextBrowser(r) {
		return false
	}
	return IsHTTPTool(r)
}

// HasTxtSuffix reports whether a request path carries the ".txt" suffix that
// forces plain text on API routes.
func HasTxtSuffix(path string) bool {
	return strings.HasSuffix(path, ".txt")
}

// TrimTxtSuffix removes a trailing ".txt" from a request path.
func TrimTxtSuffix(path string) string {
	return strings.TrimSuffix(path, ".txt")
}

// NegotiateAPI resolves the response format for an /api/** route using the
// PART 14 priority order: .txt suffix, Accept: application/json,
// Accept: text/plain, non-interactive client, then JSON.
func NegotiateAPI(r *http.Request) Format {
	if f, ok := FormatFrom(r.Context()); ok {
		return f
	}
	if HasTxtSuffix(r.URL.Path) {
		return FormatText
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	switch {
	case strings.Contains(accept, "application/json"):
		return FormatJSON
	case strings.Contains(accept, "text/plain"):
		return FormatText
	}
	if IsNonInteractiveClient(r) {
		return FormatText
	}
	return FormatJSON
}

// NegotiateFrontend resolves the response format for a frontend route using
// the PART 14 priority order: Accept: text/html, Accept: application/json
// (our CLI and JSON clients), Accept: text/plain, client detection, then
// HTML.
func NegotiateFrontend(r *http.Request) Format {
	if f, ok := FormatFrom(r.Context()); ok {
		return f
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	switch {
	case strings.Contains(accept, "text/html"):
		return FormatHTML
	case strings.Contains(accept, "application/json"):
		return FormatJSON
	case strings.Contains(accept, "text/plain"):
		return FormatText
	}
	if IsOurCLIClient(r) {
		return FormatJSON
	}
	if IsTextBrowser(r) {
		return FormatHTML
	}
	if IsHTTPTool(r) {
		return FormatText
	}
	return FormatHTML
}

// Negotiate resolves the response format for any request, choosing the API
// rules for /api/** paths and the frontend rules everywhere else. The same
// handler mounted at a frontend path and an API alias therefore answers each
// caller correctly without forking its logic.
func Negotiate(r *http.Request) Format {
	if IsAPIPath(r.URL.Path) {
		return NegotiateAPI(r)
	}
	return NegotiateFrontend(r)
}
