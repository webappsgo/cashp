package orchestrator

import (
	"context"
	"database/sql"
	"encoding/hex"
	stderrors "errors"
	"time"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/security"
)

// MaxEventPageSize caps how many audit rows one read may return, so a
// caller cannot pull the whole history of a busy node in a single request.
const MaxEventPageSize = 500

// DefaultEventPageSize is used when a caller asks for no particular size.
const DefaultEventPageSize = 100

// WorkloadRecord is the persisted ownership record for one managed
// container or virtual machine.
type WorkloadRecord struct {
	// Ref identifies the workload and the account that owns it.
	Ref Ref
	// QualifiedName is the engine-visible name derived from Ref.
	QualifiedName string
	// Backend is the engine that runs the workload.
	Backend BackendName
	// Kind is the workload kind.
	Kind Kind
	// EngineID is the engine's own identifier for the workload.
	EngineID string
	// Image is the image or template the workload was created from.
	Image string
	// ImageDigest is the pinned digest, when one was used.
	ImageDigest string
	// State is the last observed run state.
	State State
	// CPUMillicores is the CPU allowance in thousandths of a core.
	CPUMillicores int64
	// MemoryBytes is the memory ceiling.
	MemoryBytes int64
	// DiskBytes is the storage ceiling.
	DiskBytes int64
	// CreatedAt is when cashp first recorded the workload.
	CreatedAt time.Time
	// UpdatedAt is when the record last changed.
	UpdatedAt time.Time
	// RemovedAt is when the workload was destroyed; zero while it lives.
	RemovedAt time.Time
	// Version is the optimistic-locking counter.
	Version int64
}

// SnapshotRecord is the persisted record of one captured snapshot.
type SnapshotRecord struct {
	// ID is the row identifier.
	ID string
	// QualifiedName is the workload the snapshot belongs to.
	QualifiedName string
	// TenantID is the owning account.
	TenantID string
	// Name is the snapshot name as the engine knows it.
	Name string
	// Backend is the engine that holds the snapshot.
	Backend BackendName
	// SizeBytes is the reported size, when the engine reports one.
	SizeBytes int64
	// Stateful reports whether guest memory was captured too.
	Stateful bool
	// CreatedAt is when the snapshot was taken.
	CreatedAt time.Time
	// RemovedAt is when it was discarded; zero while it exists.
	RemovedAt time.Time
	// Note is an operator-supplied description.
	Note string
}

// EventRecord is the persisted record of one lifecycle action. It is the
// queryable mirror of the audit log: the log is the append-only source of
// truth, this table is what the panel reads.
type EventRecord struct {
	// ID is the row identifier.
	ID string
	// TenantID is the account the action affected.
	TenantID string
	// QualifiedName is the workload the action affected.
	QualifiedName string
	// Backend is the engine involved.
	Backend BackendName
	// Action names the operation.
	Action string
	// ActorUserID identifies who asked for it.
	ActorUserID string
	// ActorRole is the role that authorised it.
	ActorRole string
	// RequestID correlates the row with the request log.
	RequestID string
	// Outcome is "ok" or "error".
	Outcome string
	// Detail is a short, non-sensitive summary.
	Detail string
	// CreatedAt is when the action happened.
	CreatedAt time.Time
}

// Store persists orchestration state.
//
// Every statement is parameterized and every workload-scoped statement
// carries the account identifier in its WHERE clause, so a caller holding
// only a workload name can never reach another account's row.
type Store struct {
	db *database.DB
}

// NewStore builds a store over an open database handle.
func NewStore(db *database.DB) (*Store, error) {
	if db == nil {
		return nil, validationErr("database", "required")
	}
	return &Store{db: db}, nil
}

// newRowID returns a random identifier for a new row.
func newRowID() (string, error) {
	raw, err := security.RandomSecret(16)
	if err != nil {
		return "", backendErr("", "row_id", err)
	}
	return hex.EncodeToString(raw), nil
}

// unixOrZero renders a timestamp for storage, keeping the zero time as zero
// rather than as a negative epoch offset.
func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().Unix()
}

// timeOrZero is the inverse of unixOrZero.
func timeOrZero(v int64) time.Time {
	if v <= 0 {
		return time.Time{}
	}
	return time.Unix(v, 0).UTC()
}

// millicores converts a fractional core count to the integer unit stored in
// the database.
func millicores(cores float64) int64 {
	if cores <= 0 {
		return 0
	}
	return int64(cores*1000 + 0.5)
}

// coresFromMillicores is the inverse of millicores.
func coresFromMillicores(v int64) float64 {
	if v <= 0 {
		return 0
	}
	return float64(v) / 1000
}

// boolToInt stores a flag portably: not every supported driver has a native
// boolean type.
func boolToInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

// SaveWorkload writes a workload record, inserting it the first time and
// updating it afterwards. The update is scoped to the owning account, so a
// name collision across accounts cannot overwrite the wrong row.
func (s *Store) SaveWorkload(ctx context.Context, rec WorkloadRecord) error {
	if err := rec.Ref.Validate(); err != nil {
		return err
	}
	qualified, err := rec.Ref.Qualified()
	if err != nil {
		return err
	}
	now := time.Now().UTC()

	const update = `UPDATE ` + tableWorkloads + ` SET
	backend = ?, kind = ?, engine_id = ?, image = ?, image_digest = ?, state = ?,
	cpu_millicores = ?, memory_bytes = ?, disk_bytes = ?, updated_at = ?, removed_at = ?,
	version = version + 1
WHERE qualified_name = ? AND tenant_id = ?`

	res, err := s.db.ExecContext(ctx, database.TimeoutWrite, update,
		string(rec.Backend), string(rec.Kind), rec.EngineID, rec.Image, rec.ImageDigest,
		string(rec.State), rec.CPUMillicores, rec.MemoryBytes, rec.DiskBytes,
		unixOrZero(now), unixOrZero(rec.RemovedAt), qualified, rec.Ref.TenantID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err == nil && affected > 0 {
		return nil
	}

	const insert = `INSERT INTO ` + tableWorkloads + ` (
	qualified_name, tenant_id, class, workload_name, backend, kind, engine_id,
	image, image_digest, state, cpu_millicores, memory_bytes, disk_bytes,
	created_at, updated_at, removed_at, version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`

	created := rec.CreatedAt
	if created.IsZero() {
		created = now
	}
	_, err = s.db.ExecContext(ctx, database.TimeoutWrite, insert,
		qualified, rec.Ref.TenantID, string(rec.Ref.Class), rec.Ref.Name,
		string(rec.Backend), string(rec.Kind), rec.EngineID, rec.Image, rec.ImageDigest,
		string(rec.State), rec.CPUMillicores, rec.MemoryBytes, rec.DiskBytes,
		unixOrZero(created), unixOrZero(now), unixOrZero(rec.RemovedAt))
	return err
}

// CPUCores reports the stored CPU allowance as a core count, so callers can
// keep working in cores while the column stays an integer.
func (r WorkloadRecord) CPUCores() float64 {
	return coresFromMillicores(r.CPUMillicores)
}

// GetWorkload reads one workload owned by the given account.
func (s *Store) GetWorkload(ctx context.Context, tenantID, qualifiedName string) (WorkloadRecord, error) {
	var out WorkloadRecord

	if err := ValidateTenantID(tenantID); err != nil {
		return out, err
	}
	if _, ok := parseQualified(qualifiedName); !ok {
		return out, validationErr("qualified_name", "format")
	}

	const query = `SELECT qualified_name, tenant_id, class, workload_name, backend, kind,
	engine_id, image, image_digest, state, cpu_millicores, memory_bytes, disk_bytes,
	created_at, updated_at, removed_at, version
FROM ` + tableWorkloads + `
WHERE qualified_name = ? AND tenant_id = ?`

	row := s.db.QueryRowContext(ctx, database.TimeoutSelect, query, qualifiedName, tenantID)
	rec, err := scanWorkload(row)
	if err != nil {
		return out, err
	}
	return rec, nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanWorkload reads one workload row.
func scanWorkload(row rowScanner) (WorkloadRecord, error) {
	var (
		rec                                 WorkloadRecord
		class, backend, kind, state         string
		created, updated, removed, cpuMilli int64
	)
	err := row.Scan(&rec.QualifiedName, &rec.Ref.TenantID, &class, &rec.Ref.Name,
		&backend, &kind, &rec.EngineID, &rec.Image, &rec.ImageDigest, &state,
		&cpuMilli, &rec.MemoryBytes, &rec.DiskBytes, &created, &updated, &removed, &rec.Version)
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return WorkloadRecord{}, notFoundErr()
		}
		return WorkloadRecord{}, err
	}
	rec.Ref.Class = Class(class)
	rec.Backend = BackendName(backend)
	rec.Kind = Kind(kind)
	rec.State = State(state)
	rec.CPUMillicores = cpuMilli
	rec.CreatedAt = timeOrZero(created)
	rec.UpdatedAt = timeOrZero(updated)
	rec.RemovedAt = timeOrZero(removed)
	return rec, nil
}

// ListWorkloads reads every live workload owned by one account. The account
// identifier is mandatory: there is no query in this package that returns
// rows across accounts.
func (s *Store) ListWorkloads(ctx context.Context, filter Filter) ([]WorkloadRecord, error) {
	if err := ValidateTenantID(filter.TenantID); err != nil {
		return nil, err
	}

	const query = `SELECT qualified_name, tenant_id, class, workload_name, backend, kind,
	engine_id, image, image_digest, state, cpu_millicores, memory_bytes, disk_bytes,
	created_at, updated_at, removed_at, version
FROM ` + tableWorkloads + `
WHERE tenant_id = ? AND removed_at = 0
ORDER BY qualified_name`

	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect, query, filter.TenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WorkloadRecord
	for rows.Next() {
		rec, err := scanWorkload(rows)
		if err != nil {
			return nil, err
		}
		if filter.Class != "" && rec.Ref.Class != filter.Class {
			continue
		}
		if filter.Kind != "" && rec.Kind != filter.Kind {
			continue
		}
		if filter.State != "" && rec.State != filter.State {
			continue
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// MarkWorkloadRemoved records that a workload was destroyed. The row stays
// so the action remains attributable; nothing in this package deletes it.
func (s *Store) MarkWorkloadRemoved(ctx context.Context, tenantID, qualifiedName string) error {
	if err := ValidateTenantID(tenantID); err != nil {
		return err
	}
	if _, ok := parseQualified(qualifiedName); !ok {
		return validationErr("qualified_name", "format")
	}

	const query = `UPDATE ` + tableWorkloads + `
SET removed_at = ?, updated_at = ?, state = ?, version = version + 1
WHERE qualified_name = ? AND tenant_id = ? AND removed_at = 0`

	now := unixOrZero(time.Now().UTC())
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, query,
		now, now, string(StateStopped), qualifiedName, tenantID)
	return err
}

// SaveSnapshot records a captured snapshot.
func (s *Store) SaveSnapshot(ctx context.Context, rec SnapshotRecord) (string, error) {
	if err := ValidateTenantID(rec.TenantID); err != nil {
		return "", err
	}
	if err := ValidateSnapshotName(rec.Name); err != nil {
		return "", err
	}
	if _, ok := parseQualified(rec.QualifiedName); !ok {
		return "", validationErr("qualified_name", "format")
	}
	id := rec.ID
	if id == "" {
		generated, err := newRowID()
		if err != nil {
			return "", err
		}
		id = generated
	}

	const query = `INSERT INTO ` + tableSnapshots + ` (
	snapshot_id, qualified_name, tenant_id, snapshot_name, backend,
	size_bytes, stateful, created_at, removed_at, note
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`

	created := rec.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, query,
		id, rec.QualifiedName, rec.TenantID, rec.Name, string(rec.Backend),
		rec.SizeBytes, boolToInt(rec.Stateful), unixOrZero(created), rec.Note)
	if err != nil {
		return "", err
	}
	return id, nil
}

// ListSnapshotRecords reads the live snapshots of one workload owned by one
// account.
func (s *Store) ListSnapshotRecords(ctx context.Context, tenantID, qualifiedName string) ([]SnapshotRecord, error) {
	if err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	if _, ok := parseQualified(qualifiedName); !ok {
		return nil, validationErr("qualified_name", "format")
	}

	const query = `SELECT snapshot_id, qualified_name, tenant_id, snapshot_name, backend,
	size_bytes, stateful, created_at, removed_at, note
FROM ` + tableSnapshots + `
WHERE tenant_id = ? AND qualified_name = ? AND removed_at = 0
ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect, query, tenantID, qualifiedName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SnapshotRecord
	for rows.Next() {
		var (
			rec              SnapshotRecord
			backend          string
			stateful         int64
			created, removed int64
		)
		if err := rows.Scan(&rec.ID, &rec.QualifiedName, &rec.TenantID, &rec.Name, &backend,
			&rec.SizeBytes, &stateful, &created, &removed, &rec.Note); err != nil {
			return nil, err
		}
		rec.Backend = BackendName(backend)
		rec.Stateful = stateful != 0
		rec.CreatedAt = timeOrZero(created)
		rec.RemovedAt = timeOrZero(removed)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// MarkSnapshotRemoved records that a snapshot was discarded.
func (s *Store) MarkSnapshotRemoved(ctx context.Context, tenantID, snapshotID string) error {
	if err := ValidateTenantID(tenantID); err != nil {
		return err
	}
	if snapshotID == "" || hasUnsafeChars(snapshotID) {
		return validationErr("snapshot_id", "charset")
	}

	const query = `UPDATE ` + tableSnapshots + `
SET removed_at = ?
WHERE snapshot_id = ? AND tenant_id = ? AND removed_at = 0`

	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, query,
		unixOrZero(time.Now().UTC()), snapshotID, tenantID)
	return err
}

// SaveEvent records one lifecycle action.
func (s *Store) SaveEvent(ctx context.Context, rec EventRecord) (string, error) {
	if err := ValidateTenantID(rec.TenantID); err != nil {
		return "", err
	}
	id := rec.ID
	if id == "" {
		generated, err := newRowID()
		if err != nil {
			return "", err
		}
		id = generated
	}

	const query = `INSERT INTO ` + tableEvents + ` (
	event_id, tenant_id, qualified_name, backend, action,
	actor_user_id, actor_role, request_id, outcome, detail, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	created := rec.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, query,
		id, rec.TenantID, rec.QualifiedName, string(rec.Backend), rec.Action,
		rec.ActorUserID, rec.ActorRole, rec.RequestID, rec.Outcome, rec.Detail,
		unixOrZero(created))
	if err != nil {
		return "", err
	}
	return id, nil
}

// ListEvents reads one account's most recent lifecycle actions.
func (s *Store) ListEvents(ctx context.Context, tenantID string, limit int) ([]EventRecord, error) {
	if err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = DefaultEventPageSize
	}
	if limit > MaxEventPageSize {
		limit = MaxEventPageSize
	}

	const query = `SELECT event_id, tenant_id, qualified_name, backend, action,
	actor_user_id, actor_role, request_id, outcome, detail, created_at
FROM ` + tableEvents + `
WHERE tenant_id = ?
ORDER BY created_at DESC, event_id DESC
LIMIT ?`

	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect, query, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EventRecord
	for rows.Next() {
		var (
			rec     EventRecord
			backend string
			created int64
		)
		if err := rows.Scan(&rec.ID, &rec.TenantID, &rec.QualifiedName, &backend, &rec.Action,
			&rec.ActorUserID, &rec.ActorRole, &rec.RequestID, &rec.Outcome, &rec.Detail,
			&created); err != nil {
			return nil, err
		}
		rec.Backend = BackendName(backend)
		rec.CreatedAt = timeOrZero(created)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
