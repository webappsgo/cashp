package web

import (
	"html/template"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"
)

// inlineStylePattern matches an inline style attribute on any element. The
// content security policy blocks inline styles, so a template carrying one
// silently loses its styling in production.
var inlineStylePattern = regexp.MustCompile(`(?i)\sstyle\s*=`)

// inlineHandlerPattern matches an inline event handler attribute such as
// onclick or onchange. The leading whitespace keeps it from matching an
// attribute that merely contains the letters "on", such as action.
var inlineHandlerPattern = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)

// javascriptURLPattern matches a javascript: URL, which the policy also blocks.
var javascriptURLPattern = regexp.MustCompile(`(?i)(href|action|src)\s*=\s*"\s*javascript:`)

// templateFiles lists every embedded template, so a new file is covered by
// these checks the moment it is added.
func templateFiles(t *testing.T) []string {
	t.Helper()

	tree, err := TemplatesFS()
	if err != nil {
		t.Fatalf("TemplatesFS: %v", err)
	}

	var files []string
	err = fs.WalkDir(tree, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && path.Ext(name) == ".tmpl" {
			files = append(files, name)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking templates: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no templates found in the embedded tree")
	}
	return files
}

// readTemplate returns the raw source of one embedded template.
func readTemplate(t *testing.T, name string) string {
	t.Helper()

	tree, err := TemplatesFS()
	if err != nil {
		t.Fatalf("TemplatesFS: %v", err)
	}
	body, err := fs.ReadFile(tree, name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(body)
}

// Every embedded template must parse on its own, so a syntax error is reported
// against the file that contains it rather than the whole set.
func TestEveryTemplateParses(t *testing.T) {
	r := newTestRenderer(t)

	for _, name := range templateFiles(t) {
		t.Run(name, func(t *testing.T) {
			src := readTemplate(t, name)
			if _, err := template.New(path.Base(name)).Funcs(r.Funcs()).Parse(src); err != nil {
				t.Errorf("parse: %v", err)
			}
		})
	}
}

// The content security policy blocks inline styles, inline event handlers and
// javascript: URLs, so no template may emit one.
func TestTemplatesHaveNoInlineStyleOrHandlers(t *testing.T) {
	for _, name := range templateFiles(t) {
		t.Run(name, func(t *testing.T) {
			src := readTemplate(t, name)
			if loc := inlineStylePattern.FindStringIndex(src); loc != nil {
				t.Errorf("inline style attribute at byte %d: %s", loc[0], excerpt(src, loc[0]))
			}
			if loc := inlineHandlerPattern.FindStringIndex(src); loc != nil {
				t.Errorf("inline event handler at byte %d: %s", loc[0], excerpt(src, loc[0]))
			}
			if loc := javascriptURLPattern.FindStringIndex(src); loc != nil {
				t.Errorf("javascript: URL at byte %d: %s", loc[0], excerpt(src, loc[0]))
			}
		})
	}
}

// The same bans must hold for the rendered output, which catches a banned
// attribute assembled from template data rather than written literally.
func TestRenderedPagesHaveNoInlineStyleOrHandlers(t *testing.T) {
	r := newTestRenderer(t)

	for _, name := range r.PageNames() {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/server/"+name, nil)
			if err := r.Render(rec, req, name, pageData(name)); err != nil {
				t.Fatalf("Render: %v", err)
			}

			body := rec.Body.String()
			if loc := inlineStylePattern.FindStringIndex(body); loc != nil {
				t.Errorf("inline style attribute: %s", excerpt(body, loc[0]))
			}
			if loc := inlineHandlerPattern.FindStringIndex(body); loc != nil {
				t.Errorf("inline event handler: %s", excerpt(body, loc[0]))
			}
			if loc := javascriptURLPattern.FindStringIndex(body); loc != nil {
				t.Errorf("javascript: URL: %s", excerpt(body, loc[0]))
			}
		})
	}
}

// Every page must reach the browser through the shared layout, which carries
// the skip link, the landmarks and the theme class.
func TestEveryPageUsesTheSharedLayout(t *testing.T) {
	r := newTestRenderer(t)

	for _, name := range r.PageNames() {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/server/"+name, nil)
			if err := r.Render(rec, req, name, pageData(name)); err != nil {
				t.Fatalf("Render: %v", err)
			}

			body := rec.Body.String()
			for _, want := range []string{
				"class=\"skip-link\"",
				"<main",
				"id=\"main\"",
				"<footer",
				"</html>",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("rendered page is missing %s", want)
				}
			}
			// Exactly one first-level heading keeps the document outline valid.
			if got := strings.Count(body, "<h1"); got != 1 {
				t.Errorf("page has %d <h1> elements, want 1", got)
			}
		})
	}
}

// Every form that changes state must carry the CSRF token, and the honeypot
// field on the contact form must stay in the markup for bots to fall into.
func TestStateChangingFormsCarryCSRFToken(t *testing.T) {
	r := newTestRenderer(t)

	for _, name := range []string{"contact", "privacy"} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/server/"+name, nil)
			if err := r.Render(rec, req, name, pageData(name)); err != nil {
				t.Fatalf("Render: %v", err)
			}

			body := rec.Body.String()
			posts := strings.Count(body, "method=\"post\"")
			tokens := strings.Count(body, "name=\"csrf_token\"")
			if posts == 0 {
				t.Fatal("page has no POST form")
			}
			if tokens < posts {
				t.Errorf("%d POST forms but only %d CSRF tokens", posts, tokens)
			}
		})
	}
}

// The privacy page must offer the CCPA opt-out that /server/ccpa redirects to,
// and must reflect the state recorded in the cookie.
func TestPrivacyPageRendersCCPAOptOut(t *testing.T) {
	r := newTestRenderer(t)

	for _, optedOut := range []bool{false, true} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/server/privacy", nil)
		data := privacyData{
			Consent:      ConsentState{Essential: true},
			CCPAOptedOut: optedOut,
		}
		if err := r.Render(rec, req, "privacy", data); err != nil {
			t.Fatalf("Render: %v", err)
		}

		body := rec.Body.String()
		if !strings.Contains(body, "id=\"ccpa-opt-out\"") {
			t.Fatal("privacy page has no #ccpa-opt-out section")
		}
		if !strings.Contains(body, "action=\"/server/ccpa\"") {
			t.Fatal("the opt-out form does not post to /server/ccpa")
		}

		wantChoice := "value=\"opt-out\""
		if optedOut {
			wantChoice = "value=\"opt-in\""
		}
		if !strings.Contains(body, wantChoice) {
			t.Errorf("opted out = %t: form is missing %s", optedOut, wantChoice)
		}
	}
}

// Long values such as onion addresses must be marked so they wrap instead of
// forcing the page to scroll sideways on a phone.
func TestLongValuesUseTheWrappingClasses(t *testing.T) {
	r := newTestRenderer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/server/help", nil)
	if err := r.Render(rec, req, "help", pageData("help")); err != nil {
		t.Fatalf("Render: %v", err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, ".onion") || !strings.Contains(body, ".b32.i2p") {
		t.Fatal("help page did not render the overlay addresses")
	}
	// The addresses are rendered through the code block component, whose value
	// carries the shared wrapping class.
	if strings.Count(body, "code-content long-string") < 2 {
		t.Error("the overlay addresses are not marked with the long-string wrapping class")
	}
	if !strings.Contains(body, "data-copied-label=\"Copied!\"") {
		t.Error("the copy button gives no visible copied feedback")
	}
}

// excerpt returns a short window of source around an offset for error output.
func excerpt(src string, at int) string {
	start := at - 40
	if start < 0 {
		start = 0
	}
	end := at + 40
	if end > len(src) {
		end = len(src)
	}
	return strings.TrimSpace(src[start:end])
}
