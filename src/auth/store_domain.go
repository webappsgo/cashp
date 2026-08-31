package auth

import (
	"context"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

const domainColumns = `id, owner_type, owner_id, domain, is_apex, is_wildcard,
	verification_status, verification_token, verified_at, last_check_at, check_count,
	ssl_enabled, ssl_status, ssl_challenge, ssl_provider, ssl_credentials,
	ssl_cert_pem, ssl_key_pem, ssl_issued_at, ssl_expires_at, ssl_last_error,
	status, suspended_reason, created_at, updated_at`

func scanDomain(row interface{ Scan(...any) error }) (*CustomDomain, error) {
	var d CustomDomain
	var isApex, isWildcard, sslEnabled int64
	err := row.Scan(&d.ID, &d.OwnerType, &d.OwnerID, &d.Domain, &isApex, &isWildcard,
		&d.VerificationStatus, &d.VerificationToken, &d.VerifiedAt, &d.LastCheckAt, &d.CheckCount,
		&sslEnabled, &d.SSLStatus, &d.SSLChallenge, &d.SSLProvider, &d.SSLCredentials,
		&d.SSLCertPEM, &d.SSLKeyPEM, &d.SSLIssuedAt, &d.SSLExpiresAt, &d.SSLLastError,
		&d.Status, &d.SuspendedReason, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	d.IsApex = isApex != 0
	d.IsWildcard = isWildcard != 0
	d.SSLEnabled = sslEnabled != 0
	return &d, nil
}

// CreateDomain registers a custom domain in the pending state.
func (s *Store) CreateDomain(ctx context.Context, d *CustomDomain) (int64, error) {
	now := time.Now().Unix()
	if d.CreatedAt == 0 {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		INSERT INTO custom_domains (owner_type, owner_id, domain, is_apex, is_wildcard,
			verification_status, verification_token, verified_at, last_check_at, check_count,
			ssl_enabled, ssl_status, ssl_challenge, ssl_provider, ssl_credentials,
			ssl_cert_pem, ssl_key_pem, ssl_issued_at, ssl_expires_at, ssl_last_error,
			status, suspended_reason, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0, 0, ?, ?, ?, ?, ?, '', '', 0, 0, '', ?, '', ?, ?)`),
		d.OwnerType, d.OwnerID, d.Domain, boolInt(d.IsApex), boolInt(d.IsWildcard),
		d.VerificationStatus, d.VerificationToken,
		boolInt(d.SSLEnabled), d.SSLStatus, d.SSLChallenge, d.SSLProvider, d.SSLCredentials,
		d.Status, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return 0, err
	}
	d.ID = lastID(res)
	return d.ID, nil
}

// DomainByName loads a domain by its normalized name, regardless of owner.
func (s *Store) DomainByName(ctx context.Context, domain string) (*CustomDomain, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT `+domainColumns+` FROM custom_domains WHERE domain = ?`), NormalizeDomain(domain))
	return scanDomain(row)
}

// DomainByID loads a domain by primary key without an owner predicate. It exists only
// for Server Admin moderation, which is authorized across tenants; every tenant-facing
// path must use DomainByIDForOwner or DomainByNameForOwner instead.
func (s *Store) DomainByID(ctx context.Context, id int64) (*CustomDomain, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT `+domainColumns+` FROM custom_domains WHERE id = ?`), id)
	return scanDomain(row)
}

// DomainByIDForOwner loads a domain scoped to its owner. The owner predicate is part of
// the query rather than a post-fetch check, so a tenant can never read another tenant's
// row even by guessing the primary key.
func (s *Store) DomainByIDForOwner(ctx context.Context, ownerType string, ownerID, id int64) (*CustomDomain, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT `+domainColumns+` FROM custom_domains WHERE id = ? AND owner_type = ? AND owner_id = ?`),
		id, ownerType, ownerID)
	return scanDomain(row)
}

// DomainByNameForOwner loads a domain by name, scoped to its owner.
func (s *Store) DomainByNameForOwner(ctx context.Context, ownerType string, ownerID int64, domain string) (*CustomDomain, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT `+domainColumns+` FROM custom_domains WHERE domain = ? AND owner_type = ? AND owner_id = ?`),
		NormalizeDomain(domain), ownerType, ownerID)
	return scanDomain(row)
}

// ListDomains returns every domain owned by one user or organization.
func (s *Store) ListDomains(ctx context.Context, ownerType string, ownerID int64) ([]*CustomDomain, error) {
	return s.queryDomains(ctx,
		`SELECT `+domainColumns+` FROM custom_domains WHERE owner_type = ? AND owner_id = ? ORDER BY domain ASC`,
		ownerType, ownerID)
}

// ListDomainsForVerification returns pending domains whose ownership check is due.
func (s *Store) ListDomainsForVerification(ctx context.Context, before int64, limit int) ([]*CustomDomain, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.queryDomains(ctx, `SELECT `+domainColumns+` FROM custom_domains
		WHERE verification_status = ? AND last_check_at < ? ORDER BY last_check_at ASC LIMIT ?`,
		VerificationPending, before, limit)
}

// ListDomainsForRenewal returns active domains whose certificate expires within the window.
func (s *Store) ListDomainsForRenewal(ctx context.Context, before int64, limit int) ([]*CustomDomain, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.queryDomains(ctx, `SELECT `+domainColumns+` FROM custom_domains
		WHERE ssl_enabled = 1 AND ssl_expires_at > 0 AND ssl_expires_at < ? ORDER BY ssl_expires_at ASC LIMIT ?`,
		before, limit)
}

// ListActiveDomains returns every verified, active domain, used to prime the router.
func (s *Store) ListActiveDomains(ctx context.Context) ([]*CustomDomain, error) {
	return s.queryDomains(ctx, `SELECT `+domainColumns+` FROM custom_domains
		WHERE verification_status = ? AND status = ? ORDER BY domain ASC`,
		VerificationVerified, DomainStatusActive)
}

func (s *Store) queryDomains(ctx context.Context, query string, args ...any) ([]*CustomDomain, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect, s.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CustomDomain
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CountDomains returns how many domains an owner holds, for quota enforcement.
func (s *Store) CountDomains(ctx context.Context, ownerType string, ownerID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		s.q(`SELECT COUNT(*) FROM custom_domains WHERE owner_type = ? AND owner_id = ?`),
		ownerType, ownerID).Scan(&n)
	if err != nil && !isNoRows(err) {
		return 0, err
	}
	return n, nil
}

// MarkDomainChecked records a verification attempt that did not succeed.
func (s *Store) MarkDomainChecked(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		UPDATE custom_domains SET verification_status = ?, last_check_at = ?,
			check_count = check_count + 1, updated_at = ?
		WHERE id = ?`), status, time.Now().Unix(), time.Now().Unix(), id)
	return err
}

// MarkDomainVerified moves a domain out of the pending state once the TXT record matched.
func (s *Store) MarkDomainVerified(ctx context.Context, id int64, status string) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		UPDATE custom_domains SET verification_status = ?, verified_at = ?, last_check_at = ?,
			check_count = check_count + 1, status = ?, updated_at = ?
		WHERE id = ?`), VerificationVerified, now, now, status, now, id)
	return err
}

// SetDomainStatus updates the lifecycle state and its optional reason.
func (s *Store) SetDomainStatus(ctx context.Context, id int64, status, reason string) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE custom_domains SET status = ?, suspended_reason = ?, updated_at = ? WHERE id = ?`),
		status, reason, time.Now().Unix(), id)
	return err
}

// SetDomainSSLPending records the challenge type chosen for an upcoming issuance.
func (s *Store) SetDomainSSLPending(ctx context.Context, id int64, challenge string) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		UPDATE custom_domains SET ssl_enabled = 1, ssl_status = ?, ssl_challenge = ?,
			ssl_last_error = '', updated_at = ?
		WHERE id = ?`), SSLStatusPending, challenge, time.Now().Unix(), id)
	return err
}

// SetDomainSSLIssued stores the issued certificate material. The PEM values are supplied
// already encrypted by the service layer and are never logged.
func (s *Store) SetDomainSSLIssued(ctx context.Context, id int64, certPEM, keyPEM string, issuedAt, expiresAt int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		UPDATE custom_domains SET ssl_status = ?, ssl_cert_pem = ?, ssl_key_pem = ?,
			ssl_issued_at = ?, ssl_expires_at = ?, ssl_last_error = '', updated_at = ?
		WHERE id = ?`),
		SSLStatusActive, certPEM, keyPEM, issuedAt, expiresAt, time.Now().Unix(), id)
	return err
}

// SetDomainSSLError records an issuance failure. The stored message is the sanitized
// summary produced by the service layer, never a raw error string.
func (s *Store) SetDomainSSLError(ctx context.Context, id int64, message string) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`UPDATE custom_domains SET ssl_status = ?, ssl_last_error = ?, updated_at = ? WHERE id = ?`),
		SSLStatusError, message, time.Now().Unix(), id)
	return err
}

// DisableDomainSSL turns HTTPS off for a domain and clears its certificate material.
func (s *Store) DisableDomainSSL(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		UPDATE custom_domains SET ssl_enabled = 0, ssl_status = ?, ssl_cert_pem = '',
			ssl_key_pem = '', ssl_issued_at = 0, ssl_expires_at = 0, updated_at = ?
		WHERE id = ?`), SSLStatusNone, time.Now().Unix(), id)
	return err
}

// DeleteDomain removes a domain, scoped to its owner.
func (s *Store) DeleteDomain(ctx context.Context, ownerType string, ownerID, id int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		s.q(`DELETE FROM custom_domains WHERE id = ? AND owner_type = ? AND owner_id = ?`),
		id, ownerType, ownerID)
	return err
}

// PurgeStaleDomains removes never-verified domains that have sat past the grace period.
func (s *Store) PurgeStaleDomains(ctx context.Context, olderThan int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, database.TimeoutBulk,
		s.q(`DELETE FROM custom_domains WHERE verification_status <> ? AND created_at < ?`),
		VerificationVerified, olderThan)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// RecordDomainAudit appends one row to the append-only custom domain audit trail.
func (s *Store) RecordDomainAudit(ctx context.Context, domainID int64, action, actorType string, actorID int64, details string) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, s.q(`
		INSERT INTO custom_domain_audit (domain_id, action, actor_type, actor_id, details, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`),
		domainID, action, actorType, actorID, details, time.Now().Unix())
	return err
}
