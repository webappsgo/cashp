package support

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/webappsgo/cashp/src/errors"
)

// assets holds the support subsystem's own templates and its one stylesheet.
// The shared renderer keeps its embedded files private, so support embeds its
// own; the pages still link the panel's stylesheets and use its class names and
// colour tokens rather than introducing a second design system.
//
//go:embed templates
var assets embed.FS

// pageFiles maps a page name to the template file that defines its content.
var pageFiles = map[string]string{
	"bot":          "templates/page/bot.tmpl",
	"ticket-form":  "templates/page/ticket-form.tmpl",
	"tickets":      "templates/page/tickets.tmpl",
	"ticket":       "templates/page/ticket.tmpl",
	"chat":         "templates/page/chat.tmpl",
	"kb-index":     "templates/page/kb-index.tmpl",
	"kb-article":   "templates/page/kb-article.tmpl",
	"agent-queue":  "templates/page/agent-queue.tmpl",
	"agent-ticket": "templates/page/agent-ticket.tmpl",
	"agent-chats":  "templates/page/agent-chats.tmpl",
	"agent-kb":     "templates/page/agent-kb.tmpl",
	"admin":        "templates/page/admin.tmpl",
	"message":      "templates/page/message.tmpl",
}

// sharedFiles are parsed into every page.
var sharedFiles = []string{
	"templates/layout.tmpl",
	"templates/partial/banner.tmpl",
	"templates/partial/pagination.tmpl",
}

var (
	tmplOnce sync.Once
	tmplSet  map[string]*template.Template
	tmplErr  error
)

// View is the data every support page receives. Every field that carries text a
// tenant wrote is rendered through html/template's contextual escaping; nothing
// in this package ever produces template.HTML from user input.
type View struct {
	Title      string
	SiteName   string
	BasePath   string
	APIVersion string
	CSRF       string
	Role       string
	Identity   Identity
	Mode       SupportMode
	InMode     bool
	QueueCount int
	MineCount  int
	Flash      string
	FlashKind  string
	Data       any
}

// templates parses the page set once.
func (s *Service) templates() (map[string]*template.Template, error) {
	tmplOnce.Do(func() {
		set := map[string]*template.Template{}
		names := make([]string, 0, len(pageFiles))
		for name := range pageFiles {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			t := template.New("support").Funcs(supportFuncs(s.opts.Funcs))
			files := append(append([]string{}, sharedFiles...), pageFiles[name])
			parsed, err := t.ParseFS(assets, files...)
			if err != nil {
				tmplErr = err
				return
			}
			set[name] = parsed
		}
		tmplSet = set
	})
	return tmplSet, tmplErr
}

// supportFuncs merges the panel's shared helpers with the few this package
// needs. A shared helper always wins, so dates and numbers format identically
// to the rest of the panel.
func supportFuncs(shared template.FuncMap) template.FuncMap {
	funcs := template.FuncMap{
		"supportStateLabel": StateLabel,
		"supportBadgeClass": badgeClass,
		"supportRiskClass":  riskClass,
		"supportSince":      sinceLabel,
		"supportAge":        ageLabel,
		"supportLower":      strings.ToLower,
		"supportHasSuffix":  strings.HasSuffix,
		// The panel supplies these two as well; they are defined here so the
		// pages still render when a caller parses them without a function map,
		// as the tests do.
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
	}
	for name, fn := range shared {
		funcs[name] = fn
	}
	return funcs
}

// StateLabel turns a machine state into the words a person reads. The stored
// value is never changed: only its presentation is.
func StateLabel(state string) string {
	switch state {
	case StateDraft:
		return "Draft"
	case StateOpen:
		return "Open"
	case StateAssigned:
		return "Assigned"
	case StateInProgress:
		return "In progress"
	case StateAwaitingUser:
		return "Waiting on you"
	case StateAwaitingAgent:
		return "Waiting on support"
	case StateResolved:
		return "Resolved"
	case StateClosed:
		return "Closed"
	case StateReopened:
		return "Reopened"
	default:
		return state
	}
}

// badgeClass maps a state or priority to one of the shared badge classes.
func badgeClass(value string) string {
	switch value {
	case StateResolved, StateClosed, PriorityLow:
		return "badge badge-muted"
	case StateAwaitingUser, PriorityHigh:
		return "badge badge-warning"
	case PriorityUrgent:
		return "badge badge-error"
	case StateOpen, StateReopened, StateAwaitingAgent:
		return "badge badge-info"
	default:
		return "badge"
	}
}

// riskClass maps an SLA level to one of the shared badge classes.
func riskClass(level string) string {
	switch level {
	case "breach":
		return "badge badge-error"
	case "warn":
		return "badge badge-warning"
	default:
		return "badge badge-success"
	}
}

// sinceLabel renders an age in whole units, for queue and thread timestamps.
func sinceLabel(seconds int64) string {
	if seconds < 60 {
		return "just now"
	}
	switch {
	case seconds < 3600:
		return plural(seconds/60, "minute")
	case seconds < 86400:
		return plural(seconds/3600, "hour")
	default:
		return plural(seconds/86400, "day")
	}
}

// ageLabel renders how long ago a stored timestamp was, relative to the page's
// own clock. Absolute times are deliberately avoided in the thread so no page
// has to guess the reader's timezone.
func ageLabel(now, then int64) string {
	if then <= 0 || now <= then {
		return "just now"
	}
	return sinceLabel(now-then) + " ago"
}

// plural renders a count with a correctly pluralised unit.
func plural(n int64, unit string) string {
	value := formatInt(n)
	if n == 1 {
		return value + " " + unit
	}
	return value + " " + unit + "s"
}

// formatInt renders a non-negative integer without importing a formatter.
func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}

// newView assembles the shared page context, including the support-mode banner
// counts the agent needs.
func (s *Service) newView(r *http.Request, id Identity, title string, data any) View {
	mode, inMode := s.SupportModeFor(id)
	v := View{
		Title:      title,
		SiteName:   s.opts.SiteName,
		BasePath:   s.opts.BasePath,
		APIVersion: s.opts.APIVersion,
		CSRF:       s.CSRFToken(id),
		Identity:   id,
		Mode:       mode,
		InMode:     inMode,
		Data:       data,
	}
	v.Role = s.RoleOf(r.Context(), id)
	if inMode {
		if depth, err := s.store.CountTickets(r.Context(), TicketFilter{QueueOnly: true}); err == nil {
			v.QueueCount = depth
		}
		if mine, err := s.store.CountTickets(r.Context(), TicketFilter{
			AssignedTo: mode.AgentUserID,
			QueueOnly:  true,
		}); err == nil {
			v.MineCount = mine
		}
	}
	return v
}

// render writes one support page. The page is buffered first so a template
// failure produces a clean error page instead of a half-written response.
func (s *Service) render(w http.ResponseWriter, r *http.Request, status int, page string, v View) {
	set, err := s.templates()
	if err != nil {
		s.logger().Error("support templates failed to parse")
		s.writeError(w, r, errors.New(errors.CodeInternal, 500, "This page could not be rendered"))
		return
	}
	t, ok := set[page]
	if !ok {
		s.writeError(w, r, errors.New(errors.CodeInternal, 500, "This page could not be rendered"))
		return
	}

	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "support_layout", v); err != nil {
		s.logger().Error("support page render failed", "page", page)
		s.writeError(w, r, errors.New(errors.CodeInternal, 500, "This page could not be rendered"))
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		s.logger().Debug("support page write failed")
	}
}

// renderMessage shows a single-sentence outcome page for a browser.
func (s *Service) renderMessage(w http.ResponseWriter, r *http.Request, id Identity, status int, title, message string) {
	v := s.newView(r, id, title, message)
	v.Flash = message
	v.FlashKind = "info"
	s.render(w, r, status, "message", v)
}

// fail responds to a failed request in whichever form the caller asked for.
func (s *Service) fail(w http.ResponseWriter, r *http.Request, id Identity, err error) {
	if s.wantsJSON(r) {
		s.writeError(w, r, err)
		return
	}
	e := errors.From(err)
	v := s.newView(r, id, "Support", e.Message)
	v.Flash = e.Message
	v.FlashKind = "error"
	s.render(w, r, e.HTTPStatus, "message", v)
}

// asset serves one of the two embedded static files. The name is chosen by the
// route, never taken from the request, so no path can be walked out of the
// embedded filesystem.
func (s *Service) asset(w http.ResponseWriter, r *http.Request, name, contentType string) {
	body, err := assets.ReadFile("templates/" + name)
	if err != nil {
		s.writeError(w, r, errors.New(errors.CodeNotFound, 404, "Not found"))
		return
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", "public, max-age=3600")
	h.Set("X-Content-Type-Options", "nosniff")
	if _, err := w.Write(body); err != nil {
		s.logger().Debug("support asset write failed")
	}
}

// stylesheet serves the support-specific stylesheet. It defines only the few
// components the panel does not already have and builds them from the shared
// colour tokens.
func (s *Service) stylesheet(w http.ResponseWriter, r *http.Request) {
	s.asset(w, r, "support.css", "text/css; charset=utf-8")
}

// script serves the optional progressive-enhancement script.
func (s *Service) script(w http.ResponseWriter, r *http.Request) {
	s.asset(w, r, "support.js", "text/javascript; charset=utf-8")
}
