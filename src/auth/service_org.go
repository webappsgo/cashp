package auth

import (
	"context"
	"log/slog"
	"strings"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/security"
)

// OrgInput carries the operator-editable organization fields.
type OrgInput struct {
	Slug        string
	Name        string
	Description string
	Website     string
	Location    string
	Visibility  string
}

// CreateOrg registers an organization and seats the creator as its owner.
// The slug shares the username namespace, so a name that belongs to a user, or that a
// deleted user left behind, can never be claimed here.
func (s *Service) CreateOrg(ctx context.Context, creatorID int64, in OrgInput, inviteCode string) (*Org, *apperr.Error) {
	if !s.cfg.OrgsEnabled {
		return nil, ErrFeatureDisabled("Organizations")
	}
	switch s.cfg.OrgCreationMode {
	case OrgCreationDisabled, OrgCreationAdminOnly:
		return nil, ErrOrgCreationClosed()
	case OrgCreationInvite:
		if strings.TrimSpace(inviteCode) == "" {
			return nil, ErrInviteRequired()
		}
		invite, err := s.store.InviteByHash(ctx, security.HashToken(inviteCode))
		if err != nil || !invite.Usable() || invite.Kind != InviteKindOrg || invite.OrgID != 0 {
			return nil, ErrInviteInvalid()
		}
		if err := s.store.ConsumeInvite(ctx, invite.ID); err != nil {
			return nil, ErrInviteInvalid()
		}
	}

	slug := NormalizeName(in.Slug)
	if err := ValidateSlugFormat(slug); err != nil {
		return nil, ErrValidation("slug", err.Error())
	}
	if IsBlockedName(slug) {
		return nil, ErrNameReserved("slug")
	}
	taken, err := s.store.NameTaken(ctx, slug)
	if err != nil {
		return nil, ErrInternal(err)
	}
	tombstoned, err := s.store.NameTombstoned(ctx, slug)
	if err != nil {
		return nil, ErrInternal(err)
	}
	if taken || tombstoned {
		return nil, ErrNameUnavailable("slug")
	}

	if s.cfg.MaxOrgsPerUser > 0 {
		owned, err := s.store.CountOwnedOrgs(ctx, creatorID)
		if err != nil {
			return nil, ErrInternal(err)
		}
		if owned >= s.cfg.MaxOrgsPerUser {
			return nil, ErrQuota("You have reached the maximum number of organizations")
		}
	}

	org := &Org{
		Slug:       slug,
		Name:       strings.TrimSpace(in.Name),
		Visibility: VisibilityPublic,
		OwnerID:    creatorID,
	}
	if org.Name == "" {
		org.Name = slug
	}
	if aerr := applyOrgFields(org, in); aerr != nil {
		return nil, aerr
	}
	if _, err := s.store.CreateOrg(ctx, org); err != nil {
		return nil, ErrInternal(err)
	}
	if err := s.store.RecordOrgAudit(ctx, org.ID, "org.created", OwnerUser, creatorID, org.Slug); err != nil {
		s.log.Warn("record org audit", slog.String("error", err.Error()))
	}
	s.audit("org.created",
		slog.Int64("org_id", org.ID),
		slog.String("slug", org.Slug),
		slog.Int64("actor_id", creatorID))
	return org, nil
}

// applyOrgFields validates and copies the editable profile fields onto an org.
func applyOrgFields(org *Org, in OrgInput) *apperr.Error {
	name := strings.TrimSpace(in.Name)
	if name != "" {
		org.Name = name
	}
	org.Description = strings.TrimSpace(in.Description)
	org.Website = strings.TrimSpace(in.Website)
	org.Location = strings.TrimSpace(in.Location)
	if len(org.Name) > 100 || len(org.Description) > 500 ||
		len(org.Website) > 255 || len(org.Location) > 100 {
		return ErrValidation("profile", "One of the organization fields is too long")
	}
	if org.Website != "" {
		if err := security.ValidateOutboundURL(org.Website); err != nil {
			return ErrValidation("website", "Enter a valid public website address")
		}
	}
	if in.Visibility == VisibilityPublic || in.Visibility == VisibilityPrivate {
		org.Visibility = in.Visibility
	}
	return nil
}

// UpdateOrg writes the editable organization fields. The caller's role is enforced by
// the RequireOrgRole middleware before this is reached.
func (s *Service) UpdateOrg(ctx context.Context, orgID, actorID int64, in OrgInput) (*Org, *apperr.Error) {
	org, err := s.store.OrgByID(ctx, orgID)
	if err != nil {
		return nil, ErrNotFound("Organization")
	}
	if aerr := applyOrgFields(org, in); aerr != nil {
		return nil, aerr
	}
	if err := s.store.UpdateOrg(ctx, org); err != nil {
		return nil, ErrInternal(err)
	}
	if err := s.store.RecordOrgAudit(ctx, orgID, "org.updated", OwnerUser, actorID, ""); err != nil {
		s.log.Warn("record org audit", slog.String("error", err.Error()))
	}
	return org, nil
}

// DeleteOrg removes an organization and tombstones its slug. Owner only.
func (s *Service) DeleteOrg(ctx context.Context, orgID, actorID int64, confirmSlug string) *apperr.Error {
	org, err := s.store.OrgByID(ctx, orgID)
	if err != nil {
		return ErrNotFound("Organization")
	}
	// The typed confirmation makes an accidental or forged single-click deletion
	// impossible even if a CSRF token were somehow obtained.
	if NormalizeName(confirmSlug) != org.Slug {
		return ErrValidation("confirm", "Type the organization name exactly to confirm deletion")
	}
	if err := s.store.DeleteOrg(ctx, orgID); err != nil {
		return ErrInternal(err)
	}
	s.audit("org.deleted",
		slog.Int64("org_id", orgID),
		slog.String("slug", org.Slug),
		slog.Int64("actor_id", actorID))
	return nil
}

// ListUserOrgs returns every organization the user belongs to, with their role.
func (s *Service) ListUserOrgs(ctx context.Context, userID int64) ([]PublicOrg, *apperr.Error) {
	orgs, roles, err := s.store.ListUserOrgs(ctx, userID)
	if err != nil {
		return nil, ErrInternal(err)
	}
	out := make([]PublicOrg, 0, len(orgs))
	for i, o := range orgs {
		count, err := s.store.CountOrgMembers(ctx, o.ID)
		if err != nil {
			return nil, ErrInternal(err)
		}
		out = append(out, o.Public(count, roles[i]))
	}
	return out, nil
}

// ViewOrg returns an organization as the caller may see it. A private organization is
// reported as missing to a non-member rather than as forbidden, so its existence is not
// disclosed.
func (s *Service) ViewOrg(ctx context.Context, slug string, viewerID int64) (PublicOrg, *apperr.Error) {
	org, err := s.store.OrgBySlug(ctx, NormalizeName(slug))
	if err != nil {
		return PublicOrg{}, ErrNotFound("Organization")
	}
	role := ""
	if viewerID > 0 {
		role, err = s.store.OrgRole(ctx, org.ID, viewerID)
		if err != nil {
			return PublicOrg{}, ErrInternal(err)
		}
	}
	if org.Visibility == VisibilityPrivate && role == "" {
		return PublicOrg{}, ErrNotFound("Organization")
	}
	if org.Suspended && role == "" {
		return PublicOrg{}, ErrNotFound("Organization")
	}
	count, err := s.store.CountOrgMembers(ctx, org.ID)
	if err != nil {
		return PublicOrg{}, ErrInternal(err)
	}
	return org.Public(count, role), nil
}

// ListOrgMembers returns the membership roster.
func (s *Service) ListOrgMembers(ctx context.Context, orgID int64) ([]*OrgMember, *apperr.Error) {
	rows, err := s.store.ListOrgMembers(ctx, orgID)
	if err != nil {
		return nil, ErrInternal(err)
	}
	return rows, nil
}

// AddOrgMember seats an existing account directly, used when the operator has disabled
// email delivery and invites cannot be sent.
func (s *Service) AddOrgMember(ctx context.Context, orgID, actorID int64, username, role string) *apperr.Error {
	if role != OrgRoleAdmin && role != OrgRoleMember {
		return ErrValidation("role", "Choose either admin or member")
	}
	if aerr := s.checkMemberCap(ctx, orgID); aerr != nil {
		return aerr
	}
	u, err := s.store.UserByUsername(ctx, NormalizeName(username))
	if err != nil {
		return ErrNotFound("User")
	}
	existing, err := s.store.OrgRole(ctx, orgID, u.ID)
	if err != nil {
		return ErrInternal(err)
	}
	if existing != "" {
		return apperr.New(apperr.CodeConflict, 409, "That account is already a member")
	}
	if err := s.store.AddOrgMember(ctx, orgID, u.ID, role); err != nil {
		return ErrInternal(err)
	}
	if err := s.store.RecordOrgAudit(ctx, orgID, "member.added", OwnerUser, actorID, u.Username); err != nil {
		s.log.Warn("record org audit", slog.String("error", err.Error()))
	}
	s.audit("org.member_added",
		slog.Int64("org_id", orgID),
		slog.Int64("user_id", u.ID),
		slog.String("role", role),
		slog.Int64("actor_id", actorID))
	return nil
}

// checkMemberCap enforces the configured membership ceiling.
func (s *Service) checkMemberCap(ctx context.Context, orgID int64) *apperr.Error {
	if s.cfg.MaxMembersPerOrg <= 0 {
		return nil
	}
	count, err := s.store.CountOrgMembers(ctx, orgID)
	if err != nil {
		return ErrInternal(err)
	}
	if count >= s.cfg.MaxMembersPerOrg {
		return ErrQuota("This organization has reached its member limit")
	}
	return nil
}

// SetOrgMemberRole changes a member's role. The owner role is granted only through
// TransferOrgOwnership, and the final owner can never be demoted, so an organization
// can never be left with nobody able to administer it.
func (s *Service) SetOrgMemberRole(ctx context.Context, orgID, actorID, targetID int64, role string) *apperr.Error {
	if role != OrgRoleAdmin && role != OrgRoleMember {
		return ErrValidation("role", "Choose either admin or member")
	}
	current, err := s.store.OrgRole(ctx, orgID, targetID)
	if err != nil {
		return ErrInternal(err)
	}
	if current == "" {
		return ErrNotFound("Member")
	}
	if current == OrgRoleOwner {
		owners, err := s.store.CountOrgOwners(ctx, orgID)
		if err != nil {
			return ErrInternal(err)
		}
		if owners <= 1 {
			return ErrLastOwner()
		}
	}
	if err := s.store.SetOrgMemberRole(ctx, orgID, targetID, role); err != nil {
		return ErrInternal(err)
	}
	if err := s.store.RecordOrgAudit(ctx, orgID, "member.role_changed", OwnerUser, actorID, role); err != nil {
		s.log.Warn("record org audit", slog.String("error", err.Error()))
	}
	s.audit("org.member_role_changed",
		slog.Int64("org_id", orgID),
		slog.Int64("user_id", targetID),
		slog.String("role", role),
		slog.Int64("actor_id", actorID))
	return nil
}

// RemoveOrgMember removes a member. A member may always remove themselves; removing
// anyone else requires a managing role, which the middleware has already enforced.
func (s *Service) RemoveOrgMember(ctx context.Context, orgID, actorID, targetID int64) *apperr.Error {
	current, err := s.store.OrgRole(ctx, orgID, targetID)
	if err != nil {
		return ErrInternal(err)
	}
	if current == "" {
		return ErrNotFound("Member")
	}
	if current == OrgRoleOwner {
		owners, err := s.store.CountOrgOwners(ctx, orgID)
		if err != nil {
			return ErrInternal(err)
		}
		if owners <= 1 {
			return ErrLastOwner()
		}
	}
	if err := s.store.RemoveOrgMember(ctx, orgID, targetID); err != nil {
		return ErrInternal(err)
	}
	if err := s.store.RecordOrgAudit(ctx, orgID, "member.removed", OwnerUser, actorID, ""); err != nil {
		s.log.Warn("record org audit", slog.String("error", err.Error()))
	}
	s.audit("org.member_removed",
		slog.Int64("org_id", orgID),
		slog.Int64("user_id", targetID),
		slog.Int64("actor_id", actorID))
	return nil
}

// TransferOrgOwnership hands the owner role to another member. Owner only.
func (s *Service) TransferOrgOwnership(ctx context.Context, orgID, actorID, targetID int64) *apperr.Error {
	if actorID == targetID {
		return ErrValidation("member", "Choose a different member to transfer ownership to")
	}
	role, err := s.store.OrgRole(ctx, orgID, targetID)
	if err != nil {
		return ErrInternal(err)
	}
	if role == "" {
		return ErrNotFound("Member")
	}
	if err := s.store.TransferOrgOwnership(ctx, orgID, actorID, targetID); err != nil {
		return ErrInternal(err)
	}
	if err := s.store.RecordOrgAudit(ctx, orgID, "org.ownership_transferred", OwnerUser, actorID, ""); err != nil {
		s.log.Warn("record org audit", slog.String("error", err.Error()))
	}
	s.audit("org.ownership_transferred",
		slog.Int64("org_id", orgID),
		slog.Int64("from_user_id", actorID),
		slog.Int64("to_user_id", targetID))
	return nil
}

// InviteOrgMember issues an invitation. The plaintext code is returned once so the
// caller can mail it or hand it over directly; only its hash is stored.
func (s *Service) InviteOrgMember(ctx context.Context, orgID, actorID int64, email, role string) (string, *apperr.Error) {
	if role != OrgRoleAdmin && role != OrgRoleMember {
		return "", ErrValidation("role", "Choose either admin or member")
	}
	email = NormalizeEmail(email)
	if email != "" {
		if err := ValidateEmail(email); err != nil {
			return "", ErrValidation("email", err.Error())
		}
	}
	if aerr := s.checkMemberCap(ctx, orgID); aerr != nil {
		return "", aerr
	}
	org, err := s.store.OrgByID(ctx, orgID)
	if err != nil {
		return "", ErrNotFound("Organization")
	}

	code, err := newSecret()
	if err != nil {
		return "", ErrInternal(err)
	}
	invite := &Invite{
		Kind:      InviteKindOrg,
		CodeHash:  security.HashToken(code),
		Email:     email,
		OrgID:     orgID,
		Role:      role,
		MaxUses:   1,
		ExpiresAt: time.Now().Add(InviteTTL).Unix(),
		CreatedBy: actorID,
	}
	if _, err := s.store.CreateInvite(ctx, invite); err != nil {
		return "", ErrInternal(err)
	}
	if email != "" {
		link := s.cfg.BaseURL + "/orgs/invites/accept?code=" + code
		s.send(ctx, email, "You have been invited to "+org.Name+" on "+s.cfg.SiteName,
			"Use the link below within seven days to join "+org.Name+".\n\n"+link)
	}
	if err := s.store.RecordOrgAudit(ctx, orgID, "invite.created", OwnerUser, actorID, role); err != nil {
		s.log.Warn("record org audit", slog.String("error", err.Error()))
	}
	s.audit("org.invite_created",
		slog.Int64("org_id", orgID),
		slog.Int64("actor_id", actorID),
		slog.String("role", role))
	return code, nil
}

// ListOrgInvites returns the outstanding invitations for an organization.
func (s *Service) ListOrgInvites(ctx context.Context, orgID int64) ([]*Invite, *apperr.Error) {
	rows, err := s.store.ListOrgInvites(ctx, orgID)
	if err != nil {
		return nil, ErrInternal(err)
	}
	return rows, nil
}

// RevokeOrgInvite cancels an invitation, scoped to the issuing organization.
func (s *Service) RevokeOrgInvite(ctx context.Context, orgID, actorID, inviteID int64) *apperr.Error {
	if err := s.store.RevokeInvite(ctx, orgID, inviteID); err != nil {
		return ErrInternal(err)
	}
	if err := s.store.RecordOrgAudit(ctx, orgID, "invite.revoked", OwnerUser, actorID, ""); err != nil {
		s.log.Warn("record org audit", slog.String("error", err.Error()))
	}
	return nil
}

// AcceptOrgInvite redeems an invitation for the signed-in account.
func (s *Service) AcceptOrgInvite(ctx context.Context, userID int64, code string) (*Org, *apperr.Error) {
	invite, err := s.store.InviteByHash(ctx, security.HashToken(code))
	if err != nil || !invite.Usable() || invite.Kind != InviteKindOrg || invite.OrgID == 0 {
		return nil, ErrInviteInvalid()
	}
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return nil, ErrUnauthenticated()
	}
	// An address-bound invite may only be redeemed by that address, so forwarding the
	// link does not hand over the seat.
	if invite.Email != "" && invite.Email != u.Email {
		return nil, ErrInviteInvalid()
	}
	org, err := s.store.OrgByID(ctx, invite.OrgID)
	if err != nil {
		return nil, ErrInviteInvalid()
	}
	if aerr := s.checkMemberCap(ctx, org.ID); aerr != nil {
		return nil, aerr
	}
	role := invite.Role
	if role != OrgRoleAdmin && role != OrgRoleMember {
		role = OrgRoleMember
	}
	// Consume first: if two people race the same single-use code, only the redemption
	// that changes the row proceeds to add a member.
	if err := s.store.ConsumeInvite(ctx, invite.ID); err != nil {
		return nil, ErrInviteInvalid()
	}
	if err := s.store.AddOrgMember(ctx, org.ID, userID, role); err != nil {
		return nil, ErrInternal(err)
	}
	if err := s.store.RecordOrgAudit(ctx, org.ID, "invite.accepted", OwnerUser, userID, role); err != nil {
		s.log.Warn("record org audit", slog.String("error", err.Error()))
	}
	s.audit("org.invite_accepted",
		slog.Int64("org_id", org.ID),
		slog.Int64("user_id", userID),
		slog.String("role", role))
	return org, nil
}

// CreateUserInvite issues a registration invite, used when RegistrationMode is invite.
func (s *Service) CreateUserInvite(ctx context.Context, actorID int64, email string, maxUses int) (string, *apperr.Error) {
	email = NormalizeEmail(email)
	if email != "" {
		if err := ValidateEmail(email); err != nil {
			return "", ErrValidation("email", err.Error())
		}
	}
	if maxUses < 0 || maxUses > 1000 {
		return "", ErrValidation("max_uses", "Choose a use count between 0 and 1000")
	}
	code, err := newSecret()
	if err != nil {
		return "", ErrInternal(err)
	}
	invite := &Invite{
		Kind:      InviteKindUser,
		CodeHash:  security.HashToken(code),
		Email:     email,
		MaxUses:   maxUses,
		ExpiresAt: time.Now().Add(InviteTTL).Unix(),
		CreatedBy: actorID,
	}
	if _, err := s.store.CreateInvite(ctx, invite); err != nil {
		return "", ErrInternal(err)
	}
	if email != "" {
		link := s.cfg.BaseURL + "/auth/register?invite=" + code
		s.send(ctx, email, "Your invitation to "+s.cfg.SiteName,
			"Use the link below within seven days to create your account.\n\n"+link)
	}
	s.audit("invite.created", slog.Int64("actor_id", actorID), slog.String("kind", InviteKindUser))
	return code, nil
}
