package auth

import (
	"net/http"
	"strconv"
)

// bootstrapBody is the primary-admin creation request.
type bootstrapBody struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// HandleBootstrapStatus reports whether the first Server Admin still has to be created.
// It leaks nothing beyond that single fact, which the sign-in page needs anyway.
func (s *Service) HandleBootstrapStatus(w http.ResponseWriter, r *http.Request) {
	needed, err := s.BootstrapNeeded(r.Context())
	if err != nil {
		fail(w, r, ErrInternal(err))
		return
	}
	ok(w, r, struct {
		BootstrapRequired bool `json:"bootstrap_required"`
	}{BootstrapRequired: needed})
}

// HandleBootstrap redeems the single-use setup token and creates the primary admin.
// The token is minted at first start and printed to the server console only, so an
// attacker who can reach the endpoint but not the console can never complete this.
func (s *Service) HandleBootstrap(w http.ResponseWriter, r *http.Request) {
	var body bootstrapBody
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Token = r.PostFormValue("token")
		body.Username = r.PostFormValue("username")
		body.Email = r.PostFormValue("email")
		body.Password = r.PostFormValue("password")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	admin, session, aerr := s.CompleteBootstrap(r.Context(), BootstrapInput{
		SetupToken: body.Token,
		Username:   body.Username,
		Email:      body.Email,
		Password:   body.Password,
		IP:         s.ClientIP(r),
		UserAgent:  r.UserAgent(),
	})
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	s.SetAdminCookie(w, session)
	created(w, r, admin.Public())
}

// HandleAdminLogin authenticates a Server Admin.
func (s *Service) HandleAdminLogin(w http.ResponseWriter, r *http.Request) {
	var body loginBody
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Login = r.PostFormValue("login")
		body.Password = r.PostFormValue("password")
		body.Code = r.PostFormValue("code")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	admin, session, aerr := s.AdminLogin(r.Context(), LoginInput{
		Identifier: body.Login,
		Password:   body.Password,
		TOTPCode:   body.Code,
		IP:         s.ClientIP(r),
		UserAgent:  r.UserAgent(),
	})
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	s.SetAdminCookie(w, session)
	ok(w, r, admin.Public())
}

// HandleAdminLogout ends the admin session.
func (s *Service) HandleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(AdminCookieName); err == nil {
		s.AdminLogout(r.Context(), c.Value)
	}
	s.ClearAdminCookie(w)
	ok(w, r, messageOnly{Message: "Signed out"})
}

// HandleAdminMe returns the signed-in Server Admin.
func (s *Service) HandleAdminMe(w http.ResponseWriter, r *http.Request) {
	admin, found := AdminFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	ok(w, r, admin.Public())
}

// HandleAdminChangePassword rotates the admin's password and re-issues the session.
func (s *Service) HandleAdminChangePassword(w http.ResponseWriter, r *http.Request) {
	admin, found := AdminFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	var body changePasswordBody
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.CurrentPassword = r.PostFormValue("current_password")
		body.NewPassword = r.PostFormValue("new_password")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	session, aerr := s.ChangeAdminPassword(r.Context(), admin.ID,
		body.CurrentPassword, body.NewPassword, s.ClientIP(r), r.UserAgent())
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	s.SetAdminCookie(w, session)
	ok(w, r, messageOnly{Message: "Password changed. Every other admin session was signed out"})
}

// HandleAdminBeginTOTP starts admin two-factor enrolment.
func (s *Service) HandleAdminBeginTOTP(w http.ResponseWriter, r *http.Request) {
	admin, found := AdminFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	secret, uri, aerr := s.BeginAdminTOTP(r.Context(), admin.ID)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, totpSetup{Secret: secret, URI: uri})
}

// HandleAdminConfirmTOTP activates admin two-factor.
func (s *Service) HandleAdminConfirmTOTP(w http.ResponseWriter, r *http.Request) {
	admin, found := AdminFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Code = r.PostFormValue("code")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	if aerr := s.ConfirmAdminTOTP(r.Context(), admin.ID, body.Code, s.ClientIP(r)); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Two-factor authentication is on"})
}

// HandleAdminDisableTOTP turns admin two-factor off.
func (s *Service) HandleAdminDisableTOTP(w http.ResponseWriter, r *http.Request) {
	admin, found := AdminFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Password = r.PostFormValue("password")
		body.Code = r.PostFormValue("code")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	if aerr := s.DisableAdminTOTP(r.Context(), admin.ID, body.Password, body.Code); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Two-factor authentication is off"})
}

// HandleAdminCreateAdmin adds another Server Admin.
func (s *Service) HandleAdminCreateAdmin(w http.ResponseWriter, r *http.Request) {
	admin, found := AdminFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	var body struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Username = r.PostFormValue("username")
		body.Email = r.PostFormValue("email")
		body.Password = r.PostFormValue("password")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	out, aerr := s.CreateAdmin(r.Context(), admin.ID, body.Username, body.Email, body.Password)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	created(w, r, out.Public())
}

// HandleAdminListUsers pages through every account.
func (s *Service) HandleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	limit, offset := pageParams(r)
	rows, err := s.store.ListUsers(r.Context(), limit, offset)
	if err != nil {
		fail(w, r, ErrInternal(err))
		return
	}
	out := make([]PublicUser, 0, len(rows))
	for _, u := range rows {
		out = append(out, u.Profile())
	}
	ok(w, r, out)
}

// HandleAdminSetUserFlags approves, suspends or reinstates an account.
func (s *Service) HandleAdminSetUserFlags(w http.ResponseWriter, r *http.Request) {
	admin, found := AdminFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	var body struct {
		Approved bool `json:"approved"`
		Disabled bool `json:"disabled"`
	}
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Approved = formBool(r, "approved")
		body.Disabled = formBool(r, "disabled")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	if aerr := s.SetUserApproval(r.Context(), admin.ID, pathInt(r, "id"), body.Approved, body.Disabled); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Account updated"})
}

// HandleAdminSuspendOrg suspends or reinstates an organization.
func (s *Service) HandleAdminSuspendOrg(w http.ResponseWriter, r *http.Request) {
	admin, found := AdminFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	var body struct {
		Suspended bool `json:"suspended"`
	}
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Suspended = formBool(r, "suspended")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	if aerr := s.SuspendOrg(r.Context(), admin.ID, pathInt(r, "id"), body.Suspended); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Organization updated"})
}

// HandleAdminActivateDomain approves a verified domain when approval is required.
func (s *Service) HandleAdminActivateDomain(w http.ResponseWriter, r *http.Request) {
	admin, found := AdminFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	if aerr := s.ActivateDomain(r.Context(), admin.ID, pathInt(r, "id")); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Domain activated"})
}

// HandleAdminSuspendDomain parks a domain on abuse.
func (s *Service) HandleAdminSuspendDomain(w http.ResponseWriter, r *http.Request) {
	admin, found := AdminFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Reason = r.PostFormValue("reason")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	if aerr := s.SuspendDomain(r.Context(), admin.ID, pathInt(r, "id"), body.Reason); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Domain suspended"})
}

// HandleAdminCreateInvite issues a registration invite for a closed sign-up server.
func (s *Service) HandleAdminCreateInvite(w http.ResponseWriter, r *http.Request) {
	admin, found := AdminFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	var body struct {
		Email   string `json:"email,omitempty"`
		MaxUses int    `json:"max_uses,omitempty"`
	}
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Email = r.PostFormValue("email")
		body.MaxUses = int(formInt(r, "max_uses"))
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	if body.MaxUses == 0 {
		body.MaxUses = 1
	}
	code, aerr := s.CreateUserInvite(r.Context(), admin.ID, body.Email, body.MaxUses)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	created(w, r, PublicInvite{Email: NormalizeEmail(body.Email), MaxUses: body.MaxUses, Code: code})
}

// pageParams reads bounded paging values. The limit is clamped so a caller cannot ask
// the database for an unbounded result set.
func pageParams(r *http.Request) (limit, offset int) {
	limit = 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > 200 {
		limit = 200
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
		offset = v
	}
	return limit, offset
}
