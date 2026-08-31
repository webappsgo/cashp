// Package database opens and manages the application's SQL database
// connection, applies the self-creating idempotent schema, and provides the
// query-timeout, transaction, retry and cluster primitives required by
// AI.md PART 10 (Database & Cluster).
//
// This package deliberately does not import src/config: the config package
// owns the YAML/env layer and would create an import cycle. Callers copy
// config.DatabaseConfig into database.Config.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Query timeout budgets per AI.md PART 10 -> "Query Timeouts". Every call
// through this package must pass one of these (or an explicit duration).
const (
	// TimeoutSelect is the budget for a simple single-table SELECT.
	TimeoutSelect = 5 * time.Second
	// TimeoutWrite is the budget for INSERT/UPDATE/DELETE statements.
	TimeoutWrite = 10 * time.Second
	// TimeoutJoin is the budget for multi-table/JOIN SELECT statements.
	TimeoutJoin = 15 * time.Second
	// TimeoutBulk is the budget for bulk data operations.
	TimeoutBulk = 60 * time.Second
	// TimeoutSchema is the budget for schema creation and updates.
	TimeoutSchema = 5 * time.Minute
	// TimeoutReport is the budget for aggregation/report queries.
	TimeoutReport = 2 * time.Minute
)

// Connection pool defaults per AI.md PART 10 -> "Pool Sizing Guidelines"
// (Small deployment, 1-2 nodes).
const (
	defaultMaxOpen     = 25
	defaultMaxIdle     = 5
	defaultMaxLifetime = 5 * time.Minute
	defaultMaxIdleTime = time.Minute
	// SQLite is a single-file embedded engine; a smaller pool avoids
	// needless writer contention while still allowing concurrent WAL reads.
	sqliteMaxOpen = 8
	sqliteMaxIdle = 4
)

// Canonical driver identifiers used throughout the package. The values are
// the names the underlying database/sql drivers register themselves under.
const (
	DriverSQLite    = "sqlite"
	DriverPostgres  = "pgx"
	DriverMySQL     = "mysql"
	DriverSQLServer = "sqlserver"
	DriverLibSQL    = "libsql"
)

// Config mirrors config.DatabaseConfig plus optional pool tuning. The three
// core fields are the contract other packages build against; the pool fields
// are optional and fall back to the PART 10 defaults when left zero.
type Config struct {
	// Driver is the configured driver name or alias (sqlite, postgres,
	// mysql, mariadb, mssql, libsql, ...).
	Driver string
	// URL is the full DSN for network databases. Optional for SQLite.
	URL string
	// Dir is the data directory used to place the SQLite database file
	// when URL is empty.
	Dir string
	// MaxOpen caps the total number of open connections.
	MaxOpen int
	// MaxIdle caps the number of idle connections kept in the pool.
	MaxIdle int
	// MaxLifetime is the maximum age of a pooled connection.
	MaxLifetime time.Duration
	// MaxIdleTime is how long a connection may sit idle before closing.
	MaxIdleTime time.Duration
}

// DB wraps *sql.DB with the driver identity, timeout helpers, retry logic
// and the cluster primitives.
type DB struct {
	db     *sql.DB
	driver string
}

// Sentinel errors returned by this package. They carry the PART 10 error
// codes so HTTP handlers can map them without inspecting driver errors.
var (
	// ErrTimeout is returned when a query exceeded its timeout budget.
	ErrTimeout = errors.New("TIMEOUT: request timed out")
	// ErrNotFound is returned when a query matched no rows.
	ErrNotFound = errors.New("NOT_FOUND: resource not found")
	// ErrCanceled is returned when the caller canceled the request.
	ErrCanceled = errors.New("CANCELED: request was canceled")
	// ErrConflict is returned when an optimistic-locking update lost the
	// race against a concurrent writer.
	ErrConflict = errors.New("CONFLICT: resource was modified by another request")
	// ErrUnsupportedDriver is returned by Open for an unknown driver name.
	ErrUnsupportedDriver = errors.New("unsupported database driver")
	// ErrMissingURL is returned when a network driver has no DSN configured.
	ErrMissingURL = errors.New("database url is required for this driver")
	// ErrMaxRetries is returned when every retry attempt hit a
	// serialization or write conflict.
	ErrMaxRetries = errors.New("max retries exceeded")
)

// Open resolves the driver alias, builds the DSN, configures the connection
// pool and verifies connectivity. It does not create the schema; callers
// invoke EnsureSchema once every feature package has registered its DDL.
func Open(cfg Config) (*DB, error) {
	driver, err := NormalizeDriver(cfg.Driver)
	if err != nil {
		return nil, err
	}

	dsn, err := buildDSN(driver, cfg)
	if err != nil {
		return nil, err
	}

	handle, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open database (%s): %w", driver, err)
	}

	applyPool(handle, driver, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), TimeoutSelect)
	defer cancel()
	if err := handle.PingContext(ctx); err != nil {
		// Close before returning so a failed Open never leaks the pool.
		_ = handle.Close()
		return nil, fmt.Errorf("database ping failed (%s): %w", driver, err)
	}

	return &DB{db: handle, driver: driver}, nil
}

// New wraps an already-open *sql.DB. It exists for tests and for callers
// that obtained a handle by other means.
func New(handle *sql.DB, driver string) (*DB, error) {
	if handle == nil {
		return nil, errors.New("database handle is nil")
	}
	name, err := NormalizeDriver(driver)
	if err != nil {
		return nil, err
	}
	return &DB{db: handle, driver: name}, nil
}

// SQL returns the underlying pool for callers that need the standard API.
// Every such call still has to carry its own context timeout.
func (db *DB) SQL() *sql.DB { return db.db }

// Driver returns the canonical driver name backing this connection.
func (db *DB) Driver() string { return db.driver }

// Stats exposes pool statistics for health and metrics endpoints.
func (db *DB) Stats() sql.DBStats { return db.db.Stats() }

// Close releases the connection pool.
func (db *DB) Close() error {
	if db == nil || db.db == nil {
		return nil
	}
	return db.db.Close()
}

// Ping verifies the connection is alive within the simple-SELECT budget.
func (db *DB) Ping(ctx context.Context) error {
	pctx, cancel := context.WithTimeout(ctx, TimeoutSelect)
	defer cancel()
	if err := db.db.PingContext(pctx); err != nil {
		return Classify(err)
	}
	return nil
}

// QueryContext runs a multi-row query under the given timeout. The deadline
// stays live while the rows are being read and is released when it fires.
func (db *DB) QueryContext(ctx context.Context, timeout time.Duration, query string, args ...any) (*sql.Rows, error) {
	qctx, cancel := context.WithTimeout(ctx, resolveTimeout(timeout))
	rows, err := db.db.QueryContext(qctx, db.Rebind(query), args...)
	if err != nil {
		cancel()
		return nil, Classify(err)
	}
	// Rows are consumed after this function returns, so the deadline must
	// outlive the call; release it once the context is done.
	context.AfterFunc(qctx, cancel)
	return rows, nil
}

// QueryRowContext runs a single-row query under the given timeout. The
// deadline stays live until Scan has had a chance to run.
func (db *DB) QueryRowContext(ctx context.Context, timeout time.Duration, query string, args ...any) *sql.Row {
	qctx, cancel := context.WithTimeout(ctx, resolveTimeout(timeout))
	row := db.db.QueryRowContext(qctx, db.Rebind(query), args...)
	context.AfterFunc(qctx, cancel)
	return row
}

// ExecContext runs a statement under the given timeout.
func (db *DB) ExecContext(ctx context.Context, timeout time.Duration, query string, args ...any) (sql.Result, error) {
	ectx, cancel := context.WithTimeout(ctx, resolveTimeout(timeout))
	defer cancel()
	res, err := db.db.ExecContext(ectx, db.Rebind(query), args...)
	if err != nil {
		return nil, Classify(err)
	}
	return res, nil
}

// UpdateVersioned runs an optimistic-locking UPDATE (PART 10 -> "Optimistic
// Locking"). The statement must carry its own `version = version + 1` and a
// `WHERE ... AND version = ?` guard; a zero row count means another writer
// won the race.
func (db *DB) UpdateVersioned(ctx context.Context, query string, args ...any) error {
	res, err := db.ExecContext(ctx, TimeoutWrite, query, args...)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return Classify(err)
	}
	if rows == 0 {
		return ErrConflict
	}
	return nil
}

// Rebind converts the package's `?` placeholders into the dialect the active
// driver expects. It rewrites placeholders only, never values, and assumes
// the query text contains no literal `?` inside a string constant — true for
// every query in this codebase, which is parameterized throughout.
func (db *DB) Rebind(query string) string {
	switch db.driver {
	case DriverPostgres:
		return rebindNumbered(query, "$")
	case DriverSQLServer:
		return rebindNumbered(query, "@p")
	default:
		return query
	}
}

func rebindNumbered(query, prefix string) string {
	if !strings.Contains(query, "?") {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString(prefix)
			b.WriteString(fmt.Sprint(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// NormalizeDriver maps a configured driver name or common alias onto the
// canonical database/sql driver name this package registers.
func NormalizeDriver(name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "sqlite", "sqlite3", "file":
		return DriverSQLite, nil
	case "postgres", "postgresql", "pgx", "pg":
		return DriverPostgres, nil
	case "mysql", "mariadb":
		return DriverMySQL, nil
	case "mssql", "sqlserver", "azuresql":
		return DriverSQLServer, nil
	case "libsql", "turso":
		return DriverLibSQL, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedDriver, name)
	}
}

// buildDSN produces the connection string for the driver. SQLite falls back
// to a file under the configured data directory when no URL is given.
func buildDSN(driver string, cfg Config) (string, error) {
	if driver != DriverSQLite {
		if strings.TrimSpace(cfg.URL) == "" {
			return "", fmt.Errorf("%w: %s", ErrMissingURL, driver)
		}
		return cfg.URL, nil
	}

	if strings.TrimSpace(cfg.URL) != "" {
		return cfg.URL, nil
	}

	dir := strings.TrimSpace(cfg.Dir)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(dir, "cashp.db")

	// WAL for concurrent readers, a busy timeout so writers wait instead of
	// failing, foreign keys on, and immediate transactions so cluster lock
	// acquisition takes the write lock up front (PART 10 -> advisory lock).
	params := url.Values{}
	params.Add("_pragma", "busy_timeout(5000)")
	params.Add("_pragma", "journal_mode(WAL)")
	params.Add("_pragma", "foreign_keys(ON)")
	params.Set("_txlock", "immediate")

	return "file:" + path + "?" + params.Encode(), nil
}

// applyPool configures the connection pool from Config, filling in the
// PART 10 defaults for any unset value.
func applyPool(handle *sql.DB, driver string, cfg Config) {
	maxOpen, maxIdle := defaultMaxOpen, defaultMaxIdle
	if driver == DriverSQLite {
		maxOpen, maxIdle = sqliteMaxOpen, sqliteMaxIdle
	}
	if cfg.MaxOpen > 0 {
		maxOpen = cfg.MaxOpen
	}
	if cfg.MaxIdle > 0 {
		maxIdle = cfg.MaxIdle
	}
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}
	lifetime, idleTime := defaultMaxLifetime, defaultMaxIdleTime
	if cfg.MaxLifetime > 0 {
		lifetime = cfg.MaxLifetime
	}
	if cfg.MaxIdleTime > 0 {
		idleTime = cfg.MaxIdleTime
	}

	handle.SetMaxOpenConns(maxOpen)
	handle.SetMaxIdleConns(maxIdle)
	handle.SetConnMaxLifetime(lifetime)
	handle.SetConnMaxIdleTime(idleTime)
}

// resolveTimeout guards against a zero or negative budget being passed in.
func resolveTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return TimeoutSelect
	}
	return timeout
}
