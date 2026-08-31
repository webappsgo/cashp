package auth

import (
	"context"
	"testing"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

func TestCreateInviteAndLookup(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "inviteowner", "inviteowner@example.com")
	org := newTestOrg(t, store, "inviteco", "Invite Co", owner)

	inv := &Invite{
		Kind:      InviteKindOrg,
		CodeHash:  "invitehash1",
		Email:     "New@Example.com",
		OrgID:     org.ID,
		Role:      OrgRoleMember,
		CreatedBy: owner.ID,
	}
	id, err := store.CreateInvite(ctx, inv)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if id == 0 {
		t.Fatal("CreateInvite returned id 0")
	}
	if inv.ID == 0 {
		t.Fatal("CreateInvite left Invite.ID at 0")
	}
	if inv.MaxUses != 1 {
		t.Errorf("CreateInvite default MaxUses = %d, want 1", inv.MaxUses)
	}

	found, err := store.InviteByHash(ctx, "invitehash1")
	if err != nil {
		t.Fatalf("InviteByHash: %v", err)
	}
	if found.Email != "new@example.com" {
		t.Errorf("InviteByHash email = %q, want normalized new@example.com", found.Email)
	}
	if found.OrgID != org.ID {
		t.Errorf("InviteByHash OrgID = %d, want %d", found.OrgID, org.ID)
	}
	if !found.Usable() {
		t.Error("a freshly created invite must be Usable()")
	}
}

func TestCreateInviteExplicitMaxUsesPreserved(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "inviteexplicit", "inviteexplicit@example.com")
	org := newTestOrg(t, store, "inviteexplicitco", "Invite Explicit Co", owner)

	inv := &Invite{Kind: InviteKindOrg, CodeHash: "explicitmaxuses", OrgID: org.ID, Role: OrgRoleMember, MaxUses: 5, CreatedBy: owner.ID}
	if _, err := store.CreateInvite(ctx, inv); err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if inv.MaxUses != 5 {
		t.Errorf("CreateInvite MaxUses = %d, want 5 (explicit value must be preserved)", inv.MaxUses)
	}
}

func TestInviteByHashNotFound(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	if _, err := store.InviteByHash(context.Background(), "unknown-hash"); err == nil {
		t.Error("InviteByHash(missing) = nil error, want not-found error")
	}
}

func TestListOrgInvitesExcludesRevoked(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "listinviteowner", "listinviteowner@example.com")
	org := newTestOrg(t, store, "listinviteco", "List Invite Co", owner)

	live := &Invite{Kind: InviteKindOrg, CodeHash: "livehash", OrgID: org.ID, Role: OrgRoleMember, CreatedBy: owner.ID}
	if _, err := store.CreateInvite(ctx, live); err != nil {
		t.Fatalf("CreateInvite(live): %v", err)
	}
	revoked := &Invite{Kind: InviteKindOrg, CodeHash: "revokedhash", OrgID: org.ID, Role: OrgRoleMember, CreatedBy: owner.ID}
	if _, err := store.CreateInvite(ctx, revoked); err != nil {
		t.Fatalf("CreateInvite(to-revoke): %v", err)
	}
	if err := store.RevokeInvite(ctx, org.ID, revoked.ID); err != nil {
		t.Fatalf("RevokeInvite: %v", err)
	}

	list, err := store.ListOrgInvites(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListOrgInvites: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListOrgInvites len = %d, want 1 (revoked invite excluded)", len(list))
	}
	if list[0].ID != live.ID {
		t.Errorf("ListOrgInvites returned the wrong invite: %d, want %d", list[0].ID, live.ID)
	}
}

func TestListOrgInvitesEmpty(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "noinviteowner", "noinviteowner@example.com")
	org := newTestOrg(t, store, "noinviteco", "No Invite Co", owner)

	list, err := store.ListOrgInvites(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListOrgInvites(empty): %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ListOrgInvites(empty) len = %d, want 0", len(list))
	}
}

func TestConsumeInviteSingleUseCannotBeReplayed(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "consumeowner", "consumeowner@example.com")
	org := newTestOrg(t, store, "consumeco", "Consume Co", owner)

	inv := &Invite{Kind: InviteKindOrg, CodeHash: "consumehash", OrgID: org.ID, Role: OrgRoleMember, MaxUses: 1, CreatedBy: owner.ID}
	if _, err := store.CreateInvite(ctx, inv); err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}

	if err := store.ConsumeInvite(ctx, inv.ID); err != nil {
		t.Fatalf("ConsumeInvite (first redemption): %v", err)
	}

	// Second redemption of a single-use invite must fail with ErrConflict — this is
	// the mechanism preventing double-redemption.
	if err := store.ConsumeInvite(ctx, inv.ID); err != database.ErrConflict {
		t.Errorf("ConsumeInvite (second redemption) err = %v, want database.ErrConflict", err)
	}
}

func TestConsumeInviteMultiUse(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "multiuseowner", "multiuseowner@example.com")
	org := newTestOrg(t, store, "multiuseco", "Multi Use Co", owner)

	inv := &Invite{Kind: InviteKindOrg, CodeHash: "multiusehash", OrgID: org.ID, Role: OrgRoleMember, MaxUses: 2, CreatedBy: owner.ID}
	if _, err := store.CreateInvite(ctx, inv); err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}

	if err := store.ConsumeInvite(ctx, inv.ID); err != nil {
		t.Fatalf("ConsumeInvite (1st of 2): %v", err)
	}
	if err := store.ConsumeInvite(ctx, inv.ID); err != nil {
		t.Fatalf("ConsumeInvite (2nd of 2): %v", err)
	}
	if err := store.ConsumeInvite(ctx, inv.ID); err != database.ErrConflict {
		t.Errorf("ConsumeInvite (3rd, over cap) err = %v, want database.ErrConflict", err)
	}
}

func TestConsumeInviteExpired(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "expiredinviteowner", "expiredinviteowner@example.com")
	org := newTestOrg(t, store, "expiredinviteco", "Expired Invite Co", owner)

	inv := &Invite{Kind: InviteKindOrg, CodeHash: "expiredinvitehash", OrgID: org.ID, Role: OrgRoleMember, ExpiresAt: 1, CreatedBy: owner.ID}
	if _, err := store.CreateInvite(ctx, inv); err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if err := store.ConsumeInvite(ctx, inv.ID); err != database.ErrConflict {
		t.Errorf("ConsumeInvite(expired) err = %v, want database.ErrConflict", err)
	}
}

func TestConsumeInviteUnknownID(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	if err := store.ConsumeInvite(context.Background(), 999999); err != database.ErrConflict {
		t.Errorf("ConsumeInvite(unknown id) err = %v, want database.ErrConflict", err)
	}
}

func TestRevokeInviteScopedByOrg(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "revokeinviteowner", "revokeinviteowner@example.com")
	org := newTestOrg(t, store, "revokeinviteco", "Revoke Invite Co", owner)
	otherOwner := newTestUser(t, store, "revokeinviteother", "revokeinviteother@example.com")
	otherOrg := newTestOrg(t, store, "revokeinviteotherco", "Other Invite Co", otherOwner)

	inv := &Invite{Kind: InviteKindOrg, CodeHash: "scopedinvitehash", OrgID: org.ID, Role: OrgRoleMember, CreatedBy: owner.ID}
	if _, err := store.CreateInvite(ctx, inv); err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}

	// Cross-org revoke must be a no-op.
	if err := store.RevokeInvite(ctx, otherOrg.ID, inv.ID); err != nil {
		t.Fatalf("RevokeInvite(wrong org): %v", err)
	}
	found, err := store.InviteByHash(ctx, "scopedinvitehash")
	if err != nil {
		t.Fatalf("InviteByHash: %v", err)
	}
	if found.Revoked {
		t.Error("cross-org RevokeInvite revoked an invite it did not own")
	}

	if err := store.RevokeInvite(ctx, org.ID, inv.ID); err != nil {
		t.Fatalf("RevokeInvite(owner org): %v", err)
	}
	found, err = store.InviteByHash(ctx, "scopedinvitehash")
	if err != nil {
		t.Fatalf("InviteByHash: %v", err)
	}
	if !found.Revoked {
		t.Error("invite not revoked after org-scoped RevokeInvite")
	}
}

func TestPurgeExpiredInvites(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "purgeinviteowner", "purgeinviteowner@example.com")
	org := newTestOrg(t, store, "purgeinviteco", "Purge Invite Co", owner)

	expired := &Invite{Kind: InviteKindOrg, CodeHash: "purgeexpired", OrgID: org.ID, Role: OrgRoleMember, ExpiresAt: 1, CreatedBy: owner.ID}
	if _, err := store.CreateInvite(ctx, expired); err != nil {
		t.Fatalf("CreateInvite(expired): %v", err)
	}
	live := &Invite{Kind: InviteKindOrg, CodeHash: "purgelive", OrgID: org.ID, Role: OrgRoleMember, ExpiresAt: time.Now().Add(time.Hour).Unix(), CreatedBy: owner.ID}
	if _, err := store.CreateInvite(ctx, live); err != nil {
		t.Fatalf("CreateInvite(live): %v", err)
	}
	exhausted := &Invite{Kind: InviteKindOrg, CodeHash: "purgeexhausted", OrgID: org.ID, Role: OrgRoleMember, MaxUses: 1, CreatedBy: owner.ID}
	if _, err := store.CreateInvite(ctx, exhausted); err != nil {
		t.Fatalf("CreateInvite(exhausted): %v", err)
	}
	if err := store.ConsumeInvite(ctx, exhausted.ID); err != nil {
		t.Fatalf("ConsumeInvite(exhausted): %v", err)
	}

	if err := store.PurgeExpiredInvites(ctx); err != nil {
		t.Fatalf("PurgeExpiredInvites: %v", err)
	}

	if _, err := store.InviteByHash(ctx, "purgeexpired"); err == nil {
		t.Error("expired invite survived PurgeExpiredInvites")
	}
	if _, err := store.InviteByHash(ctx, "purgeexhausted"); err == nil {
		t.Error("exhausted invite survived PurgeExpiredInvites")
	}
	if _, err := store.InviteByHash(ctx, "purgelive"); err != nil {
		t.Error("live invite was purged by PurgeExpiredInvites")
	}

	// Idempotency: purging again must not error.
	if err := store.PurgeExpiredInvites(ctx); err != nil {
		t.Errorf("PurgeExpiredInvites (second run): %v", err)
	}
}
