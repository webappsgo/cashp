package auth

import (
	"context"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

const sessionColumns = `id, user_id, token_hash, ip_address, user_agent, location, expires_at, created_at`

func scanSession(row interface{ Scan(...any) error }) (*Session, error) {
	var s Session
	err := row.Scan(&s.ID, &s.UserID, &s.TokenHash, &s.IPAddress, &s.UserAgent, &s.Location,
		&s.ExpiresAt, &s.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// CreateSession stores a session row. Only the SHA-256 hash of the session token is
// persisted, so a database leak cannot be replayed as a live cookie.
func (s *Store) CreateSession(ctx context.Context, sess *Session) (int64, error) {
	if sess.CreatedAt == 0 {
		sess.CreatedAt = time.Now().Unix()
	}
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		INSERT INTO user_sessions (user_id, token_hash, ip_address, user_agent, location, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`),
		sess.UserID, sess.TokenHash, sess.IPAddress, sess.UserAgent, sess.Location,
		sess.ExpiresAt, sess.CreatedAt)
	if err != nil {
		return 0, err
	}
	sess.ID = lastID(res)
	return sess.ID, nil
}

// SessionByHash loads a session by the hash of its presented token.
func (s *Store) SessionByHash(ctx context.Context, hash string) (*Session, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT `+sessionColumns+` FROM user_sessions WHERE token_hash = ?`), hash)
	return scanSession(row)
}

// ListSessions returns every live session for a user, newest first.
func (s *Store) ListSessions(ctx context.Context, userID int64) ([]*Session, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		s.q(`SELECT `+sessionColumns+` FROM user_sessions WHERE user_id = ? ORDER BY created_at DESC`),
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// DeleteSessionByHash revokes a single session.
func (s *Store) DeleteSessionByHash(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`DELETE FROM user_sessions WHERE token_hash = ?`), hash)
	return err
}

// DeleteSession revokes one session owned by the given user. Scoping the delete by
// user_id is what stops one account from revoking another account's sessions.
func (s *Store) DeleteSession(ctx context.Context, userID, sessionID int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`DELETE FROM user_sessions WHERE id = ? AND user_id = ?`), sessionID, userID)
	return err
}

// DeleteUserSessions revokes every session for a user. Called on password change so a
// stolen cookie cannot outlive the credential it was issued against.
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`DELETE FROM user_sessions WHERE user_id = ?`), userID)
	return err
}

// TrimUserSessions enforces the concurrent-session cap by dropping the oldest rows.
func (s *Store) TrimUserSessions(ctx context.Context, userID int64, keep int) error {
	if keep <= 0 {
		return nil
	}
	sessions, err := s.ListSessions(ctx, userID)
	if err != nil {
		return err
	}
	if len(sessions) <= keep {
		return nil
	}
	for _, old := range sessions[keep:] {
		if err := s.DeleteSession(ctx, userID, old.ID); err != nil {
			return err
		}
	}
	return nil
}

// PurgeExpiredSessions deletes every session past its expiry. Bound to the scheduler's
// session_cleanup task.
func (s *Store) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, database.TimeoutBulk,
		s.q(`DELETE FROM user_sessions WHERE expires_at > 0 AND expires_at < ?`), now)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	adminRes, err := s.db.ExecContext(ctx, database.TimeoutBulk,
		s.q(`DELETE FROM admin_sessions WHERE expires_at > 0 AND expires_at < ?`), now)
	if err != nil {
		return n, err
	}
	an, _ := adminRes.RowsAffected()
	return n + an, nil
}

// CreatePasswordReset stores a hashed single-use reset token.
func (s *Store) CreatePasswordReset(ctx context.Context, userID int64, hash string, expiresAt int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		INSERT INTO password_resets (user_id, token_hash, used, expires_at, created_at)
		VALUES (?, ?, 0, ?, ?)`), userID, hash, expiresAt, time.Now().Unix())
	return err
}

// PasswordResetByHash loads an unused, unexpired reset request.
func (s *Store) PasswordResetByHash(ctx context.Context, hash string) (userID int64, resetID int64, err error) {
	var used int64
	var expiresAt int64
	err = s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT id, user_id, used, expires_at FROM password_resets WHERE token_hash = ?`), hash).
		Scan(&resetID, &userID, &used, &expiresAt)
	if err != nil {
		return 0, 0, err
	}
	if used != 0 || (expiresAt > 0 && time.Now().Unix() > expiresAt) {
		return 0, 0, database.ErrNotFound
	}
	return userID, resetID, nil
}

// ConsumePasswordReset marks a reset token as spent so it cannot be replayed.
func (s *Store) ConsumePasswordReset(ctx context.Context, resetID int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE password_resets SET used = 1 WHERE id = ?`), resetID)
	return err
}

// CreateEmailVerification stores a hashed single-use address confirmation token.
func (s *Store) CreateEmailVerification(ctx context.Context, userID int64, email, hash string, expiresAt int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		INSERT INTO email_verifications (user_id, email, token_hash, used, expires_at, created_at)
		VALUES (?, ?, ?, 0, ?, ?)`),
		userID, NormalizeEmail(email), hash, expiresAt, time.Now().Unix())
	return err
}

// EmailVerificationByHash loads an unused, unexpired confirmation request.
func (s *Store) EmailVerificationByHash(ctx context.Context, hash string) (userID int64, email string, recordID int64, err error) {
	var used, expiresAt int64
	err = s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT id, user_id, email, used, expires_at FROM email_verifications WHERE token_hash = ?`), hash).
		Scan(&recordID, &userID, &email, &used, &expiresAt)
	if err != nil {
		return 0, "", 0, err
	}
	if used != 0 || (expiresAt > 0 && time.Now().Unix() > expiresAt) {
		return 0, "", 0, database.ErrNotFound
	}
	return userID, email, recordID, nil
}

// ConsumeEmailVerification marks a confirmation token as spent.
func (s *Store) ConsumeEmailVerification(ctx context.Context, recordID int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE email_verifications SET used = 1 WHERE id = ?`), recordID)
	return err
}

// PurgeExpiredGrants clears spent and expired reset and verification rows.
func (s *Store) PurgeExpiredGrants(ctx context.Context) error {
	now := time.Now().Unix()
	if _, err := s.db.ExecContext(ctx, database.TimeoutBulk,
		s.q(`DELETE FROM password_resets WHERE used = 1 OR (expires_at > 0 AND expires_at < ?)`), now); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, database.TimeoutBulk,
		s.q(`DELETE FROM email_verifications WHERE used = 1 OR (expires_at > 0 AND expires_at < ?)`), now)
	return err
}
