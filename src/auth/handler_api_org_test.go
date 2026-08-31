package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// orgTestFixture wires a real Service with an owner + member + outsider and
// a live organization, so the handler tests below drive the real
// RequireUser -> RequireOrgRole auth chain rather than bypassing it.
type orgTestFixture struct {
	svc                                   *Service
	owner, member, outsider               *User
	ownerToken, memberToken, outsiderToken string
	org                                   *Org
}

func newOrgTestFixture(t *testing.T) *orgTestFixture {
	t.Helper()
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()

	owner := registerTestUser(t, svc, "orghandlerowner", "orghandlerowner@example.com")
	member := registerTestUser(t, svc, "orghandlermember", "orghandlermember@example.com")
	outsider := registerTestUser(t, svc, "orghandleroutsider", "orghandleroutsider@example.com")

	org, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Slug: "handler-org", Name: "Handler Org"}, "")
	if aerr != nil {
		t.Fatalf("CreateOrg: %v", aerr)
	}
	if aerr := svc.AddOrgMember(ctx, org.ID, owner.ID, member.Username, OrgRoleMember); aerr != nil {
		t.Fatalf("AddOrgMember: %v", aerr)
	}

	_, ownerToken, aerr := svc.Login(ctx, LoginInput{Identifier: owner.Username, Password: "a-good-password"})
	if aerr != nil {
		t.Fatalf("Login owner: %v", aerr)
	}
	_, memberToken, aerr := svc.Login(ctx, LoginInput{Identifier: member.Username, Password: "a-good-password"})
	if aerr != nil {
		t.Fatalf("Login member: %v", aerr)
	}
	_, outsiderToken, aerr := svc.Login(ctx, LoginInput{Identifier: outsider.Username, Password: "a-good-password"})
	if aerr != nil {
		t.Fatalf("Login outsider: %v", aerr)
	}

	return &orgTestFixture{
		svc: svc, owner: owner, member: member, outsider: outsider,
		ownerToken: ownerToken, memberToken: memberToken, outsiderToken: outsiderToken,
		org: org,
	}
}

func orgReq(method, path, sessionToken, body string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.Header.Set("Accept", "application/json")
	if sessionToken != "" {
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
	}
	return r
}

// TestHandleListAndCreateOrg covers HandleListOrgs and HandleCreateOrg,
// including the unauthenticated-rejected -> authenticated-accepted flow and
// a validation failure on create.
func TestHandleListAndCreateOrg(t *testing.T) {
	f := newOrgTestFixture(t)

	t.Run("list rejects unauthenticated", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodGet, "/api/v1/orgs", "", "")
		f.svc.RequireUser(http.HandlerFunc(f.svc.HandleListOrgs)).ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("list returns the caller's orgs", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodGet, "/api/v1/orgs", f.ownerToken, "")
		f.svc.RequireUser(http.HandlerFunc(f.svc.HandleListOrgs)).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("create rejects a malformed slug", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodPost, "/api/v1/orgs", f.ownerToken, `{"slug":"not a valid slug!","name":"Bad Slug Org"}`)
		f.svc.RequireUser(http.HandlerFunc(f.svc.HandleCreateOrg)).ServeHTTP(w, r)
		if w.Code < 400 {
			t.Errorf("status = %d, want 4xx for a malformed slug", w.Code)
		}
	})

	t.Run("create succeeds and seats the caller as owner", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodPost, "/api/v1/orgs", f.ownerToken, `{"slug":"second-org","name":"Second Org"}`)
		f.svc.RequireUser(http.HandlerFunc(f.svc.HandleCreateOrg)).ServeHTTP(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
	})
}

// TestHandleGetAndUpdateOrg covers HandleGetOrg (public, no RequireOrgRole)
// and HandleUpdateOrg (behind RequireOrgRole).
func TestHandleGetAndUpdateOrg(t *testing.T) {
	f := newOrgTestFixture(t)

	t.Run("get works for an anonymous viewer", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodGet, "/api/v1/orgs/handler-org", "", "")
		r.SetPathValue("slug", "handler-org")
		f.svc.HandleGetOrg(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("get 404s an unknown org", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodGet, "/api/v1/orgs/nope", "", "")
		r.SetPathValue("slug", "nope")
		f.svc.HandleGetOrg(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	update := Chain(http.HandlerFunc(f.svc.HandleUpdateOrg), f.svc.RequireUser, f.svc.RequireOrgRole(OrgRoleAdmin))

	t.Run("update rejects a non-member with the anti-enumeration 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodPatch, "/api/v1/orgs/handler-org", f.outsiderToken, `{"name":"Hijacked"}`)
		r.SetPathValue("slug", "handler-org")
		update.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	t.Run("update rejects a plain member (needs admin+)", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodPatch, "/api/v1/orgs/handler-org", f.memberToken, `{"name":"Member Edit"}`)
		r.SetPathValue("slug", "handler-org")
		update.ServeHTTP(w, r)
		if w.Code == http.StatusOK {
			t.Errorf("status = 200, want a member-role update to be rejected")
		}
	})

	t.Run("owner can update the org", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodPatch, "/api/v1/orgs/handler-org", f.ownerToken, `{"name":"Renamed Org"}`)
		r.SetPathValue("slug", "handler-org")
		update.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})
}

// TestHandleMembersFlow covers list/add/set-role/remove, including the
// self-removal-vs-managing-role authorization branch in HandleRemoveMember.
func TestHandleMembersFlow(t *testing.T) {
	f := newOrgTestFixture(t)

	list := Chain(http.HandlerFunc(f.svc.HandleListMembers), f.svc.RequireUser, f.svc.RequireOrgRole(OrgRoleMember))
	add := Chain(http.HandlerFunc(f.svc.HandleAddMember), f.svc.RequireUser, f.svc.RequireOrgRole(OrgRoleAdmin))
	setRole := Chain(http.HandlerFunc(f.svc.HandleSetMemberRole), f.svc.RequireUser, f.svc.RequireOrgRole(OrgRoleAdmin))
	remove := Chain(http.HandlerFunc(f.svc.HandleRemoveMember), f.svc.RequireUser, f.svc.RequireOrgRole(OrgRoleMember))

	t.Run("list members as a member", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodGet, "/api/v1/orgs/handler-org/members", f.memberToken, "")
		r.SetPathValue("slug", "handler-org")
		list.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("outsider cannot add a member", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodPost, "/api/v1/orgs/handler-org/members", f.outsiderToken, `{"username":"orghandleroutsider","role":"member"}`)
		r.SetPathValue("slug", "handler-org")
		add.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404 (anti-enumeration)", w.Code)
		}
	})

	t.Run("owner adds the outsider as a member", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodPost, "/api/v1/orgs/handler-org/members", f.ownerToken, `{"username":"orghandleroutsider","role":"member"}`)
		r.SetPathValue("slug", "handler-org")
		add.ServeHTTP(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("owner promotes the member to admin", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodPatch, "/api/v1/orgs/handler-org/members/orghandlermember/role", f.ownerToken, `{"role":"admin"}`)
		r.SetPathValue("slug", "handler-org")
		r.SetPathValue("username", "orghandlermember")
		setRole.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("a member may remove themselves", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodDelete, "/api/v1/orgs/handler-org/members/orghandleroutsider", f.outsiderToken, "")
		r.SetPathValue("slug", "handler-org")
		r.SetPathValue("username", "orghandleroutsider")
		remove.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("a plain member cannot remove someone else", func(t *testing.T) {
		// Re-seat an outsider2 as a plain member to be the removal target.
		another := registerTestUser(t, f.svc, "orghandlerother", "orghandlerother@example.com")
		if aerr := f.svc.AddOrgMember(context.Background(), f.org.ID, f.owner.ID, another.Username, OrgRoleMember); aerr != nil {
			t.Fatalf("AddOrgMember: %v", aerr)
		}
		w := httptest.NewRecorder()
		// The now-admin f.member tries to remove `another` — this should be
		// allowed since the member was promoted to admin above. To test the
		// forbidden branch, use a fresh plain member instead.
		plain := registerTestUser(t, f.svc, "orghandlerplain", "orghandlerplain@example.com")
		if aerr := f.svc.AddOrgMember(context.Background(), f.org.ID, f.owner.ID, plain.Username, OrgRoleMember); aerr != nil {
			t.Fatalf("AddOrgMember: %v", aerr)
		}
		_, plainToken, aerr := f.svc.Login(context.Background(), LoginInput{Identifier: plain.Username, Password: "a-good-password"})
		if aerr != nil {
			t.Fatalf("Login plain: %v", aerr)
		}
		r := orgReq(http.MethodDelete, "/api/v1/orgs/handler-org/members/orghandlerother", plainToken, "")
		r.SetPathValue("slug", "handler-org")
		r.SetPathValue("username", "orghandlerother")
		remove.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403 (plain member removing someone else)", w.Code)
		}
	})

	t.Run("remove 404s an unknown username", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodDelete, "/api/v1/orgs/handler-org/members/does-not-exist", f.ownerToken, "")
		r.SetPathValue("slug", "handler-org")
		r.SetPathValue("username", "does-not-exist")
		remove.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})
}

// TestHandleTransferOrg covers HandleTransferOrg's happy path and its
// unknown-target-username failure.
func TestHandleTransferOrg(t *testing.T) {
	f := newOrgTestFixture(t)
	transfer := Chain(http.HandlerFunc(f.svc.HandleTransferOrg), f.svc.RequireUser, f.svc.RequireOrgRole(OrgRoleOwner))

	t.Run("rejects an unknown target username", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodPost, "/api/v1/orgs/handler-org/transfer", f.ownerToken, `{"username":"does-not-exist"}`)
		r.SetPathValue("slug", "handler-org")
		transfer.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	t.Run("owner transfers ownership to the member", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodPost, "/api/v1/orgs/handler-org/transfer", f.ownerToken, `{"username":"orghandlermember"}`)
		r.SetPathValue("slug", "handler-org")
		transfer.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})
}

// TestHandleInvitesFlow covers list/create/revoke/accept for org invites.
func TestHandleInvitesFlow(t *testing.T) {
	f := newOrgTestFixture(t)

	list := Chain(http.HandlerFunc(f.svc.HandleListInvites), f.svc.RequireUser, f.svc.RequireOrgRole(OrgRoleAdmin))
	create := Chain(http.HandlerFunc(f.svc.HandleCreateInvite), f.svc.RequireUser, f.svc.RequireOrgRole(OrgRoleAdmin))
	revoke := Chain(http.HandlerFunc(f.svc.HandleRevokeInvite), f.svc.RequireUser, f.svc.RequireOrgRole(OrgRoleAdmin))

	t.Run("list starts empty", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodGet, "/api/v1/orgs/handler-org/invites", f.ownerToken, "")
		r.SetPathValue("slug", "handler-org")
		list.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	var code string
	t.Run("create issues an invite code", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodPost, "/api/v1/orgs/handler-org/invites", f.ownerToken, `{"role":"member"}`)
		r.SetPathValue("slug", "handler-org")
		create.ServeHTTP(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
		var pub PublicInvite
		decodeOK(t, w, &pub)
		if pub.Code == "" {
			t.Fatal("HandleCreateInvite returned an empty code")
		}
		code = pub.Code
	})

	t.Run("a stranger can accept the invite code", func(t *testing.T) {
		stranger := registerTestUser(t, f.svc, "orginvitee", "orginvitee@example.com")
		_, strangerToken, aerr := f.svc.Login(context.Background(), LoginInput{Identifier: stranger.Username, Password: "a-good-password"})
		if aerr != nil {
			t.Fatalf("Login stranger: %v", aerr)
		}
		w := httptest.NewRecorder()
		r := orgReq(http.MethodPost, "/api/v1/orgs/accept?code="+code, strangerToken, "")
		f.svc.RequireUser(http.HandlerFunc(f.svc.HandleAcceptInvite)).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("accept rejects an unauthenticated caller", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodPost, "/api/v1/orgs/accept?code=bogus", "", "")
		f.svc.RequireUser(http.HandlerFunc(f.svc.HandleAcceptInvite)).ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("create another invite and revoke it", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodPost, "/api/v1/orgs/handler-org/invites", f.ownerToken, `{"role":"member"}`)
		r.SetPathValue("slug", "handler-org")
		create.ServeHTTP(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
		rows, aerr := f.svc.ListOrgInvites(context.Background(), f.org.ID)
		if aerr != nil {
			t.Fatalf("ListOrgInvites: %v", aerr)
		}
		if len(rows) == 0 {
			t.Fatal("no invites to revoke")
		}
		id := rows[len(rows)-1].ID

		w2 := httptest.NewRecorder()
		r2 := orgReq(http.MethodDelete, "/api/v1/orgs/handler-org/invites/x", f.ownerToken, "")
		r2.SetPathValue("slug", "handler-org")
		r2.SetPathValue("id", itoaHelper(id))
		revoke.ServeHTTP(w2, r2)
		if w2.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w2.Code, w2.Body.String())
		}
	})
}

// TestHandleOrgTokensFlow covers list/create/revoke for org API tokens,
// including revoke-twice idempotency.
func TestHandleOrgTokensFlow(t *testing.T) {
	f := newOrgTestFixture(t)

	list := Chain(http.HandlerFunc(f.svc.HandleListOrgTokens), f.svc.RequireUser, f.svc.RequireOrgRole(OrgRoleAdmin))
	create := Chain(http.HandlerFunc(f.svc.HandleCreateOrgToken), f.svc.RequireUser, f.svc.RequireOrgRole(OrgRoleAdmin))
	revoke := Chain(http.HandlerFunc(f.svc.HandleRevokeOrgToken), f.svc.RequireUser, f.svc.RequireOrgRole(OrgRoleAdmin))

	t.Run("list starts empty", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodGet, "/api/v1/orgs/handler-org/tokens", f.ownerToken, "")
		r.SetPathValue("slug", "handler-org")
		list.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	var tokenID int64
	t.Run("create mints a token", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodPost, "/api/v1/orgs/handler-org/tokens", f.ownerToken, `{"name":"ci-token","scopes":["profile:read"]}`)
		r.SetPathValue("slug", "handler-org")
		create.ServeHTTP(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
		var pub struct {
			ID    int64  `json:"id"`
			Token string `json:"token"`
		}
		decodeOK(t, w, &pub)
		if pub.Token == "" {
			t.Fatal("HandleCreateOrgToken returned no plaintext token")
		}
		tokenID = pub.ID
	})

	t.Run("revoke succeeds", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodDelete, "/api/v1/orgs/handler-org/tokens/x", f.ownerToken, "")
		r.SetPathValue("slug", "handler-org")
		r.SetPathValue("id", itoaHelper(tokenID))
		revoke.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("revoking the same token twice is idempotent", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodDelete, "/api/v1/orgs/handler-org/tokens/x", f.ownerToken, "")
		r.SetPathValue("slug", "handler-org")
		r.SetPathValue("id", itoaHelper(tokenID))
		revoke.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 (idempotent repeat revoke)", w.Code)
		}
	})
}

// TestHandleDeleteOrg covers the typed-confirmation delete flow.
func TestHandleDeleteOrg(t *testing.T) {
	f := newOrgTestFixture(t)
	del := Chain(http.HandlerFunc(f.svc.HandleDeleteOrg), f.svc.RequireUser, f.svc.RequireOrgRole(OrgRoleOwner))

	t.Run("rejects a missing/incorrect confirmation", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodPost, "/api/v1/orgs/handler-org/delete", f.ownerToken, `{"confirm":"nope"}`)
		r.SetPathValue("slug", "handler-org")
		del.ServeHTTP(w, r)
		if w.Code < 400 {
			t.Errorf("status = %d, want 4xx for an incorrect confirmation", w.Code)
		}
	})

	t.Run("owner deletes with the correct confirmation", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := orgReq(http.MethodPost, "/api/v1/orgs/handler-org/delete", f.ownerToken, `{"confirm":"handler-org"}`)
		r.SetPathValue("slug", "handler-org")
		del.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})
}
