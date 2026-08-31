package hosting

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

// Query timeouts per AI.md PART 10: simple reads 5s, writes 10s, list reads
// 15s. No database call in this package runs without one.
const (
	readTimeout  = 5 * time.Second
	writeTimeout = 10 * time.Second
	listTimeout  = 15 * time.Second
)

// SQLStore is the production Store backed by src/database. Every statement is
// parameterized and every tenant-owned statement filters on tenant_id, so an
// IDOR attempt cannot reach another tenant's row even if an id is guessed.
type SQLStore struct {
	db *database.DB
}

// NewSQLStore builds a Store over an open database handle.
func NewSQLStore(db *database.DB) *SQLStore { return &SQLStore{db: db} }

// unix renders a time as Unix seconds, mapping the zero time to zero.
func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// fromUnix rebuilds a UTC time from Unix seconds, mapping zero to the zero time.
func fromUnix(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(v, 0).UTC()
}

// boolInt renders a bool as the integer form used by every driver.
func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// splitList decodes a comma-joined column into a slice.
func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// exec runs a write statement with the standard write timeout.
func (s *SQLStore) exec(ctx context.Context, query string, args ...any) error {
	_, err := s.db.ExecContext(ctx, writeTimeout, s.db.Rebind(query), args...)
	if err != nil {
		return internalErr(err, "storage is unavailable")
	}
	return nil
}

// mustAffect runs a write and reports a not-found error when no row matched,
// which is what a cross-tenant id lookup produces.
func (s *SQLStore) mustAffect(ctx context.Context, kind, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, writeTimeout, s.db.Rebind(query), args...)
	if err != nil {
		return internalErr(err, "storage is unavailable")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil
	}
	if n == 0 {
		return notFound(kind)
	}
	return nil
}

// scanRow maps sql.ErrNoRows onto the generic not-found error.
func scanRow(kind string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return notFound(kind)
	}
	return internalErr(err, "storage is unavailable")
}

const siteColumns = `id, tenant_id, name, primary_domain, aliases, doc_root, php_version, tls_enabled, enabled,
	disk_quota_mb, bandwidth_quota_mb, disk_used_mb, git_remote, git_branch, created_at, updated_at`

// scanSite reads one site row.
func scanSite(sc interface{ Scan(...any) error }) (Site, error) {
	var (
		s                    Site
		aliases              string
		tls, enabled         int64
		createdAt, updatedAt int64
	)
	err := sc.Scan(&s.ID, &s.TenantID, &s.Name, &s.PrimaryDomain, &aliases, &s.DocRoot, &s.PHPVersion,
		&tls, &enabled, &s.DiskQuotaMB, &s.BandwidthQuotaMB, &s.DiskUsedMB, &s.GitRemote, &s.GitBranch,
		&createdAt, &updatedAt)
	if err != nil {
		return Site{}, err
	}
	s.Aliases = splitList(aliases)
	s.TLSEnabled = tls != 0
	s.Enabled = enabled != 0
	s.CreatedAt = fromUnix(createdAt)
	s.UpdatedAt = fromUnix(updatedAt)
	return s, nil
}

// CreateSite inserts a new site row.
func (s *SQLStore) CreateSite(ctx context.Context, v Site) error {
	return s.exec(ctx, `INSERT INTO hosting_sites (`+siteColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.ID, v.TenantID, v.Name, v.PrimaryDomain, strings.Join(v.Aliases, ","), v.DocRoot, v.PHPVersion,
		boolInt(v.TLSEnabled), boolInt(v.Enabled), v.DiskQuotaMB, v.BandwidthQuotaMB, v.DiskUsedMB,
		v.GitRemote, v.GitBranch, unix(v.CreatedAt), unix(v.UpdatedAt))
}

// UpdateSite rewrites the mutable columns of a site the tenant owns.
func (s *SQLStore) UpdateSite(ctx context.Context, v Site) error {
	return s.mustAffect(ctx, "site", `UPDATE hosting_sites SET name = ?, primary_domain = ?, aliases = ?,
		doc_root = ?, php_version = ?, tls_enabled = ?, enabled = ?, disk_quota_mb = ?, bandwidth_quota_mb = ?,
		disk_used_mb = ?, git_remote = ?, git_branch = ?, updated_at = ? WHERE id = ? AND tenant_id = ?`,
		v.Name, v.PrimaryDomain, strings.Join(v.Aliases, ","), v.DocRoot, v.PHPVersion, boolInt(v.TLSEnabled),
		boolInt(v.Enabled), v.DiskQuotaMB, v.BandwidthQuotaMB, v.DiskUsedMB, v.GitRemote, v.GitBranch,
		unix(v.UpdatedAt), v.ID, v.TenantID)
}

// GetSite loads one site scoped to its tenant.
func (s *SQLStore) GetSite(ctx context.Context, tenantID, id string) (Site, error) {
	row := s.db.QueryRowContext(ctx, readTimeout,
		s.db.Rebind(`SELECT `+siteColumns+` FROM hosting_sites WHERE id = ? AND tenant_id = ?`), id, tenantID)
	v, err := scanSite(row)
	return v, scanRow("site", err)
}

// ListSites returns every site of one tenant.
func (s *SQLStore) ListSites(ctx context.Context, tenantID string) ([]Site, error) {
	rows, err := s.db.QueryContext(ctx, listTimeout,
		s.db.Rebind(`SELECT `+siteColumns+` FROM hosting_sites WHERE tenant_id = ? ORDER BY name`), tenantID)
	if err != nil {
		return nil, internalErr(err, "storage is unavailable")
	}
	defer rows.Close()
	return collect(rows, scanSite)
}

// ListAllSites returns every site on the installation, for scheduler sweeps.
func (s *SQLStore) ListAllSites(ctx context.Context) ([]Site, error) {
	rows, err := s.db.QueryContext(ctx, listTimeout,
		s.db.Rebind(`SELECT `+siteColumns+` FROM hosting_sites ORDER BY tenant_id, name`))
	if err != nil {
		return nil, internalErr(err, "storage is unavailable")
	}
	defer rows.Close()
	return collect(rows, scanSite)
}

// DeleteSite removes a site the tenant owns.
func (s *SQLStore) DeleteSite(ctx context.Context, tenantID, id string) error {
	return s.mustAffect(ctx, "site", `DELETE FROM hosting_sites WHERE id = ? AND tenant_id = ?`, id, tenantID)
}

// SiteByDomain finds the site serving a domain anywhere on the installation.
// It exists so a domain cannot be claimed twice; the caller never exposes the
// owning tenant to another tenant.
func (s *SQLStore) SiteByDomain(ctx context.Context, domain string) (Site, error) {
	row := s.db.QueryRowContext(ctx, readTimeout,
		s.db.Rebind(`SELECT `+siteColumns+` FROM hosting_sites WHERE primary_domain = ?`), domain)
	v, err := scanSite(row)
	return v, scanRow("site", err)
}

const zoneColumns = `id, tenant_id, name, primary_ns, hostmaster, serial, refresh, retry, expire, minimum,
	default_ttl, dnssec, enabled, created_at, updated_at`

// scanZone reads one zone row.
func scanZone(sc interface{ Scan(...any) error }) (Zone, error) {
	var (
		z                    Zone
		dnssec, enabled      int64
		createdAt, updatedAt int64
	)
	err := sc.Scan(&z.ID, &z.TenantID, &z.Name, &z.PrimaryNS, &z.Hostmaster, &z.Serial, &z.Refresh, &z.Retry,
		&z.Expire, &z.Minimum, &z.DefaultTTL, &dnssec, &enabled, &createdAt, &updatedAt)
	if err != nil {
		return Zone{}, err
	}
	z.DNSSEC = dnssec != 0
	z.Enabled = enabled != 0
	z.CreatedAt = fromUnix(createdAt)
	z.UpdatedAt = fromUnix(updatedAt)
	return z, nil
}

// CreateZone inserts a new zone row.
func (s *SQLStore) CreateZone(ctx context.Context, v Zone) error {
	return s.exec(ctx, `INSERT INTO hosting_dns_zones (`+zoneColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.ID, v.TenantID, v.Name, v.PrimaryNS, v.Hostmaster, v.Serial, v.Refresh, v.Retry, v.Expire,
		v.Minimum, v.DefaultTTL, boolInt(v.DNSSEC), boolInt(v.Enabled), unix(v.CreatedAt), unix(v.UpdatedAt))
}

// UpdateZone rewrites the mutable columns of a zone the tenant owns.
func (s *SQLStore) UpdateZone(ctx context.Context, v Zone) error {
	return s.mustAffect(ctx, "zone", `UPDATE hosting_dns_zones SET primary_ns = ?, hostmaster = ?, serial = ?,
		refresh = ?, retry = ?, expire = ?, minimum = ?, default_ttl = ?, dnssec = ?, enabled = ?, updated_at = ?
		WHERE id = ? AND tenant_id = ?`,
		v.PrimaryNS, v.Hostmaster, v.Serial, v.Refresh, v.Retry, v.Expire, v.Minimum, v.DefaultTTL,
		boolInt(v.DNSSEC), boolInt(v.Enabled), unix(v.UpdatedAt), v.ID, v.TenantID)
}

// GetZone loads one zone scoped to its tenant.
func (s *SQLStore) GetZone(ctx context.Context, tenantID, id string) (Zone, error) {
	row := s.db.QueryRowContext(ctx, readTimeout,
		s.db.Rebind(`SELECT `+zoneColumns+` FROM hosting_dns_zones WHERE id = ? AND tenant_id = ?`), id, tenantID)
	v, err := scanZone(row)
	return v, scanRow("zone", err)
}

// ZoneByName finds a zone by its name anywhere on the installation.
func (s *SQLStore) ZoneByName(ctx context.Context, name string) (Zone, error) {
	row := s.db.QueryRowContext(ctx, readTimeout,
		s.db.Rebind(`SELECT `+zoneColumns+` FROM hosting_dns_zones WHERE name = ?`), name)
	v, err := scanZone(row)
	return v, scanRow("zone", err)
}

// ListZones returns every zone of one tenant.
func (s *SQLStore) ListZones(ctx context.Context, tenantID string) ([]Zone, error) {
	rows, err := s.db.QueryContext(ctx, listTimeout,
		s.db.Rebind(`SELECT `+zoneColumns+` FROM hosting_dns_zones WHERE tenant_id = ? ORDER BY name`), tenantID)
	if err != nil {
		return nil, internalErr(err, "storage is unavailable")
	}
	defer rows.Close()
	return collect(rows, scanZone)
}

// ListAllZones returns every zone on the installation.
func (s *SQLStore) ListAllZones(ctx context.Context) ([]Zone, error) {
	rows, err := s.db.QueryContext(ctx, listTimeout,
		s.db.Rebind(`SELECT `+zoneColumns+` FROM hosting_dns_zones ORDER BY name`))
	if err != nil {
		return nil, internalErr(err, "storage is unavailable")
	}
	defer rows.Close()
	return collect(rows, scanZone)
}

// DeleteZone removes a zone and its records for the owning tenant.
func (s *SQLStore) DeleteZone(ctx context.Context, tenantID, id string) error {
	if err := s.exec(ctx, `DELETE FROM hosting_dns_records WHERE zone_id = ? AND tenant_id = ?`, id, tenantID); err != nil {
		return err
	}
	return s.mustAffect(ctx, "zone", `DELETE FROM hosting_dns_zones WHERE id = ? AND tenant_id = ?`, id, tenantID)
}

const recordColumns = `id, zone_id, tenant_id, name, type, value, ttl, priority, weight, port, managed,
	created_at, updated_at`

// scanRecord reads one record row.
func scanRecord(sc interface{ Scan(...any) error }) (Record, error) {
	var (
		r                    Record
		managed              int64
		createdAt, updatedAt int64
	)
	err := sc.Scan(&r.ID, &r.ZoneID, &r.TenantID, &r.Name, &r.Type, &r.Value, &r.TTL, &r.Priority, &r.Weight,
		&r.Port, &managed, &createdAt, &updatedAt)
	if err != nil {
		return Record{}, err
	}
	r.Managed = managed != 0
	r.CreatedAt = fromUnix(createdAt)
	r.UpdatedAt = fromUnix(updatedAt)
	return r, nil
}

// CreateRecord inserts a new resource record.
func (s *SQLStore) CreateRecord(ctx context.Context, v Record) error {
	return s.exec(ctx, `INSERT INTO hosting_dns_records (`+recordColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.ID, v.ZoneID, v.TenantID, v.Name, v.Type, v.Value, v.TTL, v.Priority, v.Weight, v.Port,
		boolInt(v.Managed), unix(v.CreatedAt), unix(v.UpdatedAt))
}

// UpdateRecord rewrites a record the tenant owns.
func (s *SQLStore) UpdateRecord(ctx context.Context, v Record) error {
	return s.mustAffect(ctx, "record", `UPDATE hosting_dns_records SET name = ?, type = ?, value = ?, ttl = ?,
		priority = ?, weight = ?, port = ?, managed = ?, updated_at = ? WHERE id = ? AND tenant_id = ?`,
		v.Name, v.Type, v.Value, v.TTL, v.Priority, v.Weight, v.Port, boolInt(v.Managed), unix(v.UpdatedAt),
		v.ID, v.TenantID)
}

// GetRecord loads one record scoped to its tenant.
func (s *SQLStore) GetRecord(ctx context.Context, tenantID, id string) (Record, error) {
	row := s.db.QueryRowContext(ctx, readTimeout,
		s.db.Rebind(`SELECT `+recordColumns+` FROM hosting_dns_records WHERE id = ? AND tenant_id = ?`), id, tenantID)
	v, err := scanRecord(row)
	return v, scanRow("record", err)
}

// ListRecords returns every record of one zone owned by the tenant.
func (s *SQLStore) ListRecords(ctx context.Context, tenantID, zoneID string) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx, listTimeout,
		s.db.Rebind(`SELECT `+recordColumns+` FROM hosting_dns_records WHERE tenant_id = ? AND zone_id = ?
			ORDER BY name, type`), tenantID, zoneID)
	if err != nil {
		return nil, internalErr(err, "storage is unavailable")
	}
	defer rows.Close()
	return collect(rows, scanRecord)
}

// DeleteRecord removes a record the tenant owns.
func (s *SQLStore) DeleteRecord(ctx context.Context, tenantID, id string) error {
	return s.mustAffect(ctx, "record", `DELETE FROM hosting_dns_records WHERE id = ? AND tenant_id = ?`, id, tenantID)
}

const mailDomainColumns = `id, tenant_id, domain, dkim_selector, dkim_private, dkim_public, enabled,
	created_at, updated_at`

// scanMailDomain reads one mail-domain row and decodes the stored ciphertext.
func scanMailDomain(sc interface{ Scan(...any) error }) (MailDomain, error) {
	var (
		d                    MailDomain
		private              string
		enabled              int64
		createdAt, updatedAt int64
	)
	err := sc.Scan(&d.ID, &d.TenantID, &d.Domain, &d.DKIMSelector, &private, &d.DKIMPublic, &enabled,
		&createdAt, &updatedAt)
	if err != nil {
		return MailDomain{}, err
	}
	if private != "" {
		raw, decErr := base64.StdEncoding.DecodeString(private)
		if decErr != nil {
			return MailDomain{}, decErr
		}
		d.DKIMPrivate = raw
	}
	d.Enabled = enabled != 0
	d.CreatedAt = fromUnix(createdAt)
	d.UpdatedAt = fromUnix(updatedAt)
	return d, nil
}

// CreateMailDomain inserts a new mail domain.
func (s *SQLStore) CreateMailDomain(ctx context.Context, v MailDomain) error {
	return s.exec(ctx, `INSERT INTO hosting_mail_domains (`+mailDomainColumns+`) VALUES (?,?,?,?,?,?,?,?,?)`,
		v.ID, v.TenantID, v.Domain, v.DKIMSelector, base64.StdEncoding.EncodeToString(v.DKIMPrivate),
		v.DKIMPublic, boolInt(v.Enabled), unix(v.CreatedAt), unix(v.UpdatedAt))
}

// UpdateMailDomain rewrites a mail domain the tenant owns.
func (s *SQLStore) UpdateMailDomain(ctx context.Context, v MailDomain) error {
	return s.mustAffect(ctx, "mail domain", `UPDATE hosting_mail_domains SET dkim_selector = ?, dkim_private = ?,
		dkim_public = ?, enabled = ?, updated_at = ? WHERE id = ? AND tenant_id = ?`,
		v.DKIMSelector, base64.StdEncoding.EncodeToString(v.DKIMPrivate), v.DKIMPublic, boolInt(v.Enabled),
		unix(v.UpdatedAt), v.ID, v.TenantID)
}

// GetMailDomain loads one mail domain scoped to its tenant.
func (s *SQLStore) GetMailDomain(ctx context.Context, tenantID, id string) (MailDomain, error) {
	row := s.db.QueryRowContext(ctx, readTimeout,
		s.db.Rebind(`SELECT `+mailDomainColumns+` FROM hosting_mail_domains WHERE id = ? AND tenant_id = ?`),
		id, tenantID)
	v, err := scanMailDomain(row)
	return v, scanRow("mail domain", err)
}

// MailDomainByName finds a mail domain by name anywhere on the installation.
func (s *SQLStore) MailDomainByName(ctx context.Context, domain string) (MailDomain, error) {
	row := s.db.QueryRowContext(ctx, readTimeout,
		s.db.Rebind(`SELECT `+mailDomainColumns+` FROM hosting_mail_domains WHERE domain = ?`), domain)
	v, err := scanMailDomain(row)
	return v, scanRow("mail domain", err)
}

// ListMailDomains returns every mail domain of one tenant.
func (s *SQLStore) ListMailDomains(ctx context.Context, tenantID string) ([]MailDomain, error) {
	rows, err := s.db.QueryContext(ctx, listTimeout,
		s.db.Rebind(`SELECT `+mailDomainColumns+` FROM hosting_mail_domains WHERE tenant_id = ? ORDER BY domain`),
		tenantID)
	if err != nil {
		return nil, internalErr(err, "storage is unavailable")
	}
	defer rows.Close()
	return collect(rows, scanMailDomain)
}

// ListAllMailDomains returns every mail domain on the installation.
func (s *SQLStore) ListAllMailDomains(ctx context.Context) ([]MailDomain, error) {
	rows, err := s.db.QueryContext(ctx, listTimeout,
		s.db.Rebind(`SELECT `+mailDomainColumns+` FROM hosting_mail_domains ORDER BY domain`))
	if err != nil {
		return nil, internalErr(err, "storage is unavailable")
	}
	defer rows.Close()
	return collect(rows, scanMailDomain)
}

// DeleteMailDomain removes a mail domain with its mailboxes and aliases.
func (s *SQLStore) DeleteMailDomain(ctx context.Context, tenantID, id string) error {
	if err := s.exec(ctx, `DELETE FROM hosting_mailboxes WHERE domain_id = ? AND tenant_id = ?`, id, tenantID); err != nil {
		return err
	}
	if err := s.exec(ctx, `DELETE FROM hosting_mail_aliases WHERE domain_id = ? AND tenant_id = ?`, id, tenantID); err != nil {
		return err
	}
	return s.mustAffect(ctx, "mail domain", `DELETE FROM hosting_mail_domains WHERE id = ? AND tenant_id = ?`,
		id, tenantID)
}

const mailboxColumns = `id, tenant_id, domain_id, domain, local_part, password_hash, quota_mb, enabled,
	created_at, updated_at`

// scanMailbox reads one mailbox row.
func scanMailbox(sc interface{ Scan(...any) error }) (Mailbox, error) {
	var (
		m                    Mailbox
		enabled              int64
		createdAt, updatedAt int64
	)
	err := sc.Scan(&m.ID, &m.TenantID, &m.DomainID, &m.Domain, &m.LocalPart, &m.PasswordHash, &m.QuotaMB,
		&enabled, &createdAt, &updatedAt)
	if err != nil {
		return Mailbox{}, err
	}
	m.Enabled = enabled != 0
	m.CreatedAt = fromUnix(createdAt)
	m.UpdatedAt = fromUnix(updatedAt)
	return m, nil
}

// CreateMailbox inserts a new mailbox.
func (s *SQLStore) CreateMailbox(ctx context.Context, v Mailbox) error {
	return s.exec(ctx, `INSERT INTO hosting_mailboxes (`+mailboxColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		v.ID, v.TenantID, v.DomainID, v.Domain, v.LocalPart, v.PasswordHash, v.QuotaMB, boolInt(v.Enabled),
		unix(v.CreatedAt), unix(v.UpdatedAt))
}

// UpdateMailbox rewrites a mailbox the tenant owns.
func (s *SQLStore) UpdateMailbox(ctx context.Context, v Mailbox) error {
	return s.mustAffect(ctx, "mailbox", `UPDATE hosting_mailboxes SET password_hash = ?, quota_mb = ?,
		enabled = ?, updated_at = ? WHERE id = ? AND tenant_id = ?`,
		v.PasswordHash, v.QuotaMB, boolInt(v.Enabled), unix(v.UpdatedAt), v.ID, v.TenantID)
}

// GetMailbox loads one mailbox scoped to its tenant.
func (s *SQLStore) GetMailbox(ctx context.Context, tenantID, id string) (Mailbox, error) {
	row := s.db.QueryRowContext(ctx, readTimeout,
		s.db.Rebind(`SELECT `+mailboxColumns+` FROM hosting_mailboxes WHERE id = ? AND tenant_id = ?`), id, tenantID)
	v, err := scanMailbox(row)
	return v, scanRow("mailbox", err)
}

// ListMailboxes returns the tenant's mailboxes, optionally within one domain.
func (s *SQLStore) ListMailboxes(ctx context.Context, tenantID, domainID string) ([]Mailbox, error) {
	query := `SELECT ` + mailboxColumns + ` FROM hosting_mailboxes WHERE tenant_id = ? ORDER BY domain, local_part`
	args := []any{tenantID}
	if domainID != "" {
		query = `SELECT ` + mailboxColumns + ` FROM hosting_mailboxes WHERE tenant_id = ? AND domain_id = ?
			ORDER BY domain, local_part`
		args = append(args, domainID)
	}
	rows, err := s.db.QueryContext(ctx, listTimeout, s.db.Rebind(query), args...)
	if err != nil {
		return nil, internalErr(err, "storage is unavailable")
	}
	defer rows.Close()
	return collect(rows, scanMailbox)
}

// ListAllMailboxes returns every mailbox on the installation.
func (s *SQLStore) ListAllMailboxes(ctx context.Context) ([]Mailbox, error) {
	rows, err := s.db.QueryContext(ctx, listTimeout,
		s.db.Rebind(`SELECT `+mailboxColumns+` FROM hosting_mailboxes ORDER BY domain, local_part`))
	if err != nil {
		return nil, internalErr(err, "storage is unavailable")
	}
	defer rows.Close()
	return collect(rows, scanMailbox)
}

// DeleteMailbox removes a mailbox the tenant owns.
func (s *SQLStore) DeleteMailbox(ctx context.Context, tenantID, id string) error {
	return s.mustAffect(ctx, "mailbox", `DELETE FROM hosting_mailboxes WHERE id = ? AND tenant_id = ?`, id, tenantID)
}

const aliasColumns = `id, tenant_id, domain_id, domain, source, destination, enabled, created_at, updated_at`

// scanAlias reads one alias row.
func scanAlias(sc interface{ Scan(...any) error }) (Alias, error) {
	var (
		a                    Alias
		enabled              int64
		createdAt, updatedAt int64
	)
	err := sc.Scan(&a.ID, &a.TenantID, &a.DomainID, &a.Domain, &a.Source, &a.Destination, &enabled,
		&createdAt, &updatedAt)
	if err != nil {
		return Alias{}, err
	}
	a.Enabled = enabled != 0
	a.CreatedAt = fromUnix(createdAt)
	a.UpdatedAt = fromUnix(updatedAt)
	return a, nil
}

// CreateAlias inserts a new alias.
func (s *SQLStore) CreateAlias(ctx context.Context, v Alias) error {
	return s.exec(ctx, `INSERT INTO hosting_mail_aliases (`+aliasColumns+`) VALUES (?,?,?,?,?,?,?,?,?)`,
		v.ID, v.TenantID, v.DomainID, v.Domain, v.Source, v.Destination, boolInt(v.Enabled),
		unix(v.CreatedAt), unix(v.UpdatedAt))
}

// GetAlias loads one alias scoped to its tenant.
func (s *SQLStore) GetAlias(ctx context.Context, tenantID, id string) (Alias, error) {
	row := s.db.QueryRowContext(ctx, readTimeout,
		s.db.Rebind(`SELECT `+aliasColumns+` FROM hosting_mail_aliases WHERE id = ? AND tenant_id = ?`), id, tenantID)
	v, err := scanAlias(row)
	return v, scanRow("alias", err)
}

// ListAliases returns the tenant's aliases, optionally within one domain.
func (s *SQLStore) ListAliases(ctx context.Context, tenantID, domainID string) ([]Alias, error) {
	query := `SELECT ` + aliasColumns + ` FROM hosting_mail_aliases WHERE tenant_id = ? ORDER BY domain, source`
	args := []any{tenantID}
	if domainID != "" {
		query = `SELECT ` + aliasColumns + ` FROM hosting_mail_aliases WHERE tenant_id = ? AND domain_id = ?
			ORDER BY domain, source`
		args = append(args, domainID)
	}
	rows, err := s.db.QueryContext(ctx, listTimeout, s.db.Rebind(query), args...)
	if err != nil {
		return nil, internalErr(err, "storage is unavailable")
	}
	defer rows.Close()
	return collect(rows, scanAlias)
}

// ListAllAliases returns every alias on the installation.
func (s *SQLStore) ListAllAliases(ctx context.Context) ([]Alias, error) {
	rows, err := s.db.QueryContext(ctx, listTimeout,
		s.db.Rebind(`SELECT `+aliasColumns+` FROM hosting_mail_aliases ORDER BY domain, source`))
	if err != nil {
		return nil, internalErr(err, "storage is unavailable")
	}
	defer rows.Close()
	return collect(rows, scanAlias)
}

// DeleteAlias removes an alias the tenant owns.
func (s *SQLStore) DeleteAlias(ctx context.Context, tenantID, id string) error {
	return s.mustAffect(ctx, "alias", `DELETE FROM hosting_mail_aliases WHERE id = ? AND tenant_id = ?`, id, tenantID)
}

const appColumns = `id, tenant_id, name, runtime, git_remote, git_branch, domain, port, replicas, memory_mb,
	cpu_shares, state, workload_id, release_id, database_ref, created_at, updated_at`

// scanApp reads one app row.
func scanApp(sc interface{ Scan(...any) error }) (App, error) {
	var (
		a                    App
		port, replicas       int64
		createdAt, updatedAt int64
	)
	err := sc.Scan(&a.ID, &a.TenantID, &a.Name, &a.Runtime, &a.GitRemote, &a.GitBranch, &a.Domain, &port,
		&replicas, &a.MemoryMB, &a.CPUShares, &a.State, &a.WorkloadID, &a.ReleaseID, &a.DatabaseRef,
		&createdAt, &updatedAt)
	if err != nil {
		return App{}, err
	}
	a.Port = int(port)
	a.Replicas = int(replicas)
	a.CreatedAt = fromUnix(createdAt)
	a.UpdatedAt = fromUnix(updatedAt)
	return a, nil
}

// CreateApp inserts a new app.
func (s *SQLStore) CreateApp(ctx context.Context, v App) error {
	return s.exec(ctx, `INSERT INTO hosting_apps (`+appColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.ID, v.TenantID, v.Name, v.Runtime, v.GitRemote, v.GitBranch, v.Domain, int64(v.Port), int64(v.Replicas),
		v.MemoryMB, v.CPUShares, v.State, v.WorkloadID, v.ReleaseID, v.DatabaseRef, unix(v.CreatedAt),
		unix(v.UpdatedAt))
}

// UpdateApp rewrites the mutable columns of an app the tenant owns.
func (s *SQLStore) UpdateApp(ctx context.Context, v App) error {
	return s.mustAffect(ctx, "app", `UPDATE hosting_apps SET runtime = ?, git_remote = ?, git_branch = ?,
		domain = ?, port = ?, replicas = ?, memory_mb = ?, cpu_shares = ?, state = ?, workload_id = ?,
		release_id = ?, database_ref = ?, updated_at = ? WHERE id = ? AND tenant_id = ?`,
		v.Runtime, v.GitRemote, v.GitBranch, v.Domain, int64(v.Port), int64(v.Replicas), v.MemoryMB, v.CPUShares,
		v.State, v.WorkloadID, v.ReleaseID, v.DatabaseRef, unix(v.UpdatedAt), v.ID, v.TenantID)
}

// GetApp loads one app scoped to its tenant.
func (s *SQLStore) GetApp(ctx context.Context, tenantID, id string) (App, error) {
	row := s.db.QueryRowContext(ctx, readTimeout,
		s.db.Rebind(`SELECT `+appColumns+` FROM hosting_apps WHERE id = ? AND tenant_id = ?`), id, tenantID)
	v, err := scanApp(row)
	return v, scanRow("app", err)
}

// ListApps returns every app of one tenant.
func (s *SQLStore) ListApps(ctx context.Context, tenantID string) ([]App, error) {
	rows, err := s.db.QueryContext(ctx, listTimeout,
		s.db.Rebind(`SELECT `+appColumns+` FROM hosting_apps WHERE tenant_id = ? ORDER BY name`), tenantID)
	if err != nil {
		return nil, internalErr(err, "storage is unavailable")
	}
	defer rows.Close()
	return collect(rows, scanApp)
}

// ListAllApps returns every app on the installation.
func (s *SQLStore) ListAllApps(ctx context.Context) ([]App, error) {
	rows, err := s.db.QueryContext(ctx, listTimeout,
		s.db.Rebind(`SELECT `+appColumns+` FROM hosting_apps ORDER BY tenant_id, name`))
	if err != nil {
		return nil, internalErr(err, "storage is unavailable")
	}
	defer rows.Close()
	return collect(rows, scanApp)
}

// DeleteApp removes an app with its env entries and releases.
func (s *SQLStore) DeleteApp(ctx context.Context, tenantID, id string) error {
	if err := s.exec(ctx, `DELETE FROM hosting_app_env WHERE app_id = ? AND tenant_id = ?`, id, tenantID); err != nil {
		return err
	}
	if err := s.exec(ctx, `DELETE FROM hosting_app_releases WHERE app_id = ? AND tenant_id = ?`, id, tenantID); err != nil {
		return err
	}
	return s.mustAffect(ctx, "app", `DELETE FROM hosting_apps WHERE id = ? AND tenant_id = ?`, id, tenantID)
}

// PutEnv upserts one environment entry by deleting and reinserting the key,
// which keeps the statement portable across every supported driver.
func (s *SQLStore) PutEnv(ctx context.Context, v EnvVar) error {
	if err := s.exec(ctx, `DELETE FROM hosting_app_env WHERE app_id = ? AND tenant_id = ? AND env_key = ?`,
		v.AppID, v.TenantID, v.Key); err != nil {
		return err
	}
	return s.exec(ctx, `INSERT INTO hosting_app_env (app_id, tenant_id, env_key, plain_value, enc_value,
		is_secret, updated_at) VALUES (?,?,?,?,?,?,?)`,
		v.AppID, v.TenantID, v.Key, v.Value, base64.StdEncoding.EncodeToString(v.Encrypted),
		boolInt(v.Secret), unix(v.UpdatedAt))
}

// ListEnv returns every environment entry of an app the tenant owns.
func (s *SQLStore) ListEnv(ctx context.Context, tenantID, appID string) ([]EnvVar, error) {
	rows, err := s.db.QueryContext(ctx, listTimeout,
		s.db.Rebind(`SELECT app_id, tenant_id, env_key, plain_value, enc_value, is_secret, updated_at
			FROM hosting_app_env WHERE tenant_id = ? AND app_id = ? ORDER BY env_key`), tenantID, appID)
	if err != nil {
		return nil, internalErr(err, "storage is unavailable")
	}
	defer rows.Close()
	return collect(rows, func(sc interface{ Scan(...any) error }) (EnvVar, error) {
		var (
			e         EnvVar
			enc       string
			secret    int64
			updatedAt int64
		)
		if scanErr := sc.Scan(&e.AppID, &e.TenantID, &e.Key, &e.Value, &enc, &secret, &updatedAt); scanErr != nil {
			return EnvVar{}, scanErr
		}
		if enc != "" {
			raw, decErr := base64.StdEncoding.DecodeString(enc)
			if decErr != nil {
				return EnvVar{}, decErr
			}
			e.Encrypted = raw
		}
		e.Secret = secret != 0
		e.UpdatedAt = fromUnix(updatedAt)
		return e, nil
	})
}

// DeleteEnv removes one environment entry.
func (s *SQLStore) DeleteEnv(ctx context.Context, tenantID, appID, key string) error {
	return s.mustAffect(ctx, "environment variable",
		`DELETE FROM hosting_app_env WHERE app_id = ? AND tenant_id = ? AND env_key = ?`, appID, tenantID, key)
}

const releaseColumns = `id, tenant_id, app_id, number, source, image, command, state, workload_id, log,
	created_at, updated_at`

// scanRelease reads one release row.
func scanRelease(sc interface{ Scan(...any) error }) (Release, error) {
	var (
		r                    Release
		createdAt, updatedAt int64
	)
	err := sc.Scan(&r.ID, &r.TenantID, &r.AppID, &r.Number, &r.Source, &r.Image, &r.Command, &r.State,
		&r.WorkloadID, &r.Log, &createdAt, &updatedAt)
	if err != nil {
		return Release{}, err
	}
	r.CreatedAt = fromUnix(createdAt)
	r.UpdatedAt = fromUnix(updatedAt)
	return r, nil
}

// CreateRelease inserts a new release.
func (s *SQLStore) CreateRelease(ctx context.Context, v Release) error {
	return s.exec(ctx, `INSERT INTO hosting_app_releases (`+releaseColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.ID, v.TenantID, v.AppID, v.Number, v.Source, v.Image, v.Command, v.State, v.WorkloadID, v.Log,
		unix(v.CreatedAt), unix(v.UpdatedAt))
}

// UpdateRelease rewrites the mutable columns of a release.
func (s *SQLStore) UpdateRelease(ctx context.Context, v Release) error {
	return s.mustAffect(ctx, "release", `UPDATE hosting_app_releases SET state = ?, image = ?, command = ?,
		workload_id = ?, log = ?, updated_at = ? WHERE id = ? AND tenant_id = ?`,
		v.State, v.Image, v.Command, v.WorkloadID, v.Log, unix(v.UpdatedAt), v.ID, v.TenantID)
}

// GetRelease loads one release scoped to its tenant.
func (s *SQLStore) GetRelease(ctx context.Context, tenantID, id string) (Release, error) {
	row := s.db.QueryRowContext(ctx, readTimeout,
		s.db.Rebind(`SELECT `+releaseColumns+` FROM hosting_app_releases WHERE id = ? AND tenant_id = ?`),
		id, tenantID)
	v, err := scanRelease(row)
	return v, scanRow("release", err)
}

// ListReleases returns an app's releases, newest first.
func (s *SQLStore) ListReleases(ctx context.Context, tenantID, appID string) ([]Release, error) {
	rows, err := s.db.QueryContext(ctx, listTimeout,
		s.db.Rebind(`SELECT `+releaseColumns+` FROM hosting_app_releases WHERE tenant_id = ? AND app_id = ?
			ORDER BY number DESC`), tenantID, appID)
	if err != nil {
		return nil, internalErr(err, "storage is unavailable")
	}
	defer rows.Close()
	return collect(rows, scanRelease)
}

// DeleteRelease removes one release row during deploy cleanup.
func (s *SQLStore) DeleteRelease(ctx context.Context, tenantID, id string) error {
	return s.mustAffect(ctx, "release", `DELETE FROM hosting_app_releases WHERE id = ? AND tenant_id = ?`,
		id, tenantID)
}

// PutOwnership upserts a domain-ownership row.
func (s *SQLStore) PutOwnership(ctx context.Context, v DomainOwnership) error {
	if err := s.exec(ctx, `DELETE FROM hosting_domain_ownership WHERE domain = ? AND tenant_id = ?`,
		v.Domain, v.TenantID); err != nil {
		return err
	}
	return s.exec(ctx, `INSERT INTO hosting_domain_ownership (domain, tenant_id, token, method, verified,
		verified_at, created_at) VALUES (?,?,?,?,?,?,?)`,
		v.Domain, v.TenantID, v.Token, v.Method, boolInt(v.Verified), unix(v.VerifiedAt), unix(v.CreatedAt))
}

// GetOwnership loads the ownership row for a domain.
func (s *SQLStore) GetOwnership(ctx context.Context, domain string) (DomainOwnership, error) {
	row := s.db.QueryRowContext(ctx, readTimeout,
		s.db.Rebind(`SELECT domain, tenant_id, token, method, verified, verified_at, created_at
			FROM hosting_domain_ownership WHERE domain = ?`), domain)
	v, err := scanOwnership(row)
	return v, scanRow("domain", err)
}

// ListOwnership returns every domain a tenant has claimed.
func (s *SQLStore) ListOwnership(ctx context.Context, tenantID string) ([]DomainOwnership, error) {
	rows, err := s.db.QueryContext(ctx, listTimeout,
		s.db.Rebind(`SELECT domain, tenant_id, token, method, verified, verified_at, created_at
			FROM hosting_domain_ownership WHERE tenant_id = ? ORDER BY domain`), tenantID)
	if err != nil {
		return nil, internalErr(err, "storage is unavailable")
	}
	defer rows.Close()
	return collect(rows, scanOwnership)
}

// scanOwnership reads one ownership row.
func scanOwnership(sc interface{ Scan(...any) error }) (DomainOwnership, error) {
	var (
		o                     DomainOwnership
		verified              int64
		verifiedAt, createdAt int64
	)
	err := sc.Scan(&o.Domain, &o.TenantID, &o.Token, &o.Method, &verified, &verifiedAt, &createdAt)
	if err != nil {
		return DomainOwnership{}, err
	}
	o.Verified = verified != 0
	o.VerifiedAt = fromUnix(verifiedAt)
	o.CreatedAt = fromUnix(createdAt)
	return o, nil
}

// collect drains a result set through a row scanner.
func collect[T any](rows *sql.Rows, scan func(interface{ Scan(...any) error }) (T, error)) ([]T, error) {
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, internalErr(err, "storage is unavailable")
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, internalErr(err, "storage is unavailable")
	}
	return out, nil
}
