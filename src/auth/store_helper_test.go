package auth

import (
	"context"
	"testing"

	"github.com/webappsgo/cashp/src/database"
)

// newAuthTestDB opens an isolated SQLite database in a per-test temp dir
// with the full application schema applied, matching the pattern in
// src/database/database_test.go. It is shared by every *_test.go file in
// this package that needs a real store or a real Service.
func newAuthTestDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(database.Config{Driver: "sqlite", Dir: t.TempDir()})
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
