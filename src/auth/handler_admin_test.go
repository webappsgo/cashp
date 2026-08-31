package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// adminReq builds a JSON request against an admin-panel handler, attaching the
// admin session cookie when a token is given (mirrors orgReq in
// handler_api_org_test.go).
func adminReq(method, path, adminToken, body string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.Header.Set("Accept", "application/json")
	if adminToken != "" {
		r.AddCookie(&http.Cookie{Name: AdminCookieName, Value: adminToken})
	}
	return r
}

// adminHandlerFixture wires a real Service through CompleteBootstrap so the
// admin-panel handlers run against a genuine primary admin and session token,
// exactly as the real bootstrap flow produces them.
type adminHandlerFixture struct {
	svc        *Service
	admin      *Admin
	adminToken string
}

func newAdminHandlerFixture(t *testing.T) *adminHandlerFixture {
	t.Helper()
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	tok, err := svc.IssueSetupToken(ctx)
	if err != nil {
		t.Fatalf("IssueSetupToken: %v", err)
	}
	admin, session, aerr := svc.CompleteBootstrap(ctx, BootstrapInput{
		SetupToken: tok, Username: "root-admin", Email: "root-admin@example.com", Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("CompleteBootstrap: %v", aerr)
	}
	return &adminHandlerFixture{svc: svc, admin: admin, adminToken: session}
}

// TestHandleBootstrapStatusAndBootstrap covers the pre-admin discovery
// endpoint and the one-shot bootstrap handler: status flips from required to
// not-required after a successful bootstrap, a bad setup token is rejected,
// and the token can never be redeemed twice.
func TestHandleBootstrapStatusAndBootstrap(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()

	statusReq := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/server/bootstrap", nil)
		r.Header.Set("Accept", "application/json")
		return r
	}
	readBootstrapRequired := func(w *httptest.ResponseRecorder) bool {
		var body struct {
			OK   bool `json:"ok"`
			Data struct {
				BootstrapRequired bool `json:"bootstrap_required"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("Unmarshal: %v (body=%s)", err, w.Body.String())
		}
		return body.Data.BootstrapRequired
	}

	t.Run("status reports bootstrap required before any admin exists", func(t *testing.T) {
		w := httptest.NewRecorder()
		svc.HandleBootstrapStatus(w, statusReq())
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if !readBootstrapRequired(w) {
			t.Error("bootstrap_required = false before any admin was created")
		}
	})

	t.Run("bootstrap rejects an unknown setup token", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/bootstrap", "",
			`{"token":"not-a-real-token","username":"nope","email":"nope@example.com","password":"a-good-password"}`)
		svc.HandleBootstrap(w, r)
		if w.Code == http.StatusCreated {
			t.Error("HandleBootstrap succeeded with a bogus setup token")
		}
	})

	tok, err := svc.IssueSetupToken(ctx)
	if err != nil {
		t.Fatalf("IssueSetupToken: %v", err)
	}

	t.Run("bootstrap succeeds with a valid setup token and sets the admin cookie", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/bootstrap", "",
			`{"token":"`+tok+`","username":"handler-admin","email":"handler-admin@example.com","password":"a-good-password"}`)
		svc.HandleBootstrap(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
		found := false
		for _, c := range w.Result().Cookies() {
			if c.Name == AdminCookieName && c.Value != "" {
				found = true
			}
		}
		if !found {
			t.Error("HandleBootstrap did not set the admin session cookie")
		}
	})

	t.Run("status reports bootstrap no longer required", func(t *testing.T) {
		w := httptest.NewRecorder()
		svc.HandleBootstrapStatus(w, statusReq())
		if readBootstrapRequired(w) {
			t.Error("bootstrap_required = true after an admin was created")
		}
	})

	t.Run("the same setup token cannot be redeemed twice", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/bootstrap", "",
			`{"token":"`+tok+`","username":"second-admin","email":"second-admin@example.com","password":"a-good-password"}`)
		svc.HandleBootstrap(w, r)
		if w.Code == http.StatusCreated {
			t.Error("HandleBootstrap redeemed the same setup token twice")
		}
	})
}

// TestHandleAdminLoginLogoutMe drives the admin session lifecycle end to end:
// unauthenticated Me is rejected, a bad password is rejected, a correct login
// sets the cookie and Me succeeds, and logout is idempotent.
func TestHandleAdminLoginLogoutMe(t *testing.T) {
	f := newAdminHandlerFixture(t)

	t.Run("Me rejects an unauthenticated caller", func(t *testing.T) {
		w := httptest.NewRecorder()
		f.svc.HandleAdminMe(w, adminReq(http.MethodGet, "/api/v1/server/admin/me", "", ""))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("login rejects a wrong password", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/login", "",
			`{"login":"root-admin","password":"totally-wrong"}`)
		f.svc.HandleAdminLogin(w, r)
		if w.Code == http.StatusOK {
			t.Error("HandleAdminLogin succeeded with a wrong password")
		}
	})

	var loginToken string
	t.Run("login succeeds with the right credentials", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/login", "",
			`{"login":"root-admin","password":"a-good-password"}`)
		f.svc.HandleAdminLogin(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		for _, c := range w.Result().Cookies() {
			if c.Name == AdminCookieName {
				loginToken = c.Value
			}
		}
		if loginToken == "" {
			t.Fatal("HandleAdminLogin did not set the admin session cookie")
		}
	})

	t.Run("Me succeeds with the session from login, using RequireAdmin for real context wiring", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodGet, "/api/v1/server/admin/me", loginToken, "")
		f.svc.RequireAdmin(http.HandlerFunc(f.svc.HandleAdminMe)).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("logout clears the session and is idempotent", func(t *testing.T) {
		w1 := httptest.NewRecorder()
		f.svc.HandleAdminLogout(w1, adminReq(http.MethodPost, "/api/v1/server/admin/logout", loginToken, ""))
		if w1.Code != http.StatusOK {
			t.Fatalf("first logout status = %d, want 200", w1.Code)
		}
		w2 := httptest.NewRecorder()
		f.svc.HandleAdminLogout(w2, adminReq(http.MethodPost, "/api/v1/server/admin/logout", loginToken, ""))
		if w2.Code != http.StatusOK {
			t.Errorf("second logout status = %d, want 200 (idempotent)", w2.Code)
		}

		// The revoked session must no longer authenticate.
		w3 := httptest.NewRecorder()
		r3 := adminReq(http.MethodGet, "/api/v1/server/admin/me", loginToken, "")
		f.svc.RequireAdmin(http.HandlerFunc(f.svc.HandleAdminMe)).ServeHTTP(w3, r3)
		if w3.Code != http.StatusUnauthorized {
			t.Errorf("Me after logout status = %d, want 401", w3.Code)
		}
	})
}

// TestHandleAdminChangePassword covers the authenticated-only guard and the
// happy path, which must rotate the session (old cookie stops working).
func TestHandleAdminChangePassword(t *testing.T) {
	f := newAdminHandlerFixture(t)

	t.Run("rejects an unauthenticated caller", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/password", "",
			`{"current_password":"a-good-password","new_password":"a-new-good-password"}`)
		f.svc.HandleAdminChangePassword(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("rejects the wrong current password", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/password", f.adminToken,
			`{"current_password":"nope-not-it","new_password":"a-new-good-password"}`)
		f.svc.RequireAdmin(http.HandlerFunc(f.svc.HandleAdminChangePassword)).ServeHTTP(w, r)
		if w.Code == http.StatusOK {
			t.Error("HandleAdminChangePassword succeeded with a wrong current password")
		}
	})

	t.Run("succeeds and revokes the old session", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/password", f.adminToken,
			`{"current_password":"a-good-password","new_password":"a-new-good-password"}`)
		f.svc.RequireAdmin(http.HandlerFunc(f.svc.HandleAdminChangePassword)).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}

		w2 := httptest.NewRecorder()
		r2 := adminReq(http.MethodGet, "/api/v1/server/admin/me", f.adminToken, "")
		f.svc.RequireAdmin(http.HandlerFunc(f.svc.HandleAdminMe)).ServeHTTP(w2, r2)
		if w2.Code != http.StatusUnauthorized {
			t.Errorf("old session after password change status = %d, want 401", w2.Code)
		}
	})
}

// TestHandleAdminTOTPFlow drives begin -> confirm -> disable, all of which
// require an authenticated admin.
func TestHandleAdminTOTPFlow(t *testing.T) {
	f := newAdminHandlerFixture(t)

	t.Run("begin rejects an unauthenticated caller", func(t *testing.T) {
		w := httptest.NewRecorder()
		f.svc.HandleAdminBeginTOTP(w, adminReq(http.MethodPost, "/api/v1/server/admin/totp/begin", "", ""))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	var secret string
	t.Run("begin returns a provisioning secret", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/totp/begin", f.adminToken, "")
		f.svc.RequireAdmin(http.HandlerFunc(f.svc.HandleAdminBeginTOTP)).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var body struct {
			Data struct {
				Secret string `json:"secret"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		secret = body.Data.Secret
		if secret == "" {
			t.Fatal("HandleAdminBeginTOTP returned an empty secret")
		}
	})

	t.Run("confirm rejects a wrong code", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/totp/confirm", f.adminToken, `{"code":"000000"}`)
		f.svc.RequireAdmin(http.HandlerFunc(f.svc.HandleAdminConfirmTOTP)).ServeHTTP(w, r)
		if w.Code == http.StatusOK {
			t.Error("HandleAdminConfirmTOTP succeeded with a wrong code")
		}
	})

	t.Run("confirm succeeds with the real code", func(t *testing.T) {
		code, err := TOTPCode(secret, time.Now())
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/totp/confirm", f.adminToken, `{"code":"`+code+`"}`)
		f.svc.RequireAdmin(http.HandlerFunc(f.svc.HandleAdminConfirmTOTP)).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("disable rejects the wrong password", func(t *testing.T) {
		code, err := TOTPCode(secret, time.Now())
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/totp/disable", f.adminToken,
			`{"password":"nope","code":"`+code+`"}`)
		f.svc.RequireAdmin(http.HandlerFunc(f.svc.HandleAdminDisableTOTP)).ServeHTTP(w, r)
		if w.Code == http.StatusOK {
			t.Error("HandleAdminDisableTOTP succeeded with the wrong password")
		}
	})

	t.Run("disable succeeds with the right password and code", func(t *testing.T) {
		code, err := TOTPCode(secret, time.Now())
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/totp/disable", f.adminToken,
			`{"password":"a-good-password","code":"`+code+`"}`)
		f.svc.RequireAdmin(http.HandlerFunc(f.svc.HandleAdminDisableTOTP)).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})
}

// TestHandleAdminCreateAdmin covers the authenticated-only guard and the
// happy path for adding a second Server Admin.
func TestHandleAdminCreateAdmin(t *testing.T) {
	f := newAdminHandlerFixture(t)

	t.Run("rejects an unauthenticated caller", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/admins", "",
			`{"username":"second-admin","email":"second-admin@example.com","password":"a-good-password"}`)
		f.svc.HandleAdminCreateAdmin(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("owner can create a second admin", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/admins", f.adminToken,
			`{"username":"second-admin","email":"second-admin@example.com","password":"a-good-password"}`)
		f.svc.RequireAdmin(http.HandlerFunc(f.svc.HandleAdminCreateAdmin)).ServeHTTP(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("rejects a duplicate username", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/admins", f.adminToken,
			`{"username":"second-admin","email":"other@example.com","password":"a-good-password"}`)
		f.svc.RequireAdmin(http.HandlerFunc(f.svc.HandleAdminCreateAdmin)).ServeHTTP(w, r)
		if w.Code == http.StatusCreated {
			t.Error("HandleAdminCreateAdmin succeeded with a duplicate username")
		}
	})
}

// TestHandleAdminListUsersAndSetFlags covers paging plus the approve/suspend
// toggle on a real registered user.
func TestHandleAdminListUsersAndSetFlags(t *testing.T) {
	f := newAdminHandlerFixture(t)
	u := registerTestUser(t, f.svc, "flagtarget", "flagtarget@example.com")

	t.Run("list returns the registered user", func(t *testing.T) {
		w := httptest.NewRecorder()
		f.svc.HandleAdminListUsers(w, adminReq(http.MethodGet, "/api/v1/server/admin/users", "", ""))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var body struct {
			Data []PublicUser `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		found := false
		for _, pu := range body.Data {
			if pu.Username == "flagtarget" {
				found = true
			}
		}
		if !found {
			t.Errorf("list did not contain the registered user, got %+v", body.Data)
		}
	})

	t.Run("set-flags rejects an unauthenticated caller", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/users/x", "", `{"approved":true,"disabled":false}`)
		f.svc.HandleAdminSetUserFlags(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("owner suspends the user", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/users/x", f.adminToken, `{"approved":true,"disabled":true}`)
		r.SetPathValue("id", itoa64(u.ID))
		f.svc.RequireAdmin(http.HandlerFunc(f.svc.HandleAdminSetUserFlags)).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})
}

// TestHandleAdminSuspendOrg covers the authenticated-only guard and the
// suspend/reinstate toggle against a real org.
func TestHandleAdminSuspendOrg(t *testing.T) {
	f := newAdminHandlerFixture(t)
	owner := registerTestUser(t, f.svc, "orgowner-panelqa", "orgowner-panelqa@example.com")
	org, aerr := f.svc.CreateOrg(context.Background(), owner.ID, OrgInput{Slug: "panelqa-target-org", Name: "Admin Target Org"}, "")
	if aerr != nil {
		t.Fatalf("CreateOrg: %v", aerr)
	}

	t.Run("rejects an unauthenticated caller", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/orgs/x", "", `{"suspended":true}`)
		f.svc.HandleAdminSuspendOrg(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("owner suspends then reinstates the org", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/orgs/x", f.adminToken, `{"suspended":true}`)
		r.SetPathValue("id", itoa64(org.ID))
		f.svc.RequireAdmin(http.HandlerFunc(f.svc.HandleAdminSuspendOrg)).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("suspend status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}

		w2 := httptest.NewRecorder()
		r2 := adminReq(http.MethodPost, "/api/v1/server/admin/orgs/x", f.adminToken, `{"suspended":false}`)
		r2.SetPathValue("id", itoa64(org.ID))
		f.svc.RequireAdmin(http.HandlerFunc(f.svc.HandleAdminSuspendOrg)).ServeHTTP(w2, r2)
		if w2.Code != http.StatusOK {
			t.Fatalf("reinstate status = %d, want 200 (body=%s)", w2.Code, w2.Body.String())
		}
	})
}

// TestHandleAdminDomainActivateAndSuspend drives a domain through add -> verify
// -> admin-activate -> admin-suspend, reusing the fakeResolver pattern already
// established in service_domain_test.go / handler_api_domain_test.go.
func TestHandleAdminDomainActivateAndSuspend(t *testing.T) {
	svc := newTestDomainService(t, fakeResolver{}, nil)
	ctx := context.Background()
	tok, err := svc.IssueSetupToken(ctx)
	if err != nil {
		t.Fatalf("IssueSetupToken: %v", err)
	}
	_, adminToken, aerr := svc.CompleteBootstrap(ctx, BootstrapInput{
		SetupToken: tok, Username: "domain-admin", Email: "domain-admin@example.com", Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("CompleteBootstrap: %v", aerr)
	}
	user := registerTestUser(t, svc, "domainowner-panelqa", "domainowner-panelqa@example.com")
	owner := DomainOwner{Type: OwnerUser, ID: user.ID}

	d, aerr := svc.AddDomain(ctx, owner, user.ID, "admin-flow.example.com")
	if aerr != nil {
		t.Fatalf("AddDomain: %v", aerr)
	}

	t.Run("activate rejects an unauthenticated caller", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/domains/x/activate", "", "")
		svc.HandleAdminActivateDomain(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("activate rejects a domain that has not proven ownership yet", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/domains/x/activate", adminToken, "")
		r.SetPathValue("id", itoa64(d.ID))
		svc.RequireAdmin(http.HandlerFunc(svc.HandleAdminActivateDomain)).ServeHTTP(w, r)
		if w.Code == http.StatusOK {
			t.Error("HandleAdminActivateDomain succeeded before the domain was verified")
		}
	})

	// Prove ownership the same way handler_api_domain_test.go does: point the
	// fake resolver at the domain's own verification token.
	svc.resolver = fakeResolver{txt: []string{d.VerificationToken}}
	if _, aerr := svc.VerifyDomain(ctx, owner, user.ID, d.Domain); aerr != nil {
		t.Fatalf("VerifyDomain: %v", aerr)
	}

	t.Run("activate succeeds once verified", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/domains/x/activate", adminToken, "")
		r.SetPathValue("id", itoa64(d.ID))
		svc.RequireAdmin(http.HandlerFunc(svc.HandleAdminActivateDomain)).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("suspend rejects an unauthenticated caller", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/domains/x/suspend", "", `{"reason":"abuse report"}`)
		svc.HandleAdminSuspendDomain(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("admin suspends the active domain", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/domains/x/suspend", adminToken, `{"reason":"abuse report"}`)
		r.SetPathValue("id", itoa64(d.ID))
		svc.RequireAdmin(http.HandlerFunc(svc.HandleAdminSuspendDomain)).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})
}

// TestHandleAdminCreateInvite covers the authenticated-only guard, the
// default max-uses-of-1 behavior, and an explicit max-uses value.
func TestHandleAdminCreateInvite(t *testing.T) {
	f := newAdminHandlerFixture(t)

	t.Run("rejects an unauthenticated caller", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/invites", "", `{"email":"invitee@example.com"}`)
		f.svc.HandleAdminCreateInvite(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("defaults max_uses to 1 when omitted", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/invites", f.adminToken, `{"email":"invitee@example.com"}`)
		f.svc.RequireAdmin(http.HandlerFunc(f.svc.HandleAdminCreateInvite)).ServeHTTP(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
		var body struct {
			Data PublicInvite `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if body.Data.MaxUses != 1 {
			t.Errorf("MaxUses = %d, want 1 (default)", body.Data.MaxUses)
		}
		if body.Data.Code == "" {
			t.Error("HandleAdminCreateInvite returned an empty code")
		}
	})

	t.Run("honors an explicit max_uses", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := adminReq(http.MethodPost, "/api/v1/server/admin/invites", f.adminToken,
			`{"email":"invitee2@example.com","max_uses":5}`)
		f.svc.RequireAdmin(http.HandlerFunc(f.svc.HandleAdminCreateInvite)).ServeHTTP(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
		var body struct {
			Data PublicInvite `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if body.Data.MaxUses != 5 {
			t.Errorf("MaxUses = %d, want 5", body.Data.MaxUses)
		}
	})
}

// TestPageParams covers the bounded paging helper directly: defaults, clamping
// of an oversized limit, negative/non-numeric inputs, and a valid offset.
func TestPageParams(t *testing.T) {
	cases := []struct {
		name       string
		query      string
		wantLimit  int
		wantOffset int
	}{
		{"defaults with no query", "", 50, 0},
		{"explicit limit and offset", "limit=10&offset=20", 10, 20},
		{"limit above 200 is clamped", "limit=9999", 200, 0},
		{"negative limit falls back to default", "limit=-5", 50, 0},
		{"non-numeric limit falls back to default", "limit=bogus", 50, 0},
		{"negative offset falls back to zero", "offset=-5", 50, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/x?"+c.query, nil)
			limit, offset := pageParams(r)
			if limit != c.wantLimit || offset != c.wantOffset {
				t.Errorf("pageParams(%q) = (%d, %d), want (%d, %d)", c.query, limit, offset, c.wantLimit, c.wantOffset)
			}
		})
	}
}

// itoa64 formats an int64 for SetPathValue, matching what pathInt parses back.
func itoa64(v int64) string {
	return strconv.FormatInt(v, 10)
}
