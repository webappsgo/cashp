// Package web implements the server-rendered web frontend described in
// AI.md PART 16: templates, static assets, theming, client detection and the
// public /server/* page handlers.
//
// Everything in this package is server-rendered with html/template. JavaScript
// is a progressive enhancement only: every page and every form works with
// JavaScript disabled.
package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"
)

// assets holds every template and static file, embedded into the binary so the
// server runs from a single artifact with no external files.
//
//go:embed templates static
var assets embed.FS

// Options configures the renderer. All fields are optional; New applies safe
// defaults for anything left empty.
type Options struct {
	// BaseURL is the canonical external URL of this instance, used for
	// absolute links in SEO meta tags and the web app manifest.
	BaseURL string
	// AppName is the display name shown in the header, title and manifest.
	AppName string
	// DefaultTheme is used when the request carries no theme preference.
	DefaultTheme string
	// Debug renders template execution errors instead of swallowing them.
	Debug bool
	// Version is the running application version, shown in the footer.
	Version string
	// BuildDate is the human-readable build stamp shown in the footer.
	BuildDate string
}

// Renderer parses the embedded templates once at startup and renders pages.
type Renderer struct {
	opts   Options
	funcs  template.FuncMap
	pages  map[string]*template.Template
	static http.Handler
	// submitContact delivers public contact-form submissions; it is installed
	// by the server once mail transport is available.
	submitContact ContactSubmitter
	// onionAddress and i2pAddress are published on the help page when the
	// matching overlay network is running.
	onionAddress string
	i2pAddress   string
}

// Site carries instance-wide values every template can rely on.
type Site struct {
	AppName     string
	Tagline     string
	Description string
	BaseURL     string
	Version     string
	BuildDate   string
	RepoURL     string
	DocsURL     string
	Debug       bool
}

// NavItem is a single entry in the public navigation bar.
type NavItem struct {
	Label string
	Href  string
}

// Flash is a one-shot message rendered after a POST/redirect/GET cycle.
type Flash struct {
	// Level is one of success, error, warning or info.
	Level   string `json:"level"`
	Message string `json:"message"`
}

// Context is the value every template is executed with. Page-specific values
// live under Data; everything else is filled in by the renderer.
type Context struct {
	Site       Site
	Theme      string
	Path       string
	CSRFToken  string
	Flashes    []Flash
	Nav        []NavItem
	ClientType string
	Year       int
	// ShowConsent is true until the visitor answers the cookie banner.
	ShowConsent bool
	// OnionAddress and I2PAddress are rendered in the footer only while the
	// matching overlay network is running and publishing an address.
	OnionAddress string
	I2PAddress   string
	// Data is whatever the caller passed to Render.
	Data any
}

// defaultTagline and defaultDescription come from IDEA.md and are the only
// product copy the renderer itself owns.
const (
	defaultTagline     = "Self-hosted hosting control panel"
	defaultDescription = "CasHp is a self-hostable, all-in-one hosting control panel for web hosting, applications, containers, virtual machines, email, DNS and databases on hardware you own."
	repoURL            = "https://github.com/webappsgo/cashp"
	docsURL            = "https://cashp.readthedocs.io"
)

// New parses every embedded template and returns a ready renderer.
func New(opts Options) (*Renderer, error) {
	if opts.AppName == "" {
		opts.AppName = "CasHp"
	}
	if opts.DefaultTheme == "" {
		opts.DefaultTheme = ThemeDark
	}
	if !validTheme(opts.DefaultTheme) {
		return nil, fmt.Errorf("web: invalid default theme %q", opts.DefaultTheme)
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}
	opts.BaseURL = strings.TrimRight(opts.BaseURL, "/")

	r := &Renderer{opts: opts}
	r.funcs = buildFuncs()

	pages, err := parsePages(r.funcs)
	if err != nil {
		return nil, err
	}
	r.pages = pages

	staticFS, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, fmt.Errorf("web: static assets: %w", err)
	}
	r.static = staticFileHandler(staticFS)

	return r, nil
}

// parsePages builds one template set per page so that pages may override the
// same block names without colliding.
func parsePages(funcs template.FuncMap) (map[string]*template.Template, error) {
	shared := []string{
		"templates/layout/*.tmpl",
		"templates/partial/*.tmpl",
		"templates/component/*.tmpl",
	}

	entries, err := fs.Glob(assets, "templates/page/*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("web: listing pages: %w", err)
	}
	if len(entries) == 0 {
		return nil, errors.New("web: no page templates embedded")
	}
	sort.Strings(entries)

	pages := make(map[string]*template.Template, len(entries))
	for _, entry := range entries {
		name := strings.TrimSuffix(path.Base(entry), ".tmpl")
		tmpl := template.New(name).Funcs(funcs)
		tmpl, err = tmpl.ParseFS(assets, append(append([]string{}, shared...), entry)...)
		if err != nil {
			return nil, fmt.Errorf("web: parsing page %s: %w", name, err)
		}
		pages[name] = tmpl
	}
	return pages, nil
}

// Funcs returns the template function map so other packages can reuse the same
// helpers when they parse their own templates.
func (r *Renderer) Funcs() template.FuncMap {
	out := make(template.FuncMap, len(r.funcs))
	for k, v := range r.funcs {
		out[k] = v
	}
	return out
}

// Options returns a copy of the renderer configuration.
func (r *Renderer) Options() Options {
	return r.opts
}

// PageNames returns the sorted list of renderable page names.
func (r *Renderer) PageNames() []string {
	names := make([]string, 0, len(r.pages))
	for name := range r.pages {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// newContext assembles the shared template context for a request.
func (r *Renderer) newContext(w http.ResponseWriter, req *http.Request, data any) *Context {
	token := ensureCSRFToken(w, req)
	return &Context{
		Site: Site{
			AppName:     r.opts.AppName,
			Tagline:     defaultTagline,
			Description: defaultDescription,
			BaseURL:     r.opts.BaseURL,
			Version:     r.opts.Version,
			BuildDate:   r.opts.BuildDate,
			RepoURL:     repoURL,
			DocsURL:     docsURL,
			Debug:       r.opts.Debug,
		},
		Theme:        r.themeFor(req),
		Path:         requestPath(req),
		CSRFToken:    token,
		Flashes:      takeFlashes(w, req),
		Nav:          publicNav(),
		ClientType:   string(DetectClientType(req)),
		Year:         time.Now().Year(),
		ShowConsent:  !hasConsentCookie(req),
		OnionAddress: r.onionAddress,
		I2PAddress:   r.i2pAddress,
		Data:         data,
	}
}

// Render executes the named page and writes it as a complete HTML document.
// The page is rendered into a buffer first so a template failure never emits a
// half-written body.
func (r *Renderer) Render(w http.ResponseWriter, req *http.Request, name string, data any) error {
	tmpl, ok := r.pages[name]
	if !ok {
		return fmt.Errorf("web: unknown page %q", name)
	}

	ctx := r.newContext(w, req, data)

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", ctx); err != nil {
		return fmt.Errorf("web: rendering %s: %w", name, err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-store")
	}
	_, err := buf.WriteTo(w)
	return err
}

// RenderStatus renders a page with an explicit HTTP status code.
func (r *Renderer) RenderStatus(w http.ResponseWriter, req *http.Request, status int, name string, data any) error {
	rec := &statusRecorder{ResponseWriter: w, status: status}
	return r.Render(rec, req, name, data)
}

// errorPayload is the page data for the themed error page and the body of the
// JSON error response, keeping both representations in sync.
type errorPayload struct {
	Status  int    `json:"-"`
	Code    string `json:"error"`
	Message string `json:"message"`
	Title   string `json:"-"`
	Hint    string `json:"-"`
}

// RenderError writes an error in the representation the client asked for:
// JSON for API clients, plain text for CLI tools, and the themed error page for
// browsers. Every branch terminates in a written response.
func (r *Renderer) RenderError(w http.ResponseWriter, req *http.Request, status int, code, message string) {
	if status < 400 || status > 599 {
		status = http.StatusInternalServerError
	}
	if code == "" {
		code = defaultErrorCode(status)
	}
	if message == "" {
		message = http.StatusText(status)
	}

	payload := errorPayload{
		Status:  status,
		Code:    code,
		Message: message,
		Title:   errorTitle(status),
		Hint:    errorHint(status),
	}

	switch DetectClientType(req) {
	case ClientJSON:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		body, err := json.Marshal(map[string]any{
			"ok":      false,
			"error":   payload.Code,
			"message": payload.Message,
		})
		if err != nil {
			body = []byte(`{"ok":false,"error":"internal_error","message":"response could not be encoded"}`)
		}
		body = append(body, '\n')
		_, _ = w.Write(body)
	case ClientText:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		fmt.Fprintf(w, "ERROR: %s: %s\n", strings.ToUpper(payload.Code), payload.Message)
	default:
		rec := &statusRecorder{ResponseWriter: w, status: status}
		if err := r.Render(rec, req, "error", payload); err != nil {
			writeFallbackError(w, payload)
		}
	}
}

// writeFallbackError emits a minimal themed document when the error template
// itself cannot be rendered, so no request ever ends without a response.
func writeFallbackError(w http.ResponseWriter, payload errorPayload) {
	if hw, ok := w.(*statusRecorder); ok && hw.wrote {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(payload.Status)
	fmt.Fprintf(w, `<!doctype html>
<html lang="en" class="theme-dark">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>%d %s</title><link rel="stylesheet" href="/static/css/common.css"></head>
<body class="page-error"><main class="container"><h1>%d %s</h1><p>%s</p><p><a class="btn btn-primary" href="/">Return home</a></p></main></body>
</html>
`, payload.Status, template.HTMLEscapeString(payload.Title),
		payload.Status, template.HTMLEscapeString(payload.Title),
		template.HTMLEscapeString(payload.Message))
}

// statusRecorder lets Render write a non-200 status without duplicating the
// buffering logic in Render itself.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

// WriteHeader forces the recorded status for the first write.
func (s *statusRecorder) WriteHeader(int) {
	if s.wrote {
		return
	}
	s.wrote = true
	s.ResponseWriter.WriteHeader(s.status)
}

// Write ensures the recorded status is emitted before any body bytes.
func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.WriteHeader(s.status)
	}
	return s.ResponseWriter.Write(b)
}

// defaultErrorCode maps a status code to the machine-readable error code used
// by the unified response format.
func defaultErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusRequestEntityTooLarge:
		return "payload_too_large"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	default:
		return "internal_error"
	}
}

// errorTitle returns the short heading shown on the themed error page.
func errorTitle(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "Bad request"
	case http.StatusUnauthorized:
		return "Sign in required"
	case http.StatusForbidden:
		return "Access denied"
	case http.StatusNotFound:
		return "Page not found"
	case http.StatusMethodNotAllowed:
		return "Method not allowed"
	case http.StatusTooManyRequests:
		return "Too many requests"
	case http.StatusServiceUnavailable:
		return "Service unavailable"
	default:
		return "Something went wrong"
	}
}

// errorHint returns the actionable next step shown under the error message.
func errorHint(status int) string {
	switch status {
	case http.StatusNotFound:
		return "Check the address for typos, or start again from the home page."
	case http.StatusUnauthorized:
		return "Sign in and try again."
	case http.StatusForbidden:
		return "Your account does not have permission to view this page."
	case http.StatusTooManyRequests:
		return "Wait a minute before retrying — this instance limits how often a client may repeat a request."
	case http.StatusServiceUnavailable:
		return "The instance is starting up or under maintenance. Retry in a few moments."
	default:
		return "The problem has been recorded in the server log. Retry, and contact the administrator if it persists."
	}
}

// publicNav returns the public navigation. It never contains an admin link:
// the admin path must not be discoverable from a public page.
func publicNav() []NavItem {
	return []NavItem{
		{Label: "Home", Href: "/"},
		{Label: "About", Href: "/server/about"},
		{Label: "Help", Href: "/server/help"},
		{Label: "Contact", Href: "/server/contact"},
	}
}

// requestPath returns the path of the current request, defaulting to the root.
func requestPath(req *http.Request) string {
	if req == nil || req.URL == nil || req.URL.Path == "" {
		return "/"
	}
	return req.URL.Path
}
