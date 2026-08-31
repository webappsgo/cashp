package auth

import (
	"net/http"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// orgContext pulls the authenticated user and the resolved organization that
// RequireOrgRole placed in the request context.
func orgContext(r *http.Request) (*User, *Org, *apperr.Error) {
	u, found := UserFrom(r.Context())
	if !found {
		return nil, nil, ErrUnauthenticated()
	}
	org, found := OrgFrom(r.Context())
	if !found {
		return nil, nil, ErrNotFound("Organization")
	}
	return u, org, nil
}

// resolveMemberID maps a {username} path segment to a user ID.
func (s *Service) resolveMemberID(r *http.Request) (int64, *apperr.Error) {
	u, err := s.store.UserByUsername(r.Context(), NormalizeName(r.PathValue("username")))
	if err != nil {
		return 0, ErrNotFound("Member")
	}
	return u.ID, nil
}

// orgBody is the organization create and update request.
type orgBody struct {
	Slug        string `json:"slug,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Website     string `json:"website,omitempty"`
	Location    string `json:"location,omitempty"`
	Visibility  string `json:"visibility,omitempty"`
	Invite      string `json:"invite,omitempty"`
}

// bindOrgBody fills an organization request from JSON or a form.
func bindOrgBody(w http.ResponseWriter, r *http.Request, body *orgBody) *apperr.Error {
	return bind(w, r, body, func(r *http.Request) {
		body.Slug = r.PostFormValue("slug")
		body.Name = r.PostFormValue("name")
		body.Description = r.PostFormValue("description")
		body.Website = r.PostFormValue("website")
		body.Location = r.PostFormValue("location")
		body.Visibility = r.PostFormValue("visibility")
		body.Invite = r.PostFormValue("invite")
	})
}

// HandleListOrgs returns the organizations the caller belongs to.
func (s *Service) HandleListOrgs(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	rows, aerr := s.ListUserOrgs(r.Context(), u.ID)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, rows)
}

// HandleCreateOrg creates an organization owned by the caller.
func (s *Service) HandleCreateOrg(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	var body orgBody
	if aerr := bindOrgBody(w, r, &body); aerr != nil {
		fail(w, r, aerr)
		return
	}
	org, aerr := s.CreateOrg(r.Context(), u.ID, OrgInput{
		Slug:        body.Slug,
		Name:        body.Name,
		Description: body.Description,
		Website:     body.Website,
		Location:    body.Location,
		Visibility:  body.Visibility,
	}, body.Invite)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	created(w, r, org.Public(1, OrgRoleOwner))
}

// HandleGetOrg returns one organization as the caller may see it.
func (s *Service) HandleGetOrg(w http.ResponseWriter, r *http.Request) {
	viewerID := int64(0)
	if u, found := UserFrom(r.Context()); found {
		viewerID = u.ID
	}
	out, aerr := s.ViewOrg(r.Context(), OrgSlugFrom(r), viewerID)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, out)
}

// HandleUpdateOrg writes the editable organization fields.
func (s *Service) HandleUpdateOrg(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	var body orgBody
	if aerr := bindOrgBody(w, r, &body); aerr != nil {
		fail(w, r, aerr)
		return
	}
	updated, aerr := s.UpdateOrg(r.Context(), org.ID, u.ID, OrgInput{
		Name:        body.Name,
		Description: body.Description,
		Website:     body.Website,
		Location:    body.Location,
		Visibility:  body.Visibility,
	})
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	count, err := s.store.CountOrgMembers(r.Context(), org.ID)
	if err != nil {
		fail(w, r, ErrInternal(err))
		return
	}
	ok(w, r, updated.Public(count, OrgRoleFrom(r.Context())))
}

// HandleDeleteOrg deletes an organization after a typed confirmation.
func (s *Service) HandleDeleteOrg(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Confirm = r.PostFormValue("confirm")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	if aerr := s.DeleteOrg(r.Context(), org.ID, u.ID, body.Confirm); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Organization deleted"})
}

// HandleListMembers returns the organization roster.
func (s *Service) HandleListMembers(w http.ResponseWriter, r *http.Request) {
	_, org, aerr := orgContext(r)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	rows, aerr := s.ListOrgMembers(r.Context(), org.ID)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, publicMembers(rows))
}

// HandleAddMember seats an existing account directly.
func (s *Service) HandleAddMember(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	var body struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Username = r.PostFormValue("username")
		body.Role = r.PostFormValue("role")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	if aerr := s.AddOrgMember(r.Context(), org.ID, u.ID, body.Username, body.Role); aerr != nil {
		fail(w, r, aerr)
		return
	}
	created(w, r, messageOnly{Message: "Member added"})
}

// HandleSetMemberRole changes a member's role.
func (s *Service) HandleSetMemberRole(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	targetID, aerr := s.resolveMemberID(r)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Role = r.PostFormValue("role")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	if aerr := s.SetOrgMemberRole(r.Context(), org.ID, u.ID, targetID, body.Role); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Role updated"})
}

// HandleRemoveMember removes a member. A member may always remove themselves; removing
// anyone else needs a managing role, which the route's middleware enforces.
func (s *Service) HandleRemoveMember(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	targetID, aerr := s.resolveMemberID(r)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	if targetID != u.ID && !CanManageMembers(OrgRoleFrom(r.Context())) {
		fail(w, r, ErrForbidden())
		return
	}
	if aerr := s.RemoveOrgMember(r.Context(), org.ID, u.ID, targetID); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Member removed"})
}

// HandleTransferOrg hands ownership to another member.
func (s *Service) HandleTransferOrg(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	var body struct {
		Username string `json:"username"`
	}
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Username = r.PostFormValue("username")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	target, err := s.store.UserByUsername(r.Context(), NormalizeName(body.Username))
	if err != nil {
		fail(w, r, ErrNotFound("Member"))
		return
	}
	if aerr := s.TransferOrgOwnership(r.Context(), org.ID, u.ID, target.ID); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Ownership transferred"})
}

// HandleListInvites returns the outstanding invitations.
func (s *Service) HandleListInvites(w http.ResponseWriter, r *http.Request) {
	_, org, aerr := orgContext(r)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	rows, aerr := s.ListOrgInvites(r.Context(), org.ID)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, publicInvites(rows))
}

// HandleCreateInvite issues an invitation and returns the code exactly once.
func (s *Service) HandleCreateInvite(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	var body struct {
		Email string `json:"email,omitempty"`
		Role  string `json:"role"`
	}
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Email = r.PostFormValue("email")
		body.Role = r.PostFormValue("role")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	code, aerr := s.InviteOrgMember(r.Context(), org.ID, u.ID, body.Email, body.Role)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	created(w, r, PublicInvite{Email: NormalizeEmail(body.Email), Role: body.Role, MaxUses: 1, Code: code})
}

// HandleRevokeInvite cancels an invitation.
func (s *Service) HandleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	if aerr := s.RevokeOrgInvite(r.Context(), org.ID, u.ID, pathInt(r, "id")); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Invitation revoked"})
}

// HandleAcceptInvite redeems an invitation for the signed-in account.
func (s *Service) HandleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" && r.Method == http.MethodPost {
		if aerr := parseForm(w, r); aerr != nil {
			fail(w, r, aerr)
			return
		}
		code = r.PostFormValue("code")
	}
	org, aerr := s.AcceptOrgInvite(r.Context(), u.ID, code)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	count, err := s.store.CountOrgMembers(r.Context(), org.ID)
	if err != nil {
		fail(w, r, ErrInternal(err))
		return
	}
	ok(w, r, org.Public(count, OrgRoleMember))
}

// HandleListOrgTokens returns an organization's API tokens.
func (s *Service) HandleListOrgTokens(w http.ResponseWriter, r *http.Request) {
	_, org, aerr := orgContext(r)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	rows, aerr := s.ListOrgTokens(r.Context(), org.ID)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, rows)
}

// HandleCreateOrgToken mints an organization token.
func (s *Service) HandleCreateOrgToken(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	var body tokenBody
	if aerr := bindTokenBody(w, r, &body); aerr != nil {
		fail(w, r, aerr)
		return
	}
	out, aerr := s.CreateOrgToken(r.Context(), org.ID, u.ID, TokenInput{
		Name:      body.Name,
		Scopes:    body.Scopes,
		ExpiresAt: body.ExpiresAt,
	})
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	created(w, r, out)
}

// HandleRevokeOrgToken revokes an organization token.
func (s *Service) HandleRevokeOrgToken(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	if aerr := s.RevokeOrgToken(r.Context(), org.ID, u.ID, pathInt(r, "id")); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Token revoked"})
}
