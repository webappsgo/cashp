package auth

import (
	"context"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

// Admin is a Server Admin account. Credentials are never written to the config file,
// and the primary admin is created only by redeeming a one-time setup token.
type Admin struct {
	ID           int64
	Username     string
	Email        string
	PasswordHash string
	TokenHash    string
	TokenPrefix  string
	TOTPSecret   string
	TOTPEnabled  bool
	IsPrimary    bool
	FailedLogins int
	LockedUntil  int64
	CreatedAt    int64
	UpdatedAt    int64
	LastLoginAt  int64
}

// Locked reports whether the admin account is inside a lockout window.
func (a *Admin) Locked() bool { return a.LockedUntil > time.Now().Unix() }

// PublicAdmin is the response shape for a Server Admin. No hash or secret is included.
type PublicAdmin struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	TOTPEnabled bool   `json:"totp_enabled"`
	IsPrimary   bool   `json:"is_primary"`
	CreatedAt   int64  `json:"created_at"`
	LastLoginAt int64  `json:"last_login_at,omitempty"`
}

// Public converts an Admin into its response shape.
func (a *Admin) Public() PublicAdmin {
	return PublicAdmin{
		ID:          a.ID,
		Username:    a.Username,
		Email:       a.Email,
		TOTPEnabled: a.TOTPEnabled,
		IsPrimary:   a.IsPrimary,
		CreatedAt:   a.CreatedAt,
		LastLoginAt: a.LastLoginAt,
	}
}

const adminColumns = `id, username, email, password_hash, token_hash, token_prefix,
	totp_secret, totp_enabled, is_primary, failed_logins, locked_until,
	created_at, updated_at, last_login_at`

func scanAdmin(row interface{ Scan(...any) error }) (*Admin, error) {
	var a Admin
	var totpEnabled, isPrimary int64
	var tokenHash, tokenPrefix, totpSecret *string
	err := row.Scan(&a.ID, &a.Username, &a.Email, &a.PasswordHash, &tokenHash, &tokenPrefix,
		&totpSecret, &totpEnabled, &isPrimary, &a.FailedLogins, &a.LockedUntil,
		&a.CreatedAt, &a.UpdatedAt, &a.LastLoginAt)
	if err != nil {
		return nil, err
	}
	if tokenHash != nil {
		a.TokenHash = *tokenHash
	}
	if tokenPrefix != nil {
		a.TokenPrefix = *tokenPrefix
	}
	if totpSecret != nil {
		a.TOTPSecret = *totpSecret
	}
	a.TOTPEnabled = totpEnabled != 0
	a.IsPrimary = isPrimary != 0
	return &a, nil
}

// CreateAdmin inserts a Server Admin account.
func (s *Store) CreateAdmin(ctx context.Context, a *Admin) (int64, error) {
	now := time.Now().Unix()
	if a.CreatedAt == 0 {
		a.CreatedAt = now
	}
	a.UpdatedAt = now
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		INSERT INTO admins (username, email, password_hash, token_hash, token_prefix,
			totp_secret, totp_enabled, is_primary, failed_logins, locked_until,
			created_at, updated_at, last_login_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?, 0)`),
		a.Username, a.Email, a.PasswordHash, a.TokenHash, a.TokenPrefix,
		a.TOTPSecret, boolInt(a.TOTPEnabled), boolInt(a.IsPrimary), a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return 0, err
	}
	if id := lastID(res); id != 0 {
		a.ID = id
		return id, nil
	}
	found, err := s.AdminByUsername(ctx, a.Username)
	if err != nil {
		return 0, err
	}
	a.ID = found.ID
	return found.ID, nil
}

// AdminByID loads a Server Admin by primary key.
func (s *Store) AdminByID(ctx context.Context, id int64) (*Admin, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT `+adminColumns+` FROM admins WHERE id = ?`), id)
	return scanAdmin(row)
}

// AdminByUsername loads a Server Admin by username.
func (s *Store) AdminByUsername(ctx context.Context, username string) (*Admin, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT `+adminColumns+` FROM admins WHERE username = ?`), NormalizeName(username))
	return scanAdmin(row)
}

// AdminByTokenHash resolves an admin API token to its account.
func (s *Store) AdminByTokenHash(ctx context.Context, hash string) (*Admin, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT `+adminColumns+` FROM admins WHERE token_hash = ?`), hash)
	return scanAdmin(row)
}

// CountAdmins returns how many Server Admin accounts exist. Zero means the server is
// still awaiting the one-time primary admin bootstrap.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect, s.q(`SELECT COUNT(*) FROM admins`)).Scan(&n)
	if err != nil && !isNoRows(err) {
		return 0, err
	}
	return n, nil
}

// SetAdminPassword replaces a Server Admin's Argon2id hash.
func (s *Store) SetAdminPassword(ctx context.Context, adminID int64, hash string) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE admins SET password_hash = ?, updated_at = ? WHERE id = ?`),
		hash, time.Now().Unix(), adminID)
	return err
}

// SetAdminTOTP stores or clears a Server Admin's second-factor secret.
func (s *Store) SetAdminTOTP(ctx context.Context, adminID int64, secret string, enabled bool) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE admins SET totp_secret = ?, totp_enabled = ?, updated_at = ? WHERE id = ?`),
		secret, boolInt(enabled), time.Now().Unix(), adminID)
	return err
}

// RecordAdminLoginSuccess clears the failure counter and stamps the login time.
func (s *Store) RecordAdminLoginSuccess(ctx context.Context, adminID int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE admins SET failed_logins = 0, locked_until = 0, last_login_at = ? WHERE id = ?`),
		time.Now().Unix(), adminID)
	return err
}

// RecordAdminLoginFailure increments the counter and locks the account at the threshold.
func (s *Store) RecordAdminLoginFailure(ctx context.Context, adminID int64) error {
	a, err := s.AdminByID(ctx, adminID)
	if err != nil {
		return err
	}
	failed := a.FailedLogins + 1
	locked := a.LockedUntil
	if failed >= LockoutThreshold {
		locked = time.Now().Add(LockoutDuration).Unix()
		failed = 0
	}
	_, err = s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE admins SET failed_logins = ?, locked_until = ? WHERE id = ?`), failed, locked, adminID)
	return err
}

// CreateAdminSession stores an admin browser session by token hash.
func (s *Store) CreateAdminSession(ctx context.Context, sess *Session) (int64, error) {
	if sess.CreatedAt == 0 {
		sess.CreatedAt = time.Now().Unix()
	}
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		INSERT INTO admin_sessions (admin_id, token_hash, ip_address, user_agent, location, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`),
		sess.UserID, sess.TokenHash, sess.IPAddress, sess.UserAgent, sess.Location,
		sess.ExpiresAt, sess.CreatedAt)
	if err != nil {
		return 0, err
	}
	sess.ID = lastID(res)
	return sess.ID, nil
}

// AdminSessionByHash loads an admin session by the hash of its presented token.
func (s *Store) AdminSessionByHash(ctx context.Context, hash string) (*Session, error) {
	var sess Session
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect, s.q(`
		SELECT id, admin_id, token_hash, ip_address, user_agent, location, expires_at, created_at
		FROM admin_sessions WHERE token_hash = ?`), hash).
		Scan(&sess.ID, &sess.UserID, &sess.TokenHash, &sess.IPAddress, &sess.UserAgent,
			&sess.Location, &sess.ExpiresAt, &sess.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

// DeleteAdminSessionByHash revokes one admin session.
func (s *Store) DeleteAdminSessionByHash(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`DELETE FROM admin_sessions WHERE token_hash = ?`), hash)
	return err
}

// DeleteAdminSessions revokes every session for an admin.
func (s *Store) DeleteAdminSessions(ctx context.Context, adminID int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`DELETE FROM admin_sessions WHERE admin_id = ?`), adminID)
	return err
}

// CreateSetupToken records the hash of a one-time bootstrap token.
func (s *Store) CreateSetupToken(ctx context.Context, hash, purpose string, expiresAt int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		INSERT INTO setup_tokens (token_hash, purpose, used, used_at, expires_at, created_at)
		VALUES (?, ?, 0, 0, ?, ?)`), hash, purpose, expiresAt, time.Now().Unix())
	return err
}

// SetupTokenByHash loads an unused, unexpired bootstrap token.
func (s *Store) SetupTokenByHash(ctx context.Context, hash, purpose string) (int64, error) {
	var id, used, expiresAt int64
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT id, used, expires_at FROM setup_tokens WHERE token_hash = ? AND purpose = ?`),
		hash, purpose).Scan(&id, &used, &expiresAt)
	if err != nil {
		return 0, err
	}
	if used != 0 || (expiresAt > 0 && time.Now().Unix() > expiresAt) {
		return 0, database.ErrNotFound
	}
	return id, nil
}

// ConsumeSetupToken marks a bootstrap token as spent. Because the primary admin can only
// be created by consuming a token, and a token can only be consumed once, the bootstrap
// cannot be re-run by anyone who later gains access to the panel.
func (s *Store) ConsumeSetupToken(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE setup_tokens SET used = 1, used_at = ? WHERE id = ? AND used = 0`),
		time.Now().Unix(), id)
	return err
}
