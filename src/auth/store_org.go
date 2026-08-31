package auth

import (
	"context"
	"database/sql"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

const orgColumns = `id, slug, name, description, avatar_type, avatar_url, website,
	location, visibility, owner_id, suspended, created_at, updated_at`

func scanOrg(row interface{ Scan(...any) error }) (*Org, error) {
	var o Org
	var suspended int64
	err := row.Scan(&o.ID, &o.Slug, &o.Name, &o.Description, &o.AvatarType, &o.AvatarURL,
		&o.Website, &o.Location, &o.Visibility, &o.OwnerID, &suspended, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return nil, err
	}
	o.Suspended = suspended != 0
	return &o, nil
}

// CreateOrg inserts an organization and seats its creator as the owner in one
// transaction, so an org can never exist without an owner row.
func (s *Store) CreateOrg(ctx context.Context, o *Org) (int64, error) {
	now := time.Now().Unix()
	if o.CreatedAt == 0 {
		o.CreatedAt = now
	}
	o.UpdatedAt = now
	err := s.db.Tx(ctx, database.TimeoutWrite, func(tx *sql.Tx) error {
		res, err := s.db.TxExec(ctx, tx, s.q(`
			INSERT INTO orgs (slug, name, description, avatar_type, avatar_url, website,
				location, visibility, owner_id, suspended, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`),
			o.Slug, o.Name, o.Description, o.AvatarType, o.AvatarURL, o.Website,
			o.Location, o.Visibility, o.OwnerID, o.CreatedAt, o.UpdatedAt)
		if err != nil {
			return err
		}
		id := lastID(res)
		if id == 0 {
			if err := s.db.TxQueryRow(ctx, tx, s.q(`SELECT id FROM orgs WHERE slug = ?`), o.Slug).Scan(&id); err != nil {
				return err
			}
		}
		o.ID = id
		_, err = s.db.TxExec(ctx, tx, s.q(`
			INSERT INTO org_members (org_id, user_id, role, joined_at) VALUES (?, ?, ?, ?)`),
			id, o.OwnerID, OrgRoleOwner, now)
		return err
	})
	if err != nil {
		return 0, err
	}
	return o.ID, nil
}

// OrgByID loads an organization by primary key.
func (s *Store) OrgByID(ctx context.Context, id int64) (*Org, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT `+orgColumns+` FROM orgs WHERE id = ?`), id)
	return scanOrg(row)
}

// OrgBySlug loads an organization by its normalized slug.
func (s *Store) OrgBySlug(ctx context.Context, slug string) (*Org, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT `+orgColumns+` FROM orgs WHERE slug = ?`), NormalizeName(slug))
	return scanOrg(row)
}

// UpdateOrg writes the settings an org Owner or Admin may change.
func (s *Store) UpdateOrg(ctx context.Context, o *Org) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		UPDATE orgs SET name = ?, description = ?, avatar_type = ?, avatar_url = ?,
			website = ?, location = ?, visibility = ?, updated_at = ?
		WHERE id = ?`),
		o.Name, o.Description, o.AvatarType, o.AvatarURL, o.Website, o.Location,
		o.Visibility, time.Now().Unix(), o.ID)
	return err
}

// TransferOrgOwnership moves the owner seat to another existing member.
func (s *Store) TransferOrgOwnership(ctx context.Context, orgID, fromUserID, toUserID int64) error {
	now := time.Now().Unix()
	return s.db.Tx(ctx, database.TimeoutWrite, func(tx *sql.Tx) error {
		if _, err := s.db.TxExec(ctx, tx,
			s.q(`UPDATE org_members SET role = ? WHERE org_id = ? AND user_id = ?`),
			OrgRoleOwner, orgID, toUserID); err != nil {
			return err
		}
		if _, err := s.db.TxExec(ctx, tx,
			s.q(`UPDATE org_members SET role = ? WHERE org_id = ? AND user_id = ?`),
			OrgRoleAdmin, orgID, fromUserID); err != nil {
			return err
		}
		_, err := s.db.TxExec(ctx, tx,
			s.q(`UPDATE orgs SET owner_id = ?, updated_at = ? WHERE id = ?`), toUserID, now, orgID)
		return err
	})
}

// SetOrgSuspended toggles the Server Admin suspension flag.
func (s *Store) SetOrgSuspended(ctx context.Context, orgID int64, suspended bool) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE orgs SET suspended = ?, updated_at = ? WHERE id = ?`),
		boolInt(suspended), time.Now().Unix(), orgID)
	return err
}

// DeleteOrg removes an organization, its members, its tokens, its invites and its
// custom domains, then tombstones the slug so it can never be re-registered.
func (s *Store) DeleteOrg(ctx context.Context, orgID int64) error {
	o, err := s.OrgByID(ctx, orgID)
	if err != nil {
		return err
	}
	return s.db.Tx(ctx, database.TimeoutWrite, func(tx *sql.Tx) error {
		if _, err := s.db.TxExec(ctx, tx,
			s.q(`INSERT INTO name_tombstones (name, kind, created_at) VALUES (?, ?, ?)`),
			o.Slug, OwnerOrg, time.Now().Unix()); err != nil {
			return err
		}
		for _, stmt := range []string{
			`DELETE FROM org_members WHERE org_id = ?`,
			`DELETE FROM org_tokens WHERE org_id = ?`,
			`DELETE FROM invites WHERE org_id = ?`,
			`DELETE FROM orgs WHERE id = ?`,
		} {
			if _, err := s.db.TxExec(ctx, tx, s.q(stmt), orgID); err != nil {
				return err
			}
		}
		_, err := s.db.TxExec(ctx, tx,
			s.q(`DELETE FROM custom_domains WHERE owner_type = ? AND owner_id = ?`), OwnerOrg, orgID)
		return err
	})
}

// OrgRole returns the caller's role inside an org, or "" when they are not a member.
// Every org-scoped authorization decision starts here.
func (s *Store) OrgRole(ctx context.Context, orgID, userID int64) (string, error) {
	var role string
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT role FROM org_members WHERE org_id = ? AND user_id = ?`), orgID, userID).Scan(&role)
	if err != nil {
		if isNoRows(err) {
			return "", nil
		}
		return "", err
	}
	return role, nil
}

// AddOrgMember seats a user in an organization.
func (s *Store) AddOrgMember(ctx context.Context, orgID, userID int64, role string) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`INSERT INTO org_members (org_id, user_id, role, joined_at) VALUES (?, ?, ?, ?)`),
		orgID, userID, role, time.Now().Unix())
	return err
}

// SetOrgMemberRole changes an existing member's role.
func (s *Store) SetOrgMemberRole(ctx context.Context, orgID, userID int64, role string) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE org_members SET role = ? WHERE org_id = ? AND user_id = ?`), role, orgID, userID)
	return err
}

// RemoveOrgMember unseats a member.
func (s *Store) RemoveOrgMember(ctx context.Context, orgID, userID int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`DELETE FROM org_members WHERE org_id = ? AND user_id = ?`), orgID, userID)
	return err
}

// ListOrgMembers returns every member of an organization with their username.
func (s *Store) ListOrgMembers(ctx context.Context, orgID int64) ([]*OrgMember, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutJoin, s.q(`
		SELECT m.org_id, m.user_id, u.username, m.role, m.joined_at
		FROM org_members m JOIN users u ON u.id = m.user_id
		WHERE m.org_id = ? ORDER BY m.joined_at ASC`), orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*OrgMember
	for rows.Next() {
		var m OrgMember
		if err := rows.Scan(&m.OrgID, &m.UserID, &m.Username, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// CountOrgMembers returns how many members an organization has.
func (s *Store) CountOrgMembers(ctx context.Context, orgID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT COUNT(*) FROM org_members WHERE org_id = ?`), orgID).Scan(&n)
	if err != nil && !isNoRows(err) {
		return 0, err
	}
	return n, nil
}

// CountOrgOwners returns how many owner seats an organization has, used to refuse the
// removal or demotion of the last owner.
func (s *Store) CountOrgOwners(ctx context.Context, orgID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT COUNT(*) FROM org_members WHERE org_id = ? AND role = ?`), orgID, OrgRoleOwner).Scan(&n)
	if err != nil && !isNoRows(err) {
		return 0, err
	}
	return n, nil
}

// ListUserOrgs returns every organization the user belongs to, with their role.
func (s *Store) ListUserOrgs(ctx context.Context, userID int64) ([]*Org, []string, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutJoin, s.q(`
		SELECT o.id, o.slug, o.name, o.description, o.avatar_type, o.avatar_url, o.website,
			o.location, o.visibility, o.owner_id, o.suspended, o.created_at, o.updated_at, m.role
		FROM orgs o JOIN org_members m ON m.org_id = o.id
		WHERE m.user_id = ? ORDER BY o.slug ASC`), userID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var orgs []*Org
	var roles []string
	for rows.Next() {
		var o Org
		var suspended int64
		var role string
		if err := rows.Scan(&o.ID, &o.Slug, &o.Name, &o.Description, &o.AvatarType, &o.AvatarURL,
			&o.Website, &o.Location, &o.Visibility, &o.OwnerID, &suspended, &o.CreatedAt,
			&o.UpdatedAt, &role); err != nil {
			return nil, nil, err
		}
		o.Suspended = suspended != 0
		orgs = append(orgs, &o)
		roles = append(roles, role)
	}
	return orgs, roles, rows.Err()
}

// CountOwnedOrgs returns how many organizations a user owns, for quota enforcement.
func (s *Store) CountOwnedOrgs(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT COUNT(*) FROM orgs WHERE owner_id = ?`), userID).Scan(&n)
	if err != nil && !isNoRows(err) {
		return 0, err
	}
	return n, nil
}

// RecordOrgAudit appends one row to the append-only organization audit trail.
func (s *Store) RecordOrgAudit(ctx context.Context, orgID int64, action, actorType string, actorID int64, details string) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		INSERT INTO org_audit (org_id, action, actor_type, actor_id, details, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`),
		orgID, action, actorType, actorID, details, time.Now().Unix())
	return err
}
