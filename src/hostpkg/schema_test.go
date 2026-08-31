package hostpkg

import (
	"strings"
	"testing"

	"github.com/webappsgo/cashp/src/database"
)

func TestHostpkgSchemaIsAdditiveAndIdempotent(t *testing.T) {
	drivers := []string{
		database.DriverSQLite,
		database.DriverPostgres,
		database.DriverMySQL,
		database.DriverSQLServer,
		database.DriverLibSQL,
	}

	// Nothing in a cashp schema may destroy or rename existing data.
	forbidden := []string{"DROP ", "DELETE ", "TRUNCATE", "RENAME", "ALTER COLUMN"}

	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			statements := hostpkgSchema(driver)
			if len(statements) == 0 {
				t.Fatal("schema is empty")
			}

			joined := strings.Join(statements, "\n")
			for _, table := range []string{"host_packages", "host_repos"} {
				if !strings.Contains(joined, "CREATE TABLE IF NOT EXISTS "+table) {
					t.Errorf("table %q is not created idempotently:\n%s", table, joined)
				}
			}

			upper := strings.ToUpper(joined)
			for _, word := range forbidden {
				if strings.Contains(upper, word) {
					t.Errorf("schema contains a destructive statement %q:\n%s", word, joined)
				}
			}

			for _, stmt := range statements {
				trimmed := strings.TrimSpace(strings.ToUpper(stmt))
				if !strings.HasPrefix(trimmed, "CREATE TABLE") && !strings.HasPrefix(trimmed, "CREATE INDEX") {
					t.Errorf("unexpected statement kind:\n%s", stmt)
				}
			}
		})
	}
}

func TestHostpkgSchemaColumnsMatchTheRecords(t *testing.T) {
	joined := strings.Join(hostpkgSchema(database.DriverSQLite), "\n")

	columns := []string{
		"package_name", "service", "manager", "distribution", "version", "installed_at", "removed_at",
		"repo_id", "definition_path", "fingerprints", "added_at",
	}
	for _, col := range columns {
		if !strings.Contains(joined, col) {
			t.Errorf("column %q is missing from the schema:\n%s", col, joined)
		}
	}
}
