package i18n

import (
	"html/template"
	"sort"
	"strings"
	"testing"
)

// render executes src with funcs and returns the output.
func render(t *testing.T, funcs template.FuncMap, src string, data any) string {
	t.Helper()

	tmpl, err := template.New("test").Funcs(funcs).Parse(src)
	if err != nil {
		t.Fatalf("parsing template: %v", err)
	}

	var out strings.Builder
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("executing template: %v", err)
	}

	return out.String()
}

func TestFuncsLocaleBound(t *testing.T) {
	bundle := newTestBundle(t)

	src := `<html {{langAttrs}}><body {{mainAttrs}}>` +
		`{{t "nav.home"}}|{{tp "plurals.items" 3}}|{{num 1234.56}}|{{count 1000}}|` +
		`{{pct 45.5}}|{{money 12.5 "USD"}}|{{dir}}|{{lang}}|{{isRTL}}|` +
		`{{range skipLinks}}{{.Href}} {{end}}</body></html>`

	got := render(t, bundle.Funcs("de"), src, nil)

	for _, want := range []string{
		`lang="de"`,
		`dir="ltr"`,
		`id="main-content"`,
		"Startseite",
		"1.234,56",
		"1.000",
		"12,50",
		"#main-content",
		"#navigation",
		"false",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q is missing %q", got, want)
		}
	}
}

func TestFuncMapLangFirst(t *testing.T) {
	bundle := newTestBundle(t)

	src := `<div {{navAttrs .Lang}}>{{t .Lang "nav.home"}}|{{tp .Lang "plurals.items" 3}}|` +
		`{{dir .Lang}}|{{isRTL .Lang}}|{{num .Lang 1234.56}}|{{requiredMarker .Lang}}</div>`

	got := render(t, bundle.FuncMap(), src, struct{ Lang string }{Lang: "ar"})

	for _, want := range []string{
		`role="navigation"`,
		"rtl",
		"true",
		"١٬٢٣٤٫٥٦",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q is missing %q", got, want)
		}
	}
}

func TestFuncsInterpolationIsLiteral(t *testing.T) {
	bundle := newTestBundle(t)

	got := render(t, bundle.Funcs("en"), `{{t "common.page_x_of_y" "current" 4 "total" 9}}`, nil)
	if !strings.Contains(got, "4") || !strings.Contains(got, "9") {
		t.Errorf("interpolation through the template failed: %q", got)
	}
}

func TestBothFuncMapsExposeTheSameNames(t *testing.T) {
	bundle := newTestBundle(t)

	bound := names(bundle.Funcs("en"))
	langFirst := names(bundle.FuncMap())

	if !equalStrings(bound, langFirst) {
		t.Errorf("func maps differ: only in Funcs %v, only in FuncMap %v",
			difference(bound, langFirst), difference(langFirst, bound))
	}

	// The names other packages are documented to rely on must never disappear.
	for _, want := range []string{
		"t", "tf", "tp", "lang", "dir", "isRTL", "langs", "langAttrs",
		"num", "decimal", "count", "pct", "money", "date", "clock", "datetime",
		"skipLinks", "navAttrs", "mainAttrs", "bannerAttrs", "footerAttrs",
		"asideAttrs", "landmarkAttrs", "attrs", "ariaAttrs", "liveRegion",
		"dialogAttrs", "fieldAttrs", "errorRegion", "toggleAttrs", "expandAttrs",
		"busyAttrs", "decorative", "accessibleName", "idList",
		"requiredMarker", "optionalMarker",
	} {
		if !contains(bound, want) {
			t.Errorf("template function %q is missing", want)
		}
	}
}

// names returns the sorted function names of a FuncMap.
func names(funcs template.FuncMap) []string {
	out := make([]string, 0, len(funcs))
	for name := range funcs {
		out = append(out, name)
	}

	sort.Strings(out)

	return out
}
