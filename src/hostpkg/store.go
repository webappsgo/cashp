package hostpkg

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/webappsgo/cashp/src/database"
	apperr "github.com/webappsgo/cashp/src/errors"
)

// PackageRecord is the ownership record for one package cashp installed.
type PackageRecord struct {
	// Name is the package name as the host package manager knows it.
	Name string
	// Service is the managed service the package belongs to.
	Service Service
	// Manager is the package manager that installed it.
	Manager ManagerKind
	// Distribution is the os-release ID of the host at install time.
	Distribution string
	// Version is the installed version, when the manager reported one.
	Version string
	// InstalledAt is the install time in Unix seconds.
	InstalledAt int64
}

// RepoRecord is the record for one third-party repository cashp added.
type RepoRecord struct {
	// ID is the repository identifier.
	ID RepoID
	// Manager is the package manager the definition was written for.
	Manager ManagerKind
	// DefinitionPath is the file cashp wrote.
	DefinitionPath string
	// Fingerprints are the pinned key fingerprints that were verified.
	Fingerprints []string
	// AddedAt is the time the repository was added, in Unix seconds.
	AddedAt int64
}

// Recorder persists what cashp installed. It is an interface so the package
// can be exercised without a database, and so the ownership rule -- cashp
// never removes a package it did not install -- has exactly one definition.
type Recorder interface {
	RecordInstall(ctx context.Context, rec PackageRecord) error
	RecordRemoval(ctx context.Context, name string) error
	Owned(ctx context.Context, name string) (bool, error)
	RecordRepo(ctx context.Context, rec RepoRecord) error
	RepoRecorded(ctx context.Context, id RepoID) (bool, error)
}

// Store is the database-backed Recorder.
type Store struct {
	db *database.DB
}

// NewStore returns a Store over an open database handle.
func NewStore(db *database.DB) (*Store, error) {
	if db == nil {
		return nil, failUnavailable(ErrCommandFailed, "package inventory is unavailable")
	}

	return &Store{db: db}, nil
}

// RecordInstall inserts or refreshes the ownership row for a package. It is
// an update-then-insert upsert so it behaves identically on every driver.
func (s *Store) RecordInstall(ctx context.Context, rec PackageRecord) error {
	if err := ValidatePackageName(rec.Name); err != nil {
		return err
	}
	if rec.InstalledAt == 0 {
		rec.InstalledAt = time.Now().UTC().Unix()
	}

	const update = `UPDATE host_packages
SET service = ?, manager = ?, distribution = ?, version = ?, installed_at = ?, removed_at = 0
WHERE package_name = ?`

	res, err := s.db.ExecContext(ctx, database.TimeoutWrite, update,
		string(rec.Service), string(rec.Manager), rec.Distribution, rec.Version, rec.InstalledAt, rec.Name)
	if err != nil {
		return storeFailure(err)
	}
	affected, err := res.RowsAffected()
	if err == nil && affected > 0 {
		return nil
	}

	const insert = `INSERT INTO host_packages
(package_name, service, manager, distribution, version, installed_at, removed_at)
VALUES (?, ?, ?, ?, ?, ?, 0)`

	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite, insert,
		rec.Name, string(rec.Service), string(rec.Manager), rec.Distribution, rec.Version, rec.InstalledAt); err != nil {
		if database.IsConflict(err) {
			return nil
		}
		return storeFailure(err)
	}

	return nil
}

// RecordRemoval marks a package as no longer installed by cashp. The row is
// kept so the history of what cashp touched survives the removal.
func (s *Store) RecordRemoval(ctx context.Context, name string) error {
	if err := ValidatePackageName(name); err != nil {
		return err
	}

	const stmt = `UPDATE host_packages SET removed_at = ? WHERE package_name = ?`
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite, stmt, time.Now().UTC().Unix(), name); err != nil {
		return storeFailure(err)
	}

	return nil
}

// Owned reports whether cashp installed the package and has not removed it.
func (s *Store) Owned(ctx context.Context, name string) (bool, error) {
	if err := ValidatePackageName(name); err != nil {
		return false, err
	}

	const query = `SELECT removed_at FROM host_packages WHERE package_name = ?`

	var removedAt int64
	if err := s.db.QueryRowContext(ctx, database.TimeoutSelect, query, name).Scan(&removedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) || database.IsNotFound(err) {
			return false, nil
		}
		return false, storeFailure(err)
	}

	return removedAt == 0, nil
}

// RecordRepo records a third-party repository cashp added.
func (s *Store) RecordRepo(ctx context.Context, rec RepoRecord) error {
	if err := ValidateRepoName(string(rec.ID)); err != nil {
		return err
	}
	if rec.AddedAt == 0 {
		rec.AddedAt = time.Now().UTC().Unix()
	}
	fingerprints := strings.Join(rec.Fingerprints, ",")

	const update = `UPDATE host_repos
SET manager = ?, definition_path = ?, fingerprints = ?, added_at = ?
WHERE repo_id = ?`

	res, err := s.db.ExecContext(ctx, database.TimeoutWrite, update,
		string(rec.Manager), rec.DefinitionPath, fingerprints, rec.AddedAt, string(rec.ID))
	if err != nil {
		return storeFailure(err)
	}
	affected, err := res.RowsAffected()
	if err == nil && affected > 0 {
		return nil
	}

	const insert = `INSERT INTO host_repos (repo_id, manager, definition_path, fingerprints, added_at)
VALUES (?, ?, ?, ?, ?)`

	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite, insert,
		string(rec.ID), string(rec.Manager), rec.DefinitionPath, fingerprints, rec.AddedAt); err != nil {
		if database.IsConflict(err) {
			return nil
		}
		return storeFailure(err)
	}

	return nil
}

// RepoRecorded reports whether a repository has already been added.
func (s *Store) RepoRecorded(ctx context.Context, id RepoID) (bool, error) {
	if err := ValidateRepoName(string(id)); err != nil {
		return false, err
	}

	const query = `SELECT added_at FROM host_repos WHERE repo_id = ?`

	var addedAt int64
	if err := s.db.QueryRowContext(ctx, database.TimeoutSelect, query, string(id)).Scan(&addedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) || database.IsNotFound(err) {
			return false, nil
		}
		return false, storeFailure(err)
	}

	return addedAt > 0, nil
}

// storeFailure converts a database error into an API-safe typed error; the
// driver message, which can carry a DSN or a filesystem path, is dropped.
func storeFailure(err error) error {
	if database.IsTimeout(err) {
		return fail(ErrCommandTimeout, apperr.CodeTimeout, http.StatusGatewayTimeout, "package inventory timed out")
	}

	return failUnavailable(ErrCommandFailed, "package inventory is unavailable")
}

// MemoryRecorder is an in-memory Recorder for tests and for a dry run.
type MemoryRecorder struct {
	mu       sync.Mutex
	packages map[string]PackageRecord
	removed  map[string]bool
	repos    map[RepoID]RepoRecord
}

// NewMemoryRecorder returns an empty in-memory recorder.
func NewMemoryRecorder() *MemoryRecorder {
	return &MemoryRecorder{
		packages: map[string]PackageRecord{},
		removed:  map[string]bool{},
		repos:    map[RepoID]RepoRecord{},
	}
}

// RecordInstall stores an ownership record in memory.
func (r *MemoryRecorder) RecordInstall(_ context.Context, rec PackageRecord) error {
	if err := ValidatePackageName(rec.Name); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if rec.InstalledAt == 0 {
		rec.InstalledAt = time.Now().UTC().Unix()
	}
	r.packages[rec.Name] = rec
	delete(r.removed, rec.Name)

	return nil
}

// RecordRemoval marks a package as removed.
func (r *MemoryRecorder) RecordRemoval(_ context.Context, name string) error {
	if err := ValidatePackageName(name); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.removed[name] = true

	return nil
}

// Owned reports whether the recorder holds a live ownership record.
func (r *MemoryRecorder) Owned(_ context.Context, name string) (bool, error) {
	if err := ValidatePackageName(name); err != nil {
		return false, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.packages[name]

	return ok && !r.removed[name], nil
}

// RecordRepo stores a repository record in memory.
func (r *MemoryRecorder) RecordRepo(_ context.Context, rec RepoRecord) error {
	if err := ValidateRepoName(string(rec.ID)); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if rec.AddedAt == 0 {
		rec.AddedAt = time.Now().UTC().Unix()
	}
	r.repos[rec.ID] = rec

	return nil
}

// RepoRecorded reports whether a repository record exists.
func (r *MemoryRecorder) RepoRecorded(_ context.Context, id RepoID) (bool, error) {
	if err := ValidateRepoName(string(id)); err != nil {
		return false, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.repos[id]

	return ok, nil
}

// Packages returns the recorded package names in sorted order.
func (r *MemoryRecorder) Packages() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	names := make([]string, 0, len(r.packages))
	for name := range r.packages {
		if r.removed[name] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	return names
}
