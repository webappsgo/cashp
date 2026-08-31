package auth

import (
	"context"
	"testing"
)

// newTestOrg creates an org owned by owner and fails the test if the returned
// id is zero — the regression this whole pass exists to catch.
func newTestOrg(t *testing.T, store *Store, slug, name string, owner *User) *Org {
	t.Helper()
	o := &Org{
		Slug:       slug,
		Name:       name,
		Visibility: VisibilityPublic,
		OwnerID:    owner.ID,
	}
	id, err := store.CreateOrg(context.Background(), o)
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	if id == 0 {
		t.Fatal("CreateOrg returned id 0")
	}
	if o.ID == 0 {
		t.Fatal("CreateOrg left Org.ID at 0")
	}
	return o
}

func TestCreateOrgSeatsOwner(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "orgowner", "orgowner@example.com")
	org := newTestOrg(t, store, "acme", "Acme Inc", owner)

	role, err := store.OrgRole(ctx, org.ID, owner.ID)
	if err != nil {
		t.Fatalf("OrgRole: %v", err)
	}
	if role != OrgRoleOwner {
		t.Errorf("OrgRole = %q, want %q", role, OrgRoleOwner)
	}

	n, err := store.CountOrgOwners(ctx, org.ID)
	if err != nil {
		t.Fatalf("CountOrgOwners: %v", err)
	}
	if n != 1 {
		t.Errorf("CountOrgOwners = %d, want 1", n)
	}
}

func TestOrgByIDAndBySlug(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "sluglookup", "sluglookup@example.com")
	org := newTestOrg(t, store, "widgets", "Widgets Co", owner)

	byID, err := store.OrgByID(ctx, org.ID)
	if err != nil {
		t.Fatalf("OrgByID: %v", err)
	}
	if byID.Slug != "widgets" {
		t.Errorf("OrgByID slug = %q, want widgets", byID.Slug)
	}

	bySlug, err := store.OrgBySlug(ctx, "WIDGETS")
	if err != nil {
		t.Fatalf("OrgBySlug (case-insensitive): %v", err)
	}
	if bySlug.ID != org.ID {
		t.Error("OrgBySlug resolved a different org")
	}
}

func TestOrgByIDNotFound(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	if _, err := store.OrgByID(context.Background(), 999999); err == nil {
		t.Error("OrgByID(missing) = nil error, want not-found error")
	}
}

func TestUpdateOrg(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "updateorg", "updateorg@example.com")
	org := newTestOrg(t, store, "updateco", "Update Co", owner)

	org.Name = "Renamed Co"
	org.Description = "a new description"
	org.Visibility = VisibilityPrivate
	if err := store.UpdateOrg(ctx, org); err != nil {
		t.Fatalf("UpdateOrg: %v", err)
	}

	reloaded, err := store.OrgByID(ctx, org.ID)
	if err != nil {
		t.Fatalf("OrgByID: %v", err)
	}
	if reloaded.Name != "Renamed Co" || reloaded.Description != "a new description" || reloaded.Visibility != VisibilityPrivate {
		t.Errorf("UpdateOrg did not persist: %+v", reloaded)
	}
}

func TestSetOrgSuspended(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "suspendorg", "suspendorg@example.com")
	org := newTestOrg(t, store, "suspendco", "Suspend Co", owner)

	if err := store.SetOrgSuspended(ctx, org.ID, true); err != nil {
		t.Fatalf("SetOrgSuspended(true): %v", err)
	}
	reloaded, err := store.OrgByID(ctx, org.ID)
	if err != nil {
		t.Fatalf("OrgByID: %v", err)
	}
	if !reloaded.Suspended {
		t.Error("org not suspended after SetOrgSuspended(true)")
	}

	if err := store.SetOrgSuspended(ctx, org.ID, false); err != nil {
		t.Fatalf("SetOrgSuspended(false): %v", err)
	}
	reloaded, err = store.OrgByID(ctx, org.ID)
	if err != nil {
		t.Fatalf("OrgByID: %v", err)
	}
	if reloaded.Suspended {
		t.Error("org still suspended after SetOrgSuspended(false)")
	}
}

func TestOrgMemberLifecycle(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "memberorgowner", "memberorgowner@example.com")
	member := newTestUser(t, store, "orgmember", "orgmember@example.com")
	org := newTestOrg(t, store, "memberco", "Member Co", owner)

	if err := store.AddOrgMember(ctx, org.ID, member.ID, OrgRoleMember); err != nil {
		t.Fatalf("AddOrgMember: %v", err)
	}

	n, err := store.CountOrgMembers(ctx, org.ID)
	if err != nil {
		t.Fatalf("CountOrgMembers: %v", err)
	}
	if n != 2 {
		t.Errorf("CountOrgMembers = %d, want 2 (owner + member)", n)
	}

	members, err := store.ListOrgMembers(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListOrgMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("ListOrgMembers len = %d, want 2", len(members))
	}

	if err := store.SetOrgMemberRole(ctx, org.ID, member.ID, OrgRoleAdmin); err != nil {
		t.Fatalf("SetOrgMemberRole: %v", err)
	}
	role, err := store.OrgRole(ctx, org.ID, member.ID)
	if err != nil {
		t.Fatalf("OrgRole: %v", err)
	}
	if role != OrgRoleAdmin {
		t.Errorf("OrgRole after SetOrgMemberRole = %q, want %q", role, OrgRoleAdmin)
	}

	if err := store.RemoveOrgMember(ctx, org.ID, member.ID); err != nil {
		t.Fatalf("RemoveOrgMember: %v", err)
	}
	role, err = store.OrgRole(ctx, org.ID, member.ID)
	if err != nil {
		t.Fatalf("OrgRole after removal: %v", err)
	}
	if role != "" {
		t.Errorf("OrgRole after RemoveOrgMember = %q, want empty string", role)
	}
}

func TestOrgRoleNotAMember(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "solooworgowner", "solooworgowner@example.com")
	outsider := newTestUser(t, store, "outsider", "outsider@example.com")
	org := newTestOrg(t, store, "solooco", "Soloo Co", owner)

	role, err := store.OrgRole(ctx, org.ID, outsider.ID)
	if err != nil {
		t.Fatalf("OrgRole(non-member) unexpected error: %v", err)
	}
	if role != "" {
		t.Errorf("OrgRole(non-member) = %q, want empty string (not an error)", role)
	}
}

func TestTransferOrgOwnership(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "transferowner", "transferowner@example.com")
	newOwner := newTestUser(t, store, "transfernew", "transfernew@example.com")
	org := newTestOrg(t, store, "transferco", "Transfer Co", owner)

	if err := store.AddOrgMember(ctx, org.ID, newOwner.ID, OrgRoleMember); err != nil {
		t.Fatalf("AddOrgMember: %v", err)
	}

	if err := store.TransferOrgOwnership(ctx, org.ID, owner.ID, newOwner.ID); err != nil {
		t.Fatalf("TransferOrgOwnership: %v", err)
	}

	newRole, err := store.OrgRole(ctx, org.ID, newOwner.ID)
	if err != nil {
		t.Fatalf("OrgRole(new owner): %v", err)
	}
	if newRole != OrgRoleOwner {
		t.Errorf("new owner role = %q, want %q", newRole, OrgRoleOwner)
	}

	oldRole, err := store.OrgRole(ctx, org.ID, owner.ID)
	if err != nil {
		t.Fatalf("OrgRole(old owner): %v", err)
	}
	if oldRole != OrgRoleAdmin {
		t.Errorf("previous owner role = %q, want %q (demoted to admin)", oldRole, OrgRoleAdmin)
	}

	reloaded, err := store.OrgByID(ctx, org.ID)
	if err != nil {
		t.Fatalf("OrgByID: %v", err)
	}
	if reloaded.OwnerID != newOwner.ID {
		t.Errorf("orgs.owner_id = %d, want %d", reloaded.OwnerID, newOwner.ID)
	}
}

func TestListUserOrgs(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "listorgsowner", "listorgsowner@example.com")
	newTestOrg(t, store, "listorgsa", "List Orgs A", owner)
	newTestOrg(t, store, "listorgsb", "List Orgs B", owner)

	orgs, roles, err := store.ListUserOrgs(ctx, owner.ID)
	if err != nil {
		t.Fatalf("ListUserOrgs: %v", err)
	}
	if len(orgs) != 2 || len(roles) != 2 {
		t.Fatalf("ListUserOrgs len = %d/%d, want 2/2", len(orgs), len(roles))
	}
	for _, r := range roles {
		if r != OrgRoleOwner {
			t.Errorf("role = %q, want %q", r, OrgRoleOwner)
		}
	}
}

func TestListUserOrgsEmpty(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "noorgsuser", "noorgsuser@example.com")

	orgs, roles, err := store.ListUserOrgs(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListUserOrgs: %v", err)
	}
	if len(orgs) != 0 || len(roles) != 0 {
		t.Errorf("ListUserOrgs(no orgs) = %d/%d, want 0/0", len(orgs), len(roles))
	}
}

func TestCountOwnedOrgs(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "countownedowner", "countownedowner@example.com")
	newTestOrg(t, store, "countowneda", "Count Owned A", owner)

	n, err := store.CountOwnedOrgs(ctx, owner.ID)
	if err != nil {
		t.Fatalf("CountOwnedOrgs: %v", err)
	}
	if n != 1 {
		t.Errorf("CountOwnedOrgs = %d, want 1", n)
	}
}

func TestDeleteOrgCascades(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "deleteorgowner", "deleteorgowner@example.com")
	member := newTestUser(t, store, "deleteorgmember", "deleteorgmember@example.com")
	org := newTestOrg(t, store, "deleteco", "Delete Co", owner)
	if err := store.AddOrgMember(ctx, org.ID, member.ID, OrgRoleMember); err != nil {
		t.Fatalf("AddOrgMember: %v", err)
	}

	dom := &CustomDomain{
		OwnerType: OwnerOrg,
		OwnerID:   org.ID,
		Domain:    "deleteco-example.com",
	}
	if _, err := store.CreateDomain(ctx, dom); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	if err := store.DeleteOrg(ctx, org.ID); err != nil {
		t.Fatalf("DeleteOrg: %v", err)
	}

	if _, err := store.OrgByID(ctx, org.ID); err == nil {
		t.Error("org still readable after DeleteOrg")
	}
	role, err := store.OrgRole(ctx, org.ID, member.ID)
	if err != nil {
		t.Fatalf("OrgRole after delete: %v", err)
	}
	if role != "" {
		t.Error("org_members row survived DeleteOrg")
	}
	domains, err := store.ListDomains(ctx, OwnerOrg, org.ID)
	if err != nil {
		t.Fatalf("ListDomains after delete: %v", err)
	}
	if len(domains) != 0 {
		t.Error("custom_domains row survived DeleteOrg")
	}
	taken, err := store.NameTaken(ctx, "deleteco")
	if err != nil {
		t.Fatalf("NameTaken: %v", err)
	}
	if taken {
		t.Error("NameTaken should be false once the org row is gone")
	}
}

func TestRecordOrgAudit(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "auditorgowner", "auditorgowner@example.com")
	org := newTestOrg(t, store, "auditco", "Audit Co", owner)

	if err := store.RecordOrgAudit(ctx, org.ID, "member_added", OwnerUser, owner.ID, `{"target":"someone"}`); err != nil {
		t.Errorf("RecordOrgAudit: %v", err)
	}
}
