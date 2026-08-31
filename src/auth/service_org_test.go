package auth

import (
	"context"
	"testing"
)

func registerTestUser(t *testing.T, svc *Service, username, email string) *User {
	t.Helper()
	u, _, aerr := svc.Register(context.Background(), RegisterInput{
		Username: username, Email: email, Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("Register(%s): %v", username, aerr)
	}
	return u
}

func TestCreateOrgSeatsCreatorAsOwner(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	owner := registerTestUser(t, svc, "orgcreator", "orgcreator@example.com")

	org, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Slug: "svc-org", Name: "Service Org"}, "")
	if aerr != nil {
		t.Fatalf("CreateOrg: %v", aerr)
	}
	if org.ID == 0 {
		t.Fatal("CreateOrg left Org.ID at 0 — id regression")
	}
	if org.OwnerID != owner.ID {
		t.Errorf("CreateOrg OwnerID = %d, want %d", org.OwnerID, owner.ID)
	}

	orgs, aerr := svc.ListUserOrgs(ctx, owner.ID)
	if aerr != nil {
		t.Fatalf("ListUserOrgs: %v", aerr)
	}
	found := false
	for _, o := range orgs {
		if o.ID == org.ID && o.YourRole == OrgRoleOwner {
			found = true
		}
	}
	if !found {
		t.Error("creator was not seated as owner")
	}
}

func TestCreateOrgRejectsWhenOrgsDisabled(t *testing.T) {
	svc := newTestServiceWithConfig(t, func(c *Config) { c.OrgsEnabled = false })
	owner := registerTestUser(t, svc, "disabledorgcreator", "disabledorgcreator@example.com")
	if _, aerr := svc.CreateOrg(context.Background(), owner.ID, OrgInput{Slug: "nope", Name: "Nope"}, ""); aerr == nil {
		t.Fatal("CreateOrg succeeded with OrgsEnabled=false, want ErrFeatureDisabled")
	}
}

func TestCreateOrgRejectsWhenCreationClosed(t *testing.T) {
	svc := newTestServiceWithConfig(t, func(c *Config) { c.OrgCreationMode = OrgCreationDisabled })
	owner := registerTestUser(t, svc, "closedorgcreator", "closedorgcreator@example.com")
	if _, aerr := svc.CreateOrg(context.Background(), owner.ID, OrgInput{Slug: "closed", Name: "Closed"}, ""); aerr == nil {
		t.Fatal("CreateOrg succeeded with OrgCreationMode=disabled, want failure")
	}
}

func TestCreateOrgRejectsDuplicateAndReservedSlug(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	owner := registerTestUser(t, svc, "dupeorgcreator", "dupeorgcreator@example.com")

	if _, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Slug: "dupe-org", Name: "Dupe"}, ""); aerr != nil {
		t.Fatalf("first CreateOrg: %v", aerr)
	}
	if _, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Slug: "dupe-org", Name: "Dupe Again"}, ""); aerr == nil {
		t.Error("CreateOrg succeeded with a taken slug, want failure")
	}
	if _, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Slug: "admin", Name: "Admin"}, ""); aerr == nil {
		t.Error("CreateOrg succeeded with a blocklisted slug, want failure")
	}
	// Slug namespace is shared with usernames.
	if _, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Slug: "dupeorgcreator", Name: "Clash"}, ""); aerr == nil {
		t.Error("CreateOrg succeeded with a slug matching an existing username, want failure")
	}
}

func TestCreateOrgEnforcesMaxOrgsPerUser(t *testing.T) {
	svc := newTestServiceWithConfig(t, func(c *Config) { c.MaxOrgsPerUser = 1 })
	ctx := context.Background()
	owner := registerTestUser(t, svc, "quotaorgcreator", "quotaorgcreator@example.com")

	if _, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Slug: "quota-org-one", Name: "One"}, ""); aerr != nil {
		t.Fatalf("first CreateOrg: %v", aerr)
	}
	if _, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Slug: "quota-org-two", Name: "Two"}, ""); aerr == nil {
		t.Error("CreateOrg exceeded MaxOrgsPerUser, want ErrQuota")
	}
}

func TestCreateOrgRequiresInviteInInviteMode(t *testing.T) {
	svc := newTestServiceWithConfig(t, func(c *Config) { c.OrgCreationMode = OrgCreationInvite })
	ctx := context.Background()
	owner := registerTestUser(t, svc, "inviteorgcreator", "inviteorgcreator@example.com")

	if _, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Slug: "invite-org", Name: "Invite Org"}, ""); aerr == nil {
		t.Fatal("CreateOrg succeeded without an invite code in invite mode, want ErrInviteRequired")
	}

	code, aerr := svc.CreateUserInvite(ctx, owner.ID, "", 1)
	if aerr != nil {
		t.Fatalf("CreateUserInvite: %v", aerr)
	}
	// A user-kind invite must not satisfy org creation.
	if _, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Slug: "invite-org", Name: "Invite Org"}, code); aerr == nil {
		t.Error("CreateOrg accepted a user-kind invite code, want failure")
	}
}

func TestUpdateOrgAndValidation(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	owner := registerTestUser(t, svc, "updateorgcreator", "updateorgcreator@example.com")
	org, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Slug: "update-org", Name: "Update Org"}, "")
	if aerr != nil {
		t.Fatalf("CreateOrg: %v", aerr)
	}

	updated, aerr := svc.UpdateOrg(ctx, org.ID, owner.ID, OrgInput{
		Name: "Renamed Org", Description: "a description", Visibility: VisibilityPrivate,
	})
	if aerr != nil {
		t.Fatalf("UpdateOrg: %v", aerr)
	}
	if updated.Name != "Renamed Org" || updated.Visibility != VisibilityPrivate {
		t.Errorf("UpdateOrg did not persist changes: %+v", updated)
	}

	if _, aerr := svc.UpdateOrg(ctx, org.ID, owner.ID, OrgInput{Website: "not a valid url"}); aerr == nil {
		t.Error("UpdateOrg accepted an invalid website URL, want failure")
	}

	if _, aerr := svc.UpdateOrg(ctx, 999999, owner.ID, OrgInput{Name: "x"}); aerr == nil {
		t.Error("UpdateOrg succeeded for a non-existent org id, want ErrNotFound")
	}
}

func TestDeleteOrgRequiresTypedConfirmation(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	owner := registerTestUser(t, svc, "deleteorgcreator", "deleteorgcreator@example.com")
	org, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Slug: "delete-org", Name: "Delete Org"}, "")
	if aerr != nil {
		t.Fatalf("CreateOrg: %v", aerr)
	}

	if aerr := svc.DeleteOrg(ctx, org.ID, owner.ID, "wrong-slug"); aerr == nil {
		t.Error("DeleteOrg succeeded with a mismatched confirmation, want failure")
	}
	if aerr := svc.DeleteOrg(ctx, org.ID, owner.ID, "delete-org"); aerr != nil {
		t.Fatalf("DeleteOrg: %v", aerr)
	}
	if _, aerr := svc.ViewOrg(ctx, "delete-org", owner.ID); aerr == nil {
		t.Error("ViewOrg succeeded after DeleteOrg, want ErrNotFound")
	}
}

func TestViewOrgHidesPrivateOrgFromNonMembers(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	owner := registerTestUser(t, svc, "privateorgowner", "privateorgowner@example.com")
	stranger := registerTestUser(t, svc, "privateorgstranger", "privateorgstranger@example.com")
	org, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{
		Slug: "private-org", Name: "Private Org", Visibility: VisibilityPrivate,
	}, "")
	if aerr != nil {
		t.Fatalf("CreateOrg: %v", aerr)
	}

	if _, aerr := svc.ViewOrg(ctx, org.Slug, owner.ID); aerr != nil {
		t.Errorf("ViewOrg(owner): %v", aerr)
	}
	if _, aerr := svc.ViewOrg(ctx, org.Slug, stranger.ID); aerr == nil {
		t.Error("ViewOrg(non-member) succeeded on a private org, want ErrNotFound")
	}
}

func TestOrgMembershipLifecycle(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	owner := registerTestUser(t, svc, "memberorgowner", "memberorgowner@example.com")
	member := registerTestUser(t, svc, "memberorgmember", "memberorgmember@example.com")
	org, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Slug: "member-org", Name: "Member Org"}, "")
	if aerr != nil {
		t.Fatalf("CreateOrg: %v", aerr)
	}

	if aerr := svc.AddOrgMember(ctx, org.ID, owner.ID, member.Username, "not-a-role"); aerr == nil {
		t.Error("AddOrgMember accepted an invalid role, want failure")
	}
	if aerr := svc.AddOrgMember(ctx, org.ID, owner.ID, member.Username, OrgRoleMember); aerr != nil {
		t.Fatalf("AddOrgMember: %v", aerr)
	}
	if aerr := svc.AddOrgMember(ctx, org.ID, owner.ID, member.Username, OrgRoleMember); aerr == nil {
		t.Error("AddOrgMember succeeded for an already-seated member, want conflict")
	}

	members, aerr := svc.ListOrgMembers(ctx, org.ID)
	if aerr != nil {
		t.Fatalf("ListOrgMembers: %v", aerr)
	}
	if len(members) != 2 {
		t.Errorf("ListOrgMembers len = %d, want 2", len(members))
	}

	if aerr := svc.SetOrgMemberRole(ctx, org.ID, owner.ID, member.ID, OrgRoleAdmin); aerr != nil {
		t.Fatalf("SetOrgMemberRole: %v", aerr)
	}
	if aerr := svc.SetOrgMemberRole(ctx, org.ID, owner.ID, 999999, OrgRoleAdmin); aerr == nil {
		t.Error("SetOrgMemberRole succeeded for a non-member, want ErrNotFound")
	}

	// The sole owner can never be demoted or removed.
	if aerr := svc.SetOrgMemberRole(ctx, org.ID, owner.ID, owner.ID, OrgRoleMember); aerr == nil {
		t.Error("SetOrgMemberRole demoted the last owner, want ErrLastOwner")
	}
	if aerr := svc.RemoveOrgMember(ctx, org.ID, owner.ID, owner.ID); aerr == nil {
		t.Error("RemoveOrgMember removed the last owner, want ErrLastOwner")
	}

	if aerr := svc.RemoveOrgMember(ctx, org.ID, owner.ID, member.ID); aerr != nil {
		t.Fatalf("RemoveOrgMember: %v", aerr)
	}
	if aerr := svc.RemoveOrgMember(ctx, org.ID, owner.ID, member.ID); aerr == nil {
		t.Error("RemoveOrgMember succeeded for an already-removed member, want ErrNotFound")
	}
}

func TestTransferOrgOwnershipService(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	owner := registerTestUser(t, svc, "transferorgowner", "transferorgowner@example.com")
	member := registerTestUser(t, svc, "transferorgmember", "transferorgmember@example.com")
	org, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Slug: "transfer-org", Name: "Transfer Org"}, "")
	if aerr != nil {
		t.Fatalf("CreateOrg: %v", aerr)
	}

	if aerr := svc.TransferOrgOwnership(ctx, org.ID, owner.ID, owner.ID); aerr == nil {
		t.Error("TransferOrgOwnership succeeded with the same actor and target, want failure")
	}
	if aerr := svc.TransferOrgOwnership(ctx, org.ID, owner.ID, member.ID); aerr == nil {
		t.Error("TransferOrgOwnership succeeded for a non-member target, want ErrNotFound")
	}

	if aerr := svc.AddOrgMember(ctx, org.ID, owner.ID, member.Username, OrgRoleMember); aerr != nil {
		t.Fatalf("AddOrgMember: %v", aerr)
	}
	if aerr := svc.TransferOrgOwnership(ctx, org.ID, owner.ID, member.ID); aerr != nil {
		t.Fatalf("TransferOrgOwnership: %v", aerr)
	}

	orgs, aerr := svc.ListUserOrgs(ctx, member.ID)
	if aerr != nil {
		t.Fatalf("ListUserOrgs: %v", aerr)
	}
	found := false
	for _, o := range orgs {
		if o.ID == org.ID && o.YourRole == OrgRoleOwner {
			found = true
		}
	}
	if !found {
		t.Error("TransferOrgOwnership did not seat the new owner")
	}
}

func TestOrgInviteLifecycleService(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	owner := registerTestUser(t, svc, "inviteorgowner", "inviteorgowner@example.com")
	invitee := registerTestUser(t, svc, "inviteorginvitee", "inviteorginvitee@example.com")
	org, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Slug: "invite-lifecycle-org", Name: "Invite Org"}, "")
	if aerr != nil {
		t.Fatalf("CreateOrg: %v", aerr)
	}

	if _, aerr := svc.InviteOrgMember(ctx, org.ID, owner.ID, "invitee@example.com", "not-a-role"); aerr == nil {
		t.Error("InviteOrgMember accepted an invalid role, want failure")
	}
	code, aerr := svc.InviteOrgMember(ctx, org.ID, owner.ID, "", OrgRoleMember)
	if aerr != nil {
		t.Fatalf("InviteOrgMember: %v", aerr)
	}
	if code == "" {
		t.Fatal("InviteOrgMember returned an empty code")
	}

	invites, aerr := svc.ListOrgInvites(ctx, org.ID)
	if aerr != nil {
		t.Fatalf("ListOrgInvites: %v", aerr)
	}
	if len(invites) != 1 {
		t.Errorf("ListOrgInvites len = %d, want 1", len(invites))
	}

	acceptedOrg, aerr := svc.AcceptOrgInvite(ctx, invitee.ID, code)
	if aerr != nil {
		t.Fatalf("AcceptOrgInvite: %v", aerr)
	}
	if acceptedOrg.ID != org.ID {
		t.Errorf("AcceptOrgInvite org id = %d, want %d", acceptedOrg.ID, org.ID)
	}

	// Single-use: replaying the same code fails.
	if _, aerr := svc.AcceptOrgInvite(ctx, invitee.ID, code); aerr == nil {
		t.Error("AcceptOrgInvite succeeded on a replayed code, want failure")
	}

	// Revoking an already-consumed invite must not error (idempotent no-op).
	if aerr := svc.RevokeOrgInvite(ctx, org.ID, owner.ID, invites[0].ID); aerr != nil {
		t.Errorf("RevokeOrgInvite: %v", aerr)
	}
}

func TestOrgInviteEmailBoundToAddress(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	owner := registerTestUser(t, svc, "boundorgowner", "boundorgowner@example.com")
	wrongUser := registerTestUser(t, svc, "boundorgwrong", "boundorgwrong@example.com")
	org, aerr := svc.CreateOrg(ctx, owner.ID, OrgInput{Slug: "bound-org", Name: "Bound Org"}, "")
	if aerr != nil {
		t.Fatalf("CreateOrg: %v", aerr)
	}

	code, aerr := svc.InviteOrgMember(ctx, org.ID, owner.ID, "specific@example.com", OrgRoleMember)
	if aerr != nil {
		t.Fatalf("InviteOrgMember: %v", aerr)
	}
	if _, aerr := svc.AcceptOrgInvite(ctx, wrongUser.ID, code); aerr == nil {
		t.Error("AcceptOrgInvite succeeded for a user whose email does not match the invite, want failure")
	}
}

func TestCreateUserInviteValidation(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	owner := registerTestUser(t, svc, "userinvitecreator", "userinvitecreator@example.com")

	if _, aerr := svc.CreateUserInvite(ctx, owner.ID, "not-an-email", 1); aerr == nil {
		t.Error("CreateUserInvite accepted an invalid email, want failure")
	}
	if _, aerr := svc.CreateUserInvite(ctx, owner.ID, "", -1); aerr == nil {
		t.Error("CreateUserInvite accepted a negative max_uses, want failure")
	}
	if _, aerr := svc.CreateUserInvite(ctx, owner.ID, "", 1001); aerr == nil {
		t.Error("CreateUserInvite accepted max_uses over 1000, want failure")
	}
	code, aerr := svc.CreateUserInvite(ctx, owner.ID, "", 5)
	if aerr != nil {
		t.Fatalf("CreateUserInvite: %v", aerr)
	}
	if code == "" {
		t.Fatal("CreateUserInvite returned an empty code")
	}
}
