package billing

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/webappsgo/cashp/src/logging"
)

// pageAssets holds this package's own pages, embedded into the binary so the
// billing screens render without touching the filesystem at runtime.
//
//go:embed templates
var pageAssets embed.FS

// The parsed page set, built once on first use. Each page is parsed together
// with the shared layout and partials so two pages may define the same block
// name without colliding.
var (
	pagesOnce sync.Once
	pagesMap  map[string]*template.Template
	pagesErr  error
)

// loadPages parses every embedded billing page.
func loadPages() (map[string]*template.Template, error) {
	pagesOnce.Do(func() {
		entries, err := pageAssets.ReadDir("templates/page")
		if err != nil {
			pagesErr = err
			return
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".tmpl") {
				names = append(names, "templates/page/"+e.Name())
			}
		}
		sort.Strings(names)
		out := make(map[string]*template.Template, len(names))
		for _, entry := range names {
			name := strings.TrimSuffix(path.Base(entry), ".tmpl")
			tmpl := template.New(name).Funcs(pageFuncs())
			tmpl, err = tmpl.ParseFS(pageAssets,
				"templates/layout/*.tmpl", "templates/partial/*.tmpl", entry)
			if err != nil {
				pagesErr = err
				return
			}
			out[name] = tmpl
		}
		pagesMap = out
	})
	return pagesMap, pagesErr
}

// pageFuncs are the helpers the billing pages use. Every one returns an
// escaped value; none produce template.HTML, so no page can be made to emit
// markup that came from a tenant, a provider or an invoice line.
func pageFuncs() template.FuncMap {
	return template.FuncMap{
		"money": FormatMinor,
		"date": func(unix int64) string {
			if unix <= 0 {
				return "never"
			}
			return time.Unix(unix, 0).UTC().Format("2006-01-02")
		},
		"datetime": func(unix int64) string {
			if unix <= 0 {
				return "never"
			}
			return time.Unix(unix, 0).UTC().Format("2006-01-02 15:04 MST")
		},
		"percent": func(n int64) string { return itoa(n) + "%" },
		"label": func(in string) string {
			in = strings.ReplaceAll(in, "_", " ")
			if in == "" {
				return in
			}
			return strings.ToUpper(in[:1]) + in[1:]
		},
		"eqs": func(a, b string) bool { return a == b },
		"barWidth": func(used, limit int64) string {
			if limit <= 0 {
				return "0"
			}
			pct := used * 100 / limit
			if pct > 100 {
				pct = 100
			}
			return itoa(pct)
		},
		// barWidthClass returns a discrete CSS width class (w-0..w-100 in
		// steps of 5) instead of an inline style="width:...%" attribute,
		// which frontend-rules.md forbids (CSP blocks inline style/JS).
		"barWidthClass": func(used, limit int64) string {
			if limit <= 0 {
				return "w-0"
			}
			pct := used * 100 / limit
			if pct > 100 {
				pct = 100
			}
			rounded := (pct + 2) / 5 * 5
			if rounded > 100 {
				rounded = 100
			}
			return "w-" + itoa(rounded)
		},
	}
}

// PageData is the context every billing page receives. It is exported so a
// server that supplies its own Renderer can wrap these pages in its own shell.
type PageData struct {
	Title       string
	Heading     string
	Description string
	CSRFToken   string
	BasePath    string
	AdminPath   string
	Notice      string
	Error       string
	Enabled     bool
	Identity    Identity

	Account      Account
	Summary      TenantSummary
	Plans        []Plan
	Invoices     []Invoice
	Invoice      Invoice
	Lines        []InvoiceLine
	CreditNotes  []CreditNote
	Methods      []PaymentMethod
	Payments     []PaymentAttempt
	Quotas       []QuotaStatus
	Preview      ProrationPreview
	Dashboard    Dashboard
	Categories   []ProviderCategory
	Provider     ProviderView
	Driver       DriverInfo
	Test         ProviderTest
	Reconcile    []ReconcileSummary
	Webhooks     []WebhookEvent
	Audit        []AuditEntry
	Settings     map[string]string
	EnabledCount int
	TotalCount   int
}

// ProviderCategory groups providers for the administration screen.
type ProviderCategory struct {
	Name      string         `json:"name"`
	Providers []ProviderView `json:"providers"`
}

// render writes one billing page. When the server injected a Renderer, that is
// used instead, so an install with its own shell keeps one consistent layout.
// A template failure is never streamed half written: the page is completed in
// a buffer before any byte reaches the response.
func (s *Service) render(w http.ResponseWriter, r *http.Request, status int, name string, data PageData) {
	if data.AdminPath == "" {
		data.AdminPath = s.adminPth
	}
	if data.Notice == "" {
		data.Notice = r.URL.Query().Get("notice")
	}
	if s.renderer != nil {
		if err := s.renderer.Render(w, r, name, data); err == nil {
			return
		}
		logging.L().Warn("billing renderer failed, using the built-in page", "page", name)
	}
	set, err := loadPages()
	if err != nil {
		logging.L().Error("billing pages unavailable", "error", err.Error())
		http.Error(w, "The billing pages could not be loaded", http.StatusInternalServerError)
		return
	}
	tmpl, found := set[name]
	if !found {
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		logging.L().Error("render billing page", "page", name, "error", err.Error())
		http.Error(w, "The page could not be displayed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		logging.L().Debug("write billing page", "error", err.Error())
	}
}

// groupProviders arranges providers by category for the administration list,
// keeping the categories and the providers inside them in a stable order.
func groupProviders(views []ProviderView) []ProviderCategory {
	order := []string{}
	byCategory := map[string][]ProviderView{}
	for _, v := range views {
		category := v.Category
		if category == "" {
			category = "other"
		}
		if _, seen := byCategory[category]; !seen {
			order = append(order, category)
		}
		byCategory[category] = append(byCategory[category], v)
	}
	sort.Strings(order)
	out := make([]ProviderCategory, 0, len(order))
	for _, category := range order {
		group := byCategory[category]
		sort.Slice(group, func(i, j int) bool {
			if group[i].Priority != group[j].Priority {
				return group[i].Priority < group[j].Priority
			}
			return group[i].Name < group[j].Name
		})
		out = append(out, ProviderCategory{Name: category, Providers: group})
	}
	return out
}
