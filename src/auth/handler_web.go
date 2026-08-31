package auth

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// webBasePath is where the server-rendered account pages are mounted. Fail() already
// redirects unauthenticated browsers to this prefix, so the two must stay in step.
const webBasePath = "/auth"

// base builds the page context shared by every rendered page: the CSRF token bound to
// the caller's session, the signed-in identities, and any flash carried by the redirect
// that landed here.
func (s *Service) base(r *http.Request, title, heading string) pageData {
	d := pageData{
		Title:     title,
		Heading:   heading,
		CSRFToken: s.CSRFToken(r),
		BasePath:  webBasePath,
		Notice:    message(r.URL.Query().Get("notice")),
		Error:     message(r.URL.Query().Get("problem")),
	}
	if u, found := UserFrom(r.Context()); found {
		pub := u.Public()
		d.User = &pub
	}
	if a, found := AdminFrom(r.Context()); found {
		pub := a.Public()
		d.Admin = &pub
	}
	return d
}

// goBack sends the browser to a page with a flash key attached. Only keys present in
// Messages render, so the redirect target cannot be used to plant text on a page.
func (s *Service) goBack(w http.ResponseWriter, r *http.Request, path, noticeKey, problemKey string) {
	target := path
	switch {
	case noticeKey != "":
		target += "?notice=" + urlQueryEscape(noticeKey)
	case problemKey != "":
		target += "?problem=" + urlQueryEscape(problemKey)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// webPathInt reads a numeric path segment and reports a validation error instead of
// silently treating a malformed segment as row zero.
func webPathInt(r *http.Request, name string) (int64, *apperr.Error) {
	v, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || v <= 0 {
		return 0, ErrValidation(name, "That item reference is not valid.")
	}
	return v, nil
}

// formValue reads a trimmed form field after parsing the body under the shared size cap.
func formValue(r *http.Request, name string) string {
	return strings.TrimSpace(r.PostFormValue(name))
}

// PageLogin renders the sign-in form.
func (s *Service) PageLogin(w http.ResponseWriter, r *http.Request) {
	if _, found := UserFrom(r.Context()); found {
		http.Redirect(w, r, webBasePath+"/account", http.StatusSeeOther)
		return
	}
	d := s.base(r, "Sign in", "Sign in")
	d.Next = SafeNext(r.URL.Query().Get("next"), "")
	s.render(w, http.StatusOK, "login", d)
}

// SubmitLogin verifies credentials and starts a session.
func (s *Service) SubmitLogin(w http.ResponseWriter, r *http.Request) {
	if aerr := parseForm(w, r); aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	in := LoginInput{
		Identifier: formValue(r, "login"),
		Password:   r.PostFormValue("password"),
		TOTPCode:   formValue(r, "totp_code"),
		IP:         s.ClientIP(r),
		UserAgent:  r.UserAgent(),
	}
	_, token, aerr := s.Login(r.Context(), in)
	if aerr != nil {
		d := s.base(r, "Sign in", "Sign in")
		d.Next = SafeNext(formValue(r, "next"), "")
		d.Error = aerr.Message
		s.render(w, aerr.HTTPStatus, "login", d)
		return
	}
	s.SetSessionCookie(w, token)
	http.Redirect(w, r, SafeNext(formValue(r, "next"), webBasePath+"/account"), http.StatusSeeOther)
}

// SubmitLogout ends the current session.
func (s *Service) SubmitLogout(w http.ResponseWriter, r *http.Request) {
	s.Logout(r.Context(), cookieValue(r, SessionCookieName))
	s.ClearSessionCookie(w)
	s.goBack(w, r, webBasePath+"/login", "auth.signed_out", "")
}

// PageRegister renders the registration form.
func (s *Service) PageRegister(w http.ResponseWriter, r *http.Request) {
	if _, found := UserFrom(r.Context()); found {
		http.Redirect(w, r, webBasePath+"/account", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "register", s.base(r, "Create account", "Create your account"))
}

// SubmitRegister creates an account and signs the new user in.
func (s *Service) SubmitRegister(w http.ResponseWriter, r *http.Request) {
	if aerr := parseForm(w, r); aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	in := RegisterInput{
		Username:   formValue(r, "username"),
		Email:      formValue(r, "email"),
		Password:   r.PostFormValue("password"),
		InviteCode: formValue(r, "invite"),
		IP:         s.ClientIP(r),
		UserAgent:  r.UserAgent(),
	}
	u, token, aerr := s.Register(r.Context(), in)
	if aerr != nil {
		d := s.base(r, "Create account", "Create your account")
		d.Error = aerr.Message
		s.render(w, aerr.HTTPStatus, "register", d)
		return
	}
	if display := formValue(r, "display_name"); display != "" {
		if _, uerr := s.UpdateProfile(r.Context(), u.ID, PublicUser{DisplayName: display}); uerr != nil {
			s.log.Debug("set display name at registration", "error", uerr.Error())
		}
	}
	if token == "" {
		key := "auth.registered.approval"
		if s.cfg.RequireEmailVerification {
			key = "auth.registered.verify"
		}
		s.goBack(w, r, webBasePath+"/login", key, "")
		return
	}
	s.SetSessionCookie(w, token)
	s.goBack(w, r, webBasePath+"/account", "auth.registered", "")
}

// PagePasswordForgot renders the reset request form.
func (s *Service) PagePasswordForgot(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "password_forgot", s.base(r, "Reset password", "Reset your password"))
}

// SubmitPasswordForgot requests a reset link. The reply is identical whether or not the
// address belongs to an account.
func (s *Service) SubmitPasswordForgot(w http.ResponseWriter, r *http.Request) {
	if aerr := parseForm(w, r); aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	if aerr := s.RequestPasswordReset(r.Context(), formValue(r, "email"), s.ClientIP(r)); aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	s.goBack(w, r, webBasePath+"/login", "auth.password.reset_sent", "")
}

// PagePasswordReset renders the new-password form for a reset link.
func (s *Service) PagePasswordReset(w http.ResponseWriter, r *http.Request) {
	d := s.base(r, "Set new password", "Set a new password")
	d.Next = r.URL.Query().Get("code")
	s.render(w, http.StatusOK, "password_reset", d)
}

// SubmitPasswordReset consumes the reset code and stores the new password.
func (s *Service) SubmitPasswordReset(w http.ResponseWriter, r *http.Request) {
	if aerr := parseForm(w, r); aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	code := formValue(r, "code")
	next := r.PostFormValue("password")
	if next != r.PostFormValue("password_confirm") {
		d := s.base(r, "Set new password", "Set a new password")
		d.Next = code
		d.Error = Messages["auth.password.mismatch"]
		s.render(w, http.StatusBadRequest, "password_reset", d)
		return
	}
	if aerr := s.ConfirmPasswordReset(r.Context(), code, next, s.ClientIP(r)); aerr != nil {
		d := s.base(r, "Set new password", "Set a new password")
		d.Next = code
		d.Error = aerr.Message
		s.render(w, aerr.HTTPStatus, "password_reset", d)
		return
	}
	s.goBack(w, r, webBasePath+"/login", "auth.password.reset_done", "")
}

// PageVerifyEmail consumes an email verification link.
func (s *Service) PageVerifyEmail(w http.ResponseWriter, r *http.Request) {
	aerr := s.VerifyEmail(r.Context(), r.URL.Query().Get("code"), s.ClientIP(r))
	d := s.base(r, "Email verification", "Email verification")
	if aerr != nil {
		d.Error = aerr.Message
		s.render(w, aerr.HTTPStatus, "message", d)
		return
	}
	d.Notice = Messages["auth.email.verified"]
	s.render(w, http.StatusOK, "message", d)
}

// accountData assembles the account page: profile, sessions and tokens.
func (s *Service) accountData(r *http.Request) (pageData, *apperr.Error) {
	d := s.base(r, "Account", "Your account")
	u, found := UserFrom(r.Context())
	if !found {
		return d, ErrUnauthenticated()
	}
	sessions, aerr := s.ListSessions(r.Context(), u.ID)
	if aerr != nil {
		return d, aerr
	}
	var currentID int64
	if sess, ok2 := SessionFrom(r.Context()); ok2 {
		currentID = sess.ID
	}
	d.Sessions = publicSessions(sessions, currentID)

	tokens, aerr := s.ListUserTokens(r.Context(), u.ID)
	if aerr != nil {
		return d, aerr
	}
	d.Tokens = tokens
	return d, nil
}

// PageAccount renders the account overview.
func (s *Service) PageAccount(w http.ResponseWriter, r *http.Request) {
	d, aerr := s.accountData(r)
	if aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	s.render(w, http.StatusOK, "account", d)
}

// accountFailure re-renders the account page with an error rather than replacing the
// whole page with a bare error, so the user keeps their context.
func (s *Service) accountFailure(w http.ResponseWriter, r *http.Request, cause *apperr.Error) {
	d, aerr := s.accountData(r)
	if aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	d.Error = cause.Message
	s.render(w, cause.HTTPStatus, "account", d)
}

// SubmitProfile saves the editable profile fields.
func (s *Service) SubmitProfile(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		s.Fail(w, r, ErrUnauthenticated())
		return
	}
	if aerr := parseForm(w, r); aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	in := PublicUser{
		DisplayName: formValue(r, "display_name"),
		Bio:         formValue(r, "bio"),
		Website:     formValue(r, "website"),
		Location:    formValue(r, "location"),
		Visibility:  formValue(r, "visibility"),
	}
	if _, aerr := s.UpdateProfile(r.Context(), u.ID, in); aerr != nil {
		s.accountFailure(w, r, aerr)
		return
	}
	s.goBack(w, r, webBasePath+"/account", "auth.profile.saved", "")
}

// SubmitAccountPassword changes the password and re-establishes the current session.
func (s *Service) SubmitAccountPassword(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		s.Fail(w, r, ErrUnauthenticated())
		return
	}
	if aerr := parseForm(w, r); aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	token, aerr := s.ChangePassword(r.Context(), u.ID,
		r.PostFormValue("current_password"), r.PostFormValue("new_password"),
		s.ClientIP(r), r.UserAgent())
	if aerr != nil {
		s.accountFailure(w, r, aerr)
		return
	}
	s.SetSessionCookie(w, token)
	s.goBack(w, r, webBasePath+"/account", "auth.password.changed", "")
}

// SubmitResendVerification sends another verification email.
func (s *Service) SubmitResendVerification(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		s.Fail(w, r, ErrUnauthenticated())
		return
	}
	if aerr := s.ResendEmailVerification(r.Context(), u.ID); aerr != nil {
		s.accountFailure(w, r, aerr)
		return
	}
	s.goBack(w, r, webBasePath+"/account", "auth.email.verification_sent", "")
}

// SubmitTOTPBegin starts enrolment and shows the secret once.
func (s *Service) SubmitTOTPBegin(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		s.Fail(w, r, ErrUnauthenticated())
		return
	}
	secret, uri, aerr := s.BeginTOTP(r.Context(), u.ID)
	if aerr != nil {
		s.accountFailure(w, r, aerr)
		return
	}
	d, derr := s.accountData(r)
	if derr != nil {
		s.Fail(w, r, derr)
		return
	}
	d.TOTP = &totpSetup{Secret: secret, URI: uri}
	d.Notice = Messages["auth.totp.started"]
	s.render(w, http.StatusOK, "account", d)
}

// SubmitTOTPConfirm finishes enrolment.
func (s *Service) SubmitTOTPConfirm(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		s.Fail(w, r, ErrUnauthenticated())
		return
	}
	if aerr := parseForm(w, r); aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	if aerr := s.ConfirmTOTP(r.Context(), u.ID, formValue(r, "code"), s.ClientIP(r)); aerr != nil {
		s.accountFailure(w, r, aerr)
		return
	}
	s.goBack(w, r, webBasePath+"/account", "auth.totp.enabled", "")
}

// SubmitTOTPDisable turns two-factor off after re-authentication.
func (s *Service) SubmitTOTPDisable(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		s.Fail(w, r, ErrUnauthenticated())
		return
	}
	if aerr := parseForm(w, r); aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	if aerr := s.DisableTOTP(r.Context(), u.ID,
		r.PostFormValue("password"), formValue(r, "code")); aerr != nil {
		s.accountFailure(w, r, aerr)
		return
	}
	s.goBack(w, r, webBasePath+"/account", "auth.totp.disabled", "")
}

// SubmitRevokeSession signs out one other session.
func (s *Service) SubmitRevokeSession(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		s.Fail(w, r, ErrUnauthenticated())
		return
	}
	id, aerr := webPathInt(r, "id")
	if aerr != nil {
		s.accountFailure(w, r, aerr)
		return
	}
	if aerr := s.RevokeSession(r.Context(), u.ID, id); aerr != nil {
		s.accountFailure(w, r, aerr)
		return
	}
	s.goBack(w, r, webBasePath+"/account", "auth.session.revoked", "")
}

// SubmitRevokeAllSessions signs out every session except the current one.
func (s *Service) SubmitRevokeAllSessions(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		s.Fail(w, r, ErrUnauthenticated())
		return
	}
	if aerr := s.RevokeAllSessions(r.Context(), u.ID); aerr != nil {
		s.accountFailure(w, r, aerr)
		return
	}
	s.goBack(w, r, webBasePath+"/account", "auth.session.revoked_all", "")
}

// tokenInputFromForm reads the token creation fields from a posted form.
func tokenInputFromForm(r *http.Request) TokenInput {
	in := TokenInput{
		Name:   formValue(r, "name"),
		Scopes: formList(r, "scopes"),
	}
	if days := formInt(r, "expires_in_days"); days > 0 {
		in.ExpiresAt = time.Now().Unix() + days*86400
	}
	return in
}

// SubmitCreateToken issues a personal token and shows it once.
func (s *Service) SubmitCreateToken(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		s.Fail(w, r, ErrUnauthenticated())
		return
	}
	if aerr := parseForm(w, r); aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	token, aerr := s.CreateUserToken(r.Context(), u.ID, tokenInputFromForm(r))
	if aerr != nil {
		s.accountFailure(w, r, aerr)
		return
	}
	d, derr := s.accountData(r)
	if derr != nil {
		s.Fail(w, r, derr)
		return
	}
	d.NewToken = token.Token
	s.render(w, http.StatusCreated, "account", d)
}

// SubmitRevokeToken revokes a personal token.
func (s *Service) SubmitRevokeToken(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		s.Fail(w, r, ErrUnauthenticated())
		return
	}
	id, aerr := webPathInt(r, "id")
	if aerr != nil {
		s.accountFailure(w, r, aerr)
		return
	}
	if aerr := s.RevokeUserToken(r.Context(), u.ID, id); aerr != nil {
		s.accountFailure(w, r, aerr)
		return
	}
	s.goBack(w, r, webBasePath+"/account", "auth.token.revoked", "")
}

// SubmitDeleteAccount closes the account after a typed confirmation.
func (s *Service) SubmitDeleteAccount(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		s.Fail(w, r, ErrUnauthenticated())
		return
	}
	if aerr := parseForm(w, r); aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	if formValue(r, "confirm") != u.Username {
		s.accountFailure(w, r, ErrValidation("confirm", "Type your username exactly to confirm."))
		return
	}
	if aerr := s.DeleteAccount(r.Context(), u.ID, r.PostFormValue("password")); aerr != nil {
		s.accountFailure(w, r, aerr)
		return
	}
	s.ClearSessionCookie(w)
	s.goBack(w, r, webBasePath+"/login", "auth.account.deleted", "")
}

// PageOrgs lists the caller's organizations and offers the creation form.
func (s *Service) PageOrgs(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		s.Fail(w, r, ErrUnauthenticated())
		return
	}
	d := s.base(r, "Organizations", "Organizations")
	orgs, aerr := s.ListUserOrgs(r.Context(), u.ID)
	if aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	d.Orgs = orgs
	s.render(w, http.StatusOK, "orgs", d)
}

// SubmitCreateOrg creates an organization owned by the caller.
func (s *Service) SubmitCreateOrg(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		s.Fail(w, r, ErrUnauthenticated())
		return
	}
	if aerr := parseForm(w, r); aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	in := OrgInput{
		Slug:        formValue(r, "slug"),
		Name:        formValue(r, "name"),
		Description: formValue(r, "description"),
		Website:     formValue(r, "website"),
		Location:    formValue(r, "location"),
		Visibility:  formValue(r, "visibility"),
	}
	org, aerr := s.CreateOrg(r.Context(), u.ID, in, formValue(r, "invite"))
	if aerr != nil {
		d := s.base(r, "Organizations", "Organizations")
		if orgs, lerr := s.ListUserOrgs(r.Context(), u.ID); lerr == nil {
			d.Orgs = orgs
		}
		d.Error = aerr.Message
		s.render(w, aerr.HTTPStatus, "orgs", d)
		return
	}
	s.goBack(w, r, webBasePath+"/orgs/"+org.Slug, "org.created", "")
}

// orgPageData assembles the organization page for the org already resolved by
// RequireOrgRole, which proved the caller is a member before this runs.
func (s *Service) orgPageData(r *http.Request) (pageData, *apperr.Error) {
	d := s.base(r, "Organization", "Organization")
	u, org, aerr := orgContext(r)
	if aerr != nil {
		return d, aerr
	}
	view, aerr := s.ViewOrg(r.Context(), org.Slug, u.ID)
	if aerr != nil {
		return d, aerr
	}
	d.Org = &view
	d.Title = view.Name
	d.Heading = view.Name
	d.ActionBase = webBasePath + "/orgs/" + view.Slug

	members, aerr := s.ListOrgMembers(r.Context(), org.ID)
	if aerr != nil {
		return d, aerr
	}
	d.Members = publicMembers(members)

	if roleAtLeast(OrgRoleFrom(r.Context()), OrgRoleAdmin) {
		invites, ierr := s.ListOrgInvites(r.Context(), org.ID)
		if ierr != nil {
			return d, ierr
		}
		d.Invites = publicInvites(invites)

		tokens, terr := s.ListOrgTokens(r.Context(), org.ID)
		if terr != nil {
			return d, terr
		}
		d.Tokens = tokens
	}
	return d, nil
}

// PageOrg renders one organization.
func (s *Service) PageOrg(w http.ResponseWriter, r *http.Request) {
	d, aerr := s.orgPageData(r)
	if aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	s.render(w, http.StatusOK, "org", d)
}

// orgFailure re-renders the organization page carrying an error.
func (s *Service) orgFailure(w http.ResponseWriter, r *http.Request, cause *apperr.Error) {
	d, aerr := s.orgPageData(r)
	if aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	d.Error = cause.Message
	s.render(w, cause.HTTPStatus, "org", d)
}

// orgPath returns the page path for the organization on this request.
func orgPath(r *http.Request) string {
	return webBasePath + "/orgs/" + OrgSlugFrom(r)
}

// SubmitOrgSettings saves the organization profile.
func (s *Service) SubmitOrgSettings(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	if perr := parseForm(w, r); perr != nil {
		s.Fail(w, r, perr)
		return
	}
	in := OrgInput{
		Slug:        org.Slug,
		Name:        formValue(r, "name"),
		Description: formValue(r, "description"),
		Website:     formValue(r, "website"),
		Location:    formValue(r, "location"),
		Visibility:  formValue(r, "visibility"),
	}
	if _, uerr := s.UpdateOrg(r.Context(), org.ID, u.ID, in); uerr != nil {
		s.orgFailure(w, r, uerr)
		return
	}
	s.goBack(w, r, orgPath(r), "org.saved", "")
}

// SubmitDeleteOrg deletes the organization after a typed confirmation.
func (s *Service) SubmitDeleteOrg(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	if perr := parseForm(w, r); perr != nil {
		s.Fail(w, r, perr)
		return
	}
	if derr := s.DeleteOrg(r.Context(), org.ID, u.ID, formValue(r, "confirm")); derr != nil {
		s.orgFailure(w, r, derr)
		return
	}
	s.goBack(w, r, webBasePath+"/orgs", "org.deleted", "")
}

// SubmitTransferOrg hands ownership to another member.
func (s *Service) SubmitTransferOrg(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	if perr := parseForm(w, r); perr != nil {
		s.Fail(w, r, perr)
		return
	}
	target, terr := strconv.ParseInt(formValue(r, "user_id"), 10, 64)
	if terr != nil || target <= 0 {
		s.orgFailure(w, r, ErrValidation("user_id", "Choose the member who should become the owner."))
		return
	}
	if xerr := s.TransferOrgOwnership(r.Context(), org.ID, u.ID, target); xerr != nil {
		s.orgFailure(w, r, xerr)
		return
	}
	s.goBack(w, r, orgPath(r), "org.transferred", "")
}

// SubmitAddMember adds an existing user to the organization.
func (s *Service) SubmitAddMember(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	if perr := parseForm(w, r); perr != nil {
		s.Fail(w, r, perr)
		return
	}
	if merr := s.AddOrgMember(r.Context(), org.ID, u.ID,
		formValue(r, "username"), formValue(r, "role")); merr != nil {
		s.orgFailure(w, r, merr)
		return
	}
	s.goBack(w, r, orgPath(r), "org.member.added", "")
}

// SubmitMemberRole changes one member's role.
func (s *Service) SubmitMemberRole(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	if perr := parseForm(w, r); perr != nil {
		s.Fail(w, r, perr)
		return
	}
	target, terr := webPathInt(r, "id")
	if terr != nil {
		s.orgFailure(w, r, terr)
		return
	}
	if rerr := s.SetOrgMemberRole(r.Context(), org.ID, u.ID, target, formValue(r, "role")); rerr != nil {
		s.orgFailure(w, r, rerr)
		return
	}
	s.goBack(w, r, orgPath(r), "org.member.role_set", "")
}

// SubmitRemoveMember removes a member. A member may always remove themselves.
func (s *Service) SubmitRemoveMember(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	target, terr := webPathInt(r, "id")
	if terr != nil {
		s.orgFailure(w, r, terr)
		return
	}
	if rerr := s.RemoveOrgMember(r.Context(), org.ID, u.ID, target); rerr != nil {
		s.orgFailure(w, r, rerr)
		return
	}
	if target == u.ID {
		s.goBack(w, r, webBasePath+"/orgs", "org.member.left", "")
		return
	}
	s.goBack(w, r, orgPath(r), "org.member.removed", "")
}

// SubmitCreateOrgInvite issues an invitation and shows the code once.
func (s *Service) SubmitCreateOrgInvite(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	if perr := parseForm(w, r); perr != nil {
		s.Fail(w, r, perr)
		return
	}
	code, ierr := s.InviteOrgMember(r.Context(), org.ID, u.ID,
		formValue(r, "email"), formValue(r, "role"))
	if ierr != nil {
		s.orgFailure(w, r, ierr)
		return
	}
	d, derr := s.orgPageData(r)
	if derr != nil {
		s.Fail(w, r, derr)
		return
	}
	d.NewInvite = code
	d.Notice = Messages["org.invite.sent"]
	s.render(w, http.StatusCreated, "org", d)
}

// SubmitRevokeOrgInvite cancels an outstanding invitation.
func (s *Service) SubmitRevokeOrgInvite(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	id, ierr := webPathInt(r, "id")
	if ierr != nil {
		s.orgFailure(w, r, ierr)
		return
	}
	if rerr := s.RevokeOrgInvite(r.Context(), org.ID, u.ID, id); rerr != nil {
		s.orgFailure(w, r, rerr)
		return
	}
	s.goBack(w, r, orgPath(r), "org.invite.revoked", "")
}

// SubmitCreateOrgToken issues an organization token and shows it once.
func (s *Service) SubmitCreateOrgToken(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	if perr := parseForm(w, r); perr != nil {
		s.Fail(w, r, perr)
		return
	}
	token, terr := s.CreateOrgToken(r.Context(), org.ID, u.ID, tokenInputFromForm(r))
	if terr != nil {
		s.orgFailure(w, r, terr)
		return
	}
	d, derr := s.orgPageData(r)
	if derr != nil {
		s.Fail(w, r, derr)
		return
	}
	d.NewToken = token.Token
	s.render(w, http.StatusCreated, "org", d)
}

// SubmitRevokeOrgToken revokes an organization token.
func (s *Service) SubmitRevokeOrgToken(w http.ResponseWriter, r *http.Request) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	id, ierr := webPathInt(r, "id")
	if ierr != nil {
		s.orgFailure(w, r, ierr)
		return
	}
	if rerr := s.RevokeOrgToken(r.Context(), org.ID, u.ID, id); rerr != nil {
		s.orgFailure(w, r, rerr)
		return
	}
	s.goBack(w, r, orgPath(r), "auth.token.revoked", "")
}

// PageAcceptInvite renders the invitation form, pre-filled from the link.
func (s *Service) PageAcceptInvite(w http.ResponseWriter, r *http.Request) {
	d := s.base(r, "Accept invitation", "Accept an invitation")
	d.Next = r.URL.Query().Get("code")
	s.render(w, http.StatusOK, "invite", d)
}

// SubmitAcceptInvite redeems an organization invitation.
func (s *Service) SubmitAcceptInvite(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		s.Fail(w, r, ErrUnauthenticated())
		return
	}
	if aerr := parseForm(w, r); aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	code := formValue(r, "code")
	org, aerr := s.AcceptOrgInvite(r.Context(), u.ID, code)
	if aerr != nil {
		d := s.base(r, "Accept invitation", "Accept an invitation")
		d.Next = code
		d.Error = aerr.Message
		s.render(w, aerr.HTTPStatus, "invite", d)
		return
	}
	s.goBack(w, r, webBasePath+"/orgs/"+org.Slug, "org.invite.accepted", "")
}

// domainsPageData assembles the domain page for one owner.
func (s *Service) domainsPageData(r *http.Request, resolve domainOwnerFunc, actionBase string) (pageData, *apperr.Error) {
	d := s.base(r, "Custom domains", "Custom domains")
	d.ActionBase = actionBase
	owner, _, aerr := resolve(r)
	if aerr != nil {
		return d, aerr
	}
	rows, aerr := s.ListDomains(r.Context(), owner)
	if aerr != nil {
		return d, aerr
	}
	d.Domains = s.publicDomains(rows)
	return d, nil
}

// domainPage renders the domain page for one owner kind.
func (s *Service) domainPage(resolve domainOwnerFunc, actionBase func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, aerr := s.domainsPageData(r, resolve, actionBase(r))
		if aerr != nil {
			s.Fail(w, r, aerr)
			return
		}
		s.render(w, http.StatusOK, "domains", d)
	}
}

// domainFailure re-renders the domain page carrying an error.
func (s *Service) domainFailure(w http.ResponseWriter, r *http.Request, resolve domainOwnerFunc,
	actionBase string, cause *apperr.Error) {
	d, aerr := s.domainsPageData(r, resolve, actionBase)
	if aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	d.Error = cause.Message
	s.render(w, cause.HTTPStatus, "domains", d)
}

// domainAdd registers a domain and returns to the listing with the DNS instructions.
func (s *Service) domainAdd(resolve domainOwnerFunc, actionBase func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		base := actionBase(r)
		owner, actorID, aerr := resolve(r)
		if aerr != nil {
			s.Fail(w, r, aerr)
			return
		}
		if perr := parseForm(w, r); perr != nil {
			s.Fail(w, r, perr)
			return
		}
		if _, derr := s.AddDomain(r.Context(), owner, actorID, formValue(r, "domain")); derr != nil {
			s.domainFailure(w, r, resolve, base, derr)
			return
		}
		s.goBack(w, r, base+"/domains", "domain.added", "")
	}
}

// domainVerify runs the TXT ownership check on demand.
func (s *Service) domainVerify(resolve domainOwnerFunc, actionBase func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		base := actionBase(r)
		owner, actorID, aerr := resolve(r)
		if aerr != nil {
			s.Fail(w, r, aerr)
			return
		}
		d, derr := s.VerifyDomain(r.Context(), owner, actorID, r.PathValue("domain"))
		if derr != nil {
			s.domainFailure(w, r, resolve, base, derr)
			return
		}
		key := "domain.pending"
		if d.VerificationStatus == VerificationVerified {
			key = "domain.verified"
		}
		s.goBack(w, r, base+"/domains", key, "")
	}
}

// domainDelete removes a domain from its owner.
func (s *Service) domainDelete(resolve domainOwnerFunc, actionBase func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		base := actionBase(r)
		owner, actorID, aerr := resolve(r)
		if aerr != nil {
			s.Fail(w, r, aerr)
			return
		}
		if derr := s.DeleteDomain(r.Context(), owner, actorID, r.PathValue("domain")); derr != nil {
			s.domainFailure(w, r, resolve, base, derr)
			return
		}
		s.goBack(w, r, base+"/domains", "domain.removed", "")
	}
}

// userDomainBase is the action prefix for a user's own domains.
func userDomainBase(*http.Request) string { return webBasePath }

// orgDomainBase is the action prefix for an organization's domains.
func orgDomainBase(r *http.Request) string { return orgPath(r) }

// PageAdminSetup renders the one-time primary administrator form.
func (s *Service) PageAdminSetup(w http.ResponseWriter, r *http.Request) {
	needed, err := s.BootstrapNeeded(r.Context())
	if err != nil {
		s.Fail(w, r, ErrInternal(err))
		return
	}
	if !needed {
		http.Redirect(w, r, "/"+s.cfg.AdminPath+"/login", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "admin_setup", s.base(r, "Server setup", "Create the first administrator"))
}

// SubmitAdminSetup redeems the bootstrap token and creates the primary administrator.
func (s *Service) SubmitAdminSetup(w http.ResponseWriter, r *http.Request) {
	if aerr := parseForm(w, r); aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	in := BootstrapInput{
		SetupToken: formValue(r, "bootstrap_token"),
		Username:   formValue(r, "username"),
		Email:      formValue(r, "email"),
		Password:   r.PostFormValue("password"),
		IP:         s.ClientIP(r),
		UserAgent:  r.UserAgent(),
	}
	_, token, aerr := s.CompleteBootstrap(r.Context(), in)
	if aerr != nil {
		d := s.base(r, "Server setup", "Create the first administrator")
		d.Error = aerr.Message
		s.render(w, aerr.HTTPStatus, "admin_setup", d)
		return
	}
	s.SetAdminCookie(w, token)
	s.goBack(w, r, "/"+s.cfg.AdminPath+"/", "admin.bootstrapped", "")
}

// PageAdminLogin renders the administrator sign-in form.
func (s *Service) PageAdminLogin(w http.ResponseWriter, r *http.Request) {
	needed, err := s.BootstrapNeeded(r.Context())
	if err != nil {
		s.Fail(w, r, ErrInternal(err))
		return
	}
	if needed {
		http.Redirect(w, r, "/"+s.cfg.AdminPath+"/setup", http.StatusSeeOther)
		return
	}
	d := s.base(r, "Administrator sign in", "Administrator sign in")
	d.Next = SafeNext(r.URL.Query().Get("next"), "")
	s.render(w, http.StatusOK, "admin_login", d)
}

// SubmitAdminLogin authenticates an administrator.
func (s *Service) SubmitAdminLogin(w http.ResponseWriter, r *http.Request) {
	if aerr := parseForm(w, r); aerr != nil {
		s.Fail(w, r, aerr)
		return
	}
	in := LoginInput{
		Identifier: formValue(r, "username"),
		Password:   r.PostFormValue("password"),
		TOTPCode:   formValue(r, "totp_code"),
		IP:         s.ClientIP(r),
		UserAgent:  r.UserAgent(),
	}
	_, token, aerr := s.AdminLogin(r.Context(), in)
	if aerr != nil {
		d := s.base(r, "Administrator sign in", "Administrator sign in")
		d.Next = SafeNext(formValue(r, "next"), "")
		d.Error = aerr.Message
		s.render(w, aerr.HTTPStatus, "admin_login", d)
		return
	}
	s.SetAdminCookie(w, token)
	http.Redirect(w, r, SafeNext(formValue(r, "next"), "/"+s.cfg.AdminPath+"/"), http.StatusSeeOther)
}

// SubmitAdminLogout ends the administrator session.
func (s *Service) SubmitAdminLogout(w http.ResponseWriter, r *http.Request) {
	s.AdminLogout(r.Context(), cookieValue(r, AdminCookieName))
	s.ClearAdminCookie(w)
	s.goBack(w, r, "/"+s.cfg.AdminPath+"/login", "auth.signed_out", "")
}
