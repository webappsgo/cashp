package i18n

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// expectedLocales is the language set AI.md PART 31 mandates.
var expectedLocales = []string{"ar", "de", "en", "es", "fr", "ja", "zh"}

// newTestBundle builds the embedded bundle or fails the test.
func newTestBundle(t *testing.T) *Bundle {
	t.Helper()

	bundle, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	return bundle
}

func TestNewParsesEveryLocale(t *testing.T) {
	bundle := newTestBundle(t)

	got := bundle.Locales()
	if !equalStrings(got, expectedLocales) {
		t.Fatalf("Locales() = %v, want %v", got, expectedLocales)
	}

	for _, code := range got {
		meta := bundle.Meta(code)

		if meta.Language != code {
			t.Errorf("%s: meta.language = %q, want %q", code, meta.Language, code)
		}

		if meta.NativeName == "" || meta.Name == "" || meta.Version == "" {
			t.Errorf("%s: incomplete meta %+v", code, meta)
		}

		if meta.Direction != DirLTR && meta.Direction != DirRTL {
			t.Errorf("%s: meta.direction = %q", code, meta.Direction)
		}

		if len(bundle.Keys(code)) == 0 {
			t.Errorf("%s: catalog has no messages", code)
		}
	}
}

func TestCatalogCompleteness(t *testing.T) {
	bundle := newTestBundle(t)

	wantKeys := bundle.Keys(DefaultLocale)
	wantPlurals := bundle.PluralKeys(DefaultLocale)

	if len(wantPlurals) == 0 {
		t.Fatal("default locale defines no plural groups")
	}

	for _, code := range bundle.Locales() {
		if code == DefaultLocale {
			continue
		}

		if got := bundle.Keys(code); !equalStrings(got, wantKeys) {
			t.Errorf("%s: message keys differ from %s: missing %v, extra %v",
				code, DefaultLocale, difference(wantKeys, got), difference(got, wantKeys))
		}

		if got := bundle.PluralKeys(code); !equalStrings(got, wantPlurals) {
			t.Errorf("%s: plural groups differ from %s: missing %v, extra %v",
				code, DefaultLocale, difference(wantPlurals, got), difference(got, wantPlurals))
		}

		for _, key := range wantPlurals {
			forms, ok := bundle.PluralFormsFor(code, key)
			if !ok {
				continue
			}

			for _, required := range RequiredCategories(code) {
				if !contains(forms, required) {
					t.Errorf("%s: plural group %q lacks required category %q (has %v)",
						code, key, required, forms)
				}
			}
		}
	}
}

// formatVerb matches the fmt verbs and positional placeholders that PART 31
// forbids in catalogs, because messages are interpolated literally.
var formatVerb = regexp.MustCompile(`%[0-9.+#-]*[bcdeEfFgGopqstTUvxX]|\{[0-9]+\}`)

func TestCatalogsUseNamedTokensOnly(t *testing.T) {
	bundle := newTestBundle(t)

	for _, code := range bundle.Locales() {
		for _, key := range bundle.Keys(code) {
			msg := bundle.T(code, key)

			if match := formatVerb.FindString(msg); match != "" {
				t.Errorf("%s: key %q contains forbidden placeholder %q in %q", code, key, match, msg)
			}
		}
	}
}

// namedToken matches the {token} placeholders used for interpolation.
var namedToken = regexp.MustCompile(`\{[a-z_A-Z][a-zA-Z0-9_]*\}`)

func TestCatalogsPreserveInterpolationTokens(t *testing.T) {
	bundle := newTestBundle(t)

	for _, code := range bundle.Locales() {
		if code == DefaultLocale {
			continue
		}

		for _, key := range bundle.Keys(DefaultLocale) {
			want := tokenSet(bundle.T(DefaultLocale, key))
			got := tokenSet(bundle.T(code, key))

			if !equalStrings(want, got) {
				t.Errorf("%s: key %q token mismatch: want %v, got %v", code, key, want, got)
			}
		}
	}
}

func TestTranslationsAreNotEnglishCopies(t *testing.T) {
	bundle := newTestBundle(t)

	// A handful of unmistakably language-specific strings; if a catalog were
	// English copied wholesale these would all match the English text.
	keys := []string{"common.save", "common.cancel", "nav.home", "auth.password", "errors.not_found"}

	for _, code := range bundle.Locales() {
		if code == DefaultLocale {
			continue
		}

		identical := 0

		for _, key := range keys {
			if bundle.T(code, key) == bundle.T(DefaultLocale, key) {
				identical++
			}
		}

		if identical == len(keys) {
			t.Errorf("%s: every sampled message is identical to English", code)
		}
	}
}

func TestTranslateFallsBackToDefaultLocale(t *testing.T) {
	bundle := newTestBundle(t)

	if got := bundle.T("de", "nav.home"); got == "" || got == "nav.home" {
		t.Fatalf("T(de, nav.home) = %q, want a translation", got)
	}

	if got := bundle.T("xx", "nav.home"); got != bundle.T(DefaultLocale, "nav.home") {
		t.Errorf("unsupported locale did not fall back to %s: %q", DefaultLocale, got)
	}
}

func TestTranslateMissingKeyReturnsKey(t *testing.T) {
	bundle := newTestBundle(t)

	const missing = "does.not.exist.anywhere"

	for _, code := range bundle.Locales() {
		if got := bundle.T(code, missing); got != missing {
			t.Errorf("%s: T(missing) = %q, want the key itself", code, got)
		}
	}

	if got := bundle.T("en", ""); got != "" {
		t.Errorf(`T(en, "") = %q, want ""`, got)
	}
}

func TestInterpolation(t *testing.T) {
	bundle := newTestBundle(t)

	got := bundle.T("en", "common.page_x_of_y", "current", 2, "total", 7)
	if !strings.Contains(got, "2") || !strings.Contains(got, "7") {
		t.Errorf("pair interpolation failed: %q", got)
	}

	got = bundle.T("en", "common.page_x_of_y", map[string]string{"current": "2", "total": "7"})
	if !strings.Contains(got, "2") || !strings.Contains(got, "7") {
		t.Errorf("map interpolation failed: %q", got)
	}

	// An unsupplied token must stay visible rather than vanish silently.
	got = bundle.T("en", "common.page_x_of_y", "current", 2)
	if !strings.Contains(got, "{total}") {
		t.Errorf("unresolved token was dropped: %q", got)
	}

	// A trailing unpaired argument must be ignored, not panic.
	if got := bundle.T("en", "common.save", "stray"); got == "" {
		t.Error("odd argument list produced an empty message")
	}
}

func TestPluralSelection(t *testing.T) {
	bundle := newTestBundle(t)

	one := bundle.N("en", "plurals.items", 1)
	many := bundle.N("en", "plurals.items", 5)
	none := bundle.N("en", "plurals.items", 0)

	if one == many {
		t.Errorf("en: singular and plural are identical: %q", one)
	}

	if !strings.Contains(one, "1") || !strings.Contains(many, "5") {
		t.Errorf("en: count was not interpolated: one=%q other=%q", one, many)
	}

	if strings.Contains(none, "0") {
		t.Errorf("en: explicit zero form should not carry the digit: %q", none)
	}

	// Chinese has no plural inflection, so both forms must still render text.
	if got := bundle.N("zh", "plurals.items", 3); got == "" || got == "plurals.items" {
		t.Errorf("zh: N returned %q", got)
	}

	// Arabic must reach its dual and few forms.
	two := bundle.N("ar", "plurals.days", 2)
	few := bundle.N("ar", "plurals.days", 5)

	if two == few {
		t.Errorf("ar: dual and few forms are identical: %q", two)
	}

	// A key with no plural group falls back to the flat message.
	if got := bundle.N("en", "common.save", 3); got != bundle.T("en", "common.save") {
		t.Errorf("N on a non-plural key = %q", got)
	}

	if got := bundle.N("en", "no.such.plural", 3); got != "no.such.plural" {
		t.Errorf("N on a missing key = %q", got)
	}
}

func TestCategory(t *testing.T) {
	cases := []struct {
		locale string
		count  int
		want   string
	}{
		{"en", 0, CategoryOther},
		{"en", 1, CategoryOne},
		{"en", 2, CategoryOther},
		{"es", 1, CategoryOne},
		{"de", 1, CategoryOne},
		{"fr", 0, CategoryOne},
		{"fr", 1, CategoryOne},
		{"fr", 2, CategoryOther},
		{"zh", 1, CategoryOther},
		{"ja", 5, CategoryOther},
		{"ar", 0, CategoryZero},
		{"ar", 1, CategoryOne},
		{"ar", 2, CategoryTwo},
		{"ar", 3, CategoryFew},
		{"ar", 10, CategoryFew},
		{"ar", 11, CategoryMany},
		{"ar", 99, CategoryMany},
		{"ar", 100, CategoryOther},
		{"ar", -3, CategoryFew},
		{"xx", 1, CategoryOther},
	}

	for _, tc := range cases {
		if got := Category(tc.locale, tc.count); got != tc.want {
			t.Errorf("Category(%q, %d) = %q, want %q", tc.locale, tc.count, got, tc.want)
		}
	}
}

func TestRequiredCategories(t *testing.T) {
	if got := RequiredCategories("ar"); len(got) != 6 {
		t.Errorf("RequiredCategories(ar) = %v", got)
	}

	if got := RequiredCategories("zh"); !equalStrings(got, []string{CategoryOther}) {
		t.Errorf("RequiredCategories(zh) = %v", got)
	}

	if got := RequiredCategories("xx"); !equalStrings(got, RequiredCategories(DefaultLocale)) {
		t.Errorf("RequiredCategories(xx) = %v", got)
	}

	if len(PluralCategories()) != 6 {
		t.Errorf("PluralCategories() = %v", PluralCategories())
	}
}

func TestDirectionAndRTL(t *testing.T) {
	bundle := newTestBundle(t)

	if got := bundle.Dir("ar"); got != DirRTL {
		t.Errorf("Dir(ar) = %q, want %q", got, DirRTL)
	}

	if !bundle.IsRTL("ar") || !bundle.IsRTL("ar-EG") {
		t.Error("Arabic was not detected as RTL")
	}

	for _, code := range []string{"en", "es", "de", "fr", "zh", "ja"} {
		if got := bundle.Dir(code); got != DirLTR {
			t.Errorf("Dir(%s) = %q, want %q", code, got, DirLTR)
		}
	}

	if got := bundle.Dir("xx"); got != DirLTR {
		t.Errorf("Dir(unsupported) = %q, want %q", got, DirLTR)
	}

	if got := string(bundle.LangAttrs("ar")); !strings.Contains(got, `dir="rtl"`) || !strings.Contains(got, `lang="ar"`) {
		t.Errorf("LangAttrs(ar) = %q", got)
	}
}

func TestHasAndResolve(t *testing.T) {
	bundle := newTestBundle(t)

	if !bundle.Has("en") || !bundle.Has("ar") {
		t.Error("Has reported a shipped locale as absent")
	}

	if bundle.Has("en-US") || bundle.Has("xx") {
		t.Error("Has matched something other than an exact locale code")
	}

	cases := map[string]string{
		"en":      "en",
		"ES":      "es",
		"pt-BR":   "en",
		"zh-Hans": "zh",
		"ja_JP":   "ja",
		"":        "en",
		"  fr  ":  "fr",
	}

	for in, want := range cases {
		if got := bundle.Resolve(in); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNegotiateAcceptLanguage(t *testing.T) {
	bundle := newTestBundle(t)

	cases := []struct {
		header string
		want   string
		ok     bool
	}{
		{"de", "de", true},
		{"de-CH,de;q=0.9", "de", true},
		{"pt-BR,pt;q=0.9,es;q=0.8,en;q=0.7", "es", true},
		{"en;q=0.5, ja;q=0.9", "ja", true},
		{"fr;q=0, de", "de", true},
		{"*", "en", true},
		{"pt-BR", "", false},
		{"", "", false},
		{"   ", "", false},
		{"de;q=bogus,ja", "de", true},
	}

	for _, tc := range cases {
		got, ok := bundle.Negotiate(tc.header)
		if got != tc.want || ok != tc.ok {
			t.Errorf("Negotiate(%q) = (%q, %v), want (%q, %v)", tc.header, got, ok, tc.want, tc.ok)
		}
	}
}

func TestFromRequestPrecedence(t *testing.T) {
	bundle := newTestBundle(t)

	build := func(query, cookie, header string) *http.Request {
		target := "/dashboard"
		if query != "" {
			target += "?lang=" + query
		}

		r := httptest.NewRequest(http.MethodGet, target, nil)

		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
		}

		if header != "" {
			r.Header.Set("Accept-Language", header)
		}

		return r
	}

	cases := []struct {
		name     string
		userPref string
		query    string
		cookie   string
		header   string
		want     string
	}{
		{name: "user preference wins", userPref: "ja", query: "de", cookie: "fr", header: "es", want: "ja"},
		{name: "query beats cookie", query: "de", cookie: "fr", header: "es", want: "de"},
		{name: "cookie beats header", cookie: "fr", header: "es", want: "fr"},
		{name: "header used last", header: "es-MX,es;q=0.9", want: "es"},
		{name: "nothing set", want: DefaultLocale},
		{name: "unsupported user preference", userPref: "xx", want: DefaultLocale},
		{name: "unsupported query", query: "xx", cookie: "fr", want: DefaultLocale},
		{name: "unsupported cookie", cookie: "xx", header: "de", want: DefaultLocale},
		{name: "unsupported header", header: "pt-BR", want: DefaultLocale},
		{name: "region subtag in query", query: "de-AT", want: "de"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FromRequest(bundle, build(tc.query, tc.cookie, tc.header), tc.userPref)
			if got != tc.want {
				t.Errorf("FromRequest = %q, want %q", got, tc.want)
			}
		})
	}

	if got := FromRequest(bundle, nil, ""); got != DefaultLocale {
		t.Errorf("FromRequest(nil request) = %q", got)
	}
}

func TestMiddlewareSetsCookieAndContext(t *testing.T) {
	bundle := newTestBundle(t)

	var seen string

	handler := bundle.Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = FromContext(r.Context())
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?lang=ar", nil))

	if seen != "ar" {
		t.Errorf("context locale = %q, want ar", seen)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != CookieName || cookies[0].Value != "ar" {
		t.Fatalf("language cookie not set: %+v", cookies)
	}

	if cookies[0].MaxAge != CookieMaxAge || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie attributes wrong: %+v", cookies[0])
	}

	if vary := rec.Header().Values("Vary"); len(vary) != 2 {
		t.Errorf("Vary = %v, want Accept-Language and Cookie", vary)
	}

	// No explicit choice means no cookie is written.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Result().Cookies(); len(got) != 0 {
		t.Errorf("cookie written without an explicit choice: %+v", got)
	}

	// A stored user preference overrides everything the request carries.
	withPref := bundle.Middleware(func(*http.Request) string { return "ja" })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = FromContext(r.Context())
		}))

	withPref.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/?lang=de", nil))

	if seen != "ja" {
		t.Errorf("stored preference ignored: %q", seen)
	}
}

func TestContextHelpers(t *testing.T) {
	if got := FromContext(context.Background()); got != DefaultLocale {
		t.Errorf("FromContext(empty) = %q", got)
	}

	ctx := NewContext(context.Background(), "fr")
	if got := FromContext(ctx); got != "fr" {
		t.Errorf("FromContext = %q, want fr", got)
	}
}

func TestAvailableAndRaw(t *testing.T) {
	bundle := newTestBundle(t)

	available := bundle.Available()
	if len(available) != len(expectedLocales) {
		t.Fatalf("Available() returned %d locales", len(available))
	}

	if available[0].Code != DefaultLocale {
		t.Errorf("Available()[0] = %q, want the default locale first", available[0].Code)
	}

	for _, info := range available {
		if info.NativeName == "" || info.Direction == "" {
			t.Errorf("incomplete language selector entry: %+v", info)
		}
	}

	raw, err := bundle.Raw("ar")
	if err != nil {
		t.Fatalf("Raw(ar) error: %v", err)
	}

	if !strings.Contains(string(raw), `"direction": "rtl"`) {
		t.Error("Raw(ar) did not return the Arabic catalog")
	}

	if _, err := bundle.Raw("xx"); err != nil {
		t.Errorf("Raw(unsupported) should fall back to the default catalog: %v", err)
	}
}

func TestGlobalHelpers(t *testing.T) {
	if !IsSupported("de") || !IsSupported("de-CH") {
		t.Error("IsSupported rejected a shipped language")
	}

	if IsSupported("pt") {
		t.Error("IsSupported accepted an unshipped language")
	}

	if got := SupportedLanguages(); !equalStrings(got, expectedLocales) {
		t.Errorf("SupportedLanguages() = %v", got)
	}

	if got := Translate("de", "nav.home"); got == "" || got == "nav.home" {
		t.Errorf("Translate(de, nav.home) = %q", got)
	}

	if got := TranslateFormat("en", "common.page_x_of_y", map[string]string{"current": "3", "total": "9"}); !strings.Contains(got, "3") {
		t.Errorf("TranslateFormat = %q", got)
	}

	if got := TranslatePlural("en", "plurals.users", 2); !strings.Contains(got, "2") {
		t.Errorf("TranslatePlural = %q", got)
	}

	if got := Direction("ar"); got != DirRTL {
		t.Errorf("Direction(ar) = %q", got)
	}

	r := httptest.NewRequest(http.MethodGet, "/?lang=fr", nil)
	if got := LangFromRequest(r); got != "fr" {
		t.Errorf("LangFromRequest = %q", got)
	}
}

func TestParseLocaleRejectsBadCatalogs(t *testing.T) {
	cases := map[string]string{
		"not json":          `{`,
		"no meta":           `{"common":{"save":"Save"}}`,
		"language mismatch": `{"meta":{"language":"zz","direction":"ltr"}}`,
		"bad direction":     `{"meta":{"language":"en","direction":"sideways"}}`,
		"non string leaf":   `{"meta":{"language":"en","direction":"ltr"},"common":{"save":7}}`,
	}

	for name, body := range cases {
		if _, err := parseLocale("en", []byte(body)); err == nil {
			t.Errorf("%s: parseLocale accepted an invalid catalog", name)
		}
	}
}

// tokenSet returns the sorted, de-duplicated {token} names in a message.
func tokenSet(msg string) []string {
	seen := map[string]bool{}

	for _, match := range namedToken.FindAllString(msg, -1) {
		seen[match] = true
	}

	out := make([]string, 0, len(seen))
	for token := range seen {
		out = append(out, token)
	}

	sort.Strings(out)

	return out
}

// equalStrings reports whether two sorted string slices hold the same values.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

// difference returns the entries of a that are absent from b.
func difference(a, b []string) []string {
	present := make(map[string]bool, len(b))
	for _, v := range b {
		present[v] = true
	}

	var out []string

	for _, v := range a {
		if !present[v] {
			out = append(out, v)
		}
	}

	return out
}

// contains reports whether values holds want.
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}

	return false
}
