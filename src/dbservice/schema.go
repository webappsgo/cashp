package dbservice

import (
	"github.com/webappsgo/cashp/src/database"
)

// Schema tables owned by the managed-database layer. Every statement is
// additive and idempotent per AI.md PART 10: no DROP, no DELETE, no rename and
// no migration files. Timestamps are stored as Unix seconds so the DDL stays
// portable across every supported driver, and encrypted credentials are held
// as base64 text for the same reason.
func init() {
	database.RegisterSchema("dbservice", dbserviceSchema)
}

// dbserviceSchema returns the DDL for the managed-database tables in the
// dialect of the active driver.
func dbserviceSchema(driver string) []string {
	d := database.DialectFor(driver)

	stmts := []string{
		// Managed database instances. Every row carries its tenant, and every
		// query in this package filters on that column, so one tenant's
		// instances are never reachable from another tenant's session. A
		// destroyed instance is tombstoned rather than deleted so the record
		// stays auditable and its name is never silently reused.
		`CREATE TABLE IF NOT EXISTS db_instances (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL DEFAULT '',
			name ` + d.Key + ` NOT NULL DEFAULT '',
			engine ` + d.Key + ` NOT NULL DEFAULT '',
			engine_version ` + d.Key + ` NOT NULL DEFAULT '',
			role ` + d.Key + ` NOT NULL DEFAULT 'primary',
			primary_id ` + d.Key + ` NOT NULL DEFAULT '',
			state ` + d.Key + ` NOT NULL DEFAULT 'pending',
			container_id ` + d.Text + ` NOT NULL DEFAULT '',
			container_name ` + d.Key + ` NOT NULL DEFAULT '',
			volume_name ` + d.Key + ` NOT NULL DEFAULT '',
			network ` + d.Key + ` NOT NULL DEFAULT '',
			host ` + d.Text + ` NOT NULL DEFAULT '',
			port ` + d.Int + ` NOT NULL DEFAULT 0,
			admin_user ` + d.Key + ` NOT NULL DEFAULT '',
			cpu_millicores ` + d.Int + ` NOT NULL DEFAULT 0,
			memory_bytes ` + d.Int + ` NOT NULL DEFAULT 0,
			disk_bytes ` + d.Int + ` NOT NULL DEFAULT 0,
			pids_limit ` + d.Int + ` NOT NULL DEFAULT 0,
			health ` + d.Key + ` NOT NULL DEFAULT 'unknown',
			health_detail ` + d.Text + ` NOT NULL DEFAULT '',
			health_checked_at ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0,
			destroyed_at ` + d.Int + ` NOT NULL DEFAULT 0,
			row_version ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Accounts issued inside a managed instance. The password is stored
		// only as AES-256-GCM ciphertext, base64 encoded; the plaintext exists
		// in memory for the length of one operation and is never logged.
		`CREATE TABLE IF NOT EXISTS db_credentials (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL DEFAULT '',
			instance_id ` + d.Key + ` NOT NULL DEFAULT '',
			username ` + d.Key + ` NOT NULL DEFAULT '',
			role ` + d.Key + ` NOT NULL DEFAULT 'app',
			database_name ` + d.Key + ` NOT NULL DEFAULT '',
			grant_level ` + d.Key + ` NOT NULL DEFAULT '',
			secret ` + d.Text + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			rotated_at ` + d.Int + ` NOT NULL DEFAULT 0,
			revoked_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Named databases inside an instance. Engines without named databases
		// never write a row here.
		`CREATE TABLE IF NOT EXISTS db_databases (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL DEFAULT '',
			instance_id ` + d.Key + ` NOT NULL DEFAULT '',
			name ` + d.Key + ` NOT NULL DEFAULT '',
			owner ` + d.Key + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			dropped_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Native engine dumps stored in the backup repository. The artifact
		// itself lives in the repository, deduplicated and encrypted there;
		// this table is the tenant-scoped index of what exists.
		`CREATE TABLE IF NOT EXISTS db_backups (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL DEFAULT '',
			instance_id ` + d.Key + ` NOT NULL DEFAULT '',
			artifact_id ` + d.Text + ` NOT NULL DEFAULT '',
			engine ` + d.Key + ` NOT NULL DEFAULT '',
			engine_version ` + d.Key + ` NOT NULL DEFAULT '',
			database_name ` + d.Key + ` NOT NULL DEFAULT '',
			size_bytes ` + d.Int + ` NOT NULL DEFAULT 0,
			checksum ` + d.Text + ` NOT NULL DEFAULT '',
			encrypted ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			deleted_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,
	}

	return append(stmts,
		database.CreateIndex(driver, "idx_db_instances_tenant", "db_instances", "tenant_id"),
		database.CreateIndex(driver, "idx_db_instances_state", "db_instances", "state"),
		database.CreateIndex(driver, "idx_db_instances_primary", "db_instances", "primary_id"),
		database.CreateIndex(driver, "idx_db_credentials_tenant", "db_credentials", "tenant_id"),
		database.CreateIndex(driver, "idx_db_credentials_instance", "db_credentials", "instance_id"),
		database.CreateIndex(driver, "idx_db_databases_tenant", "db_databases", "tenant_id"),
		database.CreateIndex(driver, "idx_db_databases_instance", "db_databases", "instance_id"),
		database.CreateIndex(driver, "idx_db_backups_tenant", "db_backups", "tenant_id"),
		database.CreateIndex(driver, "idx_db_backups_instance", "db_backups", "instance_id"),
		database.CreateIndex(driver, "idx_db_backups_created", "db_backups", "created_at"),
	)
}
