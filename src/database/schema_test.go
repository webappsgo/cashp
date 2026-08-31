package database

import (
	"context"
	"strings"
	"testing"
)

func TestDialectFor(t *testing.T) {
	if d := DialectFor(DriverPostgres); d.Int != "BIGINT" || d.Text != "TEXT" {
		t.Errorf("postgres dialect = %+v", d)
	}
	if d := DialectFor(DriverMySQL); !strings.HasPrefix(d.Text, "VARCHAR") {
		t.Errorf("mysql dialect = %+v", d)
	}
	if d := DialectFor(DriverSQLServer); !strings.HasPrefix(d.Key, "NVARCHAR") {
		t.Errorf("sqlserver dialect = %+v", d)
	}
	if d := DialectFor(DriverSQLite); d.Int != "INTEGER" {
		t.Errorf("sqlite dialect = %+v", d)
	}
	if d := DialectFor(DriverLibSQL); d.Int != "INTEGER" {
		t.Errorf("libsql dialect = %+v", d)
	}
}

func TestCreateIndex(t *testing.T) {
	got := CreateIndex(DriverSQLite, "idx_a", "t", "a", "b")
	want := "CREATE INDEX IF NOT EXISTS idx_a ON t (a, b)"
	if got != want {
		t.Errorf("sqlite index = %q, want %q", got, want)
	}

	got = CreateIndex(DriverMySQL, "idx_a", "t", "a")
	if strings.Contains(got, "IF NOT EXISTS") {
		t.Errorf("mysql index must not use IF NOT EXISTS: %q", got)
	}
	got = CreateIndex(DriverSQLServer, "idx_a", "t", "a")
	if strings.Contains(got, "IF NOT EXISTS") {
		t.Errorf("sqlserver index must not use IF NOT EXISTS: %q", got)
	}
}

func TestAddColumn(t *testing.T) {
	got := AddColumn(DriverPostgres, "users", "nickname", "TEXT", "DEFAULT ''")
	if !strings.Contains(got, "ADD COLUMN IF NOT EXISTS nickname TEXT DEFAULT ''") {
		t.Errorf("postgres add column = %q", got)
	}

	got = AddColumn(DriverSQLite, "users", "nickname", "TEXT", "")
	if got != "ALTER TABLE users ADD COLUMN nickname TEXT" {
		t.Errorf("sqlite add column = %q", got)
	}
}

func TestRegisteredSchemas(t *testing.T) {
	names := RegisteredSchemas()
	if len(names) < 2 {
		t.Fatalf("expected at least the cluster and test fragments, got %v", names)
	}
	if names[0] != "cluster" {
		t.Errorf("cluster schema must register first, got %q", names[0])
	}

	sorted := SortedSchemas()
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1] > sorted[i] {
			t.Fatalf("SortedSchemas is not sorted: %v", sorted)
		}
	}
}

func TestRegisterSchemaRejectsBadInput(t *testing.T) {
	assertPanic(t, "empty name", func() { RegisterSchema("", func(string) []string { return nil }) })
	assertPanic(t, "nil ddl", func() { RegisterSchema("nil_ddl_fragment", nil) })
	assertPanic(t, "duplicate name", func() {
		RegisterSchema("cluster", func(string) []string { return nil })
	})
}

func assertPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("expected a panic for %s", what)
		}
	}()
	fn()
}

func TestEnsureSchemaIsIdempotent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// newTestDB already applied it once; two more passes must be clean.
	for i := 0; i < 2; i++ {
		if err := db.EnsureSchema(ctx); err != nil {
			t.Fatalf("EnsureSchema pass %d: %v", i, err)
		}
	}

	// The additive column from the fixture fragment must exist and be
	// usable after repeated application.
	if _, err := db.ExecContext(ctx, TimeoutWrite,
		`INSERT INTO test_items (id, name, extra) VALUES (?, ?, ?)`, 11, "x", "y"); err != nil {
		t.Fatalf("insert into altered table: %v", err)
	}

	var extra string
	if err := db.QueryRowContext(ctx, TimeoutSelect,
		`SELECT extra FROM test_items WHERE id = ?`, 11).Scan(&extra); err != nil {
		t.Fatalf("select added column: %v", err)
	}
	if extra != "y" {
		t.Errorf("extra = %q, want y", extra)
	}
}

func TestEnsureSchemaReportsRealErrors(t *testing.T) {
	db := newTestDB(t)

	// Applied directly rather than via RegisterSchema so the broken DDL does
	// not leak into the global registry used by the other tests.
	broken := []schemaFragment{{
		name: "broken_fragment",
		ddl:  func(string) []string { return []string{"", "THIS IS NOT SQL"} },
	}}

	if err := db.applyFragments(context.Background(), broken); err == nil {
		t.Fatal("expected EnsureSchema to fail on invalid DDL")
	} else if !strings.Contains(err.Error(), "broken_fragment") {
		t.Errorf("error should name the fragment: %v", err)
	}
}
