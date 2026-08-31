package i18n

import (
	"math"
	"strings"
	"testing"
)

func TestAttrsRendersSafely(t *testing.T) {
	cases := []struct {
		name  string
		pairs []string
		want  string
	}{
		{
			name:  "plain pairs",
			pairs: []string{"id", "main-content", "role", "main"},
			want:  `id="main-content" role="main"`,
		},
		{
			name:  "empty values are omitted",
			pairs: []string{"id", "x", "aria-describedby", ""},
			want:  `id="x"`,
		},
		{
			name:  "invalid names are dropped",
			pairs: []string{"onclick=alert(1) x", "boom", "id", "safe"},
			want:  `id="safe"`,
		},
		{
			name:  "uppercase names are rejected",
			pairs: []string{"ID", "x"},
			want:  ``,
		},
		{
			name:  "values are escaped",
			pairs: []string{"aria-label", `a"b<c&d`},
			want:  `aria-label="a&#34;b&lt;c&amp;d"`,
		},
		{
			name:  "trailing unpaired argument is ignored",
			pairs: []string{"id", "x", "role"},
			want:  `id="x"`,
		},
		{
			name:  "no pairs",
			pairs: nil,
			want:  ``,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(Attrs(tc.pairs...)); got != tc.want {
				t.Errorf("Attrs = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAriaAttrs(t *testing.T) {
	if got := string(AriaAttrs("label", "Close", "hidden", "true")); got != `aria-label="Close" aria-hidden="true"` {
		t.Errorf("AriaAttrs = %q", got)
	}

	if got := string(AriaAttrs("aria-live", "polite")); got != `aria-live="polite"` {
		t.Errorf("AriaAttrs did not keep an existing prefix: %q", got)
	}
}

func TestLandmarkHelpers(t *testing.T) {
	bundle := newTestBundle(t)

	if got := string(MainAttrs()); got != `id="main-content" role="main" tabindex="-1"` {
		t.Errorf("MainAttrs = %q", got)
	}

	if got := string(BannerAttrs()); got != `role="banner"` {
		t.Errorf("BannerAttrs = %q", got)
	}

	if got := string(ContentInfoAttrs()); got != `role="contentinfo"` {
		t.Errorf("ContentInfoAttrs = %q", got)
	}

	if got := string(ComplementaryAttrs("Filters")); got != `role="complementary" aria-label="Filters"` {
		t.Errorf("ComplementaryAttrs = %q", got)
	}

	if got := string(LandmarkAttrs("search", "Site search")); got != `role="search" aria-label="Site search"` {
		t.Errorf("LandmarkAttrs = %q", got)
	}

	nav := string(bundle.NavAttrs("es"))
	if !strings.HasPrefix(nav, `id="navigation" role="navigation" aria-label="`) {
		t.Errorf("NavAttrs = %q", nav)
	}

	if strings.Contains(nav, bundle.T(DefaultLocale, "nav.main_navigation")) {
		t.Errorf("NavAttrs(es) used the English label: %q", nav)
	}
}

func TestSkipLinksMatchLandmarkIDs(t *testing.T) {
	bundle := newTestBundle(t)

	main := string(MainAttrs())
	nav := string(bundle.NavAttrs("en"))

	for _, code := range bundle.Locales() {
		links := bundle.SkipLinks(code)
		if len(links) != 2 {
			t.Fatalf("%s: SkipLinks returned %d links", code, len(links))
		}

		for _, link := range links {
			if link.Label == "" || strings.HasPrefix(link.Label, "a11y.") {
				t.Errorf("%s: skip link %q has no translated label: %q", code, link.Href, link.Label)
			}

			id := strings.TrimPrefix(link.Href, "#")
			if !strings.Contains(main, `id="`+id+`"`) && !strings.Contains(nav, `id="`+id+`"`) {
				t.Errorf("skip link %q targets an id no landmark renders", link.Href)
			}
		}
	}
}

func TestLiveRegionAttrs(t *testing.T) {
	cases := []struct {
		politeness string
		atomic     bool
		want       string
	}{
		{PolitenessPolite, true, `role="status" aria-live="polite" aria-atomic="true"`},
		{PolitenessAssertive, false, `role="alert" aria-live="assertive" aria-atomic="false"`},
		{"shouty", false, `role="status" aria-live="polite" aria-atomic="false"`},
	}

	for _, tc := range cases {
		if got := string(LiveRegionAttrs(tc.politeness, tc.atomic)); got != tc.want {
			t.Errorf("LiveRegionAttrs(%q, %v) = %q, want %q", tc.politeness, tc.atomic, got, tc.want)
		}
	}

	if got := string(ErrorRegionAttrs("email-error")); got != `id="email-error" role="alert" aria-live="polite"` {
		t.Errorf("ErrorRegionAttrs = %q", got)
	}
}

func TestDialogAttrs(t *testing.T) {
	full := string(DialogAttrs("dialog-title", "dialog-desc"))
	if full != `role="dialog" aria-modal="true" aria-labelledby="dialog-title" aria-describedby="dialog-desc"` {
		t.Errorf("DialogAttrs = %q", full)
	}

	if got := string(DialogAttrs("dialog-title", "")); strings.Contains(got, "aria-describedby") {
		t.Errorf("empty description produced a dangling reference: %q", got)
	}
}

func TestFieldAttrs(t *testing.T) {
	got := string(FieldAttrs(Field{ID: "email", Required: true, Invalid: true, HintID: "email-hint", ErrorID: "email-error"}))
	want := `id="email" aria-required="true" aria-invalid="true" aria-describedby="email-hint email-error"`

	if got != want {
		t.Errorf("FieldAttrs = %q, want %q", got, want)
	}

	plain := string(FieldAttrs(Field{ID: "nickname"}))
	if plain != `id="nickname"` {
		t.Errorf("optional field = %q, want only an id", plain)
	}

	labelled := string(FieldAttrs(Field{ID: "q", Label: "Search"}))
	if !strings.Contains(labelled, `aria-label="Search"`) {
		t.Errorf("FieldAttrs dropped the accessible label: %q", labelled)
	}
}

func TestStateAttrs(t *testing.T) {
	if got := string(ToggleAttrs(true, "Hide password")); got != `type="button" aria-pressed="true" aria-label="Hide password"` {
		t.Errorf("ToggleAttrs = %q", got)
	}

	if got := string(ExpandableAttrs(false, "panel-1")); got != `aria-expanded="false" aria-controls="panel-1"` {
		t.Errorf("ExpandableAttrs = %q", got)
	}

	if got := string(BusyAttrs(true)); got != `aria-busy="true"` {
		t.Errorf("BusyAttrs = %q", got)
	}

	if got := string(DecorativeAttrs()); got != `aria-hidden="true"` {
		t.Errorf("DecorativeAttrs = %q", got)
	}
}

func TestAccessibleNameAndIDList(t *testing.T) {
	if got := AccessibleName("Delete", "   ", "invoice\n 42"); got != "Delete invoice 42" {
		t.Errorf("AccessibleName = %q", got)
	}

	if got := AccessibleName(); got != "" {
		t.Errorf("AccessibleName() = %q", got)
	}

	if got := IDList("hint", "", "  ", "error"); got != "hint error" {
		t.Errorf("IDList = %q", got)
	}

	got := SortedIDs(map[string]bool{"beta": true, "alpha": true, "gamma": false, "": true})
	if !equalStrings(got, []string{"alpha", "beta"}) {
		t.Errorf("SortedIDs = %v", got)
	}
}

func TestMarkers(t *testing.T) {
	bundle := newTestBundle(t)

	for _, code := range bundle.Locales() {
		if got := bundle.RequiredMarker(code); got == "" || strings.HasPrefix(got, "form.") {
			t.Errorf("%s: RequiredMarker = %q", code, got)
		}

		if got := bundle.OptionalMarker(code); got == "" || strings.HasPrefix(got, "form.") {
			t.Errorf("%s: OptionalMarker = %q", code, got)
		}
	}
}

func TestContrastRatio(t *testing.T) {
	cases := []struct {
		fg, bg string
		want   float64
	}{
		{"#000000", "#ffffff", 21},
		{"#ffffff", "#000000", 21},
		{"#fff", "#fff", 1},
		{"#000", "#000", 1},
	}

	for _, tc := range cases {
		got, err := ContrastRatio(tc.fg, tc.bg)
		if err != nil {
			t.Fatalf("ContrastRatio(%s, %s) error: %v", tc.fg, tc.bg, err)
		}

		if math.Abs(got-tc.want) > 0.01 {
			t.Errorf("ContrastRatio(%s, %s) = %v, want %v", tc.fg, tc.bg, got, tc.want)
		}
	}

	// #767676 on white is the canonical WCAG AA boundary for normal text.
	boundary, err := ContrastRatio("#767676", "#ffffff")
	if err != nil {
		t.Fatalf("ContrastRatio error: %v", err)
	}

	if !MeetsAA(boundary, false) {
		t.Errorf("#767676 on white should pass AA for normal text (ratio %v)", boundary)
	}

	if _, err := ContrastRatio("#12", "#ffffff"); err == nil {
		t.Error("ContrastRatio accepted a malformed foreground color")
	}

	if _, err := ContrastRatio("#ffffff", "zzzzzz"); err == nil {
		t.Error("ContrastRatio accepted a malformed background color")
	}
}

func TestMeetsAA(t *testing.T) {
	if !MeetsAA(ContrastNormalTextAA, false) || MeetsAA(4.49, false) {
		t.Error("normal-text AA threshold is wrong")
	}

	if !MeetsAA(ContrastLargeTextAA, true) || MeetsAA(2.99, true) {
		t.Error("large-text AA threshold is wrong")
	}

	if !MeetsAAComponent(ContrastComponentAA) || MeetsAAComponent(2.99) {
		t.Error("component AA threshold is wrong")
	}

	if MinTouchTargetPx != 44 {
		t.Errorf("MinTouchTargetPx = %d, want 44", MinTouchTargetPx)
	}
}
