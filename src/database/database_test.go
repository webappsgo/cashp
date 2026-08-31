package database

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The test fixture table is registered like any feature package would
// register its own schema.
func init() {
	RegisterSchema("test_fixtures", func(driver string) []string {
		d := DialectFor(driver)
		return []string{
			`CREATE TABLE IF NOT EXISTS test_items (
				id ` + d.Int + ` NOT NULL PRIMARY KEY,
				name ` + d.Text + ` NOT NULL DEFAULT '',
				version ` + d.Int + ` NOT NULL DEFAULT 1
			)`,
			CreateIndex(driver, "idx_test_items_name", "test_items", "name"),
			AddColumn(driver, "test_items", "extra", d.Text, "DEFAULT ''"),
		}
	})
}

// newTestDB opens a SQLite database in a temp dir with the full schema
// applied and closes it when the test finishes.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(Config{Driver: "sqlite", Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if err := db.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	return db
}

func TestNormalizeDriver(t *testing.T) {
	cases := map[string]string{
		"":           DriverSQLite,
		"sqlite":     DriverSQLite,
		"SQLite3":    DriverSQLite,
		"postgres":   DriverPostgres,
		"postgresql": DriverPostgres,
		"mariadb":    DriverMySQL,
		"mysql":      DriverMySQL,
		"mssql":      DriverSQLServer,
		"turso":      DriverLibSQL,
	}
	for in, want := range cases {
		got, err := NormalizeDriver(in)
		if err != nil {
			t.Fatalf("NormalizeDriver(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("NormalizeDriver(%q) = %q, want %q", in, got, want)
		}
	}

	if _, err := NormalizeDriver("oracle"); !errors.Is(err, ErrUnsupportedDriver) {
		t.Errorf("NormalizeDriver(oracle) error = %v, want ErrUnsupportedDriver", err)
	}
}

func TestOpenCreatesSQLiteFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	db, err := Open(Config{Driver: "sqlite", Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	if db.Driver() != DriverSQLite {
		t.Errorf("Driver() = %q, want %q", db.Driver(), DriverSQLite)
	}
	if db.SQL() == nil {
		t.Fatal("SQL() returned nil")
	}
	if err := db.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cashp.db")); err != nil {
		t.Errorf("database file not created: %v", err)
	}
	if db.Stats().MaxOpenConnections != sqliteMaxOpen {
		t.Errorf("MaxOpenConnections = %d, want %d", db.Stats().MaxOpenConnections, sqliteMaxOpen)
	}
}

func TestOpenRejectsBadConfig(t *testing.T) {
	if _, err := Open(Config{Driver: "oracle"}); !errors.Is(err, ErrUnsupportedDriver) {
		t.Errorf("Open(oracle) error = %v, want ErrUnsupportedDriver", err)
	}
	if _, err := Open(Config{Driver: "postgres"}); !errors.Is(err, ErrMissingURL) {
		t.Errorf("Open(postgres) error = %v, want ErrMissingURL", err)
	}
}

func TestNewRejectsNilHandle(t *testing.T) {
	if _, err := New(nil, "sqlite"); err == nil {
		t.Fatal("New(nil) returned no error")
	}
	db := newTestDB(t)
	wrapped, err := New(db.SQL(), "sqlite")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if wrapped.Driver() != DriverSQLite {
		t.Errorf("Driver() = %q", wrapped.Driver())
	}
}

func TestPoolOverrides(t *testing.T) {
	db, err := Open(Config{
		Driver:      "sqlite",
		Dir:         t.TempDir(),
		MaxOpen:     3,
		MaxIdle:     9,
		MaxLifetime: time.Minute,
		MaxIdleTime: time.Second,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	stats := db.Stats()
	if stats.MaxOpenConnections != 3 {
		t.Errorf("MaxOpenConnections = %d, want 3", stats.MaxOpenConnections)
	}
}

func TestRebind(t *testing.T) {
	query := "SELECT id FROM test_items WHERE name = ? AND version = ?"

	sqlite := &DB{driver: DriverSQLite}
	if got := sqlite.Rebind(query); got != query {
		t.Errorf("sqlite Rebind changed the query: %q", got)
	}

	pg := &DB{driver: DriverPostgres}
	if got := pg.Rebind(query); !strings.HasSuffix(got, "name = $1 AND version = $2") {
		t.Errorf("postgres Rebind = %q", got)
	}

	ms := &DB{driver: DriverSQLServer}
	if got := ms.Rebind(query); !strings.HasSuffix(got, "name = @p1 AND version = @p2") {
		t.Errorf("sqlserver Rebind = %q", got)
	}

	noArgs := "SELECT 1"
	if got := pg.Rebind(noArgs); got != noArgs {
		t.Errorf("Rebind(%q) = %q", noArgs, got)
	}
}

func TestExecQueryRoundTrip(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, TimeoutWrite,
		`INSERT INTO test_items (id, name) VALUES (?, ?)`, 1, "alpha"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := db.ExecContext(ctx, TimeoutWrite,
		`INSERT INTO test_items (id, name) VALUES (?, ?)`, 2, "beta"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var name string
	if err := db.QueryRowContext(ctx, TimeoutSelect,
		`SELECT name FROM test_items WHERE id = ?`, 1).Scan(&name); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if name != "alpha" {
		t.Errorf("name = %q, want alpha", name)
	}

	rows, err := db.QueryContext(ctx, TimeoutJoin,
		`SELECT id, name FROM test_items ORDER BY id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer func() { _ = rows.Close() }()

	count := 0
	for rows.Next() {
		var id int
		var got string
		if err := rows.Scan(&id, &got); err != nil {
			t.Fatalf("scan: %v", err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if count != 2 {
		t.Errorf("row count = %d, want 2", count)
	}
}

func TestQueryErrorsAreClassified(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	var name string
	err := db.QueryRowContext(ctx, TimeoutSelect,
		`SELECT name FROM test_items WHERE id = ?`, 404).Scan(&name)
	if !IsNotFound(err) {
		t.Errorf("expected not-found, got %v", err)
	}

	if _, err := db.QueryContext(ctx, TimeoutSelect, `SELECT nope FROM test_items`); err == nil {
		t.Error("expected an error for an unknown column")
	}
	if _, err := db.ExecContext(ctx, TimeoutWrite, `INSERT INTO missing_table (id) VALUES (?)`, 1); err == nil {
		t.Error("expected an error for a missing table")
	}
}

func TestUpdateVersioned(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, TimeoutWrite,
		`INSERT INTO test_items (id, name, version) VALUES (?, ?, ?)`, 7, "seven", 1); err != nil {
		t.Fatalf("insert: %v", err)
	}

	err := db.UpdateVersioned(ctx,
		`UPDATE test_items SET name = ?, version = version + 1 WHERE id = ? AND version = ?`,
		"seven-b", 7, 1)
	if err != nil {
		t.Fatalf("UpdateVersioned: %v", err)
	}

	// The same stale version must now lose the race.
	err = db.UpdateVersioned(ctx,
		`UPDATE test_items SET name = ?, version = version + 1 WHERE id = ? AND version = ?`,
		"seven-c", 7, 1)
	if !IsConflict(err) {
		t.Errorf("expected ErrConflict, got %v", err)
	}

	if err := db.UpdateVersioned(ctx, `UPDATE missing_table SET id = ?`, 1); err == nil {
		t.Error("expected an error updating a missing table")
	}
}

func TestResolveTimeout(t *testing.T) {
	if got := resolveTimeout(0); got != TimeoutSelect {
		t.Errorf("resolveTimeout(0) = %v, want %v", got, TimeoutSelect)
	}
	if got := resolveTimeout(-time.Second); got != TimeoutSelect {
		t.Errorf("resolveTimeout(-1s) = %v, want %v", got, TimeoutSelect)
	}
	if got := resolveTimeout(TimeoutBulk); got != TimeoutBulk {
		t.Errorf("resolveTimeout(bulk) = %v", got)
	}
}

func TestCloseOnNilIsSafe(t *testing.T) {
	var db *DB
	if err := db.Close(); err != nil {
		t.Errorf("Close on nil DB: %v", err)
	}
}
