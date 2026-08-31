package hostpkg

import (
	"fmt"

	"github.com/webappsgo/cashp/src/database"
)

// The host package tables record what cashp installed and which repositories
// it added, so a removal can be refused for anything cashp did not install.
func init() {
	database.RegisterSchema("hostpkg", hostpkgSchema)
}

// hostpkgSchema returns the idempotent, additive DDL for the host package
// tables. Nothing here drops, renames or deletes.
func hostpkgSchema(driver string) []string {
	d := database.DialectFor(driver)

	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS host_packages (
	package_name %[1]s NOT NULL PRIMARY KEY,
	service %[2]s NOT NULL DEFAULT '',
	manager %[2]s NOT NULL DEFAULT '',
	distribution %[2]s NOT NULL DEFAULT '',
	version %[2]s NOT NULL DEFAULT '',
	installed_at %[3]s NOT NULL DEFAULT 0,
	removed_at %[3]s NOT NULL DEFAULT 0
)`, d.Key, d.Text, d.Int),
		database.CreateIndex(driver, "idx_host_packages_service", "host_packages", "service"),
		database.CreateIndex(driver, "idx_host_packages_removed_at", "host_packages", "removed_at"),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS host_repos (
	repo_id %[1]s NOT NULL PRIMARY KEY,
	manager %[2]s NOT NULL DEFAULT '',
	definition_path %[2]s NOT NULL DEFAULT '',
	fingerprints %[2]s NOT NULL DEFAULT '',
	added_at %[3]s NOT NULL DEFAULT 0
)`, d.Key, d.Text, d.Int),
		database.CreateIndex(driver, "idx_host_repos_added_at", "host_repos", "added_at"),
	}
}
