package auth

import (
	"context"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

// CreateUserToken stores a user-owned API token. Only the SHA-256 hash is kept.
func (s *Store) CreateUserToken(ctx context.Context, t *Token) (int64, error) {
	if t.CreatedAt == 0 {
		t.CreatedAt = time.Now().Unix()
	}
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		INSERT INTO user_tokens (user_id, name, token_hash, token_prefix, scopes, expires_at, last_used_at, revoked, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, 0, ?)`),
		t.OwnerID, t.Name, t.TokenHash, t.TokenPrefix, t.Scopes, t.ExpiresAt, t.CreatedAt)
	if err != nil {
		return 0, err
	}
	t.ID = lastID(res)
	return t.ID, nil
}

// CreateOrgToken stores an organization-owned API token.
func (s *Store) CreateOrgToken(ctx context.Context, t *Token, createdBy int64) (int64, error) {
	if t.CreatedAt == 0 {
		t.CreatedAt = time.Now().Unix()
	}
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		INSERT INTO org_tokens (org_id, created_by, name, token_hash, token_prefix, scopes, expires_at, last_used_at, revoked, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0, ?)`),
		t.OwnerID, createdBy, t.Name, t.TokenHash, t.TokenPrefix, t.Scopes, t.ExpiresAt, t.CreatedAt)
	if err != nil {
		return 0, err
	}
	t.ID = lastID(res)
	return t.ID, nil
}

// TokenByHash resolves a presented token to its owner. The user table is consulted
// first, then the org table; both are indexed on token_hash so the lookup is a point
// read rather than a scan over candidate rows.
func (s *Store) TokenByHash(ctx context.Context, hash string) (*Token, error) {
	t := &Token{OwnerType: OwnerUser}
	var revoked int64
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect, s.q(`
		SELECT id, user_id, name, token_hash, token_prefix, scopes, expires_at, last_used_at, revoked, created_at
		FROM user_tokens WHERE token_hash = ?`), hash).
		Scan(&t.ID, &t.OwnerID, &t.Name, &t.TokenHash, &t.TokenPrefix, &t.Scopes,
			&t.ExpiresAt, &t.LastUsedAt, &revoked, &t.CreatedAt)
	if err == nil {
		t.Revoked = revoked != 0
		return t, nil
	}
	if !isNoRows(err) {
		return nil, err
	}

	o := &Token{OwnerType: OwnerOrg}
	err = s.db.QueryRowContext(ctx, database.TimeoutSelect, s.q(`
		SELECT id, org_id, name, token_hash, token_prefix, scopes, expires_at, last_used_at, revoked, created_at
		FROM org_tokens WHERE token_hash = ?`), hash).
		Scan(&o.ID, &o.OwnerID, &o.Name, &o.TokenHash, &o.TokenPrefix, &o.Scopes,
			&o.ExpiresAt, &o.LastUsedAt, &revoked, &o.CreatedAt)
	if err != nil {
		return nil, err
	}
	o.Revoked = revoked != 0
	return o, nil
}

// ListUserTokens returns every token owned by a user.
func (s *Store) ListUserTokens(ctx context.Context, userID int64) ([]*Token, error) {
	return s.listTokens(ctx, `
		SELECT id, user_id, name, token_hash, token_prefix, scopes, expires_at, last_used_at, revoked, created_at
		FROM user_tokens WHERE user_id = ? ORDER BY created_at DESC`, OwnerUser, userID)
}

// ListOrgTokens returns every token owned by an organization.
func (s *Store) ListOrgTokens(ctx context.Context, orgID int64) ([]*Token, error) {
	return s.listTokens(ctx, `
		SELECT id, org_id, name, token_hash, token_prefix, scopes, expires_at, last_used_at, revoked, created_at
		FROM org_tokens WHERE org_id = ? ORDER BY created_at DESC`, OwnerOrg, orgID)
}

func (s *Store) listTokens(ctx context.Context, query, ownerType string, ownerID int64) ([]*Token, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect, s.q(query), ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Token
	for rows.Next() {
		t := &Token{OwnerType: ownerType}
		var revoked int64
		if err := rows.Scan(&t.ID, &t.OwnerID, &t.Name, &t.TokenHash, &t.TokenPrefix, &t.Scopes,
			&t.ExpiresAt, &t.LastUsedAt, &revoked, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.Revoked = revoked != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

// TouchToken records the last time a token authenticated a request.
func (s *Store) TouchToken(ctx context.Context, ownerType string, tokenID int64) error {
	table := "user_tokens"
	if ownerType == OwnerOrg {
		table = "org_tokens"
	}
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE `+table+` SET last_used_at = ? WHERE id = ?`), time.Now().Unix(), tokenID)
	return err
}

// RevokeUserToken revokes one token, scoped to its owner so a caller cannot revoke a
// token belonging to another account by guessing its ID.
func (s *Store) RevokeUserToken(ctx context.Context, userID, tokenID int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE user_tokens SET revoked = 1 WHERE id = ? AND user_id = ?`), tokenID, userID)
	return err
}

// RevokeOrgToken revokes one organization token, scoped to its owning org.
func (s *Store) RevokeOrgToken(ctx context.Context, orgID, tokenID int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE org_tokens SET revoked = 1 WHERE id = ? AND org_id = ?`), tokenID, orgID)
	return err
}

// PurgeExpiredTokens deletes revoked and expired tokens. Bound to the scheduler's
// token_cleanup task.
func (s *Store) PurgeExpiredTokens(ctx context.Context) (int64, error) {
	now := time.Now().Unix()
	var total int64
	for _, table := range []string{"user_tokens", "org_tokens"} {
		res, err := s.db.ExecContext(ctx, database.TimeoutBulk,
			s.q(`DELETE FROM `+table+` WHERE revoked = 1 OR (expires_at > 0 AND expires_at < ?)`), now)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}
