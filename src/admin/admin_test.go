package admin

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/notify"
	"github.com/webappsgo/cashp/src/security"
	"github.com/webappsgo/cashp/src/web"
)

// testAdminPath proves nothing in the panel assumes the default segment.
const testAdminPath = "control-room"

// testPassword satisfies the wizard's length and character-class rules.
const testPassword = "Correct-Horse-9-Battery"

// testCSRF is the value used for both the cookie and the submitted field, so a
// request is only accepted when the double-submit pair matches.
const testCSRF = "test-csrf-token-value-0123456789abcdef"

// newTestPanel builds a panel backed by a throwaway SQLite database.
func newTestPanel(t *testing.T, adminPath string) *Panel {
	t.Helper()

	db, err := database.Open(database.Config{Driver: database.DriverSQLite, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	renderer, err := web.New(web.Options{AppName: "CasHp", BaseURL: "https://panel.example"})
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	panel, err := New(Options{Renderer: renderer, DB: db, AdminPath: adminPath})
	if err != nil {
		t.Fatalf("new panel: %v", err)
	}
	return panel
}

// signIn creates an administrator and returns the cookies that authenticate it.
func signIn(t *testing.T, p *Panel, username string, primary bool) (*adminRecord, []*http.Cookie) {
	t.Helper()

	hash, err := security.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	rec, err := p.createAdmin(context.Background(), username, hash, username+"@example.test", primary)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	value, err := p.createSession(context.Background(), rec.ID, sessionKindActive, sessionTTL)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	cookies := []*http.Cookie{
		{Name: adminSessionCookie, Value: value},
		{Name: csrfCookieName, Value: testCSRF},
	}
	return rec, cookies
}

// get issues an authenticated GET request against the panel.
func get(p *Panel, target string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	return rec
}

// post issues a form POST against the panel.
func post(p *Panel, target string, form url.Values, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	return rec
}

// csrfForm returns a form body carrying the token that matches the test cookie.
func csrfForm(values url.Values) url.Values {
	if values == nil {
		values = url.Values{}
	}
	values.Set("csrf_token", testCSRF)
	return values
}

func TestNewRequiresDependencies(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected an error without a renderer")
	}
	renderer, err := web.New(web.Options{})
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	if _, err := New(Options{Renderer: renderer}); err == nil {
		t.Fatal("expected an error without a database")
	}
}

func TestAdminPathIsConfigurable(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)

	if got := panel.AdminPath(); got != testAdminPath {
		t.Fatalf("admin path = %q, want %q", got, testAdminPath)
	}

	base := "/server/" + testAdminPath
	handlers := panel.Handlers()
	for _, want := range []string{base, base + "/", panelCSSRoute} {
		if _, ok := handlers[want]; !ok {
			t.Fatalf("handler for %q is not exposed", want)
		}
	}

	// The default segment must not answer when another one is configured.
	if got := get(panel, "/server/administration/config/info", nil).Code; got != http.StatusNotFound {
		t.Fatalf("default path status = %d, want %d", got, http.StatusNotFound)
	}

	// Every sidebar link must live under the configured segment.
	rec, cookies := signIn(t, panel, "administrator", true)
	for _, section := range panel.navFor(rec) {
		for _, item := range section.Items {
			if !strings.HasPrefix(item.Href, base) {
				t.Fatalf("nav item %q escapes the panel prefix", item.Href)
			}
		}
	}
	if got := get(panel, base+"/config/info", cookies).Code; got != http.StatusOK {
		t.Fatalf("configured path status = %d, want %d", got, http.StatusOK)
	}
}

func TestUnauthenticatedRequestsAreRejected(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)
	base := "/server/" + testAdminPath

	protected := []string{
		base + "/config/info",
		base + "/config/logs",
		base + "/config/admins",
		base + "/config/security/tokens",
		base + "/config/cluster/nodes",
		base + "/administrator/profile",
	}
	for _, target := range protected {
		res := get(panel, target, nil)
		if res.Code != http.StatusSeeOther {
			t.Fatalf("%s status = %d, want %d", target, res.Code, http.StatusSeeOther)
		}
		if got := res.Header().Get("Location"); got != "/server/auth/login" {
			t.Fatalf("%s redirected to %q, want the shared login form", target, got)
		}
	}

	// A signed-in regular user is bounced to their own area and is never told
	// that an administrative panel exists.
	res := get(panel, base+"/config/info", []*http.Cookie{{Name: "user_session", Value: "abc"}})
	if got := res.Header().Get("Location"); got != "/users" {
		t.Fatalf("user redirected to %q, want /users", got)
	}
	if strings.Contains(strings.ToLower(res.Body.String()), "admin") {
		t.Fatal("the redirect body mentions the panel")
	}
}

func TestPanelResponsesAreNeverIndexed(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)
	_, cookies := signIn(t, panel, "administrator", true)

	res := get(panel, "/server/"+testAdminPath+"/config/info", cookies)
	if got := res.Header().Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
		t.Fatalf("X-Robots-Tag = %q, want a noindex directive", got)
	}
	if got := res.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestPostsWithoutCSRFAreRejected(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)
	_, cookies := signIn(t, panel, "administrator", true)
	base := "/server/" + testAdminPath

	targets := []string{
		base + "/config/admins",
		base + "/config/security/tokens",
		base + "/config/cluster/nodes",
		base + "/administrator/profile",
	}
	for _, target := range targets {
		res := post(panel, target, url.Values{"action": {"invite"}}, cookies)
		if res.Code != http.StatusForbidden {
			t.Fatalf("%s without a token: status = %d, want %d", target, res.Code, http.StatusForbidden)
		}
	}

	// A mismatched token is rejected just like a missing one.
	form := url.Values{"action": {"invite"}, "csrf_token": {"not-the-cookie-value"}}
	if res := post(panel, base+"/config/admins", form, cookies); res.Code != http.StatusForbidden {
		t.Fatalf("mismatched token: status = %d, want %d", res.Code, http.StatusForbidden)
	}

	// With the matching token the same request is accepted.
	form = csrfForm(url.Values{"action": {"invite"}, "username": {"backup-admin"}, "expires": {"24h"}})
	if res := post(panel, base+"/config/admins", form, cookies); res.Code != http.StatusOK {
		t.Fatalf("valid token: status = %d, want %d", res.Code, http.StatusOK)
	}
}

func TestGetOnPostOnlyRouteIsRejected(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)
	_, cookies := signIn(t, panel, "administrator", true)

	req := httptest.NewRequest(http.MethodPut, "/server/"+testAdminPath+"/config/admins", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	res := httptest.NewRecorder()
	panel.ServeHTTP(res, req)
	if res.Code != http.StatusMethodNotAllowed && res.Code != http.StatusNotFound {
		t.Fatalf("PUT status = %d, want 404 or 405", res.Code)
	}
}

func TestBootstrapTokenFlow(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)
	ctx := context.Background()
	base := "/server/" + testAdminPath

	// The sign-in limiter is process-wide, so the token gate starts from a
	// known budget no matter which tests ran before this one.
	loginLimiter.Reset("192.0.2.1")

	token, err := panel.Bootstrap(ctx)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if len(token) != setupTokenBytes*2 {
		t.Fatalf("token length = %d, want %d", len(token), setupTokenBytes*2)
	}

	// A live token is never reissued, so the console banner cannot be replayed.
	again, err := panel.Bootstrap(ctx)
	if err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if again != "" {
		t.Fatal("a second token was minted while one was still live")
	}

	// Only the hash is persisted.
	var stored string
	row := panel.db.QueryRowContext(ctx, database.TimeoutSelect, `SELECT token_hash FROM admin_setup_tokens LIMIT 1`)
	if err := row.Scan(&stored); err != nil {
		t.Fatalf("read token row: %v", err)
	}
	if stored == token {
		t.Fatal("the setup token is stored in plaintext")
	}
	if stored != security.HashToken(token) {
		t.Fatal("the stored value is not the token hash")
	}

	// The gate rejects a wrong token without opening a session.
	res := post(panel, base+"/config/setup", csrfForm(url.Values{"action": {"token"}, "token": {"deadbeef"}}), []*http.Cookie{{Name: csrfCookieName, Value: testCSRF}})
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want %d", res.Code, http.StatusUnauthorized)
	}

	// The real token opens the wizard exactly once.
	cookies := []*http.Cookie{{Name: csrfCookieName, Value: testCSRF}}
	res = post(panel, base+"/config/setup", csrfForm(url.Values{"action": {"token"}, "token": {token}}), cookies)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("valid token: status = %d, want %d", res.Code, http.StatusSeeOther)
	}
	var session string
	for _, cookie := range res.Result().Cookies() {
		if cookie.Name == adminSessionCookie {
			session = cookie.Value
		}
	}
	if session == "" {
		t.Fatal("no setup session cookie was issued")
	}

	replay := post(panel, base+"/config/setup", csrfForm(url.Values{"action": {"token"}, "token": {token}}), cookies)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("token replay: status = %d, want %d", replay.Code, http.StatusUnauthorized)
	}

	// Step 1 creates the primary administrator; after that no token is minted.
	wizard := append(cookies, &http.Cookie{Name: adminSessionCookie, Value: session})
	form := csrfForm(url.Values{
		"step":             {"1"},
		"username":         {"administrator"},
		"account_email":    {"admin@example.test"},
		"password":         {testPassword},
		"password_confirm": {testPassword},
	})
	if res := post(panel, base+"/config/setup", form, wizard); res.Code != http.StatusSeeOther && res.Code != http.StatusOK {
		t.Fatalf("wizard step 1: status = %d", res.Code)
	}
	count, err := panel.countAdmins(ctx)
	if err != nil || count != 1 {
		t.Fatalf("admin count = %d (err %v), want 1", count, err)
	}
	if minted, err := panel.Bootstrap(ctx); err != nil || minted != "" {
		t.Fatalf("bootstrap after setup returned %q (err %v), want no token", minted, err)
	}
}

func TestSetupBannerHidesNothingButLeaksNothing(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)
	banner := panel.SetupBanner("abc123", "https://panel.example")

	if !strings.Contains(banner, "abc123") {
		t.Fatal("the banner does not show the token")
	}
	if !strings.Contains(banner, "/server/"+testAdminPath+"/config/setup") {
		t.Fatal("the banner does not point at the configured setup route")
	}
}

func TestSignedInPagesRender(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)
	rec, cookies := signIn(t, panel, "administrator", true)
	base := "/server/" + testAdminPath

	targets := []string{
		base,
		base + "/config/info",
		base + "/config/logs",
		base + "/config/logs/audit?category=setup&q=token",
		base + "/help",
		base + "/config/admins",
		base + "/config/security/tokens",
		base + "/config/cluster/nodes",
		base + "/" + rec.Username + "/profile",
		base + "/" + rec.Username + "/preferences",
		base + "/" + rec.Username + "/notifications",
	}
	for _, page := range settingsPages() {
		targets = append(targets, panel.url(page.Slug))
	}

	for _, target := range targets {
		res := get(panel, target, cookies)
		if res.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", target, res.Code, http.StatusOK)
		}
		body := res.Body.String()
		if !strings.Contains(body, "</html>") {
			t.Fatalf("%s did not render a complete page", target)
		}
		if !strings.Contains(body, `content="noindex, nofollow, noarchive, nosnippet"`) {
			t.Fatalf("%s is missing its robots directive", target)
		}
		assertNoInlineHandlers(t, target, body)
	}
}

func TestNavigationHasNoDeadLinks(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)
	rec, cookies := signIn(t, panel, "administrator", true)

	for _, section := range panel.navFor(rec) {
		for _, item := range section.Items {
			res := get(panel, item.Href, cookies)
			if res.Code != http.StatusOK {
				t.Fatalf("nav link %q (%s) status = %d, want %d", item.Label, item.Href, res.Code, http.StatusOK)
			}
		}
	}
}

func TestAccountPagesArePrivateToTheirOwner(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)
	_, cookies := signIn(t, panel, "administrator", true)

	// The peer is created without a session so it cannot appear in the
	// "online now" list, which is the one place a username may legitimately show.
	hash, err := security.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := panel.createAdmin(context.Background(), "backup-admin", hash, "backup@example.test", false); err != nil {
		t.Fatalf("create second admin: %v", err)
	}

	base := "/server/" + testAdminPath
	if res := get(panel, base+"/backup-admin/profile", cookies); res.Code != http.StatusNotFound {
		t.Fatalf("peer profile status = %d, want %d", res.Code, http.StatusNotFound)
	}
	if res := get(panel, base+"/backup-admin/preferences", cookies); res.Code != http.StatusNotFound {
		t.Fatalf("peer preferences status = %d, want %d", res.Code, http.StatusNotFound)
	}

	// The admins page states the count but never lists another account.
	res := get(panel, base+"/config/admins", cookies)
	body := res.Body.String()
	if strings.Contains(body, "backup-admin") {
		t.Fatal("the admins page disclosed another administrator's username")
	}
	if !strings.Contains(body, "Administrators configured") {
		t.Fatal("the admins page does not state the administrator count")
	}
}

func TestDestructiveActionsRequireConfirmation(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)
	_, cookies := signIn(t, panel, "administrator", true)
	signIn(t, panel, "backup-admin", false)
	base := "/server/" + testAdminPath

	// The username alone is not enough: it must be typed twice.
	form := csrfForm(url.Values{"action": {"remove_admin"}, "username": {"backup-admin"}})
	if res := post(panel, base+"/config/admins", form, cookies); res.Code != http.StatusSeeOther {
		t.Fatalf("unconfirmed removal: status = %d, want a redirect", res.Code)
	}
	if _, err := panel.adminByUsername(context.Background(), "backup-admin"); err != nil {
		t.Fatal("the unconfirmed removal deleted the account")
	}

	form = csrfForm(url.Values{"action": {"remove_admin"}, "username": {"backup-admin"}, "confirm": {"backup-admin"}})
	if res := post(panel, base+"/config/admins", form, cookies); res.Code != http.StatusSeeOther {
		t.Fatalf("confirmed removal: status = %d, want a redirect", res.Code)
	}
	if _, err := panel.adminByUsername(context.Background(), "backup-admin"); err == nil {
		t.Fatal("the confirmed removal left the account in place")
	}

	// The removal is written to the audit trail.
	entries, err := panel.recentAudit(context.Background(), "admins", "", 20, 0)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	var found bool
	for _, entry := range entries {
		if entry.Event == "admin_removed" {
			found = true
		}
	}
	if !found {
		t.Fatal("the removal was not audited")
	}
}

func TestPrimaryAdminCannotBeRemoved(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)
	signIn(t, panel, "primary", true)
	_, cookies := signIn(t, panel, "second", false)

	form := csrfForm(url.Values{"action": {"remove_admin"}, "username": {"primary"}, "confirm": {"primary"}})
	post(panel, "/server/"+testAdminPath+"/config/admins", form, cookies)
	if _, err := panel.adminByUsername(context.Background(), "primary"); err != nil {
		t.Fatal("the primary administrator was removed")
	}
}

func TestAPITokenIsShownOnceAndStoredHashed(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)
	rec, cookies := signIn(t, panel, "administrator", true)
	base := "/server/" + testAdminPath

	res := post(panel, base+"/config/security/tokens", csrfForm(url.Values{"action": {"create"}, "name": {"deploy"}}), cookies)
	if res.Code != http.StatusOK {
		t.Fatalf("create token: status = %d, want %d", res.Code, http.StatusOK)
	}
	body := res.Body.String()
	if !strings.Contains(body, "shown once") {
		t.Fatal("the issued token is not marked as one-time")
	}

	tokens, err := panel.apiTokens(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("read tokens: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("token count = %d, want 1", len(tokens))
	}

	// Reloading the page must not show the secret again.
	reload := get(panel, base+"/config/security/tokens", cookies)
	if strings.Contains(reload.Body.String(), "shown once") {
		t.Fatal("the token secret is shown on a later page load")
	}
}

func TestAuditExportIsCSV(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)
	_, cookies := signIn(t, panel, "administrator", true)
	panel.recordAudit(context.Background(), "setup", "setup_token_issued", "system", "", "test entry")

	res := get(panel, "/server/"+testAdminPath+"/config/logs/export", cookies)
	if res.Code != http.StatusOK {
		t.Fatalf("export status = %d, want %d", res.Code, http.StatusOK)
	}
	if got := res.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Fatalf("Content-Type = %q, want text/csv", got)
	}
	if got := res.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Fatalf("Content-Disposition = %q, want an attachment", got)
	}
	if !strings.HasPrefix(res.Body.String(), "occurred_at,category,event,actor,target,detail") {
		t.Fatal("the export is missing its header row")
	}
}

func TestStylesheetIsServedOutsideThePanelPrefix(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)

	req := httptest.NewRequest(http.MethodGet, panelCSSRoute, nil)
	res := httptest.NewRecorder()
	panel.Handlers()[panelCSSRoute].ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("stylesheet status = %d, want %d", res.Code, http.StatusOK)
	}
	if got := res.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Fatalf("Content-Type = %q, want text/css", got)
	}
	if strings.Contains(res.Body.String(), testAdminPath) {
		t.Fatal("the stylesheet discloses where the panel is mounted")
	}
}

func TestEveryPageTemplateParses(t *testing.T) {
	renderer, err := web.New(web.Options{})
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	pages, err := parsePages(renderer.Funcs())
	if err != nil {
		t.Fatalf("parse pages: %v", err)
	}

	entries, err := fs.Glob(templateFS, "templates/page/*.tmpl")
	if err != nil {
		t.Fatalf("glob pages: %v", err)
	}
	if len(pages) != len(entries) {
		t.Fatalf("parsed %d pages, want %d", len(pages), len(entries))
	}
	for _, entry := range entries {
		name := strings.TrimSuffix(path.Base(entry), ".tmpl")
		tmpl, ok := pages[name]
		if !ok {
			t.Fatalf("page %q was not parsed", name)
		}
		if tmpl.Lookup("layout") == nil {
			t.Fatalf("page %q has no layout", name)
		}
		if tmpl.Lookup("content") == nil {
			t.Fatalf("page %q defines no content block", name)
		}
	}
}

// inlineHandler matches an inline style attribute or any inline event handler.
var inlineHandler = regexp.MustCompile(`(?i)(\sstyle\s*=|\son[a-z]+\s*=\s*["'])`)

// assertNoInlineHandlers fails when markup carries inline styling or scripting.
func assertNoInlineHandlers(t *testing.T, name, markup string) {
	t.Helper()
	if match := inlineHandler.FindString(markup); match != "" {
		t.Fatalf("%s contains inline markup %q", name, strings.TrimSpace(match))
	}
	for _, banned := range []string{"alert(", "confirm(", "javascript:"} {
		if strings.Contains(markup, banned) {
			t.Fatalf("%s contains %q", name, banned)
		}
	}
}

func TestTemplatesCarryNoInlineStyleOrScript(t *testing.T) {
	err := fs.WalkDir(templateFS, "templates", func(entry string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := fs.ReadFile(templateFS, entry)
		if readErr != nil {
			return readErr
		}
		assertNoInlineHandlers(t, entry, string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
}

func TestPanelStylesheetUsesSharedTokensOnly(t *testing.T) {
	css := string(panelCSS)
	// A literal colour here would fork the shared design system.
	if regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).MatchString(css) {
		t.Fatal("panel.css defines a literal colour instead of using a token")
	}
	if strings.Contains(css, "rgb(") || strings.Contains(css, "hsl(") {
		t.Fatal("panel.css defines a literal colour function")
	}
	if !strings.Contains(css, "var(--color-") {
		t.Fatal("panel.css does not use the shared colour tokens")
	}
}

func TestAdminPathValidation(t *testing.T) {
	valid := []string{"", "administration", "control-room", "ops2", "/Control-Room/"}
	for _, candidate := range valid {
		if _, err := NormalizeAdminPath(candidate); err != nil {
			t.Fatalf("NormalizeAdminPath(%q) = %v, want no error", candidate, err)
		}
	}
	invalid := []string{"has space", "with/slash", "ops_panel", "..", "-lead", "x", strings.Repeat("a", 200), "auth", "static"}
	for _, candidate := range invalid {
		if _, err := NormalizeAdminPath(candidate); err == nil {
			t.Fatalf("NormalizeAdminPath(%q) accepted an unusable segment", candidate)
		}
	}
}

func TestInviteRedemptionIsSingleUse(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)
	ctx := context.Background()
	signIn(t, panel, "administrator", true)

	ttl, ok := inviteTTL("24h")
	if !ok {
		t.Fatal("the 24h invite lifetime is not offered")
	}
	token, err := panel.createInvite(ctx, "second-admin", "administrator", ttl, 1)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if err := panel.RedeemInvite(ctx, token, "second-admin", testPassword, "second@example.test"); err != nil {
		t.Fatalf("redeem invite: %v", err)
	}
	if err := panel.RedeemInvite(ctx, token, "third-admin", testPassword, "third@example.test"); err == nil {
		t.Fatal("the invite was redeemed twice")
	}
	rec, err := panel.adminByUsername(ctx, "second-admin")
	if err != nil {
		t.Fatalf("lookup redeemed admin: %v", err)
	}
	if rec.IsPrimary {
		t.Fatal("an invited administrator was made primary")
	}
}

// newTestPanelWithNotifier builds a panel wired to a real, SQLite-backed
// Notifier sharing the panel's own database, so admin_login/admin_logout
// dispatch can be asserted through the notifier's own dedup store.
func newTestPanelWithNotifier(t *testing.T, adminPath string) (*Panel, *notify.Notifier) {
	t.Helper()

	db, err := database.Open(database.Config{Driver: database.DriverSQLite, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	renderer, err := web.New(web.Options{AppName: "CasHp", BaseURL: "https://panel.example"})
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	n, err := notify.New(notify.Options{DB: db, ConfigDir: t.TempDir(), AppName: "cashp"})
	if err != nil {
		t.Fatalf("new notifier: %v", err)
	}

	panel, err := New(Options{Renderer: renderer, DB: db, AdminPath: adminPath, Notifier: n})
	if err != nil {
		t.Fatalf("new panel: %v", err)
	}
	return panel, n
}

func TestLoginNotifiesAdminLogin(t *testing.T) {
	panel, n := newTestPanelWithNotifier(t, testAdminPath)

	hash, err := security.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := panel.createAdmin(context.Background(), "administrator", hash, "administrator@example.test", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	form := csrfForm(url.Values{"action": {"login"}, "username": {"administrator"}, "password": {testPassword}})
	cookies := []*http.Cookie{{Name: csrfCookieName, Value: testCSRF}}
	rec := post(panel, panel.base(), form, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login POST status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	held, err := n.Store().DedupHeld(context.Background(), notify.EventAdminLogin+":")
	if err != nil {
		t.Fatalf("dedup held: %v", err)
	}
	if !held {
		t.Fatal("expected admin_login to have been dispatched")
	}
}

func TestLogoutNotifiesAdminLogout(t *testing.T) {
	panel, n := newTestPanelWithNotifier(t, testAdminPath)
	_, cookies := signIn(t, panel, "administrator", true)

	form := csrfForm(url.Values{"action": {"logout"}})
	rec := post(panel, panel.base(), form, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("logout POST status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	held, err := n.Store().DedupHeld(context.Background(), notify.EventAdminLogout+":")
	if err != nil {
		t.Fatalf("dedup held: %v", err)
	}
	if !held {
		t.Fatal("expected admin_logout to have been dispatched")
	}
}

func TestLoginWithoutNotifierSkipsNotification(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)

	hash, err := security.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := panel.createAdmin(context.Background(), "administrator", hash, "administrator@example.test", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	form := csrfForm(url.Values{"action": {"login"}, "username": {"administrator"}, "password": {testPassword}})
	cookies := []*http.Cookie{{Name: csrfCookieName, Value: testCSRF}}
	rec := post(panel, panel.base(), form, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login POST status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
}

// TestNotificationsPageGroupsByPart18Categories checks that the notifications
// page renders the AI.md PART 18 category headings, in order, with the
// Security category's events locked on.
func TestNotificationsPageGroupsByPart18Categories(t *testing.T) {
	panel, _ := newTestPanelWithNotifier(t, testAdminPath)
	rec, cookies := signIn(t, panel, "administrator", true)

	target := "/server/" + testAdminPath + "/" + rec.Username + "/notifications"
	res := get(panel, target, cookies)
	if res.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d", target, res.Code, http.StatusOK)
	}
	body := res.Body.String()

	order := []string{
		`card-title">Security<`, `card-title">Server<`, `card-title">Backup<`,
		`card-title">Scheduler<`, `card-title">Other Admins<`,
	}
	last := -1
	for _, want := range order {
		idx := strings.Index(body, want)
		if idx == -1 {
			t.Fatalf("notifications page is missing category heading %q", want)
		}
		if idx <= last {
			t.Fatalf("category heading %q rendered out of PART 18 order", want)
		}
		last = idx
	}
	if !strings.Contains(body, "Required") {
		t.Fatal("expected the Security category to show a Required badge")
	}
}

// TestNotificationsPostSavesTogglesViaNotifyStore checks that submitting the
// notifications form persists the requested toggles through notify.Store,
// not the generic admin_preferences key/value table.
func TestNotificationsPostSavesTogglesViaNotifyStore(t *testing.T) {
	panel, n := newTestPanelWithNotifier(t, testAdminPath)
	rec, cookies := signIn(t, panel, "administrator", true)

	before, err := n.Store().Preferences(context.Background(), notify.AudienceAdmin, rec.ID)
	if err != nil {
		t.Fatalf("preferences: %v", err)
	}
	if len(before) == 0 {
		t.Fatal("expected at least one admin notification preference to seed the form")
	}
	var target notify.Preference
	for _, pref := range before {
		if !pref.Required {
			target = pref
			break
		}
	}
	if target.Event == "" {
		t.Fatal("expected at least one non-required admin notification preference")
	}

	form := url.Values{
		"notification_email": {""},
		"webui_" + target.Event: {"1"},
	}
	if target.Emailable {
		form.Set("email_"+target.Event, "1")
	}
	page := "/" + rec.Username + "/notifications"
	req := csrfForm(form)
	res := post(panel, panel.base()+page, req, cookies)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("notifications POST status = %d, want %d, body=%s", res.Code, http.StatusSeeOther, res.Body.String())
	}

	after, err := n.Store().Preferences(context.Background(), notify.AudienceAdmin, rec.ID)
	if err != nil {
		t.Fatalf("preferences after save: %v", err)
	}
	var found bool
	for _, pref := range after {
		if pref.Event != target.Event {
			continue
		}
		found = true
		if !pref.WebUI {
			t.Fatalf("expected %s WebUI toggle to be saved on", target.Event)
		}
		if target.Emailable && !pref.Email {
			t.Fatalf("expected %s Email toggle to be saved on", target.Event)
		}
	}
	if !found {
		t.Fatalf("saved preference %s not found after save", target.Event)
	}
}

// TestNotificationsPageWithoutNotifierIsInformative checks that a Panel with
// no notifier renders the notifications page without failing the request.
func TestNotificationsPageWithoutNotifierIsInformative(t *testing.T) {
	panel := newTestPanel(t, testAdminPath)
	rec, cookies := signIn(t, panel, "administrator", true)

	target := "/server/" + testAdminPath + "/" + rec.Username + "/notifications"
	res := get(panel, target, cookies)
	if res.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d", target, res.Code, http.StatusOK)
	}
	if !strings.Contains(res.Body.String(), "not available on this server yet") {
		t.Fatal("expected the nil-notifier fallback message")
	}
}
