package admin

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/logging"
	"github.com/webappsgo/cashp/src/notify"
	"github.com/webappsgo/cashp/src/web"
)

// templateFS holds the panel's own template set. The panel reuses the shared
// stylesheet and component classes from src/web; only the markup is local.
//
//go:embed templates
var templateFS embed.FS

// panelCSS is the layout-only stylesheet the panel adds on top of the shared
// component styles. It defines no colours of its own beyond the shared tokens.
//
//go:embed static/css/panel.css
var panelCSS []byte

// panelCSSRoute is the exact path the panel stylesheet is served from. It lives
// outside the admin prefix so a stylesheet request can never disclose where the
// panel is mounted.
const panelCSSRoute = "/static/css/panel.css"

// onlineWindow is how recently a session must have been seen for its admin to
// count as online.
const onlineWindow = 5 * time.Minute

// Options configures the admin panel.
type Options struct {
	// Renderer supplies the shared template helpers and the error renderer.
	Renderer *web.Renderer
	// DB is the already-opened application database.
	DB *database.DB
	// AdminPath is the configurable segment the panel is mounted under. An
	// empty value selects DefaultAdminPath.
	AdminPath string
	// Debug enables verbose server-side logging. It never changes what a
	// response body discloses.
	Debug bool
	// Notifier delivers admin_login/admin_logout notifications per AI.md
	// PART 18's decision matrix; nil disables notification entirely.
	Notifier *notify.Notifier
}

// Panel is the mounted administrative interface.
type Panel struct {
	renderer  *web.Renderer
	db        *database.DB
	adminPath string
	debug     bool
	pages     map[string]*template.Template
	mux       *http.ServeMux
	startedAt time.Time
	notifier  *notify.Notifier

	// bootstrapOnce guards the one-time setup token generation so concurrent
	// first requests cannot mint two tokens.
	bootstrapOnce sync.Mutex
}

// New builds a panel. It performs no database work: schema creation and
// bootstrap happen explicitly, after the schema has been ensured.
func New(opts Options) (*Panel, error) {
	if opts.Renderer == nil {
		return nil, fmt.Errorf("admin: renderer is required")
	}
	if opts.DB == nil {
		return nil, fmt.Errorf("admin: database is required")
	}

	adminPath, err := NormalizeAdminPath(opts.AdminPath)
	if err != nil {
		return nil, err
	}

	panel := &Panel{
		renderer:  opts.Renderer,
		db:        opts.DB,
		adminPath: adminPath,
		debug:     opts.Debug,
		startedAt: time.Now(),
		notifier:  opts.Notifier,
	}

	pages, err := parsePages(opts.Renderer.Funcs())
	if err != nil {
		return nil, err
	}
	panel.pages = pages
	panel.mux = panel.buildMux()

	return panel, nil
}

// AdminPath returns the segment the panel is mounted under.
func (p *Panel) AdminPath() string {
	return p.adminPath
}

// PageNames returns the parsed template names, sorted. It exists so tests and
// startup checks can confirm every page compiled.
func (p *Panel) PageNames() []string {
	names := make([]string, 0, len(p.pages))
	for name := range p.pages {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// parsePages compiles the layout and partials once, then clones that base for
// every page so a page template can override the shared blocks.
func parsePages(funcs template.FuncMap) (map[string]*template.Template, error) {
	base, err := template.New("layout").Funcs(funcs).ParseFS(templateFS,
		"templates/layout/*.tmpl", "templates/partial/*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("admin: parse layout: %w", err)
	}

	entries, err := fs.Glob(templateFS, "templates/page/*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("admin: list pages: %w", err)
	}

	pages := make(map[string]*template.Template, len(entries))
	for _, entry := range entries {
		clone, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("admin: clone layout: %w", err)
		}
		parsed, err := clone.ParseFS(templateFS, entry)
		if err != nil {
			return nil, fmt.Errorf("admin: parse %s: %w", entry, err)
		}
		name := strings.TrimSuffix(path.Base(entry), ".tmpl")
		pages[name] = parsed
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("admin: no page templates found")
	}
	return pages, nil
}

// Handlers returns the routes src/server must mount. Only three entries are
// exposed so the panel stays independent of the router's own conventions.
func (p *Panel) Handlers() map[string]http.Handler {
	dispatch := http.HandlerFunc(p.serveHTTP)
	return map[string]http.Handler{
		p.base():       dispatch,
		p.base() + "/": dispatch,
		panelCSSRoute:  http.HandlerFunc(p.serveCSS),
	}
}

// ServeHTTP lets the panel be used directly as a handler.
func (p *Panel) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	p.serveHTTP(w, req)
}

// serveHTTP normalises the request path and hands it to the internal mux. A
// trailing slash on a subpage is folded away so both forms resolve.
func (p *Panel) serveHTTP(w http.ResponseWriter, req *http.Request) {
	cleaned := req.URL.Path
	if len(cleaned) > len(p.base())+1 && strings.HasSuffix(cleaned, "/") {
		target := strings.TrimSuffix(cleaned, "/")
		if req.URL.RawQuery != "" {
			target += "?" + req.URL.RawQuery
		}
		http.Redirect(w, req, target, http.StatusSeeOther)
		return
	}

	// The panel is never indexed and never cached by an intermediary.
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")

	p.mux.ServeHTTP(w, req)
}

// serveCSS returns the panel's layout stylesheet.
func (p *Panel) serveCSS(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		p.renderer.RenderError(w, req, http.StatusMethodNotAllowed, "method_not_allowed", "That method is not allowed here.")
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if req.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(panelCSS)
}

// buildMux registers every panel route. Only "config" and the signed-in
// admin's own username may appear directly under the panel root.
func (p *Panel) buildMux() *http.ServeMux {
	mux := http.NewServeMux()

	mux.Handle("GET "+p.base(), http.HandlerFunc(p.handleRoot))
	mux.Handle("POST "+p.base(), http.HandlerFunc(p.handleRootPost))
	mux.Handle("GET "+p.base()+"/{$}", http.HandlerFunc(p.handleRoot))

	for _, page := range settingsPages() {
		route := p.url(page.Slug)
		mux.Handle("GET "+route, p.RequireAdmin(http.HandlerFunc(p.handleSettingsGet)))
		mux.Handle("POST "+route, p.RequireAdmin(http.HandlerFunc(p.handleSettingsPost)))
	}

	// The setup wizard authenticates with the one-time setup token instead of a
	// session, so it is mounted outside RequireAdmin.
	mux.Handle("GET "+p.url("config/setup"), http.HandlerFunc(p.handleSetup))
	mux.Handle("POST "+p.url("config/setup"), http.HandlerFunc(p.handleSetupPost))

	// Log filtering is a GET form so a filtered view can be bookmarked and works
	// with JavaScript disabled; the page therefore has no POST route.
	mux.Handle("GET "+p.url("config/logs"), p.RequireAdmin(http.HandlerFunc(p.handleLogs)))
	mux.Handle("GET "+p.url("config/logs/audit"), p.RequireAdmin(http.HandlerFunc(p.handleLogs)))
	mux.Handle("GET "+p.url("config/logs/export"), p.RequireAdmin(http.HandlerFunc(p.handleLogsExport)))
	mux.Handle("GET "+p.url("config/info"), p.RequireAdmin(http.HandlerFunc(p.handleInfo)))
	mux.Handle("GET "+p.url("help"), p.RequireAdmin(http.HandlerFunc(p.handleHelp)))
	mux.Handle("GET "+p.url("config/admins"), p.RequireAdmin(http.HandlerFunc(p.handleAdmins)))
	mux.Handle("POST "+p.url("config/admins"), p.RequireAdmin(http.HandlerFunc(p.handleAdminsPost)))
	mux.Handle("GET "+p.url("config/security/tokens"), p.RequireAdmin(http.HandlerFunc(p.handleTokens)))
	mux.Handle("POST "+p.url("config/security/tokens"), p.RequireAdmin(http.HandlerFunc(p.handleTokensPost)))
	mux.Handle("GET "+p.url("config/cluster/nodes"), p.RequireAdmin(http.HandlerFunc(p.handleNodes)))
	mux.Handle("POST "+p.url("config/cluster/nodes"), p.RequireAdmin(http.HandlerFunc(p.handleNodesPost)))

	// Per-admin pages. The username segment must match the signed-in admin:
	// PART 17 forbids one admin from viewing another admin's account.
	for _, leaf := range []string{"profile", "preferences", "notifications"} {
		route := p.base() + "/{admin}/" + leaf
		mux.Handle("GET "+route, p.RequireAdmin(http.HandlerFunc(p.handleAccountGet)))
		mux.Handle("POST "+route, p.RequireAdmin(http.HandlerFunc(p.handleAccountPost)))
	}
	mux.Handle("GET "+p.base()+"/{admin}", p.RequireAdmin(http.HandlerFunc(p.handleAccountIndex)))
	mux.Handle("GET "+p.base()+"/{admin}/{$}", p.RequireAdmin(http.HandlerFunc(p.handleAccountIndex)))

	mux.Handle("/", http.HandlerFunc(p.handleNotFound))
	return mux
}

// handleNotFound renders the shared 404 page. An unknown path under the panel
// is indistinguishable from an unknown path anywhere else.
func (p *Panel) handleNotFound(w http.ResponseWriter, req *http.Request) {
	p.renderer.RenderError(w, req, http.StatusNotFound, "not_found", "That page does not exist.")
}

// pageContext is the data every panel template receives.
type pageContext struct {
	Site        web.Site
	Theme       string
	Path        string
	Base        string
	CSRFToken   string
	Flashes     []web.Flash
	Year        int
	Title       string
	Description string
	PageClass   string
	Nav         []navSection
	Admin       *adminRecord
	Online      []string
	AdminCount  int
	Data        any
}

// navSection is one labelled group in the panel sidebar.
type navSection struct {
	Label string
	Items []navItem
}

// navItem is a single sidebar link. Every item points at a route that exists.
type navItem struct {
	Label string
	Href  string
}

// newContext assembles the template data for a request.
func (p *Panel) newContext(w http.ResponseWriter, req *http.Request, rec *adminRecord, title, description string) *pageContext {
	opts := p.renderer.Options()
	ctx := &pageContext{
		Site: web.Site{
			AppName:   opts.AppName,
			BaseURL:   opts.BaseURL,
			Version:   opts.Version,
			BuildDate: opts.BuildDate,
			Debug:     opts.Debug,
		},
		Theme:       web.ThemeFromRequest(req),
		Path:        req.URL.Path,
		Base:        p.base(),
		CSRFToken:   p.csrfToken(w, req),
		Flashes:     takeFlashes(w, req),
		Year:        time.Now().Year(),
		Title:       title,
		Description: description,
		PageClass:   "panel",
		Admin:       rec,
	}
	if rec != nil {
		ctx.Nav = p.navFor(rec)
		if count, err := p.countAdmins(req.Context()); err == nil {
			ctx.AdminCount = count
		}
		// Only usernames are ever exposed here: PART 17 forbids showing any
		// other detail about another administrator.
		if online, err := p.onlineAdmins(req.Context(), onlineWindow); err == nil {
			ctx.Online = online
		}
	}
	return ctx
}

// render writes a panel page.
func (p *Panel) render(w http.ResponseWriter, req *http.Request, name string, ctx *pageContext) {
	p.renderStatus(w, req, http.StatusOK, name, ctx)
}

// renderStatus writes a panel page with an explicit status code. The template
// is executed into a buffer first so a failure never produces a half-written
// page with a success status.
func (p *Panel) renderStatus(w http.ResponseWriter, req *http.Request, status int, name string, ctx *pageContext) {
	tmpl, ok := p.pages[name]
	if !ok {
		logging.L().Error("admin template missing", "template", name)
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The page could not be rendered.")
		return
	}

	var buf strings.Builder
	if err := tmpl.ExecuteTemplate(&buf, "layout", ctx); err != nil {
		// The error text can name internal paths, so it is logged and never
		// returned to the client, in debug builds as much as in release ones.
		logging.L().Error("admin template execute failed", "template", name, "error", err.Error())
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The page could not be rendered.")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(buf.String()))
}

// navFor builds the sidebar for a signed-in admin.
func (p *Panel) navFor(rec *adminRecord) []navSection {
	account := p.base() + "/" + url.PathEscape(rec.Username)
	return []navSection{
		{
			Label: "Overview",
			Items: []navItem{
				{Label: "Dashboard", Href: p.base()},
				{Label: "Server info", Href: p.url("config/info")},
				{Label: "Logs", Href: p.url("config/logs")},
			},
		},
		{
			Label: "Server",
			Items: []navItem{
				{Label: "Settings", Href: p.url("config/settings")},
				{Label: "Branding", Href: p.url("config/branding")},
				{Label: "TLS", Href: p.url("config/ssl")},
				{Label: "Email", Href: p.url("config/email")},
				{Label: "Notifications", Href: p.url("config/notifications")},
				{Label: "Public URLs", Href: p.url("config/url")},
				{Label: "Scheduler", Href: p.url("config/scheduler")},
				{Label: "Backup", Href: p.url("config/backup")},
				{Label: "Updates", Href: p.url("config/updates")},
				{Label: "Maintenance", Href: p.url("config/maintenance")},
			},
		},
		{
			Label: "Security",
			Items: []navItem{
				{Label: "Authentication", Href: p.url("config/security/auth")},
				{Label: "Tokens", Href: p.url("config/security/tokens")},
				{Label: "Rate limits", Href: p.url("config/security/ratelimit")},
				{Label: "Firewall", Href: p.url("config/security/firewall")},
				{Label: "Allowlist", Href: p.url("config/security/allowlist")},
			},
		},
		{
			Label: "Network",
			Items: []navItem{
				{Label: "Tor", Href: p.url("config/network/tor")},
				{Label: "I2P", Href: p.url("config/network/i2p")},
				{Label: "GeoIP", Href: p.url("config/network/geoip")},
				{Label: "Blocklists", Href: p.url("config/network/blocklists")},
			},
		},
		{
			Label: "People",
			Items: []navItem{
				{Label: "User moderation", Href: p.url("config/moderation/users")},
				{Label: "User invites", Href: p.url("config/users/invites")},
				{Label: "Administrators", Href: p.url("config/admins")},
			},
		},
		{
			Label: "Cluster",
			Items: []navItem{
				{Label: "Nodes", Href: p.url("config/cluster/nodes")},
				{Label: "Add node", Href: p.url("config/cluster/add")},
			},
		},
		{
			Label: "Account",
			Items: []navItem{
				{Label: "Profile", Href: account + "/profile"},
				{Label: "Preferences", Href: account + "/preferences"},
				{Label: "Notifications", Href: account + "/notifications"},
				{Label: "API tokens", Href: p.url("config/security/tokens")},
				{Label: "Help", Href: p.url("help")},
			},
		},
	}
}

// takeFlashes reads and clears the shared flash cookie. The panel writes
// flashes through web.AddFlash so both surfaces use one mechanism.
func takeFlashes(w http.ResponseWriter, req *http.Request) []web.Flash {
	if req == nil {
		return nil
	}
	cookie, err := req.Cookie("flash")
	if err != nil || cookie.Value == "" {
		return nil
	}
	raw, err := url.QueryUnescape(cookie.Value)
	if err != nil {
		return nil
	}
	var flashes []web.Flash
	if err := json.Unmarshal([]byte(raw), &flashes); err != nil {
		return nil
	}
	if w != nil {
		http.SetCookie(w, &http.Cookie{
			Name:     "flash",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	}
	return flashes
}

// redirect sends the browser to a panel-relative path with a flash message.
func (p *Panel) redirect(w http.ResponseWriter, req *http.Request, rel, level, message string) {
	if message != "" {
		web.AddFlash(w, req, level, message)
	}
	http.Redirect(w, req, p.url(rel), http.StatusSeeOther)
}

// requirePost validates the method and the CSRF token of a state-changing
// request. It reports whether the caller may continue.
func (p *Panel) requirePost(w http.ResponseWriter, req *http.Request) bool {
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		p.renderer.RenderError(w, req, http.StatusMethodNotAllowed, "method_not_allowed", "That method is not allowed here.")
		return false
	}
	if err := req.ParseForm(); err != nil {
		p.renderer.RenderError(w, req, http.StatusBadRequest, "invalid_request", "The submitted form could not be read.")
		return false
	}
	if !web.ValidateCSRF(req) {
		p.renderer.RenderError(w, req, http.StatusForbidden, "csrf_failed", "The form expired. Reload the page and try again.")
		return false
	}
	return true
}

// Maintenance runs the panel's periodic housekeeping: expired sessions are
// removed so the online-admin list and the session table stay accurate.
func (p *Panel) Maintenance(ctx context.Context) error {
	if err := p.purgeExpiredSessions(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
