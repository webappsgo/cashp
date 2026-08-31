package auth

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

// userColumns is the fixed projection every user scan relies on.
const userColumns = `id, username, email, password_hash, display_name, avatar_url, bio,
	location, website, visibility, role, source, external_id, groups, last_sync,
	email_verified, approved, disabled, totp_secret, totp_enabled, timezone, language,
	failed_logins, locked_until, created_at, updated_at, last_login_at`

// scanUser reads one row in userColumns order.
func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var emailVerified, approved, disabled, totpEnabled int64
	err := row.Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName, &u.AvatarURL, &u.Bio,
		&u.Location, &u.Website, &u.Visibility, &u.Role, &u.Source, &u.ExternalID, &u.Groups, &u.LastSync,
		&emailVerified, &approved, &disabled, &u.TOTPSecret, &totpEnabled, &u.Timezone, &u.Language,
		&u.FailedLogins, &u.LockedUntil, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt,
	)
	if err != nil {
		return nil, err
	}
	u.EmailVerified = emailVerified != 0
	u.Approved = approved != 0
	u.Disabled = disabled != 0
	u.TOTPEnabled = totpEnabled != 0
	return &u, nil
}

// CreateUser inserts a new account and returns its assigned ID.
func (s *Store) CreateUser(ctx context.Context, u *User) (int64, error) {
	now := time.Now().Unix()
	if u.CreatedAt == 0 {
		u.CreatedAt = now
	}
	u.UpdatedAt = now
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		INSERT INTO users (username, email, password_hash, display_name, avatar_url, bio,
			location, website, visibility, role, source, external_id, groups, last_sync,
			email_verified, approved, disabled, totp_secret, totp_enabled, timezone, language,
			failed_logins, locked_until, created_at, updated_at, last_login_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		u.Username, u.Email, u.PasswordHash, u.DisplayName, u.AvatarURL, u.Bio,
		u.Location, u.Website, u.Visibility, u.Role, u.Source, u.ExternalID, u.Groups, u.LastSync,
		boolInt(u.EmailVerified), boolInt(u.Approved), boolInt(u.Disabled), u.TOTPSecret,
		boolInt(u.TOTPEnabled), u.Timezone, u.Language,
		u.FailedLogins, u.LockedUntil, u.CreatedAt, u.UpdatedAt, u.LastLoginAt,
	)
	if err != nil {
		return 0, err
	}
	if id := lastID(res); id != 0 {
		u.ID = id
		return id, nil
	}
	found, err := s.UserByUsername(ctx, u.Username)
	if err != nil {
		return 0, err
	}
	u.ID = found.ID
	return found.ID, nil
}

// UserByID loads one account by primary key.
func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT `+userColumns+` FROM users WHERE id = ?`), id)
	return scanUser(row)
}

// UserByUsername loads one account by its normalized username.
func (s *Store) UserByUsername(ctx context.Context, username string) (*User, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT `+userColumns+` FROM users WHERE username = ?`), NormalizeName(username))
	return scanUser(row)
}

// UserByEmail loads one account by its normalized email address.
func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT `+userColumns+` FROM users WHERE email = ?`), NormalizeEmail(email))
	return scanUser(row)
}

// UserByIdentifier resolves a login identifier that may be a user ID, an email, or a
// username, following the detection order in AI.md PART 34.
func (s *Store) UserByIdentifier(ctx context.Context, identifier string) (*User, error) {
	switch DetectIdentifierType(identifier) {
	case "user_id":
		id, err := strconv.ParseInt(strings.TrimSpace(identifier), 10, 64)
		if err != nil {
			return nil, sql.ErrNoRows
		}
		return s.UserByID(ctx, id)
	case "email":
		return s.UserByEmail(ctx, identifier)
	default:
		return s.UserByUsername(ctx, identifier)
	}
}

// UpdateUserProfile writes the owner-editable profile fields.
func (s *Store) UpdateUserProfile(ctx context.Context, u *User) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		UPDATE users SET display_name = ?, avatar_url = ?, bio = ?, location = ?,
			website = ?, visibility = ?, timezone = ?, language = ?, updated_at = ?
		WHERE id = ?`),
		u.DisplayName, u.AvatarURL, u.Bio, u.Location, u.Website, u.Visibility,
		u.Timezone, u.Language, time.Now().Unix(), u.ID)
	return err
}

// SetUserPassword replaces the stored hash. Callers pass an Argon2id encoded hash.
func (s *Store) SetUserPassword(ctx context.Context, userID int64, hash string) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`),
		hash, time.Now().Unix(), userID)
	return err
}

// SetUserEmail records a new address and resets its verified flag.
func (s *Store) SetUserEmail(ctx context.Context, userID int64, email string, verified bool) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE users SET email = ?, email_verified = ?, updated_at = ? WHERE id = ?`),
		NormalizeEmail(email), boolInt(verified), time.Now().Unix(), userID)
	return err
}

// SetUserTOTP stores or clears the second-factor secret.
func (s *Store) SetUserTOTP(ctx context.Context, userID int64, secret string, enabled bool) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE users SET totp_secret = ?, totp_enabled = ?, updated_at = ? WHERE id = ?`),
		secret, boolInt(enabled), time.Now().Unix(), userID)
	return err
}

// SetUserFlags updates the moderation flags a Server Admin controls.
func (s *Store) SetUserFlags(ctx context.Context, userID int64, approved, disabled bool) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE users SET approved = ?, disabled = ?, updated_at = ? WHERE id = ?`),
		boolInt(approved), boolInt(disabled), time.Now().Unix(), userID)
	return err
}

// RecordLoginSuccess clears the failure counter and stamps the login time.
func (s *Store) RecordLoginSuccess(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE users SET failed_logins = 0, locked_until = 0, last_login_at = ? WHERE id = ?`),
		time.Now().Unix(), userID)
	return err
}

// RecordLoginFailure increments the failure counter and applies the lockout once the
// threshold is crossed. Locking is silent: the caller still returns Invalid credentials.
func (s *Store) RecordLoginFailure(ctx context.Context, userID int64) error {
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	failed := u.FailedLogins + 1
	locked := u.LockedUntil
	if failed >= LockoutThreshold {
		locked = time.Now().Add(LockoutDuration).Unix()
		failed = 0
	}
	_, err = s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE users SET failed_logins = ?, locked_until = ? WHERE id = ?`),
		failed, locked, userID)
	return err
}

// ListUsers returns a page of accounts for the admin panel, newest first.
func (s *Store) ListUsers(ctx context.Context, limit, offset int) ([]*User, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		s.q(`SELECT `+userColumns+` FROM users ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`),
		limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountUsers returns the total number of accounts.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect, s.q(`SELECT COUNT(*) FROM users`)).Scan(&n)
	if err != nil && !isNoRows(err) {
		return 0, err
	}
	return n, nil
}

// DeleteUser removes an account and tombstones its username so the name can never be
// claimed again by anyone, which is what makes former-tenant impersonation impossible.
func (s *Store) DeleteUser(ctx context.Context, userID int64) error {
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	return s.db.Tx(ctx, database.TimeoutWrite, func(tx *sql.Tx) error {
		if _, err := s.db.TxExec(ctx, tx,
			s.q(`INSERT INTO name_tombstones (name, kind, created_at) VALUES (?, ?, ?)`),
			u.Username, OwnerUser, time.Now().Unix()); err != nil {
			return err
		}
		for _, stmt := range []string{
			`DELETE FROM user_sessions WHERE user_id = ?`,
			`DELETE FROM user_tokens WHERE user_id = ?`,
			`DELETE FROM password_resets WHERE user_id = ?`,
			`DELETE FROM email_verifications WHERE user_id = ?`,
			`DELETE FROM org_members WHERE user_id = ?`,
			`DELETE FROM users WHERE id = ?`,
		} {
			if _, err := s.db.TxExec(ctx, tx, s.q(stmt), userID); err != nil {
				return err
			}
		}
		return nil
	})
}

// TombstoneName records a name as permanently unusable.
func (s *Store) TombstoneName(ctx context.Context, name, kind string) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`INSERT INTO name_tombstones (name, kind, created_at) VALUES (?, ?, ?)`),
		NormalizeName(name), kind, time.Now().Unix())
	return err
}

// NameTombstoned reports whether a username or org slug has ever been used and released.
func (s *Store) NameTombstoned(ctx context.Context, name string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT COUNT(*) FROM name_tombstones WHERE name = ?`), NormalizeName(name)).Scan(&n)
	if err != nil && !isNoRows(err) {
		return false, err
	}
	return n > 0, nil
}

// NameTaken reports whether a name is currently held by a user or an organization.
// Users and orgs share one namespace, so both tables are consulted.
func (s *Store) NameTaken(ctx context.Context, name string) (bool, error) {
	name = NormalizeName(name)
	var users, orgs int
	if err := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT COUNT(*) FROM users WHERE username = ?`), name).Scan(&users); err != nil && !isNoRows(err) {
		return false, err
	}
	if err := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT COUNT(*) FROM orgs WHERE slug = ?`), name).Scan(&orgs); err != nil && !isNoRows(err) {
		return false, err
	}
	return users+orgs > 0, nil
}

// EmailTaken reports whether an address is already registered.
func (s *Store) EmailTaken(ctx context.Context, email string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT COUNT(*) FROM users WHERE email = ?`), NormalizeEmail(email)).Scan(&n)
	if err != nil && !isNoRows(err) {
		return false, err
	}
	return n > 0, nil
}
