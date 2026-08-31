package database

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Self-creating schema per AI.md PART 10 -> "Database Schema".
//
// Feature packages (auth, billing, support, hosting, ...) call RegisterSchema
// from their init() with a function that returns idempotent DDL for the
// active driver. EnsureSchema then applies every registered fragment in
// registration order on startup. There are no migration files and no schema
// version table: every statement is CREATE TABLE IF NOT EXISTS, an additive
// ALTER TABLE, or an index creation. Destructive statements (DROP COLUMN,
// DROP TABLE, DELETE, renames) are never permitted.

// schemaFragment is one registered package's DDL contribution.
type schemaFragment struct {
	name string
	ddl  func(driver string) []string
}

var (
	schemaMu        sync.Mutex
	schemaFragments []schemaFragment
	schemaNames     = map[string]bool{}
)

// RegisterSchema records a package's idempotent DDL under a unique name.
// It is intended to be called from init(); registering the same name twice
// is a programming error and panics.
func RegisterSchema(name string, ddl func(driver string) []string) {
	if name == "" {
		panic("database: RegisterSchema requires a name")
	}
	if ddl == nil {
		panic("database: RegisterSchema requires a ddl function for " + name)
	}

	schemaMu.Lock()
	defer schemaMu.Unlock()
	if schemaNames[name] {
		panic("database: duplicate schema registration: " + name)
	}
	schemaNames[name] = true
	schemaFragments = append(schemaFragments, schemaFragment{name: name, ddl: ddl})
}

// RegisteredSchemas lists the registered fragment names in registration
// order, for diagnostics and admin surfaces.
func RegisteredSchemas() []string {
	schemaMu.Lock()
	defer schemaMu.Unlock()
	names := make([]string, 0, len(schemaFragments))
	for _, frag := range schemaFragments {
		names = append(names, frag.name)
	}
	return names
}

// EnsureSchema applies every registered DDL fragment. It is idempotent and
// safe to run on every startup and on every cluster node concurrently:
// statements that fail only because the object already exists are ignored.
func (db *DB) EnsureSchema(ctx context.Context) error {
	schemaMu.Lock()
	fragments := make([]schemaFragment, len(schemaFragments))
	copy(fragments, schemaFragments)
	schemaMu.Unlock()

	return db.applyFragments(ctx, fragments)
}

// applyFragments executes the DDL of each fragment in order, tolerating the
// "already exists" errors that make repeated application idempotent.
func (db *DB) applyFragments(ctx context.Context, fragments []schemaFragment) error {
	for _, frag := range fragments {
		for _, stmt := range frag.ddl(db.driver) {
			if stmt == "" {
				continue
			}
			// Schema statements are static DDL with no user input, so they
			// carry no placeholders and skip Rebind.
			sctx, cancel := context.WithTimeout(ctx, TimeoutSchema)
			_, err := db.db.ExecContext(sctx, stmt)
			cancel()
			if err != nil && !IsAlreadyExistsError(err) {
				return fmt.Errorf("schema %s: %w", frag.name, Classify(err))
			}
		}
	}
	return nil
}

// Dialect holds the driver-specific column types used by the DDL helpers.
type Dialect struct {
	// Key is the type for short indexed/primary-key string columns.
	Key string
	// Text is the type for general string columns that need a DEFAULT.
	Text string
	// Int is the type for 64-bit integer columns.
	Int string
	// AutoIncrementPK is the full column-type-plus-constraint fragment for a
	// server-generated, auto-incrementing integer primary key, matching
	// AI.md PART 10's own DDL examples (e.g. "id INTEGER PRIMARY KEY
	// AUTOINCREMENT" for sqlite). Feature packages whose model IDs are
	// int64, and whose Create* functions rely on sql.Result.LastInsertId()
	// (see the lastID() helper convention), must declare their id column as
	// `id ` + d.AutoIncrementPK + ` ,` — never as a bare d.Key/d.Int column
	// with no PRIMARY KEY, which leaves id permanently NULL because nothing
	// ever populates it on insert.
	AutoIncrementPK string
}

// DialectFor returns the column types for the given canonical driver so
// feature packages can emit portable DDL.
func DialectFor(driver string) Dialect {
	switch driver {
	case DriverPostgres:
		return Dialect{Key: "TEXT", Text: "TEXT", Int: "BIGINT", AutoIncrementPK: "BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY"}
	case DriverMySQL:
		// MySQL cannot put a DEFAULT on TEXT/BLOB columns and limits index
		// key length, so string columns are VARCHAR here.
		return Dialect{Key: "VARCHAR(191)", Text: "VARCHAR(255)", Int: "BIGINT", AutoIncrementPK: "BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY"}
	case DriverSQLServer:
		return Dialect{Key: "NVARCHAR(200)", Text: "NVARCHAR(400)", Int: "BIGINT", AutoIncrementPK: "BIGINT IDENTITY(1,1) PRIMARY KEY"}
	default:
		return Dialect{Key: "TEXT", Text: "TEXT", Int: "INTEGER", AutoIncrementPK: "INTEGER PRIMARY KEY AUTOINCREMENT"}
	}
}

// CreateIndex builds an idempotent index statement for the driver. MySQL and
// SQL Server have no CREATE INDEX IF NOT EXISTS, so their statements rely on
// EnsureSchema swallowing the "already exists" error instead.
func CreateIndex(driver, name, table string, columns ...string) string {
	cols := ""
	for i, col := range columns {
		if i > 0 {
			cols += ", "
		}
		cols += col
	}
	switch driver {
	case DriverMySQL, DriverSQLServer:
		return fmt.Sprintf("CREATE INDEX %s ON %s (%s)", name, table, cols)
	default:
		return fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (%s)", name, table, cols)
	}
}

// AddColumn builds an idempotent ALTER TABLE ... ADD COLUMN statement.
// PostgreSQL supports IF NOT EXISTS natively; on the other drivers the
// duplicate-column error is tolerated by EnsureSchema. Every added column
// must be nullable or carry a DEFAULT (PART 10 -> "Schema Update Rules").
func AddColumn(driver, table, column, columnType, defaultClause string) string {
	suffix := ""
	if defaultClause != "" {
		suffix = " " + defaultClause
	}
	if driver == DriverPostgres {
		return fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s %s%s", table, column, columnType, suffix)
	}
	return fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s%s", table, column, columnType, suffix)
}

// SortedSchemas lists the registered fragment names alphabetically, for
// diagnostics that want a stable view rather than registration order.
func SortedSchemas() []string {
	names := RegisteredSchemas()
	sort.Strings(names)
	return names
}
