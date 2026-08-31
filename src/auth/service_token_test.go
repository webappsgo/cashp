package auth

import (
	"context"
	"testing"
	"time"
)

func TestCreateUserTokenLifecycle(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	user := registerTestUser(t, svc, "tokenuser", "tokenuser@example.com")

	tok, aerr := svc.CreateUserToken(ctx, user.ID, TokenInput{
		Name: "ci-token", Scopes: []string{"profile:read", "profile:write", "profile:read"},
	})
	if aerr != nil {
		t.Fatalf("CreateUserToken: %v", aerr)
	}
	if tok.ID == 0 {
		t.Fatal("CreateUserToken left PublicToken.ID at 0 — id regression")
	}
	if tok.Token == "" {
		t.Fatal("CreateUserToken returned an empty plaintext token")
	}
	if len(tok.Scopes) != 2 {
		t.Errorf("CreateUserToken scopes = %v, want deduped to 2 entries", tok.Scopes)
	}

	tokens, aerr := svc.ListUserTokens(ctx, user.ID)
	if aerr != nil {
		t.Fatalf("ListUserTokens: %v", aerr)
	}
	if len(tokens) != 1 {
		t.Fatalf("ListUserTokens len = %d, want 1", len(tokens))
	}
	if tokens[0].Token != "" {
		t.Error("ListUserTokens exposed the plaintext token, want empty after creation")
	}

	if aerr := svc.RevokeUserToken(ctx, user.ID, tok.ID); aerr != nil {
		t.Fatalf("RevokeUserToken: %v", aerr)
	}
	// Idempotent: revoking an already-revoked token must not error.
	if aerr := svc.RevokeUserToken(ctx, user.ID, tok.ID); aerr != nil {
		t.Errorf("second RevokeUserToken errored: %v", aerr)
	}
}

func TestCreateUserTokenValidation(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	user := registerTestUser(t, svc, "tokenvaliduser", "tokenvaliduser@example.com")

	cases := []struct {
		name  string
		input TokenInput
	}{
		{"empty name", TokenInput{Name: ""}},
		{"name too long", TokenInput{Name: string(make([]byte, 200))}},
		{"unknown scope", TokenInput{Name: "bad-scope", Scopes: []string{"not-a-real-scope"}}},
		{"expiry in the past", TokenInput{Name: "expired", ExpiresAt: time.Now().Add(-time.Hour).Unix()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, aerr := svc.CreateUserToken(ctx, user.ID, tc.input); aerr == nil {
				t.Errorf("CreateUserToken(%+v) succeeded, want validation failure", tc.input)
			}
		})
	}
}

func TestCreateUserTokenEnforcesQuota(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	user := registerTestUser(t, svc, "tokenquotauser", "tokenquotauser@example.com")

	for i := 0; i < MaxTokensPerOwner; i++ {
		if _, aerr := svc.CreateUserToken(ctx, user.ID, TokenInput{Name: "tok", Scopes: []string{"profile:read"}}); aerr != nil {
			t.Fatalf("CreateUserToken #%d: %v", i, aerr)
		}
	}
	if _, aerr := svc.CreateUserToken(ctx, user.ID, TokenInput{Name: "one-too-many", Scopes: []string{"profile:read"}}); aerr == nil {
		t.Error("CreateUserToken exceeded MaxTokensPerOwner, want quota failure")
	}
}

func TestOrgTokenLifecycleAndOwnerScoping(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	owner := registerTestUser(t, svc, "orgtokenowner", "orgtokenowner@example.com")
	other := registerTestUser(t, svc, "orgtokenother", "orgtokenother@example.com")
	org, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Slug: "token-org", Name: "Token Org"}, "")
	if aerr != nil {
		t.Fatalf("CreateOrg: %v", aerr)
	}

	tok, aerr := svc.CreateOrgToken(ctx, org.ID, owner.ID, TokenInput{Name: "org-token", Scopes: []string{"profile:read"}})
	if aerr != nil {
		t.Fatalf("CreateOrgToken: %v", aerr)
	}
	if tok.ID == 0 {
		t.Fatal("CreateOrgToken left PublicToken.ID at 0 — id regression")
	}
	if tok.Token == "" {
		t.Fatal("CreateOrgToken returned an empty plaintext token")
	}

	tokens, aerr := svc.ListOrgTokens(ctx, org.ID)
	if aerr != nil {
		t.Fatalf("ListOrgTokens: %v", aerr)
	}
	if len(tokens) != 1 {
		t.Fatalf("ListOrgTokens len = %d, want 1", len(tokens))
	}

	// RevokeUserToken is scoped to the user_tokens table by SQL WHERE clause, so
	// calling it with an org token's ID against an unrelated user is a silent
	// no-op (zero rows affected, no error) rather than a hard failure — org
	// tokens live in a separate org_tokens table entirely.
	if aerr := svc.RevokeUserToken(ctx, other.ID, tok.ID); aerr != nil {
		t.Errorf("RevokeUserToken cross-table no-op unexpectedly errored: %v", aerr)
	}
	remaining, aerr := svc.ListOrgTokens(ctx, org.ID)
	if aerr != nil {
		t.Fatalf("ListOrgTokens after cross-owner revoke attempt: %v", aerr)
	}
	if len(remaining) != 1 {
		t.Error("cross-owner RevokeUserToken call unexpectedly removed the org token")
	}

	if aerr := svc.RevokeOrgToken(ctx, org.ID, owner.ID, tok.ID); aerr != nil {
		t.Fatalf("RevokeOrgToken: %v", aerr)
	}
}

func TestSessionListAndRevoke(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	user := registerTestUser(t, svc, "sessionlistuser", "sessionlistuser@example.com")

	_, token1, aerr := svc.Login(ctx, LoginInput{Identifier: "sessionlistuser", Password: "a-good-password"})
	if aerr != nil {
		t.Fatalf("Login #1: %v", aerr)
	}
	_, sess1, aerr := svc.ResolveSession(ctx, token1)
	if aerr != nil {
		t.Fatalf("ResolveSession #1: %v", aerr)
	}
	_, token2, aerr := svc.Login(ctx, LoginInput{Identifier: "sessionlistuser", Password: "a-good-password"})
	if aerr != nil {
		t.Fatalf("Login #2: %v", aerr)
	}
	if _, _, aerr := svc.ResolveSession(ctx, token2); aerr != nil {
		t.Fatalf("ResolveSession #2: %v", aerr)
	}

	// Register itself issues a session, so two explicit Logins add up to three.
	sessions, aerr := svc.ListSessions(ctx, user.ID)
	if aerr != nil {
		t.Fatalf("ListSessions: %v", aerr)
	}
	if len(sessions) != 3 {
		t.Fatalf("ListSessions len = %d, want 3", len(sessions))
	}

	if aerr := svc.RevokeSession(ctx, user.ID, sess1.ID); aerr != nil {
		t.Fatalf("RevokeSession: %v", aerr)
	}
	// Idempotent double revoke.
	if aerr := svc.RevokeSession(ctx, user.ID, sess1.ID); aerr != nil {
		t.Errorf("second RevokeSession errored: %v", aerr)
	}

	sessions, aerr = svc.ListSessions(ctx, user.ID)
	if aerr != nil {
		t.Fatalf("ListSessions after revoke: %v", aerr)
	}
	if len(sessions) != 2 {
		t.Errorf("ListSessions len = %d after single revoke, want 2", len(sessions))
	}

	if aerr := svc.RevokeAllSessions(ctx, user.ID); aerr != nil {
		t.Fatalf("RevokeAllSessions: %v", aerr)
	}
	sessions, aerr = svc.ListSessions(ctx, user.ID)
	if aerr != nil {
		t.Fatalf("ListSessions after RevokeAllSessions: %v", aerr)
	}
	if len(sessions) != 0 {
		t.Errorf("ListSessions len = %d after RevokeAllSessions, want 0", len(sessions))
	}
}
