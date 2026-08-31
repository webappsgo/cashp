package dbservice

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

// Store is the persistence contract for the managed-database layer. It is an
// interface so the whole package can be tested against an in-memory fake: the
// test suite never opens a database, starts a container or touches a socket.
//
// Every tenant-facing method takes a tenant identifier and every query filters
// on it. The single exception is ListAllInstances, which exists only for the
// cluster-wide scheduled sweeps and is never reachable from a tenant request.
type Store interface {
	// CreateInstance inserts a new instance row.
	CreateInstance(ctx context.Context, inst *Instance) error
	// UpdateInstance writes an existing instance row, scoped to its tenant.
	UpdateInstance(ctx context.Context, inst *Instance) error
	// GetInstance loads one instance owned by the tenant.
	GetInstance(ctx context.Context, tenantID, id string) (*Instance, error)
	// GetInstanceByName loads one instance by its tenant-visible name.
	GetInstanceByName(ctx context.Context, tenantID, name string) (*Instance, error)
	// ListInstances lists a tenant's live instances.
	ListInstances(ctx context.Context, tenantID string) ([]*Instance, error)
	// ListReplicas lists the live replicas of one primary.
	ListReplicas(ctx context.Context, tenantID, primaryID string) ([]*Instance, error)
	// ListAllInstances lists every live instance on the server. It backs the
	// scheduled health, backup and rotation sweeps and has no tenant-facing
	// caller.
	ListAllInstances(ctx context.Context) ([]*Instance, error)

	// CreateCredential inserts an issued account.
	CreateCredential(ctx context.Context, cred *Credential) error
	// UpdateCredential writes an existing account row, scoped to its tenant.
	UpdateCredential(ctx context.Context, cred *Credential) error
	// GetCredential loads one live account by username.
	GetCredential(ctx context.Context, tenantID, instanceID, username string) (*Credential, error)
	// GetAdminCredential loads an instance's administrative account.
	GetAdminCredential(ctx context.Context, tenantID, instanceID string) (*Credential, error)
	// ListCredentials lists an instance's live accounts.
	ListCredentials(ctx context.Context, tenantID, instanceID string) ([]*Credential, error)
	// ListCredentialsOlderThan lists live tenant accounts whose password was
	// last set before the cutoff. It backs the rotation sweep.
	ListCredentialsOlderThan(ctx context.Context, cutoff time.Time) ([]*Credential, error)

	// CreateDatabase inserts a named database row.
	CreateDatabase(ctx context.Context, db *Database) error
	// MarkDatabaseDropped tombstones a database row.
	MarkDatabaseDropped(ctx context.Context, tenantID, instanceID, name string, at time.Time) error
	// ListDatabases lists an instance's live databases.
	ListDatabases(ctx context.Context, tenantID, instanceID string) ([]*Database, error)

	// CreateBackup inserts a backup record.
	CreateBackup(ctx context.Context, rec *BackupRecord) error
	// GetBackup loads one backup record owned by the tenant.
	GetBackup(ctx context.Context, tenantID, id string) (*BackupRecord, error)
	// ListBackups lists an instance's restorable backups, newest first.
	ListBackups(ctx context.Context, tenantID, instanceID string) ([]*BackupRecord, error)
	// MarkBackupDeleted tombstones a backup record after retention removed
	// its artifact.
	MarkBackupDeleted(ctx context.Context, tenantID, id string, at time.Time) error
}

// sqlStore is the Store backed by cashp's own database.
type sqlStore struct {
	db *database.DB
}

// NewStore returns the Store backed by cashp's own database.
func NewStore(db *database.DB) Store {
	return &sqlStore{db: db}
}

// instanceColumns is the column list every instance read shares.
const instanceColumns = `id, tenant_id, name, engine, engine_version, role, primary_id, state,
	container_id, container_name, volume_name, network, host, port, admin_user,
	cpu_millicores, memory_bytes, disk_bytes, pids_limit,
	health, health_detail, health_checked_at, created_at, updated_at, destroyed_at, row_version`

func (s *sqlStore) CreateInstance(ctx context.Context, inst *Instance) error {
	if err := ValidateTenantID(inst.TenantID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `INSERT INTO db_instances (`+instanceColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		inst.ID, inst.TenantID, inst.Name, string(inst.Engine), inst.EngineVersion,
		string(inst.Role), inst.PrimaryID, string(inst.State),
		inst.ContainerID, inst.ContainerName, inst.VolumeName, inst.Network, inst.Host, inst.Port, inst.AdminUser,
		millicores(inst.Limits.CPUCores), inst.Limits.MemoryBytes, inst.Limits.DiskBytes, inst.Limits.PidsLimit,
		string(inst.Health), inst.HealthDetail, unixOf(inst.HealthCheckedAt),
		unixOf(inst.CreatedAt), unixOf(inst.UpdatedAt), unixOf(inst.DestroyedAt), inst.RowVersion)
	if err != nil {
		return ErrInternal(err, "That instance could not be recorded.")
	}
	return nil
}

func (s *sqlStore) UpdateInstance(ctx context.Context, inst *Instance) error {
	if err := ValidateTenantID(inst.TenantID); err != nil {
		return err
	}
	inst.RowVersion++
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `UPDATE db_instances SET
		name = ?, engine = ?, engine_version = ?, role = ?, primary_id = ?, state = ?,
		container_id = ?, container_name = ?, volume_name = ?, network = ?, host = ?, port = ?, admin_user = ?,
		cpu_millicores = ?, memory_bytes = ?, disk_bytes = ?, pids_limit = ?,
		health = ?, health_detail = ?, health_checked_at = ?, updated_at = ?, destroyed_at = ?, row_version = ?
		WHERE id = ? AND tenant_id = ?`,
		inst.Name, string(inst.Engine), inst.EngineVersion, string(inst.Role), inst.PrimaryID, string(inst.State),
		inst.ContainerID, inst.ContainerName, inst.VolumeName, inst.Network, inst.Host, inst.Port, inst.AdminUser,
		millicores(inst.Limits.CPUCores), inst.Limits.MemoryBytes, inst.Limits.DiskBytes, inst.Limits.PidsLimit,
		string(inst.Health), inst.HealthDetail, unixOf(inst.HealthCheckedAt),
		unixOf(inst.UpdatedAt), unixOf(inst.DestroyedAt), inst.RowVersion,
		inst.ID, inst.TenantID)
	if err != nil {
		return ErrInternal(err, "That instance could not be updated.")
	}
	return nil
}

func (s *sqlStore) GetInstance(ctx context.Context, tenantID, id string) (*Instance, error) {
	if err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+instanceColumns+` FROM db_instances WHERE id = ? AND tenant_id = ?`, id, tenantID)
	return scanInstanceRow(row)
}

func (s *sqlStore) GetInstanceByName(ctx context.Context, tenantID, name string) (*Instance, error) {
	if err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+instanceColumns+` FROM db_instances WHERE tenant_id = ? AND name = ? AND state <> ?`,
		tenantID, name, string(StateDestroyed))
	return scanInstanceRow(row)
}

func (s *sqlStore) ListInstances(ctx context.Context, tenantID string) ([]*Instance, error) {
	if err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+instanceColumns+` FROM db_instances WHERE tenant_id = ? AND state <> ? ORDER BY name`,
		tenantID, string(StateDestroyed))
	return scanInstanceRows(rows, err)
}

func (s *sqlStore) ListReplicas(ctx context.Context, tenantID, primaryID string) ([]*Instance, error) {
	if err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+instanceColumns+` FROM db_instances
		 WHERE tenant_id = ? AND primary_id = ? AND state <> ? ORDER BY name`,
		tenantID, primaryID, string(StateDestroyed))
	return scanInstanceRows(rows, err)
}

func (s *sqlStore) ListAllInstances(ctx context.Context) ([]*Instance, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutReport,
		`SELECT `+instanceColumns+` FROM db_instances WHERE state <> ? ORDER BY tenant_id, name`,
		string(StateDestroyed))
	return scanInstanceRows(rows, err)
}

// credentialColumns is the column list every credential read shares.
const credentialColumns = `id, tenant_id, instance_id, username, role, database_name, grant_level,
	secret, created_at, rotated_at, revoked_at`

func (s *sqlStore) CreateCredential(ctx context.Context, cred *Credential) error {
	if err := ValidateTenantID(cred.TenantID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `INSERT INTO db_credentials (`+credentialColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cred.ID, cred.TenantID, cred.InstanceID, cred.Username, string(cred.Role),
		cred.Database, string(cred.Grant), encodeSecret(cred.Secret),
		unixOf(cred.CreatedAt), unixOf(cred.RotatedAt), unixOf(cred.RevokedAt))
	if err != nil {
		return ErrInternal(err, "That credential could not be recorded.")
	}
	return nil
}

func (s *sqlStore) UpdateCredential(ctx context.Context, cred *Credential) error {
	if err := ValidateTenantID(cred.TenantID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `UPDATE db_credentials SET
		role = ?, database_name = ?, grant_level = ?, secret = ?, rotated_at = ?, revoked_at = ?
		WHERE id = ? AND tenant_id = ?`,
		string(cred.Role), cred.Database, string(cred.Grant), encodeSecret(cred.Secret),
		unixOf(cred.RotatedAt), unixOf(cred.RevokedAt), cred.ID, cred.TenantID)
	if err != nil {
		return ErrInternal(err, "That credential could not be updated.")
	}
	return nil
}

func (s *sqlStore) GetCredential(ctx context.Context, tenantID, instanceID, username string) (*Credential, error) {
	if err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+credentialColumns+` FROM db_credentials
		 WHERE tenant_id = ? AND instance_id = ? AND username = ? AND revoked_at = 0`,
		tenantID, instanceID, username)
	return scanCredentialRow(row)
}

func (s *sqlStore) GetAdminCredential(ctx context.Context, tenantID, instanceID string) (*Credential, error) {
	if err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+credentialColumns+` FROM db_credentials
		 WHERE tenant_id = ? AND instance_id = ? AND role = ? AND revoked_at = 0`,
		tenantID, instanceID, string(RoleAdmin))
	return scanCredentialRow(row)
}

func (s *sqlStore) ListCredentials(ctx context.Context, tenantID, instanceID string) ([]*Credential, error) {
	if err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+credentialColumns+` FROM db_credentials
		 WHERE tenant_id = ? AND instance_id = ? AND revoked_at = 0 ORDER BY username`,
		tenantID, instanceID)
	return scanCredentialRows(rows, err)
}

func (s *sqlStore) ListCredentialsOlderThan(ctx context.Context, cutoff time.Time) ([]*Credential, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutReport,
		`SELECT `+credentialColumns+` FROM db_credentials
		 WHERE revoked_at = 0 AND role <> ? AND
		       (CASE WHEN rotated_at > 0 THEN rotated_at ELSE created_at END) < ?
		 ORDER BY tenant_id, instance_id, username`,
		string(RoleAdmin), unixOf(cutoff))
	return scanCredentialRows(rows, err)
}

// databaseColumns is the column list every database read shares.
const databaseColumns = `id, tenant_id, instance_id, name, owner, created_at, dropped_at`

func (s *sqlStore) CreateDatabase(ctx context.Context, db *Database) error {
	if err := ValidateTenantID(db.TenantID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `INSERT INTO db_databases (`+databaseColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		db.ID, db.TenantID, db.InstanceID, db.Name, db.Owner, unixOf(db.CreatedAt), unixOf(db.DroppedAt))
	if err != nil {
		return ErrInternal(err, "That database could not be recorded.")
	}
	return nil
}

// MarkDatabaseDropped tombstones the row rather than removing it, because the
// schema rules forbid a DELETE and because a dropped database has to stay
// visible in the tenant's history.
func (s *sqlStore) MarkDatabaseDropped(ctx context.Context, tenantID, instanceID, name string, at time.Time) error {
	if err := ValidateTenantID(tenantID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE db_databases SET dropped_at = ?
		 WHERE tenant_id = ? AND instance_id = ? AND name = ? AND dropped_at = 0`,
		unixOf(at), tenantID, instanceID, name)
	if err != nil {
		return ErrInternal(err, "That database could not be updated.")
	}
	return nil
}

func (s *sqlStore) ListDatabases(ctx context.Context, tenantID, instanceID string) ([]*Database, error) {
	if err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+databaseColumns+` FROM db_databases
		 WHERE tenant_id = ? AND instance_id = ? AND dropped_at = 0 ORDER BY name`,
		tenantID, instanceID)
	if err != nil {
		return nil, ErrInternal(err, "Those databases could not be listed.")
	}
	defer rows.Close()
	out := make([]*Database, 0)
	for rows.Next() {
		var (
			rec              Database
			created, dropped int64
		)
		if err := rows.Scan(&rec.ID, &rec.TenantID, &rec.InstanceID, &rec.Name, &rec.Owner, &created, &dropped); err != nil {
			return nil, ErrInternal(err, "Those databases could not be listed.")
		}
		rec.CreatedAt = timeOf(created)
		rec.DroppedAt = timeOf(dropped)
		out = append(out, &rec)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Those databases could not be listed.")
	}
	return out, nil
}

// backupColumns is the column list every backup read shares.
const backupColumns = `id, tenant_id, instance_id, artifact_id, engine, engine_version,
	database_name, size_bytes, checksum, encrypted, created_at, deleted_at`

func (s *sqlStore) CreateBackup(ctx context.Context, rec *BackupRecord) error {
	if err := ValidateTenantID(rec.TenantID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `INSERT INTO db_backups (`+backupColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.TenantID, rec.InstanceID, rec.ArtifactID, string(rec.Engine), rec.EngineVersion,
		rec.Database, rec.SizeBytes, rec.Checksum, boolInt(rec.Encrypted),
		unixOf(rec.CreatedAt), unixOf(rec.DeletedAt))
	if err != nil {
		return ErrInternal(err, "That backup could not be recorded.")
	}
	return nil
}

func (s *sqlStore) GetBackup(ctx context.Context, tenantID, id string) (*BackupRecord, error) {
	if err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+backupColumns+` FROM db_backups WHERE id = ? AND tenant_id = ? AND deleted_at = 0`,
		id, tenantID)
	rec, err := scanBackupRow(row)
	if err != nil {
		return nil, err
	}
	return rec, nil
}

func (s *sqlStore) ListBackups(ctx context.Context, tenantID, instanceID string) ([]*BackupRecord, error) {
	if err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+backupColumns+` FROM db_backups
		 WHERE tenant_id = ? AND instance_id = ? AND deleted_at = 0 ORDER BY created_at DESC`,
		tenantID, instanceID)
	if err != nil {
		return nil, ErrInternal(err, "Those backups could not be listed.")
	}
	defer rows.Close()
	out := make([]*BackupRecord, 0)
	for rows.Next() {
		rec, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Those backups could not be listed.")
	}
	return out, nil
}

func (s *sqlStore) MarkBackupDeleted(ctx context.Context, tenantID, id string, at time.Time) error {
	if err := ValidateTenantID(tenantID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE db_backups SET deleted_at = ? WHERE id = ? AND tenant_id = ? AND deleted_at = 0`,
		unixOf(at), id, tenantID)
	if err != nil {
		return ErrInternal(err, "That backup could not be updated.")
	}
	return nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows so one scan helper
// serves single reads and listings.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanInstanceRow reads one instance, translating a missing row into the
// package's not-found error so a caller never distinguishes "does not exist"
// from "belongs to another tenant".
func scanInstanceRow(row rowScanner) (*Instance, error) {
	inst, err := scanInstance(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound("database instance")
	}
	return inst, err
}

func scanInstanceRows(rows *sql.Rows, err error) ([]*Instance, error) {
	if err != nil {
		return nil, ErrInternal(err, "Those instances could not be listed.")
	}
	defer rows.Close()
	out := make([]*Instance, 0)
	for rows.Next() {
		inst, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inst)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Those instances could not be listed.")
	}
	return out, nil
}

func scanInstance(row rowScanner) (*Instance, error) {
	var (
		inst                            Instance
		engine, role, state, health     string
		cpu, memory, disk               int64
		pids                            int
		checked, created, updated, gone int64
	)
	err := row.Scan(&inst.ID, &inst.TenantID, &inst.Name, &engine, &inst.EngineVersion,
		&role, &inst.PrimaryID, &state,
		&inst.ContainerID, &inst.ContainerName, &inst.VolumeName, &inst.Network, &inst.Host, &inst.Port, &inst.AdminUser,
		&cpu, &memory, &disk, &pids,
		&health, &inst.HealthDetail, &checked, &created, &updated, &gone, &inst.RowVersion)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, ErrInternal(err, "That instance could not be read.")
	}
	inst.Engine = Engine(engine)
	inst.Role = Role(role)
	inst.State = State(state)
	inst.Health = HealthState(health)
	inst.Limits = ResourceLimits{
		CPUCores:    float64(cpu) / 1000,
		MemoryBytes: memory,
		DiskBytes:   disk,
		PidsLimit:   pids,
	}
	inst.HealthCheckedAt = timeOf(checked)
	inst.CreatedAt = timeOf(created)
	inst.UpdatedAt = timeOf(updated)
	inst.DestroyedAt = timeOf(gone)
	return &inst, nil
}

func scanCredentialRow(row rowScanner) (*Credential, error) {
	cred, err := scanCredential(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound("credential")
	}
	return cred, err
}

func scanCredentialRows(rows *sql.Rows, err error) ([]*Credential, error) {
	if err != nil {
		return nil, ErrInternal(err, "Those credentials could not be listed.")
	}
	defer rows.Close()
	out := make([]*Credential, 0)
	for rows.Next() {
		cred, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cred)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Those credentials could not be listed.")
	}
	return out, nil
}

func scanCredential(row rowScanner) (*Credential, error) {
	var (
		cred                      Credential
		role, level, secret       string
		created, rotated, revoked int64
	)
	err := row.Scan(&cred.ID, &cred.TenantID, &cred.InstanceID, &cred.Username, &role,
		&cred.Database, &level, &secret, &created, &rotated, &revoked)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, ErrInternal(err, "That credential could not be read.")
	}
	cred.Role = CredentialRole(role)
	cred.Grant = GrantLevel(level)
	raw, decodeErr := decodeSecret(secret)
	if decodeErr != nil {
		return nil, decodeErr
	}
	cred.Secret = raw
	cred.CreatedAt = timeOf(created)
	cred.RotatedAt = timeOf(rotated)
	cred.RevokedAt = timeOf(revoked)
	return &cred, nil
}

func scanBackupRow(row rowScanner) (*BackupRecord, error) {
	rec, err := scanBackup(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound("backup")
	}
	return rec, err
}

func scanBackup(row rowScanner) (*BackupRecord, error) {
	var (
		rec              BackupRecord
		engine           string
		encrypted        int
		created, deleted int64
	)
	err := row.Scan(&rec.ID, &rec.TenantID, &rec.InstanceID, &rec.ArtifactID, &engine, &rec.EngineVersion,
		&rec.Database, &rec.SizeBytes, &rec.Checksum, &encrypted, &created, &deleted)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, ErrInternal(err, "That backup could not be read.")
	}
	rec.Engine = Engine(engine)
	rec.Encrypted = encrypted != 0
	rec.CreatedAt = timeOf(created)
	rec.DeletedAt = timeOf(deleted)
	return &rec, nil
}

// encodeSecret renders ciphertext as base64 so the DDL needs only a text
// column on every supported driver.
func encodeSecret(secret []byte) string {
	if len(secret) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(secret)
}

// decodeSecret reverses encodeSecret.
func decodeSecret(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, ErrInternal(err, "That credential could not be read.")
	}
	return raw, nil
}

// unixOf renders a time as Unix seconds, mapping the zero time to zero so an
// unset timestamp is a plain 0 in every row.
func unixOf(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// timeOf reverses unixOf.
func timeOf(v int64) time.Time {
	if v <= 0 {
		return time.Time{}
	}
	return time.Unix(v, 0).UTC()
}

// millicores renders a fractional core count as an integer so the limit
// column stays portable.
func millicores(cores float64) int64 {
	return int64(cores * 1000)
}

// boolInt renders a flag as the integer the schema stores.
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
