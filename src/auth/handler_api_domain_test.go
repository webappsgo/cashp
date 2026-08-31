package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestUserDomainHandlersFlow drives the shared list/add/get/verify/delete
// domain handlers through userDomainOwner, via the real RequireUser
// middleware chain (unauthenticated rejected -> authenticated accepted).
func TestUserDomainHandlersFlow(t *testing.T) {
	svc := newTestDomainService(t, fakeResolver{}, nil)
	user := registerTestUser(t, svc, "domainowner", "domainowner@example.com")
	_, sessionToken, aerr := svc.Login(context.Background(), LoginInput{
		Identifier: "domainowner", Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("Login: %v", aerr)
	}

	list := svc.listDomains(userDomainOwner)
	add := svc.addDomain(userDomainOwner)
	get := svc.getDomain(userDomainOwner)
	verify := svc.verifyDomain(userDomainOwner)
	del := svc.deleteDomain(userDomainOwner)

	t.Run("list rejects unauthenticated", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/me/domains", nil)
		r.Header.Set("Accept", "application/json")
		svc.RequireUser(list).ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("list starts empty", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/me/domains", nil)
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
		svc.RequireUser(list).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var rows []PublicDomain
		decodeOK(t, w, &rows)
		if len(rows) != 0 {
			t.Errorf("len(rows) = %d, want 0", len(rows))
		}
	})

	t.Run("add rejects an invalid domain", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/me/domains", strings.NewReader(`{"domain":"not a domain"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
		svc.RequireUser(add).ServeHTTP(w, r)
		if w.Code < 400 {
			t.Errorf("status = %d, want 4xx for an invalid domain", w.Code)
		}
	})

	t.Run("add succeeds", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/me/domains", strings.NewReader(`{"domain":"example.com"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
		svc.RequireUser(add).ServeHTTP(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
		var pub PublicDomain
		decodeOK(t, w, &pub)
		if pub.Domain != "example.com" {
			t.Errorf("Domain = %q, want example.com", pub.Domain)
		}
	})

	t.Run("add rejects a duplicate domain for the same owner", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/me/domains", strings.NewReader(`{"domain":"example.com"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
		svc.RequireUser(add).ServeHTTP(w, r)
		if w.Code < 400 {
			t.Errorf("status = %d, want 4xx for a duplicate domain", w.Code)
		}
	})

	t.Run("get returns the domain", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/me/domains/example.com", nil)
		r.SetPathValue("domain", "example.com")
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
		svc.RequireUser(get).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("get 404s an unknown domain", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/me/domains/nope.example", nil)
		r.SetPathValue("domain", "nope.example")
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
		svc.RequireUser(get).ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	t.Run("verify fails before the TXT record is in place", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/me/domains/example.com/verify", nil)
		r.SetPathValue("domain", "example.com")
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
		svc.RequireUser(verify).ServeHTTP(w, r)
		if w.Code < 400 {
			t.Errorf("status = %d, want 4xx with no matching TXT record", w.Code)
		}
	})

	t.Run("verify succeeds once the TXT record matches", func(t *testing.T) {
		// Fetch the expected token from the domain we just added, then point
		// the service's resolver at it (mirrors service_domain_test.go's own
		// pattern of swapping svc.resolver in place for a fake DNS answer).
		owner := DomainOwner{Type: OwnerUser, ID: user.ID}
		d, aerr := svc.GetDomain(context.Background(), owner, "example.com")
		if aerr != nil {
			t.Fatalf("GetDomain: %v", aerr)
		}
		svc.resolver = fakeResolver{txt: []string{d.VerificationToken}}

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/me/domains/example.com/verify", nil)
		r.SetPathValue("domain", "example.com")
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
		svc.RequireUser(verify).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("delete removes the domain", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/me/domains/example.com", nil)
		r.SetPathValue("domain", "example.com")
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
		svc.RequireUser(del).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("delete is idempotent-safe against a second call (not a 500)", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/me/domains/example.com", nil)
		r.SetPathValue("domain", "example.com")
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
		svc.RequireUser(del).ServeHTTP(w, r)
		if w.Code >= 500 {
			t.Errorf("status = %d, want a non-5xx response for a repeat delete", w.Code)
		}
	})
}

// TestOrgDomainOwnerRejectsWithoutOrgContext covers orgDomainOwner's guard
// clause directly: without RequireOrgRole having run first, there is no org
// in context and the shared handler must fail closed rather than panic or
// silently resolve to some default owner.
func TestOrgDomainOwnerRejectsWithoutOrgContext(t *testing.T) {
	svc := newTestDomainService(t, fakeResolver{}, nil)
	registerTestUser(t, svc, "orgdomainowner", "orgdomainowner@example.com")
	_, sessionToken, aerr := svc.Login(context.Background(), LoginInput{
		Identifier: "orgdomainowner", Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("Login: %v", aerr)
	}

	list := svc.listDomains(orgDomainOwner)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/some-org/domains", nil)
	r.Header.Set("Accept", "application/json")
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
	// RequireUser only, deliberately skipping RequireOrgRole, to prove
	// orgDomainOwner fails closed rather than assuming org context exists.
	svc.RequireUser(list).ServeHTTP(w, r)
	if w.Code < 400 {
		t.Errorf("status = %d, want a 4xx failure with no org in context", w.Code)
	}
}

// TestOrgDomainHandlersFlow drives the org-scoped domain handlers through
// the real RequireUser -> RequireOrgRole chain, proving org membership is
// actually required and actually sufficient (anti-enumeration 404 for a
// non-member, success for an admin/owner).
func TestOrgDomainHandlersFlow(t *testing.T) {
	svc := newTestDomainService(t, fakeResolver{}, nil)
	owner := registerTestUser(t, svc, "orgowner2", "orgowner2@example.com")
	registerTestUser(t, svc, "outsider2", "outsider2@example.com")
	if _, aerr := svc.CreateOrg(context.Background(), owner.ID, OrgInput{Slug: "domain-org", Name: "Domain Org"}, ""); aerr != nil {
		t.Fatalf("CreateOrg: %v", aerr)
	}

	_, ownerToken, aerr := svc.Login(context.Background(), LoginInput{Identifier: "orgowner2", Password: "a-good-password"})
	if aerr != nil {
		t.Fatalf("Login owner: %v", aerr)
	}
	_, outsiderToken, aerr := svc.Login(context.Background(), LoginInput{Identifier: "outsider2", Password: "a-good-password"})
	if aerr != nil {
		t.Fatalf("Login outsider: %v", aerr)
	}

	list := Chain(svc.listDomains(orgDomainOwner), svc.RequireUser, svc.RequireOrgRole(OrgRoleAdmin))
	add := Chain(svc.addDomain(orgDomainOwner), svc.RequireUser, svc.RequireOrgRole(OrgRoleAdmin))

	t.Run("non-member gets the anti-enumeration 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/domain-org/domains", nil)
		r.SetPathValue("slug", "domain-org")
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: outsiderToken})
		list.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	t.Run("owner can list the org's domains", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/domain-org/domains", nil)
		r.SetPathValue("slug", "domain-org")
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: ownerToken})
		list.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("owner can add a domain to the org", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/domain-org/domains", strings.NewReader(`{"domain":"org-example.com"}`))
		r.SetPathValue("slug", "domain-org")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: ownerToken})
		add.ServeHTTP(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
		var pub PublicDomain
		decodeOK(t, w, &pub)
		if pub.Domain != "org-example.com" {
			t.Errorf("Domain = %q, want org-example.com", pub.Domain)
		}
	})

	t.Run("non-member cannot add a domain to the org", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/domain-org/domains", strings.NewReader(`{"domain":"intruder.com"}`))
		r.SetPathValue("slug", "domain-org")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: outsiderToken})
		add.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404 (anti-enumeration)", w.Code)
		}
	})
}
