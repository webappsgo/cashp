package auth

import (
	"net/http"
	"strings"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// registerBody is the account creation request.
type registerBody struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Invite   string `json:"invite,omitempty"`
}

// HandleRegister creates an account and signs the new user in.
func (s *Service) HandleRegister(w http.ResponseWriter, r *http.Request) {
	var body registerBody
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Username = r.PostFormValue("username")
		body.Email = r.PostFormValue("email")
		body.Password = r.PostFormValue("password")
		body.Invite = r.PostFormValue("invite")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	u, token, aerr := s.Register(r.Context(), RegisterInput{
		Username:   body.Username,
		Email:      body.Email,
		Password:   body.Password,
		InviteCode: body.Invite,
		IP:         s.ClientIP(r),
		UserAgent:  r.UserAgent(),
	})
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	if token != "" {
		s.SetSessionCookie(w, token)
	}
	created(w, r, u.Public())
}

// checkNameQuery is the availability probe response.
type checkNameQuery struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

// HandleCheckName reports whether a name may be claimed. A taken name and a tombstoned
// name are both reported as merely unavailable, so this endpoint cannot be used to
// enumerate existing or deleted accounts; only a blocklisted name is named as reserved.
func (s *Service) HandleCheckName(w http.ResponseWriter, r *http.Request) {
	name := NormalizeName(r.URL.Query().Get("name"))
	if aerr := s.CheckName(r.Context(), name); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, checkNameQuery{Name: name, Available: true})
}

// loginBody is the sign-in request.
type loginBody struct {
	Login    string `json:"login"`
	Password string `json:"password"`
	Code     string `json:"code,omitempty"`
}

// HandleLogin authenticates a user and issues a session.
func (s *Service) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var body loginBody
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Login = r.PostFormValue("login")
		body.Password = r.PostFormValue("password")
		body.Code = r.PostFormValue("code")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	u, token, aerr := s.Login(r.Context(), LoginInput{
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
	s.SetSessionCookie(w, token)
	ok(w, r, u.Public())
}

// HandleLogout ends the caller's session.
func (s *Service) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookieName); err == nil {
		s.Logout(r.Context(), c.Value)
	}
	s.ClearSessionCookie(w)
	ok(w, r, messageOnly{Message: "Signed out"})
}

// HandleMe returns the signed-in user's own profile.
func (s *Service) HandleMe(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	ok(w, r, u.Profile())
}

// HandleUpdateMe writes the editable profile fields.
func (s *Service) HandleUpdateMe(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	var body PublicUser
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.DisplayName = r.PostFormValue("display_name")
		body.Bio = r.PostFormValue("bio")
		body.Location = r.PostFormValue("location")
		body.Website = r.PostFormValue("website")
		body.AvatarURL = r.PostFormValue("avatar_url")
		body.Visibility = r.PostFormValue("visibility")
		body.Timezone = r.PostFormValue("timezone")
		body.Language = r.PostFormValue("language")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	updated, aerr := s.UpdateProfile(r.Context(), u.ID, body)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, updated.Profile())
}

// HandleDeleteMe closes the caller's account after a password confirmation.
func (s *Service) HandleDeleteMe(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Password = r.PostFormValue("password")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	if aerr := s.DeleteAccount(r.Context(), u.ID, body.Password); aerr != nil {
		fail(w, r, aerr)
		return
	}
	s.ClearSessionCookie(w)
	ok(w, r, messageOnly{Message: "Account closed"})
}

// changePasswordBody is the password change request.
type changePasswordBody struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// HandleChangePassword rotates the caller's password and re-issues their session.
func (s *Service) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
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
	token, aerr := s.ChangePassword(r.Context(), u.ID,
		body.CurrentPassword, body.NewPassword, s.ClientIP(r), r.UserAgent())
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	s.SetSessionCookie(w, token)
	ok(w, r, messageOnly{Message: "Password changed. Every other session was signed out"})
}

// HandleRequestPasswordReset starts a reset. The response is identical whether or not
// the address is registered, so it cannot be used to test for an account.
func (s *Service) HandleRequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Email = r.PostFormValue("email")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	if aerr := s.RequestPasswordReset(r.Context(), body.Email, s.ClientIP(r)); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "If that address has an account, a reset link is on its way"})
}

// HandleConfirmPasswordReset completes a reset using the emailed token.
func (s *Service) HandleConfirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if aerr := bind(w, r, &body, func(r *http.Request) {
		body.Token = r.PostFormValue("token")
		body.NewPassword = r.PostFormValue("new_password")
	}); aerr != nil {
		fail(w, r, aerr)
		return
	}
	if aerr := s.ConfirmPasswordReset(r.Context(), body.Token, body.NewPassword, s.ClientIP(r)); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Password updated. Sign in with your new password"})
}

// HandleVerifyEmail confirms an address from the emailed token.
func (s *Service) HandleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" && r.Method == http.MethodPost {
		if aerr := parseForm(w, r); aerr != nil {
			fail(w, r, aerr)
			return
		}
		token = strings.TrimSpace(r.PostFormValue("token"))
	}
	if aerr := s.VerifyEmail(r.Context(), token, s.ClientIP(r)); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Email address confirmed"})
}

// HandleResendVerification sends a fresh confirmation email.
func (s *Service) HandleResendVerification(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	if aerr := s.ResendEmailVerification(r.Context(), u.ID); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Confirmation email sent"})
}

// totpSetup is the enrolment response. The secret is disclosed only to the account it
// belongs to, and only until enrolment is confirmed.
type totpSetup struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
}

// HandleBeginTOTP starts two-factor enrolment.
func (s *Service) HandleBeginTOTP(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	secret, uri, aerr := s.BeginTOTP(r.Context(), u.ID)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, totpSetup{Secret: secret, URI: uri})
}

// HandleConfirmTOTP activates two-factor after a correct code.
func (s *Service) HandleConfirmTOTP(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
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
	if aerr := s.ConfirmTOTP(r.Context(), u.ID, body.Code, s.ClientIP(r)); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Two-factor authentication is on"})
}

// HandleDisableTOTP turns two-factor off. Both the password and a current code are
// required, so a stolen session alone cannot remove the second factor.
func (s *Service) HandleDisableTOTP(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
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
	if aerr := s.DisableTOTP(r.Context(), u.ID, body.Password, body.Code); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Two-factor authentication is off"})
}

// HandleListSessions returns the caller's active sessions.
func (s *Service) HandleListSessions(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	rows, aerr := s.ListSessions(r.Context(), u.ID)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	currentID := int64(0)
	if sess, hasSession := SessionFrom(r.Context()); hasSession {
		currentID = sess.ID
	}
	ok(w, r, publicSessions(rows, currentID))
}

// HandleRevokeSession ends one of the caller's own sessions.
func (s *Service) HandleRevokeSession(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	if aerr := s.RevokeSession(r.Context(), u.ID, pathInt(r, "id")); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Session ended"})
}

// HandleRevokeAllSessions ends every session the caller holds, including this one.
func (s *Service) HandleRevokeAllSessions(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	if aerr := s.RevokeAllSessions(r.Context(), u.ID); aerr != nil {
		fail(w, r, aerr)
		return
	}
	s.ClearSessionCookie(w)
	ok(w, r, messageOnly{Message: "All sessions ended"})
}

// HandleListTokens returns the caller's API tokens.
func (s *Service) HandleListTokens(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	rows, aerr := s.ListUserTokens(r.Context(), u.ID)
	if aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, rows)
}

// tokenBody is the token creation request.
type tokenBody struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ExpiresAt int64    `json:"expires_at,omitempty"`
}

// bindTokenBody fills a token request from JSON or a form.
func bindTokenBody(w http.ResponseWriter, r *http.Request, body *tokenBody) *apperr.Error {
	return bind(w, r, body, func(r *http.Request) {
		body.Name = r.PostFormValue("name")
		body.Scopes = formList(r, "scopes")
		body.ExpiresAt = formInt(r, "expires_at")
	})
}

// HandleCreateToken mints a user token. The plaintext appears in this response only.
func (s *Service) HandleCreateToken(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	var body tokenBody
	if aerr := bindTokenBody(w, r, &body); aerr != nil {
		fail(w, r, aerr)
		return
	}
	out, aerr := s.CreateUserToken(r.Context(), u.ID, TokenInput{
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

// HandleRevokeToken revokes one of the caller's tokens.
func (s *Service) HandleRevokeToken(w http.ResponseWriter, r *http.Request) {
	u, found := UserFrom(r.Context())
	if !found {
		fail(w, r, ErrUnauthenticated())
		return
	}
	if aerr := s.RevokeUserToken(r.Context(), u.ID, pathInt(r, "id")); aerr != nil {
		fail(w, r, aerr)
		return
	}
	ok(w, r, messageOnly{Message: "Token revoked"})
}
