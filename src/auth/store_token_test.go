package auth

import (
	"context"
	"testing"
	"time"
)

func TestCreateUserTokenAndLookup(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "tokenuser", "tokenuser@example.com")

	tok := &Token{
		OwnerID:     u.ID,
		Name:        "CI token",
		TokenHash:   "usertokenhash",
		TokenPrefix: "usr_abcd",
		Scopes:      "read,write",
	}
	id, err := store.CreateUserToken(ctx, tok)
	if err != nil {
		t.Fatalf("CreateUserToken: %v", err)
	}
	if id == 0 {
		t.Fatal("CreateUserToken returned id 0")
	}
	if tok.ID == 0 {
		t.Fatal("CreateUserToken left Token.ID at 0")
	}

	found, err := store.TokenByHash(ctx, "usertokenhash")
	if err != nil {
		t.Fatalf("TokenByHash: %v", err)
	}
	if found.OwnerType != OwnerUser {
		t.Errorf("TokenByHash OwnerType = %q, want %q", found.OwnerType, OwnerUser)
	}
	if found.OwnerID != u.ID {
		t.Errorf("TokenByHash OwnerID = %d, want %d", found.OwnerID, u.ID)
	}
	if found.Revoked {
		t.Error("a freshly created token must not be revoked")
	}
}

func TestCreateOrgTokenAndLookup(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "orgtokenowner", "orgtokenowner@example.com")
	org := newTestOrg(t, store, "orgtokenco", "Org Token Co", owner)

	tok := &Token{
		OwnerID:     org.ID,
		Name:        "deploy token",
		TokenHash:   "orgtokenhash",
		TokenPrefix: "org_abcd",
		Scopes:      "read",
	}
	id, err := store.CreateOrgToken(ctx, tok, owner.ID)
	if err != nil {
		t.Fatalf("CreateOrgToken: %v", err)
	}
	if id == 0 {
		t.Fatal("CreateOrgToken returned id 0")
	}
	if tok.ID == 0 {
		t.Fatal("CreateOrgToken left Token.ID at 0")
	}

	found, err := store.TokenByHash(ctx, "orgtokenhash")
	if err != nil {
		t.Fatalf("TokenByHash: %v", err)
	}
	if found.OwnerType != OwnerOrg {
		t.Errorf("TokenByHash OwnerType = %q, want %q", found.OwnerType, OwnerOrg)
	}
	if found.OwnerID != org.ID {
		t.Errorf("TokenByHash OwnerID = %d, want %d", found.OwnerID, org.ID)
	}
}

func TestTokenByHashNotFound(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	if _, err := store.TokenByHash(context.Background(), "unknown-hash"); err == nil {
		t.Error("TokenByHash(missing) = nil error, want not-found error")
	}
}

func TestListUserTokensAndListOrgTokens(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "listtokenuser", "listtokenuser@example.com")
	owner := newTestUser(t, store, "listtokenowner", "listtokenowner@example.com")
	org := newTestOrg(t, store, "listtokenco", "List Token Co", owner)

	for _, hash := range []string{"lu1", "lu2"} {
		if _, err := store.CreateUserToken(ctx, &Token{OwnerID: u.ID, Name: "n", TokenHash: hash, TokenPrefix: "usr_x"}); err != nil {
			t.Fatalf("CreateUserToken(%s): %v", hash, err)
		}
	}
	if _, err := store.CreateOrgToken(ctx, &Token{OwnerID: org.ID, Name: "n", TokenHash: "lo1", TokenPrefix: "org_x"}, owner.ID); err != nil {
		t.Fatalf("CreateOrgToken: %v", err)
	}

	userTokens, err := store.ListUserTokens(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListUserTokens: %v", err)
	}
	if len(userTokens) != 2 {
		t.Errorf("ListUserTokens len = %d, want 2", len(userTokens))
	}
	for _, tok := range userTokens {
		if tok.OwnerType != OwnerUser {
			t.Errorf("ListUserTokens OwnerType = %q, want %q", tok.OwnerType, OwnerUser)
		}
	}

	orgTokens, err := store.ListOrgTokens(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListOrgTokens: %v", err)
	}
	if len(orgTokens) != 1 {
		t.Errorf("ListOrgTokens len = %d, want 1", len(orgTokens))
	}
	if orgTokens[0].OwnerType != OwnerOrg {
		t.Errorf("ListOrgTokens OwnerType = %q, want %q", orgTokens[0].OwnerType, OwnerOrg)
	}
}

func TestListUserTokensEmpty(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "notokensuser", "notokensuser@example.com")
	list, err := store.ListUserTokens(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListUserTokens(empty): %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ListUserTokens(empty) len = %d, want 0", len(list))
	}
}

func TestTouchToken(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "touchtokenuser", "touchtokenuser@example.com")
	tok := &Token{OwnerID: u.ID, Name: "n", TokenHash: "touchhash", TokenPrefix: "usr_x"}
	if _, err := store.CreateUserToken(ctx, tok); err != nil {
		t.Fatalf("CreateUserToken: %v", err)
	}

	if err := store.TouchToken(ctx, OwnerUser, tok.ID); err != nil {
		t.Fatalf("TouchToken: %v", err)
	}
	found, err := store.TokenByHash(ctx, "touchhash")
	if err != nil {
		t.Fatalf("TokenByHash: %v", err)
	}
	if found.LastUsedAt == 0 {
		t.Error("TouchToken did not set last_used_at")
	}
}

func TestRevokeUserTokenScoped(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "revoketokenowner", "revoketokenowner@example.com")
	attacker := newTestUser(t, store, "revoketokenattacker", "revoketokenattacker@example.com")
	tok := &Token{OwnerID: owner.ID, Name: "n", TokenHash: "revokehash", TokenPrefix: "usr_x"}
	if _, err := store.CreateUserToken(ctx, tok); err != nil {
		t.Fatalf("CreateUserToken: %v", err)
	}

	// Cross-user revoke must be a no-op.
	if err := store.RevokeUserToken(ctx, attacker.ID, tok.ID); err != nil {
		t.Fatalf("RevokeUserToken(wrong user): %v", err)
	}
	found, err := store.TokenByHash(ctx, "revokehash")
	if err != nil {
		t.Fatalf("TokenByHash: %v", err)
	}
	if found.Revoked {
		t.Error("cross-user RevokeUserToken revoked a token it did not own")
	}

	if err := store.RevokeUserToken(ctx, owner.ID, tok.ID); err != nil {
		t.Fatalf("RevokeUserToken(owner): %v", err)
	}
	found, err = store.TokenByHash(ctx, "revokehash")
	if err != nil {
		t.Fatalf("TokenByHash: %v", err)
	}
	if !found.Revoked {
		t.Error("token not revoked after owner-scoped RevokeUserToken")
	}

	// Idempotency: revoking twice must not error.
	if err := store.RevokeUserToken(ctx, owner.ID, tok.ID); err != nil {
		t.Errorf("RevokeUserToken (already revoked): %v", err)
	}
}

func TestRevokeOrgTokenScoped(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "revokeorgtokowner", "revokeorgtokowner@example.com")
	org := newTestOrg(t, store, "revokeorgtokco", "Revoke Org Token Co", owner)
	otherOwner := newTestUser(t, store, "revokeorgtokother", "revokeorgtokother@example.com")
	otherOrg := newTestOrg(t, store, "revokeorgtokother2", "Other Org", otherOwner)

	tok := &Token{OwnerID: org.ID, Name: "n", TokenHash: "orgrevokehash", TokenPrefix: "org_x"}
	if _, err := store.CreateOrgToken(ctx, tok, owner.ID); err != nil {
		t.Fatalf("CreateOrgToken: %v", err)
	}

	// Cross-org revoke must be a no-op.
	if err := store.RevokeOrgToken(ctx, otherOrg.ID, tok.ID); err != nil {
		t.Fatalf("RevokeOrgToken(wrong org): %v", err)
	}
	found, err := store.TokenByHash(ctx, "orgrevokehash")
	if err != nil {
		t.Fatalf("TokenByHash: %v", err)
	}
	if found.Revoked {
		t.Error("cross-org RevokeOrgToken revoked a token it did not own")
	}

	if err := store.RevokeOrgToken(ctx, org.ID, tok.ID); err != nil {
		t.Fatalf("RevokeOrgToken(owner org): %v", err)
	}
	found, err = store.TokenByHash(ctx, "orgrevokehash")
	if err != nil {
		t.Fatalf("TokenByHash: %v", err)
	}
	if !found.Revoked {
		t.Error("token not revoked after org-scoped RevokeOrgToken")
	}
}

func TestPurgeExpiredTokens(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "purgetokenuser", "purgetokenuser@example.com")
	owner := newTestUser(t, store, "purgetokenowner", "purgetokenowner@example.com")
	org := newTestOrg(t, store, "purgetokenco", "Purge Token Co", owner)

	past := time.Now().Add(-time.Hour).Unix()
	future := time.Now().Add(time.Hour).Unix()

	if _, err := store.CreateUserToken(ctx, &Token{OwnerID: u.ID, Name: "expired", TokenHash: "puexp", TokenPrefix: "usr_x", ExpiresAt: past}); err != nil {
		t.Fatalf("CreateUserToken(expired): %v", err)
	}
	live := &Token{OwnerID: u.ID, Name: "live", TokenHash: "pulive", TokenPrefix: "usr_x", ExpiresAt: future}
	if _, err := store.CreateUserToken(ctx, live); err != nil {
		t.Fatalf("CreateUserToken(live): %v", err)
	}
	revoked := &Token{OwnerID: u.ID, Name: "revoked", TokenHash: "purev", TokenPrefix: "usr_x"}
	if _, err := store.CreateUserToken(ctx, revoked); err != nil {
		t.Fatalf("CreateUserToken(to-revoke): %v", err)
	}
	if err := store.RevokeUserToken(ctx, u.ID, revoked.ID); err != nil {
		t.Fatalf("RevokeUserToken: %v", err)
	}
	if _, err := store.CreateOrgToken(ctx, &Token{OwnerID: org.ID, Name: "expired-org", TokenHash: "puexporg", TokenPrefix: "org_x", ExpiresAt: past}, owner.ID); err != nil {
		t.Fatalf("CreateOrgToken(expired): %v", err)
	}

	n, err := store.PurgeExpiredTokens(ctx)
	if err != nil {
		t.Fatalf("PurgeExpiredTokens: %v", err)
	}
	if n != 3 {
		t.Errorf("PurgeExpiredTokens purged %d rows, want 3 (expired user + revoked user + expired org)", n)
	}

	if _, err := store.TokenByHash(ctx, "pulive"); err != nil {
		t.Error("PurgeExpiredTokens removed a live, non-revoked token")
	}

	// Idempotency: purging again must not error and must purge nothing further.
	n2, err := store.PurgeExpiredTokens(ctx)
	if err != nil {
		t.Fatalf("PurgeExpiredTokens (second run): %v", err)
	}
	if n2 != 0 {
		t.Errorf("PurgeExpiredTokens (second run) purged %d rows, want 0", n2)
	}
}
