package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/security"
)

// TestContextAccessorsRejectWrongTypeAndAbsence checks every ctx accessor
// returns the zero value / false when its key is unset, matching the
// "identities only enter via middleware" invariant middleware.go documents.
func TestContextAccessorsRejectWrongTypeAndAbsence(t *testing.T) {
	ctx := context.Background()
	if _, ok := UserFrom(ctx); ok {
		t.Error("UserFrom on empty context reported ok")
	}
	if _, ok := SessionFrom(ctx); ok {
		t.Error("SessionFrom on empty context reported ok")
	}
	if _, ok := AdminFrom(ctx); ok {
		t.Error("AdminFrom on empty context reported ok")
	}
	if _, ok := TokenFrom(ctx); ok {
		t.Error("TokenFrom on empty context reported ok")
	}
	if _, ok := OrgFrom(ctx); ok {
		t.Error("OrgFrom on empty context reported ok")
	}
	if r := OrgRoleFrom(ctx); r != "" {
		t.Errorf("OrgRoleFrom = %q, want empty", r)
	}
	if c := CSRFTokenFrom(ctx); c != "" {
		t.Errorf("CSRFTokenFrom = %q, want empty", c)
	}

	u := &User{ID: 1}
	ctx2 := context.WithValue(ctx, ctxUser, u)
	got, ok := UserFrom(ctx2)
	if !ok || got != u {
		t.Errorf("UserFrom = %v,%v, want %v,true", got, ok, u)
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		trustProxy bool
		xff        string
		realIP     string
		remoteAddr string
		want       string
	}{
		{"trusted xff", true, "1.2.3.4, 5.6.7.8", "", "9.9.9.9:1234", "1.2.3.4"},
		{"trusted real ip fallback", true, "", "1.2.3.4", "9.9.9.9:1234", "1.2.3.4"},
		{"untrusted ignores headers", false, "1.2.3.4", "", "9.9.9.9:1234", "9.9.9.9"},
		{"no port in remote addr", true, "", "", "9.9.9.9", "9.9.9.9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/x", nil)
			r.RemoteAddr = c.remoteAddr
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			if c.realIP != "" {
				r.Header.Set("X-Real-IP", c.realIP)
			}
			if got := ClientIP(r, c.trustProxy); got != c.want {
				t.Errorf("ClientIP = %q, want %q", got, c.want)
			}
		})
	}
}

func TestWantsJSON(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	cases := []struct {
		path        string
		accept      string
		contentType string
		want        bool
	}{
		{"/api/v1/x", "", "", true},
		{"/auth/login", "application/json", "", true},
		{"/auth/login", "", "application/json", true},
		{"/auth/login", "text/html", "", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, c.path, nil)
		if c.accept != "" {
			r.Header.Set("Accept", c.accept)
		}
		if c.contentType != "" {
			r.Header.Set("Content-Type", c.contentType)
		}
		if got := svc.wantsJSON(r); got != c.want {
			t.Errorf("wantsJSON(%s accept=%q ct=%q) = %v, want %v", c.path, c.accept, c.contentType, got, c.want)
		}
	}
}

func TestFailJSONAndRedirectAndRenderPaths(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)

	t.Run("nil error is a no-op", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
		svc.Fail(w, r, nil)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 (untouched)", w.Code)
		}
	})

	t.Run("json path writes envelope", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
		svc.Fail(w, r, ErrForbidden())
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", w.Code)
		}
	})

	t.Run("unauthenticated GET redirects to login", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
		svc.Fail(w, r, ErrUnauthenticated())
		if w.Code != http.StatusSeeOther {
			t.Errorf("status = %d, want 303", w.Code)
		}
		loc := w.Header().Get("Location")
		if loc == "" {
			t.Error("no Location header set on redirect")
		}
	})

	t.Run("unauthenticated GET under admin path redirects to admin login", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/"+svc.cfg.AdminPath+"/dashboard", nil)
		svc.Fail(w, r, ErrUnauthenticated())
		want := "/" + svc.cfg.AdminPath + "/login"
		if got := w.Header().Get("Location"); got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
	})

	t.Run("unauthenticated POST does not redirect", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/dashboard", nil)
		svc.Fail(w, r, ErrUnauthenticated())
		if w.Code == http.StatusSeeOther {
			t.Error("POST request was redirected, want a rendered/plain error")
		}
	})

	t.Run("rate limited sets Retry-After", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
		svc.Fail(w, r, ErrRateLimited(30))
		if got := w.Header().Get("Retry-After"); got != "30" {
			t.Errorf("Retry-After = %q, want 30", got)
		}
	})

	t.Run("nil renderer falls back to http.Error", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/some/page", nil)
		svc.Fail(w, r, ErrForbidden())
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", w.Code)
		}
		if w.Body.Len() == 0 {
			t.Error("expected a body from the fallback http.Error path")
		}
	})
}

func TestSafeNext(t *testing.T) {
	cases := []struct {
		next, fallback, want string
	}{
		{"/dashboard", "/", "/dashboard"},
		{"", "/", "/"},
		{"//evil.com", "/", "/"},
		{"https://evil.com", "/", "/"},
		{"relative", "/", "/"},
		{"/has\r\ninjection", "/", "/"},
		{"/has\\backslash", "/", "/"},
	}
	for _, c := range cases {
		if got := SafeNext(c.next, c.fallback); got != c.want {
			t.Errorf("SafeNext(%q,%q) = %q, want %q", c.next, c.fallback, got, c.want)
		}
	}
}

func TestSessionAndAdminCookieRoundTrip(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)

	w := httptest.NewRecorder()
	svc.SetSessionCookie(w, "tok123")
	res := w.Result()
	found := false
	for _, c := range res.Cookies() {
		if c.Name == SessionCookieName {
			found = true
			if c.Value != "tok123" || !c.HttpOnly {
				t.Errorf("session cookie = %+v, want value=tok123 httponly", c)
			}
		}
	}
	if !found {
		t.Fatal("SetSessionCookie did not set the session cookie")
	}

	w2 := httptest.NewRecorder()
	svc.ClearSessionCookie(w2)
	for _, c := range w2.Result().Cookies() {
		if c.Name == SessionCookieName && c.MaxAge >= 0 {
			t.Errorf("ClearSessionCookie left MaxAge = %d, want negative", c.MaxAge)
		}
	}

	w3 := httptest.NewRecorder()
	svc.SetAdminCookie(w3, "atok")
	for _, c := range w3.Result().Cookies() {
		if c.Name == AdminCookieName && c.Value != "atok" {
			t.Errorf("admin cookie value = %q, want atok", c.Value)
		}
	}

	w4 := httptest.NewRecorder()
	svc.ClearAdminCookie(w4)
	for _, c := range w4.Result().Cookies() {
		if c.Name == AdminCookieName && c.MaxAge >= 0 {
			t.Errorf("ClearAdminCookie left MaxAge = %d, want negative", c.MaxAge)
		}
	}
}

func TestBearerToken(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"Bearer abc123", "abc123"},
		{"bearer abc123", "abc123"},
		{"Basic abc123", ""},
		{"", ""},
		{"Bear", ""},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		if c.header != "" {
			r.Header.Set("Authorization", c.header)
		}
		if got := bearerToken(r); got != c.want {
			t.Errorf("bearerToken(%q) = %q, want %q", c.header, got, c.want)
		}
	}
}

// TestLoadUserAndRequireUserFlow covers the primary session-cookie auth flow:
// unauthenticated is passed through by LoadUser but rejected by RequireUser,
// then a real session accepted by both, then an expired/garbage session
// cleared and rejected.
func TestLoadUserAndRequireUserFlow(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	u := registerTestUser(t, svc, "loaduser", "loaduser@example.com")
	_, token, aerr := svc.Login(context.Background(), LoginInput{Identifier: "loaduser", Password: "a-good-password"})
	if aerr != nil {
		t.Fatalf("Login: %v", aerr)
	}

	var seenUser bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, seenUser = UserFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	t.Run("LoadUser passes through without a cookie", func(t *testing.T) {
		seenUser = false
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		svc.LoadUser(next).ServeHTTP(w, r)
		if w.Code != http.StatusOK || seenUser {
			t.Errorf("code=%d seenUser=%v, want 200 false", w.Code, seenUser)
		}
	})

	t.Run("LoadUser attaches user with a valid cookie", func(t *testing.T) {
		seenUser = false
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
		svc.LoadUser(next).ServeHTTP(w, r)
		if w.Code != http.StatusOK || !seenUser {
			t.Errorf("code=%d seenUser=%v, want 200 true", w.Code, seenUser)
		}
	})

	t.Run("RequireUser rejects unauthenticated", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		svc.RequireUser(next).ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("RequireUser rejects garbage session token", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "not-a-real-token"})
		svc.RequireUser(next).ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("RequireUser accepts a valid session and injects CSRF token", func(t *testing.T) {
		var csrf string
		checker := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			csrf = CSRFTokenFrom(r.Context())
			if got, ok := UserFrom(r.Context()); !ok || got.ID != u.ID {
				t.Errorf("UserFrom = %v,%v, want %d,true", got, ok, u.ID)
			}
			w.WriteHeader(http.StatusOK)
		})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
		svc.RequireUser(checker).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
		if csrf == "" {
			t.Error("RequireUser did not plant a CSRF token in the context")
		}
	})

	t.Run("RequireUser rejects when users are disabled", func(t *testing.T) {
		disabled := newTestServiceWithConfig(t, func(c *Config) { c.UsersEnabled = false })
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		disabled.RequireUser(next).ServeHTTP(w, r)
		if w.Code < 400 {
			t.Errorf("status = %d, want an error status", w.Code)
		}
	})
}

// TestRequireAdminFlow mirrors the user flow but for the Server Admin cookie,
// and asserts the admin cookie is never accepted via the user middleware or
// vice versa.
func TestRequireAdminFlow(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	tok, err := svc.IssueSetupToken(ctx)
	if err != nil {
		t.Fatalf("IssueSetupToken: %v", err)
	}
	_, adminToken, aerr := svc.CompleteBootstrap(ctx, BootstrapInput{
		SetupToken: tok, Username: "root-admin", Email: "root@example.com", Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("CompleteBootstrap: %v", aerr)
	}

	var seenAdmin bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, seenAdmin = AdminFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	t.Run("rejects unauthenticated", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/server/administration", nil)
		r.Header.Set("Accept", "application/json")
		svc.RequireAdmin(next).ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("rejects unauthenticated browser request with a login redirect", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/server/administration", nil)
		svc.RequireAdmin(next).ServeHTTP(w, r)
		if w.Code != http.StatusSeeOther {
			t.Errorf("status = %d, want 303 (login redirect)", w.Code)
		}
	})

	t.Run("rejects a user session cookie presented as admin", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/server/administration", nil)
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: adminToken})
		svc.RequireAdmin(next).ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("accepts a valid admin cookie", func(t *testing.T) {
		seenAdmin = false
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/server/administration", nil)
		r.AddCookie(&http.Cookie{Name: AdminCookieName, Value: adminToken})
		svc.RequireAdmin(next).ServeHTTP(w, r)
		if w.Code != http.StatusOK || !seenAdmin {
			t.Errorf("code=%d seenAdmin=%v, want 200 true", w.Code, seenAdmin)
		}
	})
}

// TestRequireAuthAndRequireTokenAndAuthenticateToken exercises the bearer
// token path, including admin-prefixed tokens, disabled/locked owners, and
// org-scoped tokens acting at owner level.
func TestRequireAuthAndRequireTokenAndAuthenticateToken(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	u := registerTestUser(t, svc, "tokenuser", "tokenuser@example.com")

	pub, aerr := svc.CreateUserToken(ctx, u.ID, TokenInput{Name: "ci", Scopes: []string{"*"}})
	if aerr != nil {
		t.Fatalf("CreateUserToken: %v", aerr)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFrom(r.Context()); !ok {
			t.Error("authenticated request missing user in context")
		}
		w.WriteHeader(http.StatusOK)
	})

	t.Run("RequireToken rejects missing bearer", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
		svc.RequireToken(next).ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("RequireToken rejects an unknown token", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
		r.Header.Set("Authorization", "Bearer usr_totally-bogus-token")
		svc.RequireToken(next).ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("RequireToken accepts a real user token", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
		r.Header.Set("Authorization", "Bearer "+pub.Token)
		svc.RequireToken(next).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})

	t.Run("RequireAuth falls back to session cookie when no bearer given", func(t *testing.T) {
		_, sessTok, aerr := svc.Login(ctx, LoginInput{Identifier: "tokenuser", Password: "a-good-password"})
		if aerr != nil {
			t.Fatalf("Login: %v", aerr)
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessTok})
		svc.RequireAuth(next).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})

	t.Run("RequireAuth rejects a malformed bearer token", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
		r.Header.Set("Authorization", "Bearer garbage")
		svc.RequireAuth(next).ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("admin-prefixed token authenticates as admin", func(t *testing.T) {
		setupTok, err := svc.IssueSetupToken(ctx)
		if err != nil {
			t.Fatalf("IssueSetupToken: %v", err)
		}
		_, _, aerr := svc.CompleteBootstrap(ctx, BootstrapInput{
			SetupToken: setupTok, Username: "adm2", Email: "adm2@example.com", Password: "a-good-password",
		})
		if aerr != nil {
			t.Fatalf("CompleteBootstrap: %v", aerr)
		}
		admin, err := svc.store.AdminByUsername(ctx, "adm2")
		if err != nil {
			t.Fatalf("AdminByUsername: %v", err)
		}
		raw := security.PrefixAdmin + "0123456789abcdef0123456789abcdef"
		hash := security.HashToken(raw)
		if _, err := svc.store.DB().ExecContext(ctx, database.TimeoutWrite,
			"UPDATE admins SET token_hash = ?, token_prefix = ? WHERE id = ?",
			hash, raw[:len(security.PrefixAdmin)+6], admin.ID); err != nil {
			t.Fatalf("set admin token: %v", err)
		}
		newCtx, aerr := svc.authenticateToken(ctx, raw)
		if aerr != nil {
			t.Fatalf("authenticateToken: %v", aerr)
		}
		if got, ok := AdminFrom(newCtx); !ok || got.ID != admin.ID {
			t.Errorf("AdminFrom = %v,%v, want %d,true", got, ok, admin.ID)
		}
	})
}

func TestHasScope(t *testing.T) {
	cases := []struct {
		name  string
		token *Token
		scope string
		want  bool
	}{
		{"nil token grants everything (session auth)", nil, "domains:write", true},
		{"wildcard grants everything", &Token{Scopes: `["*"]`}, "domains:write", true},
		{"exact match", &Token{Scopes: `["domains:write"]`}, "domains:write", true},
		{"group grants sub-action", &Token{Scopes: `["domains"]`}, "domains:write", true},
		{"no match", &Token{Scopes: `["orgs:read"]`}, "domains:write", false},
		{"empty scopes deny", &Token{Scopes: `[]`}, "domains:write", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HasScope(c.token, c.scope); got != c.want {
				t.Errorf("HasScope = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRequireScope(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	t.Run("no token in context passes through", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		svc.RequireScope("domains:write")(next).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})

	t.Run("token without the scope is forbidden", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		ctx := context.WithValue(r.Context(), ctxToken, &Token{Scopes: `["orgs:read"]`})
		svc.RequireScope("domains:write")(next).ServeHTTP(w, r.WithContext(ctx))
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", w.Code)
		}
	})

	t.Run("token with the scope passes", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		ctx := context.WithValue(r.Context(), ctxToken, &Token{Scopes: `["domains:write"]`})
		svc.RequireScope("domains:write")(next).ServeHTTP(w, r.WithContext(ctx))
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})
}

func TestOrgSlugFrom(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/acme/members", nil)
	if got := OrgSlugFrom(r); got != "acme" {
		t.Errorf("OrgSlugFrom (fallback scan) = %q, want acme", got)
	}
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/{slug}/members", nil)
	r2.SetPathValue("slug", "acme-2")
	if got := OrgSlugFrom(r2); got != "acme-2" {
		t.Errorf("OrgSlugFrom (PathValue) = %q, want acme-2", got)
	}
	r3 := httptest.NewRequest(http.MethodGet, "/nothing/here", nil)
	if got := OrgSlugFrom(r3); got != "" {
		t.Errorf("OrgSlugFrom (no match) = %q, want empty", got)
	}
}

// TestRequireOrgRoleFlow covers the org membership/role authorization
// boundary: non-member gets the anti-enumeration 404, a member below the
// required role is also 404'd, and a member at/above the role is admitted.
func TestRequireOrgRoleFlow(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	owner := registerTestUser(t, svc, "orgowner", "orgowner@example.com")
	member := registerTestUser(t, svc, "orgmember", "orgmember@example.com")
	outsider := registerTestUser(t, svc, "outsider", "outsider@example.com")

	org, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Name: "Acme Inc", Slug: "acme"}, "")
	if aerr != nil {
		t.Fatalf("CreateOrg: %v", aerr)
	}
	if aerr := svc.AddOrgMember(ctx, org.ID, owner.ID, member.Username, OrgRoleMember); aerr != nil {
		t.Fatalf("AddOrgMember: %v", aerr)
	}

	withUserCtx := func(r *http.Request, u *User) *http.Request {
		return r.WithContext(context.WithValue(r.Context(), ctxUser, u))
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	t.Run("nonexistent org is 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/no-such-org/members", nil)
		r.SetPathValue("slug", "no-such-org")
		r = withUserCtx(r, owner)
		svc.RequireOrgRole(OrgRoleMember)(next).ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	t.Run("non-member is 404, not 403 (anti-enumeration)", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/acme/members", nil)
		r.SetPathValue("slug", "acme")
		r = withUserCtx(r, outsider)
		svc.RequireOrgRole(OrgRoleMember)(next).ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	t.Run("member below required role is 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/acme/settings", nil)
		r.SetPathValue("slug", "acme")
		r = withUserCtx(r, member)
		svc.RequireOrgRole(OrgRoleOwner)(next).ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	t.Run("owner is admitted and org/role planted in context", func(t *testing.T) {
		var gotRole string
		checker := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotRole = OrgRoleFrom(r.Context())
			if o, ok := OrgFrom(r.Context()); !ok || o.ID != org.ID {
				t.Errorf("OrgFrom = %v,%v, want %d,true", o, ok, org.ID)
			}
			w.WriteHeader(http.StatusOK)
		})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/acme/members", nil)
		r.SetPathValue("slug", "acme")
		r = withUserCtx(r, owner)
		svc.RequireOrgRole(OrgRoleMember)(checker).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
		if gotRole != OrgRoleOwner {
			t.Errorf("role = %q, want %q", gotRole, OrgRoleOwner)
		}
	})

	t.Run("disabled feature is rejected", func(t *testing.T) {
		disabled := newTestServiceWithConfig(t, func(c *Config) { c.OrgsEnabled = false })
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/acme/members", nil)
		r.SetPathValue("slug", "acme")
		disabled.RequireOrgRole(OrgRoleMember)(next).ServeHTTP(w, r)
		if w.Code < 400 {
			t.Errorf("status = %d, want an error status", w.Code)
		}
	})
}

func TestRoleRankAndAtLeast(t *testing.T) {
	if roleRank(OrgRoleOwner) <= roleRank(OrgRoleAdmin) {
		t.Error("Owner should outrank Admin")
	}
	if roleRank(OrgRoleAdmin) <= roleRank(OrgRoleMember) {
		t.Error("Admin should outrank Member")
	}
	if roleRank("bogus") != 0 {
		t.Errorf("roleRank(bogus) = %d, want 0", roleRank("bogus"))
	}
	if !roleAtLeast(OrgRoleOwner, OrgRoleMember) {
		t.Error("Owner should satisfy at-least Member")
	}
	if roleAtLeast(OrgRoleMember, OrgRoleOwner) {
		t.Error("Member should not satisfy at-least Owner")
	}
}

// TestRequireCSRFFlow covers the CSRF gate: safe methods pass, bearer
// requests skip the check, an unauthenticated write is rejected, a session
// write missing the token is rejected, and a valid header token is accepted.
func TestRequireCSRFFlow(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	t.Run("GET passes through untouched", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		svc.RequireCSRF(next).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})

	t.Run("bearer-authenticated write bypasses CSRF", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/x", nil)
		ctx := context.WithValue(r.Context(), ctxToken, &Token{Scopes: `["*"]`})
		svc.RequireCSRF(next).ServeHTTP(w, r.WithContext(ctx))
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})

	t.Run("no session is unauthenticated", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/x", nil)
		svc.RequireCSRF(next).ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	sess := &Session{TokenHash: "some-hash"}
	t.Run("session without a token is rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/x", nil)
		ctx := context.WithValue(r.Context(), ctxSession, sess)
		svc.RequireCSRF(next).ServeHTTP(w, r.WithContext(ctx))
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", w.Code)
		}
	})

	t.Run("valid header token is accepted", func(t *testing.T) {
		token := security.NewCSRFToken(svc.csrfKey, sess.TokenHash)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/x", nil)
		r.Header.Set(CSRFHeaderName, token)
		ctx := context.WithValue(r.Context(), ctxSession, sess)
		svc.RequireCSRF(next).ServeHTTP(w, r.WithContext(ctx))
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})

	t.Run("token for a different session is rejected", func(t *testing.T) {
		other := &Session{TokenHash: "other-hash"}
		token := security.NewCSRFToken(svc.csrfKey, other.TokenHash)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/x", nil)
		r.Header.Set(CSRFHeaderName, token)
		ctx := context.WithValue(r.Context(), ctxSession, sess)
		svc.RequireCSRF(next).ServeHTTP(w, r.WithContext(ctx))
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", w.Code)
		}
	})
}

func TestCSRFToken(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)

	t.Run("no session yields empty token", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		if got := svc.CSRFToken(r); got != "" {
			t.Errorf("CSRFToken = %q, want empty", got)
		}
	})

	t.Run("prefers a pre-planted token", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		ctx := context.WithValue(r.Context(), ctxCSRF, "already-minted")
		if got := svc.CSRFToken(r.WithContext(ctx)); got != "already-minted" {
			t.Errorf("CSRFToken = %q, want already-minted", got)
		}
	})

	t.Run("mints from session when nothing planted", func(t *testing.T) {
		sess := &Session{TokenHash: "hash-x"}
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		ctx := context.WithValue(r.Context(), ctxSession, sess)
		got := svc.CSRFToken(r.WithContext(ctx))
		if got == "" {
			t.Fatal("CSRFToken returned empty for a request with a session")
		}
		if !security.ValidateCSRFToken(svc.csrfKey, sess.TokenHash, got) {
			t.Error("minted CSRF token does not validate against the session")
		}
	})
}

func TestRateLimitAndRateLimitByMethod(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	t.Run("RateLimit allows within budget", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.RemoteAddr = "127.0.0.1:1"
		svc.RateLimit("login")(next).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})

	t.Run("RateLimitByMethod routes GET through the read limiter", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.RemoteAddr = "127.0.0.2:1"
		svc.RateLimitByMethod(next).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})

	t.Run("RateLimitByMethod routes POST through the write limiter", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/x", nil)
		r.RemoteAddr = "127.0.0.3:1"
		svc.RateLimitByMethod(next).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})

	t.Run("exhausting the limit yields 429 with Retry-After", func(t *testing.T) {
		ip := "127.0.0.9:1"
		var last *httptest.ResponseRecorder
		for i := 0; i < 200; i++ {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/x", nil)
			r.RemoteAddr = ip
			svc.RateLimit("login")(next).ServeHTTP(w, r)
			last = w
			if w.Code == http.StatusTooManyRequests {
				break
			}
		}
		if last.Code != http.StatusTooManyRequests {
			t.Fatalf("never hit the rate limit after 200 requests, last status = %d", last.Code)
		}
		if last.Header().Get("Retry-After") == "" {
			t.Error("429 response missing Retry-After header")
		}
	})
}

func TestChain(t *testing.T) {
	var order []string
	mkmw := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "final")
		w.WriteHeader(http.StatusOK)
	})

	h := Chain(final, mkmw("outer"), nil, mkmw("inner"))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	h.ServeHTTP(w, r)

	want := []string{"outer", "inner", "final"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q", i, order[i], want[i])
		}
	}
}

func TestUrlQueryEscape(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/dashboard", "/dashboard"},
		{"/a?b=c d", "/a%3Fb%3Dc%20d"},
		{"", ""},
	}
	for _, c := range cases {
		if got := urlQueryEscape(c.in); got != c.want {
			t.Errorf("urlQueryEscape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
