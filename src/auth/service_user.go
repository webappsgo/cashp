package auth

import (
	"context"
	"log/slog"
	"strings"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/security"
)

// RegisterInput carries a self-service registration request.
type RegisterInput struct {
	Username   string
	Email      string
	Password   string
	InviteCode string
	IP         string
	UserAgent  string
}

// LoginInput carries a credential check. Identifier may be a user ID, an email, or a
// username; the same generic failure is returned no matter which form was used.
type LoginInput struct {
	Identifier string
	Password   string
	TOTPCode   string
	IP         string
	UserAgent  string
}

// CheckName reports whether a username or org slug may be registered.
// Only the static blocklist produces a specific answer. Taken names and tombstoned
// names both return the same vague "unavailable", so the endpoint cannot be used to
// enumerate which accounts exist or which ones once existed.
func (s *Service) CheckName(ctx context.Context, name string) *apperr.Error {
	name = NormalizeName(name)
	if err := ValidateUsernameFormat(name); err != nil {
		return ErrValidation("username", err.Error())
	}
	if IsBlockedName(name) {
		return ErrNameReserved("username")
	}
	taken, err := s.store.NameTaken(ctx, name)
	if err != nil {
		return ErrInternal(err)
	}
	tombstoned, err := s.store.NameTombstoned(ctx, name)
	if err != nil {
		return ErrInternal(err)
	}
	if taken || tombstoned {
		return ErrNameUnavailable("username")
	}
	return nil
}

// Register creates an account and returns it together with the plaintext session
// token the caller must place in the session cookie.
func (s *Service) Register(ctx context.Context, in RegisterInput) (*User, string, *apperr.Error) {
	if !s.cfg.UsersEnabled {
		return nil, "", ErrFeatureDisabled("User accounts")
	}
	if ok, retry := s.limits.Allow(security.LimitRegistration, in.IP); !ok {
		return nil, "", ErrRateLimited(int(retry.Seconds()))
	}

	username := NormalizeName(in.Username)
	email := NormalizeEmail(in.Email)

	if err := ValidateUsernameFormat(username); err != nil {
		return nil, "", ErrValidation("username", err.Error())
	}
	if err := ValidateEmail(email); err != nil {
		return nil, "", ErrValidation("email", err.Error())
	}
	if err := ValidatePassword(in.Password); err != nil {
		return nil, "", ErrValidation("password", err.Error())
	}

	var invite *Invite
	switch s.cfg.RegistrationMode {
	case RegistrationDisabled, RegistrationAdminOnly:
		return nil, "", ErrRegistrationClosed()
	case RegistrationInvite:
		if strings.TrimSpace(in.InviteCode) == "" {
			return nil, "", ErrInviteRequired()
		}
		found, err := s.store.InviteByHash(ctx, security.HashToken(in.InviteCode))
		if err != nil || !found.Usable() || found.Kind != InviteKindUser {
			return nil, "", ErrInviteInvalid()
		}
		if found.Email != "" && found.Email != email {
			return nil, "", ErrInviteInvalid()
		}
		invite = found
	}

	if nameErr := s.CheckName(ctx, username); nameErr != nil {
		return nil, "", nameErr
	}

	// An address that is already registered gets the same generic conflict as a taken
	// username. Nothing in the response names the address or confirms it exists.
	taken, err := s.store.EmailTaken(ctx, email)
	if err != nil {
		return nil, "", ErrInternal(err)
	}
	if taken {
		return nil, "", ErrNameUnavailable("email")
	}

	hash, err := security.HashPassword(in.Password)
	if err != nil {
		return nil, "", ErrInternal(err)
	}

	u := &User{
		Username:      username,
		Email:         email,
		PasswordHash:  hash,
		DisplayName:   username,
		Visibility:    VisibilityPublic,
		Role:          RoleUser,
		Source:        "local",
		Groups:        "[]",
		EmailVerified: !s.cfg.RequireEmailVerification,
		Approved:      !s.cfg.RequireApproval,
		Timezone:      "UTC",
		Language:      "en",
	}
	if _, err := s.store.CreateUser(ctx, u); err != nil {
		return nil, "", ErrInternal(err)
	}

	if invite != nil {
		if err := s.store.ConsumeInvite(ctx, invite.ID); err != nil {
			return nil, "", ErrInternal(err)
		}
		if invite.OrgID != 0 {
			role := invite.Role
			if role != OrgRoleAdmin && role != OrgRoleMember {
				role = OrgRoleMember
			}
			if err := s.store.AddOrgMember(ctx, invite.OrgID, u.ID, role); err != nil {
				return nil, "", ErrInternal(err)
			}
		}
	}

	if s.cfg.RequireEmailVerification {
		if err := s.sendEmailVerification(ctx, u); err != nil {
			return nil, "", ErrInternal(err)
		}
	}

	token, err := s.issueSession(ctx, u.ID, in.IP, in.UserAgent)
	if err != nil {
		return nil, "", ErrInternal(err)
	}

	s.audit("user.register",
		slog.Int64("user_id", u.ID),
		slog.String("username", u.Username),
		slog.String("ip", in.IP))
	return u, token, nil
}

// Login verifies credentials and issues a session. Every failure path returns the
// identical ErrInvalidCredentials response and takes the same minimum wall-clock time,
// so an unknown account, a wrong password, a disabled account and a locked account are
// externally indistinguishable.
func (s *Service) Login(ctx context.Context, in LoginInput) (*User, string, *apperr.Error) {
	if !s.cfg.UsersEnabled {
		return nil, "", ErrFeatureDisabled("User accounts")
	}
	start := time.Now()
	if ok, retry := s.limits.Allow(security.LimitLogin, in.IP); !ok {
		s.pad(start)
		return nil, "", ErrRateLimited(int(retry.Seconds()))
	}

	u, lookupErr := s.store.UserByIdentifier(ctx, in.Identifier)
	stored := s.dummyHash
	if lookupErr == nil {
		stored = u.PasswordHash
	}

	// The hash comparison always runs, including when no account was found.
	ok, needsRehash := s.checkPassword(stored, in.Password)

	if lookupErr != nil || !ok {
		if lookupErr == nil {
			if err := s.store.RecordLoginFailure(ctx, u.ID); err != nil {
				s.log.Warn("record login failure", slog.String("error", err.Error()))
			}
		}
		s.pad(start)
		return nil, "", ErrInvalidCredentials()
	}
	if u.Locked() || u.Disabled || !u.Approved {
		s.pad(start)
		return nil, "", ErrInvalidCredentials()
	}
	if s.cfg.RequireEmailVerification && !u.EmailVerified {
		s.pad(start)
		return nil, "", ErrInvalidCredentials()
	}

	if u.TOTPEnabled {
		if strings.TrimSpace(in.TOTPCode) == "" {
			s.pad(start)
			return nil, "", ErrTwoFactorRequired()
		}
		if !ValidateTOTP(u.TOTPSecret, in.TOTPCode) {
			if err := s.store.RecordLoginFailure(ctx, u.ID); err != nil {
				s.log.Warn("record login failure", slog.String("error", err.Error()))
			}
			s.pad(start)
			return nil, "", ErrTwoFactorInvalid()
		}
	}

	// Upgrade a legacy bcrypt hash to Argon2id now that the plaintext is available.
	if needsRehash {
		if upgraded, err := security.HashPassword(in.Password); err == nil {
			if err := s.store.SetUserPassword(ctx, u.ID, upgraded); err != nil {
				s.log.Warn("rehash password", slog.String("error", err.Error()))
			}
		}
	}

	token, err := s.issueSession(ctx, u.ID, in.IP, in.UserAgent)
	if err != nil {
		s.pad(start)
		return nil, "", ErrInternal(err)
	}
	if err := s.store.RecordLoginSuccess(ctx, u.ID); err != nil {
		s.log.Warn("record login success", slog.String("error", err.Error()))
	}

	s.audit("user.login",
		slog.Int64("user_id", u.ID),
		slog.String("username", u.Username),
		slog.String("ip", in.IP))
	s.pad(start)
	return u, token, nil
}

// issueSession mints a fresh session token. A new random identifier is generated on
// every successful authentication and the plaintext is never stored, which is what
// prevents session fixation: a value the attacker planted before login is not the
// value the server accepts after it.
func (s *Service) issueSession(ctx context.Context, userID int64, ip, userAgent string) (string, error) {
	token, err := newSecret()
	if err != nil {
		return "", err
	}
	sess := &Session{
		UserID:    userID,
		TokenHash: security.HashToken(token),
		IPAddress: ip,
		UserAgent: userAgent,
		ExpiresAt: time.Now().Add(s.cfg.SessionTTL).Unix(),
	}
	if _, err := s.store.CreateSession(ctx, sess); err != nil {
		return "", err
	}
	if err := s.store.TrimUserSessions(ctx, userID, s.cfg.MaxSessionsPerUser); err != nil {
		s.log.Warn("trim sessions", slog.String("error", err.Error()))
	}
	return token, nil
}

// Logout revokes the presented session.
func (s *Service) Logout(ctx context.Context, token string) {
	if token == "" {
		return
	}
	if err := s.store.DeleteSessionByHash(ctx, security.HashToken(token)); err != nil {
		s.log.Warn("delete session", slog.String("error", err.Error()))
	}
}

// ResolveSession maps a presented session token to its account.
func (s *Service) ResolveSession(ctx context.Context, token string) (*User, *Session, *apperr.Error) {
	if token == "" {
		return nil, nil, ErrUnauthenticated()
	}
	sess, err := s.store.SessionByHash(ctx, security.HashToken(token))
	if err != nil {
		return nil, nil, ErrUnauthenticated()
	}
	if sess.Expired() {
		if err := s.store.DeleteSessionByHash(ctx, sess.TokenHash); err != nil {
			s.log.Warn("delete expired session", slog.String("error", err.Error()))
		}
		return nil, nil, ErrSessionExpired()
	}
	u, err := s.store.UserByID(ctx, sess.UserID)
	if err != nil {
		return nil, nil, ErrUnauthenticated()
	}
	if u.Disabled || !u.Approved {
		return nil, nil, ErrForbidden()
	}
	return u, sess, nil
}

// ChangePassword rotates a user's password and revokes every other session so a stolen
// cookie cannot survive the change.
func (s *Service) ChangePassword(ctx context.Context, userID int64, current, next, ip, userAgent string) (string, *apperr.Error) {
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return "", ErrUnauthenticated()
	}
	start := time.Now()
	ok, _ := s.checkPassword(u.PasswordHash, current)
	if !ok {
		s.pad(start)
		return "", ErrInvalidCredentials()
	}
	if verr := ValidatePassword(next); verr != nil {
		return "", ErrValidation("new_password", verr.Error())
	}
	hash, err := security.HashPassword(next)
	if err != nil {
		return "", ErrInternal(err)
	}
	if err := s.store.SetUserPassword(ctx, userID, hash); err != nil {
		return "", ErrInternal(err)
	}
	if err := s.store.DeleteUserSessions(ctx, userID); err != nil {
		return "", ErrInternal(err)
	}
	token, err := s.issueSession(ctx, userID, ip, userAgent)
	if err != nil {
		return "", ErrInternal(err)
	}
	s.audit("user.password_change", slog.Int64("user_id", userID), slog.String("ip", ip))
	return token, nil
}

// RequestPasswordReset issues a reset link. The caller always receives the same
// success response whether or not the address is registered, so the endpoint cannot be
// used to test whether an account exists.
func (s *Service) RequestPasswordReset(ctx context.Context, email, ip string) *apperr.Error {
	start := time.Now()
	if ok, retry := s.limits.Allow(security.LimitPasswordReset, ip); !ok {
		s.pad(start)
		return ErrRateLimited(int(retry.Seconds()))
	}
	email = NormalizeEmail(email)
	if err := ValidateEmail(email); err != nil {
		s.pad(start)
		return nil
	}
	u, err := s.store.UserByEmail(ctx, email)
	if err != nil {
		s.pad(start)
		return nil
	}
	token, err := newSecret()
	if err != nil {
		s.pad(start)
		return ErrInternal(err)
	}
	expires := time.Now().Add(PasswordResetTTL).Unix()
	if err := s.store.CreatePasswordReset(ctx, u.ID, security.HashToken(token), expires); err != nil {
		s.pad(start)
		return ErrInternal(err)
	}
	link := s.cfg.BaseURL + "/auth/password/reset/confirm?token=" + token
	s.send(ctx, u.Email, "Reset your "+s.cfg.SiteName+" password",
		"Use the link below within one hour to choose a new password.\n\n"+link+
			"\n\nIf you did not request this, no action is needed.")
	s.audit("user.password_reset_requested", slog.Int64("user_id", u.ID), slog.String("ip", ip))
	s.pad(start)
	return nil
}

// ConfirmPasswordReset redeems a reset token and sets the new password.
func (s *Service) ConfirmPasswordReset(ctx context.Context, token, next, ip string) *apperr.Error {
	if ok, retry := s.limits.Allow(security.LimitPasswordReset, ip); !ok {
		return ErrRateLimited(int(retry.Seconds()))
	}
	if err := ValidatePassword(next); err != nil {
		return ErrValidation("new_password", err.Error())
	}
	userID, resetID, err := s.store.PasswordResetByHash(ctx, security.HashToken(token))
	if err != nil {
		return ErrValidation("token", "That reset link is no longer valid, please request a new one")
	}
	hash, err := security.HashPassword(next)
	if err != nil {
		return ErrInternal(err)
	}
	if err := s.store.SetUserPassword(ctx, userID, hash); err != nil {
		return ErrInternal(err)
	}
	if err := s.store.ConsumePasswordReset(ctx, resetID); err != nil {
		return ErrInternal(err)
	}
	if err := s.store.DeleteUserSessions(ctx, userID); err != nil {
		return ErrInternal(err)
	}
	s.audit("user.password_reset", slog.Int64("user_id", userID), slog.String("ip", ip))
	return nil
}

// sendEmailVerification issues and mails a fresh confirmation link.
func (s *Service) sendEmailVerification(ctx context.Context, u *User) error {
	token, err := newSecret()
	if err != nil {
		return err
	}
	expires := time.Now().Add(EmailVerificationTTL).Unix()
	if err := s.store.CreateEmailVerification(ctx, u.ID, u.Email, security.HashToken(token), expires); err != nil {
		return err
	}
	link := s.cfg.BaseURL + "/auth/email/verify?token=" + token
	s.send(ctx, u.Email, "Confirm your "+s.cfg.SiteName+" email address",
		"Use the link below within 24 hours to confirm this address.\n\n"+link)
	return nil
}

// ResendEmailVerification re-issues a confirmation link for the signed-in account.
func (s *Service) ResendEmailVerification(ctx context.Context, userID int64) *apperr.Error {
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return ErrUnauthenticated()
	}
	if u.EmailVerified {
		return nil
	}
	if err := s.sendEmailVerification(ctx, u); err != nil {
		return ErrInternal(err)
	}
	return nil
}

// VerifyEmail redeems an address confirmation token.
func (s *Service) VerifyEmail(ctx context.Context, token, ip string) *apperr.Error {
	if ok, retry := s.limits.Allow(security.LimitWrite, ip); !ok {
		return ErrRateLimited(int(retry.Seconds()))
	}
	userID, email, recordID, err := s.store.EmailVerificationByHash(ctx, security.HashToken(token))
	if err != nil {
		return ErrValidation("token", "That confirmation link is no longer valid, please request a new one")
	}
	if err := s.store.SetUserEmail(ctx, userID, email, true); err != nil {
		return ErrInternal(err)
	}
	if err := s.store.ConsumeEmailVerification(ctx, recordID); err != nil {
		return ErrInternal(err)
	}
	s.audit("user.email_verified", slog.Int64("user_id", userID))
	return nil
}

// UpdateProfile writes the owner-editable profile fields.
func (s *Service) UpdateProfile(ctx context.Context, userID int64, in PublicUser) (*User, *apperr.Error) {
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return nil, ErrUnauthenticated()
	}
	u.DisplayName = strings.TrimSpace(in.DisplayName)
	u.Bio = strings.TrimSpace(in.Bio)
	u.Location = strings.TrimSpace(in.Location)
	u.Website = strings.TrimSpace(in.Website)
	if len(u.DisplayName) > 100 || len(u.Bio) > 500 || len(u.Location) > 100 || len(u.Website) > 255 {
		return nil, ErrValidation("profile", "One of the profile fields is too long")
	}
	if u.Website != "" {
		if err := security.ValidateOutboundURL(u.Website); err != nil {
			return nil, ErrValidation("website", "Enter a valid public website address")
		}
	}
	if in.Visibility == VisibilityPrivate || in.Visibility == VisibilityPublic {
		u.Visibility = in.Visibility
	}
	if in.Timezone != "" {
		if _, err := time.LoadLocation(in.Timezone); err != nil {
			return nil, ErrValidation("timezone", "Select a valid time zone")
		}
		u.Timezone = in.Timezone
	}
	if in.Language != "" {
		u.Language = in.Language
	}
	if err := s.store.UpdateUserProfile(ctx, u); err != nil {
		return nil, ErrInternal(err)
	}
	return u, nil
}

// BeginTOTP generates a pending second-factor secret and returns its provisioning URI.
// The secret is not yet active: it becomes usable only after ConfirmTOTP proves the
// user can produce a valid code from it.
func (s *Service) BeginTOTP(ctx context.Context, userID int64) (secret, uri string, aerr *apperr.Error) {
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return "", "", ErrUnauthenticated()
	}
	secret, err = NewTOTPSecret()
	if err != nil {
		return "", "", ErrInternal(err)
	}
	if err := s.store.SetUserTOTP(ctx, userID, secret, false); err != nil {
		return "", "", ErrInternal(err)
	}
	return secret, TOTPProvisioningURI(s.cfg.SiteName, u.Username, secret), nil
}

// ConfirmTOTP activates the pending second factor.
func (s *Service) ConfirmTOTP(ctx context.Context, userID int64, code, ip string) *apperr.Error {
	if ok, retry := s.limits.Allow(security.LimitLogin, ip); !ok {
		return ErrRateLimited(int(retry.Seconds()))
	}
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return ErrUnauthenticated()
	}
	if u.TOTPSecret == "" || !ValidateTOTP(u.TOTPSecret, code) {
		return ErrTwoFactorInvalid()
	}
	if err := s.store.SetUserTOTP(ctx, userID, u.TOTPSecret, true); err != nil {
		return ErrInternal(err)
	}
	s.audit("user.totp_enabled", slog.Int64("user_id", userID))
	return nil
}

// DisableTOTP turns the second factor off. Both the password and a current code are
// required, so a hijacked session alone cannot strip the factor.
func (s *Service) DisableTOTP(ctx context.Context, userID int64, password, code string) *apperr.Error {
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return ErrUnauthenticated()
	}
	start := time.Now()
	ok, _ := s.checkPassword(u.PasswordHash, password)
	if !ok {
		s.pad(start)
		return ErrInvalidCredentials()
	}
	if u.TOTPEnabled && !ValidateTOTP(u.TOTPSecret, code) {
		s.pad(start)
		return ErrTwoFactorInvalid()
	}
	if err := s.store.SetUserTOTP(ctx, userID, "", false); err != nil {
		return ErrInternal(err)
	}
	s.audit("user.totp_disabled", slog.Int64("user_id", userID))
	s.pad(start)
	return nil
}

// DeleteAccount removes the signed-in user and tombstones the username.
func (s *Service) DeleteAccount(ctx context.Context, userID int64, password string) *apperr.Error {
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return ErrUnauthenticated()
	}
	start := time.Now()
	ok, _ := s.checkPassword(u.PasswordHash, password)
	if !ok {
		s.pad(start)
		return ErrInvalidCredentials()
	}
	owners, err := s.store.CountOwnedOrgs(ctx, userID)
	if err != nil {
		return ErrInternal(err)
	}
	if owners > 0 {
		return apperr.New(apperr.CodeConflict, 409,
			"Transfer or delete the organizations you own before deleting your account")
	}
	if err := s.store.DeleteUser(ctx, userID); err != nil {
		return ErrInternal(err)
	}
	s.audit("user.deleted", slog.Int64("user_id", userID), slog.String("username", u.Username))
	s.pad(start)
	return nil
}

// send delivers a transactional message, tolerating a nil mailer so the server runs
// with no SMTP configuration. Message bodies never contain a password or a hash.
func (s *Service) send(ctx context.Context, to, subject, body string) {
	if s.mailer == nil {
		return
	}
	if err := s.mailer.Send(ctx, to, subject, body); err != nil {
		s.log.Warn("send mail", slog.String("subject", subject), slog.String("error", err.Error()))
	}
}
