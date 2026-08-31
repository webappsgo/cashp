package auth

import (
	"context"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

// Invite kinds. A user invite unlocks registration; an org invite seats an existing
// or newly registered account into an organization.
const (
	InviteKindUser = "user"
	InviteKindOrg  = "org"
)

const inviteColumns = `id, kind, code_hash, email, org_id, role, max_uses, use_count,
	expires_at, revoked, created_by, created_at`

func scanInvite(row interface{ Scan(...any) error }) (*Invite, error) {
	var i Invite
	var revoked int64
	err := row.Scan(&i.ID, &i.Kind, &i.CodeHash, &i.Email, &i.OrgID, &i.Role, &i.MaxUses,
		&i.UseCount, &i.ExpiresAt, &revoked, &i.CreatedBy, &i.CreatedAt)
	if err != nil {
		return nil, err
	}
	i.Revoked = revoked != 0
	return &i, nil
}

// CreateInvite stores an invitation. Only the hash of the code is persisted; the
// plaintext code is returned to the issuer exactly once.
func (s *Store) CreateInvite(ctx context.Context, i *Invite) (int64, error) {
	if i.CreatedAt == 0 {
		i.CreatedAt = time.Now().Unix()
	}
	if i.MaxUses == 0 {
		i.MaxUses = 1
	}
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		INSERT INTO invites (kind, code_hash, email, org_id, role, max_uses, use_count,
			expires_at, revoked, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?, 0, ?, ?)`),
		i.Kind, i.CodeHash, NormalizeEmail(i.Email), i.OrgID, i.Role, i.MaxUses,
		i.ExpiresAt, i.CreatedBy, i.CreatedAt)
	if err != nil {
		return 0, err
	}
	i.ID = lastID(res)
	return i.ID, nil
}

// InviteByHash loads an invitation by the hash of the presented code.
func (s *Store) InviteByHash(ctx context.Context, hash string) (*Invite, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT `+inviteColumns+` FROM invites WHERE code_hash = ?`), hash)
	return scanInvite(row)
}

// ListOrgInvites returns every outstanding invitation for an organization.
func (s *Store) ListOrgInvites(ctx context.Context, orgID int64) ([]*Invite, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		s.q(`SELECT `+inviteColumns+` FROM invites WHERE org_id = ? AND revoked = 0 ORDER BY created_at DESC`),
		orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Invite
	for rows.Next() {
		i, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// ConsumeInvite increments the use counter. The WHERE clause re-checks every liveness
// condition so two concurrent redemptions cannot both succeed on a single-use invite.
func (s *Store) ConsumeInvite(ctx context.Context, inviteID int64) error {
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		UPDATE invites SET use_count = use_count + 1
		WHERE id = ? AND revoked = 0
			AND (max_uses = 0 OR use_count < max_uses)
			AND (expires_at = 0 OR expires_at > ?)`), inviteID, now)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return database.ErrConflict
	}
	return nil
}

// RevokeInvite cancels an invitation, scoped to its org so a member of another org
// cannot revoke it by guessing the ID.
func (s *Store) RevokeInvite(ctx context.Context, orgID, inviteID int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE invites SET revoked = 1 WHERE id = ? AND org_id = ?`), inviteID, orgID)
	return err
}

// PurgeExpiredInvites deletes revoked, exhausted and expired invitations.
func (s *Store) PurgeExpiredInvites(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutBulk, s.q(`
		DELETE FROM invites
		WHERE revoked = 1
			OR (expires_at > 0 AND expires_at < ?)
			OR (max_uses > 0 AND use_count >= max_uses)`), time.Now().Unix())
	return err
}
