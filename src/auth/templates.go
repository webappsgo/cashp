package auth

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"
)

// pageAssets holds this package's own pages and stylesheet, embedded into the binary so
// the panel serves them without touching the filesystem at runtime.
//
//go:embed templates static
var pageAssets embed.FS

// pageSet holds one parsed template per page. Each page is parsed together with the
// shared layout, so pages may define the same block names without colliding.
type pageSet struct {
	pages map[string]*template.Template
}

// newPageSet parses every embedded page.
func newPageSet() (*pageSet, error) {
	entries, err := pageAssets.ReadDir("templates/page")
	if err != nil {
		return nil, fmt.Errorf("auth: listing pages: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tmpl") {
			names = append(names, "templates/page/"+e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("auth: no page templates embedded")
	}

	set := &pageSet{pages: make(map[string]*template.Template, len(names))}
	for _, entry := range names {
		name := strings.TrimSuffix(path.Base(entry), ".tmpl")
		tmpl := template.New(name).Funcs(pageFuncs())
		tmpl, err = tmpl.ParseFS(pageAssets, "templates/layout/*.tmpl", "templates/partial/*.tmpl", entry)
		if err != nil {
			return nil, fmt.Errorf("auth: parsing page %s: %w", name, err)
		}
		set.pages[name] = tmpl
	}
	return set, nil
}

// pageFuncs are the helpers the auth pages use. Every one returns an escaped value;
// none of them produce template.HTML, so no page can be made to emit raw markup.
func pageFuncs() template.FuncMap {
	return template.FuncMap{
		"date": func(unix int64) string {
			if unix <= 0 {
				return "Never"
			}
			return time.Unix(unix, 0).UTC().Format("2006-01-02 15:04 MST")
		},
		"title": func(in string) string {
			if in == "" {
				return in
			}
			return strings.ToUpper(in[:1]) + in[1:]
		},
		"fallbackText": func(in string) string {
			if strings.TrimSpace(in) == "" {
				return "Unknown"
			}
			return in
		},
		"join": func(sep string, in []string) string { return strings.Join(in, sep) },
		"eqs":  func(a, b string) bool { return a == b },
	}
}

// pageData is the context every auth page receives.
type pageData struct {
	Title       string
	Heading     string
	Description string
	CSRFToken   string
	Theme       string
	AppName     string
	BasePath    string
	// ActionBase prefixes the form targets on pages that are mounted under more than one
	// owner, so the organization copy of a page posts to the organization's own routes.
	ActionBase string
	AdminPath  string
	APIVersion  string
	Next        string
	Error       string
	Notice      string
	User        *PublicUser
	Admin       *PublicAdmin
	Org         *PublicOrg
	Orgs        []PublicOrg
	Members     []PublicMember
	Invites     []PublicInvite
	Tokens      []PublicToken
	Sessions    []PublicSession
	Domains     []PublicDomain
	NewToken    string
	NewInvite   string
	TOTP        *totpSetup
	Config      Config
	// CanRegister and CanCreateOrg are derived from the mode switches so the pages
	// never have to reimplement the mode comparison in template logic.
	CanRegister  bool
	CanCreateOrg bool
}

// render writes one page. A template failure is never streamed to the client half
// written, so the buffer is completed before any byte reaches the response.
func (s *Service) render(w http.ResponseWriter, status int, name string, data pageData) {
	tmpl, found := s.pages.pages[name]
	if !found {
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}
	if data.AppName == "" {
		data.AppName = s.cfg.SiteName
	}
	if data.Theme == "" {
		data.Theme = "dark"
	}
	if data.BasePath == "" {
		data.BasePath = webBasePath
	}
	if data.ActionBase == "" {
		data.ActionBase = data.BasePath
	}
	data.AdminPath = s.cfg.AdminPath
	data.APIVersion = s.cfg.APIVersion
	data.Config = s.cfg
	data.CanRegister = s.cfg.UsersEnabled &&
		(s.cfg.RegistrationMode == RegistrationOpen || s.cfg.RegistrationMode == RegistrationInvite)
	data.CanCreateOrg = s.cfg.OrgsEnabled &&
		(s.cfg.OrgCreationMode == OrgCreationOpen || s.cfg.OrgCreationMode == OrgCreationInvite)

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		s.log.Error("render auth page", "page", name, "error", err.Error())
		http.Error(w, "The page could not be displayed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		s.log.Debug("write auth page", "error", err.Error())
	}
}

// StaticHandler serves this package's stylesheet. It is mounted under the auth prefix
// so the pages stay self-contained even before the main asset pipeline is reachable.
func (s *Service) StaticHandler() http.Handler {
	sub, err := fs.Sub(pageAssets, "static")
	if err != nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	})
}
