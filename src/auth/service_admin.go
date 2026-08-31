package auth

import (
	"context"
	"log/slog"
	"strings"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/security"
)

// setupTokenPurpose labels the bootstrap token in the setup_tokens table.
const setupTokenPurpose = "primary_admin"

// BootstrapNeeded reports whether the server still has no Server Admin.
func (s *Service) BootstrapNeeded(ctx context.Context) (bool, error) {
	n, err := s.store.CountAdmins(ctx)
	if err != nil {
		return false, err
	}
	return n == 0, nil
}

// IssueSetupToken mints the one-time bootstrap token. It is called at startup when no
// admin exists and the plaintext is printed to the server console only, never mailed,
// never logged, and never returned over HTTP. Only the SHA-256 hash is stored, so
// reading the database does not yield a usable token, and the single-use consumption in
// CompleteBootstrap makes the flow tamper-proof: a second claim cannot succeed even if
// the token leaks afterwards.
func (s *Service) IssueSetupToken(ctx context.Context) (string, error) {
	plaintext, hash, err := security.GenerateToken(security.PrefixAdmin)
	if err != nil {
		return "", err
	}
	expires := time.Now().Add(SetupTokenTTL).Unix()
	if err := s.store.CreateSetupToken(ctx, hash, setupTokenPurpose, expires); err != nil {
		return "", err
	}
	s.audit("admin.setup_token_issued", slog.Int64("expires_at", expires))
	return plaintext, nil
}

// BootstrapInput carries the primary Server Admin the setup form creates.
type BootstrapInput struct {
	SetupToken string
	Username   string
	Email      string
	Password   string
	IP         string
	UserAgent  string
}

// CompleteBootstrap redeems the setup token and creates the primary Server Admin.
// The token row is consumed inside the same flow, so the endpoint stops working the
// moment the first admin exists.
func (s *Service) CompleteBootstrap(ctx context.Context, in BootstrapInput) (*Admin, string, *apperr.Error) {
	start := time.Now()
	if ok, retry := s.limits.Allow(security.LimitLogin, in.IP); !ok {
		s.pad(start)
		return nil, "", ErrRateLimited(int(retry.Seconds()))
	}

	count, err := s.store.CountAdmins(ctx)
	if err != nil {
		return nil, "", ErrInternal(err)
	}
	if count > 0 {
		s.pad(start)
		return nil, "", ErrForbidden()
	}

	tokenID, err := s.store.SetupTokenByHash(ctx, security.HashToken(in.SetupToken), setupTokenPurpose)
	if err != nil {
		s.pad(start)
		return nil, "", ErrInvalidCredentials()
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

	hash, err := security.HashPassword(in.Password)
	if err != nil {
		return nil, "", ErrInternal(err)
	}
	admin := &Admin{
		Username:     username,
		Email:        email,
		PasswordHash: hash,
		IsPrimary:    true,
	}
	if _, err := s.store.CreateAdmin(ctx, admin); err != nil {
		return nil, "", ErrInternal(err)
	}
	if err := s.store.ConsumeSetupToken(ctx, tokenID); err != nil {
		return nil, "", ErrInternal(err)
	}

	session, err := s.issueAdminSession(ctx, admin.ID, in.IP, in.UserAgent)
	if err != nil {
		return nil, "", ErrInternal(err)
	}
	s.audit("admin.bootstrap_completed",
		slog.Int64("admin_id", admin.ID),
		slog.String("username", admin.Username),
		slog.String("ip", in.IP))
	s.pad(start)
	return admin, session, nil
}

// AdminLogin authenticates a Server Admin. It uses the same anti-enumeration and
// timing-equalisation shape as the user login path.
func (s *Service) AdminLogin(ctx context.Context, in LoginInput) (*Admin, string, *apperr.Error) {
	start := time.Now()
	if ok, retry := s.limits.Allow(security.LimitLogin, in.IP); !ok {
		s.pad(start)
		return nil, "", ErrRateLimited(int(retry.Seconds()))
	}

	admin, lookupErr := s.store.AdminByUsername(ctx, NormalizeName(in.Identifier))
	stored := s.dummyHash
	if lookupErr == nil {
		stored = admin.PasswordHash
	}
	ok, needsRehash := s.checkPassword(stored, in.Password)

	if lookupErr != nil || !ok {
		if lookupErr == nil {
			if err := s.store.RecordAdminLoginFailure(ctx, admin.ID); err != nil {
				s.log.Warn("record admin login failure", slog.String("error", err.Error()))
			}
		}
		s.audit("admin.login_failed", slog.String("ip", in.IP))
		s.pad(start)
		return nil, "", ErrInvalidCredentials()
	}
	if admin.Locked() {
		s.pad(start)
		return nil, "", ErrInvalidCredentials()
	}
	if admin.TOTPEnabled {
		if strings.TrimSpace(in.TOTPCode) == "" {
			s.pad(start)
			return nil, "", ErrTwoFactorRequired()
		}
		if !ValidateTOTP(admin.TOTPSecret, in.TOTPCode) {
			if err := s.store.RecordAdminLoginFailure(ctx, admin.ID); err != nil {
				s.log.Warn("record admin login failure", slog.String("error", err.Error()))
			}
			s.pad(start)
			return nil, "", ErrTwoFactorInvalid()
		}
	}
	if needsRehash {
		if upgraded, err := security.HashPassword(in.Password); err == nil {
			if err := s.store.SetAdminPassword(ctx, admin.ID, upgraded); err != nil {
				s.log.Warn("rehash admin password", slog.String("error", err.Error()))
			}
		}
	}

	session, err := s.issueAdminSession(ctx, admin.ID, in.IP, in.UserAgent)
	if err != nil {
		s.pad(start)
		return nil, "", ErrInternal(err)
	}
	if err := s.store.RecordAdminLoginSuccess(ctx, admin.ID); err != nil {
		s.log.Warn("record admin login success", slog.String("error", err.Error()))
	}
	s.audit("admin.login",
		slog.Int64("admin_id", admin.ID),
		slog.String("username", admin.Username),
		slog.String("ip", in.IP))
	s.pad(start)
	return admin, session, nil
}

// issueAdminSession mints a fresh panel session token.
func (s *Service) issueAdminSession(ctx context.Context, adminID int64, ip, userAgent string) (string, error) {
	token, err := newSecret()
	if err != nil {
		return "", err
	}
	sess := &Session{
		UserID:    adminID,
		TokenHash: security.HashToken(token),
		IPAddress: ip,
		UserAgent: userAgent,
		ExpiresAt: time.Now().Add(AdminSessionTTL).Unix(),
	}
	if _, err := s.store.CreateAdminSession(ctx, sess); err != nil {
		return "", err
	}
	return token, nil
}

// AdminLogout revokes the presented panel session.
func (s *Service) AdminLogout(ctx context.Context, token string) {
	if token == "" {
		return
	}
	if err := s.store.DeleteAdminSessionByHash(ctx, security.HashToken(token)); err != nil {
		s.log.Warn("delete admin session", slog.String("error", err.Error()))
	}
}

// ResolveAdminSession maps a presented panel session token to its Server Admin.
func (s *Service) ResolveAdminSession(ctx context.Context, token string) (*Admin, *Session, *apperr.Error) {
	if token == "" {
		return nil, nil, ErrUnauthenticated()
	}
	sess, err := s.store.AdminSessionByHash(ctx, security.HashToken(token))
	if err != nil {
		return nil, nil, ErrUnauthenticated()
	}
	if sess.Expired() {
		if err := s.store.DeleteAdminSessionByHash(ctx, sess.TokenHash); err != nil {
			s.log.Warn("delete expired admin session", slog.String("error", err.Error()))
		}
		return nil, nil, ErrSessionExpired()
	}
	admin, err := s.store.AdminByID(ctx, sess.UserID)
	if err != nil {
		return nil, nil, ErrUnauthenticated()
	}
	if admin.Locked() {
		return nil, nil, ErrForbidden()
	}
	return admin, sess, nil
}

// CreateAdmin adds another Server Admin. Only an existing admin may call it.
func (s *Service) CreateAdmin(ctx context.Context, actorID int64, username, email, password string) (*Admin, *apperr.Error) {
	username = NormalizeName(username)
	email = NormalizeEmail(email)
	if err := ValidateUsernameFormat(username); err != nil {
		return nil, ErrValidation("username", err.Error())
	}
	if err := ValidateEmail(email); err != nil {
		return nil, ErrValidation("email", err.Error())
	}
	if err := ValidatePassword(password); err != nil {
		return nil, ErrValidation("password", err.Error())
	}
	if _, err := s.store.AdminByUsername(ctx, username); err == nil {
		return nil, ErrNameUnavailable("username")
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return nil, ErrInternal(err)
	}
	admin := &Admin{Username: username, Email: email, PasswordHash: hash}
	if _, err := s.store.CreateAdmin(ctx, admin); err != nil {
		return nil, ErrInternal(err)
	}
	s.audit("admin.created",
		slog.Int64("admin_id", admin.ID),
		slog.String("username", admin.Username),
		slog.Int64("actor_id", actorID))
	return admin, nil
}

// ChangeAdminPassword rotates a Server Admin password and drops the admin's other
// sessions.
func (s *Service) ChangeAdminPassword(ctx context.Context, adminID int64, current, next, ip, userAgent string) (string, *apperr.Error) {
	admin, err := s.store.AdminByID(ctx, adminID)
	if err != nil {
		return "", ErrUnauthenticated()
	}
	start := time.Now()
	ok, _ := s.checkPassword(admin.PasswordHash, current)
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
	if err := s.store.SetAdminPassword(ctx, adminID, hash); err != nil {
		return "", ErrInternal(err)
	}
	if err := s.store.DeleteAdminSessions(ctx, adminID); err != nil {
		return "", ErrInternal(err)
	}
	session, err := s.issueAdminSession(ctx, adminID, ip, userAgent)
	if err != nil {
		return "", ErrInternal(err)
	}
	s.audit("admin.password_change", slog.Int64("admin_id", adminID), slog.String("ip", ip))
	return session, nil
}

// BeginAdminTOTP generates a pending second factor for a Server Admin.
func (s *Service) BeginAdminTOTP(ctx context.Context, adminID int64) (secret, uri string, aerr *apperr.Error) {
	admin, err := s.store.AdminByID(ctx, adminID)
	if err != nil {
		return "", "", ErrUnauthenticated()
	}
	secret, err = NewTOTPSecret()
	if err != nil {
		return "", "", ErrInternal(err)
	}
	if err := s.store.SetAdminTOTP(ctx, adminID, secret, false); err != nil {
		return "", "", ErrInternal(err)
	}
	return secret, TOTPProvisioningURI(s.cfg.SiteName+" Admin", admin.Username, secret), nil
}

// ConfirmAdminTOTP activates a Server Admin's pending second factor.
func (s *Service) ConfirmAdminTOTP(ctx context.Context, adminID int64, code, ip string) *apperr.Error {
	if ok, retry := s.limits.Allow(security.LimitLogin, ip); !ok {
		return ErrRateLimited(int(retry.Seconds()))
	}
	admin, err := s.store.AdminByID(ctx, adminID)
	if err != nil {
		return ErrUnauthenticated()
	}
	if admin.TOTPSecret == "" || !ValidateTOTP(admin.TOTPSecret, code) {
		return ErrTwoFactorInvalid()
	}
	if err := s.store.SetAdminTOTP(ctx, adminID, admin.TOTPSecret, true); err != nil {
		return ErrInternal(err)
	}
	s.audit("admin.totp_enabled", slog.Int64("admin_id", adminID))
	return nil
}

// DisableAdminTOTP turns a Server Admin's second factor off, requiring both the
// password and a current code.
func (s *Service) DisableAdminTOTP(ctx context.Context, adminID int64, password, code string) *apperr.Error {
	admin, err := s.store.AdminByID(ctx, adminID)
	if err != nil {
		return ErrUnauthenticated()
	}
	start := time.Now()
	ok, _ := s.checkPassword(admin.PasswordHash, password)
	if !ok {
		s.pad(start)
		return ErrInvalidCredentials()
	}
	if admin.TOTPEnabled && !ValidateTOTP(admin.TOTPSecret, code) {
		s.pad(start)
		return ErrTwoFactorInvalid()
	}
	if err := s.store.SetAdminTOTP(ctx, adminID, "", false); err != nil {
		return ErrInternal(err)
	}
	s.audit("admin.totp_disabled", slog.Int64("admin_id", adminID))
	s.pad(start)
	return nil
}

// SetUserApproval approves or suspends an account from the admin panel.
func (s *Service) SetUserApproval(ctx context.Context, actorID, userID int64, approved, disabled bool) *apperr.Error {
	if err := s.store.SetUserFlags(ctx, userID, approved, disabled); err != nil {
		return ErrInternal(err)
	}
	s.audit("admin.user_flags_changed",
		slog.Int64("actor_id", actorID),
		slog.Int64("user_id", userID),
		slog.Bool("approved", approved),
		slog.Bool("disabled", disabled))
	return nil
}

// SuspendOrg suspends or restores an organization from the admin panel.
func (s *Service) SuspendOrg(ctx context.Context, actorID, orgID int64, suspended bool) *apperr.Error {
	if err := s.store.SetOrgSuspended(ctx, orgID, suspended); err != nil {
		return ErrInternal(err)
	}
	if err := s.store.RecordOrgAudit(ctx, orgID, "org.suspended", OwnerAdmin, actorID, boolText(suspended)); err != nil {
		s.log.Warn("record org audit", slog.String("error", err.Error()))
	}
	s.audit("admin.org_suspended",
		slog.Int64("actor_id", actorID),
		slog.Int64("org_id", orgID),
		slog.Bool("suspended", suspended))
	return nil
}

// boolText renders a flag for the audit detail column.
func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
