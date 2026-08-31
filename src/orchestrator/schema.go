package orchestrator

import (
	"fmt"

	"github.com/webappsgo/cashp/src/database"
)

// The orchestrator tables record which workloads this node owns, which
// account each one belongs to, and what was done to them. Ownership is
// recorded here so a workload can be attributed even when the engine that
// runs it has been restarted or replaced.
func init() {
	database.RegisterSchema("orchestrator", orchestratorSchema)
}

// Table names. They are constants rather than literals so a query cannot
// accidentally address a table that is not part of this schema.
const (
	// tableWorkloads records one row per managed container or VM.
	tableWorkloads = "orch_workloads"
	// tableSnapshots records one row per captured snapshot.
	tableSnapshots = "orch_snapshots"
	// tableEvents records one row per lifecycle action.
	tableEvents = "orch_events"
)

// orchestratorSchema returns the idempotent, additive DDL for the
// orchestration tables. Every statement creates or adds; nothing drops,
// renames or deletes.
func orchestratorSchema(driver string) []string {
	d := database.DialectFor(driver)

	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %[4]s (
	qualified_name %[1]s NOT NULL PRIMARY KEY,
	tenant_id %[1]s NOT NULL DEFAULT '',
	class %[1]s NOT NULL DEFAULT '',
	workload_name %[1]s NOT NULL DEFAULT '',
	backend %[1]s NOT NULL DEFAULT '',
	kind %[1]s NOT NULL DEFAULT '',
	engine_id %[2]s NOT NULL DEFAULT '',
	image %[2]s NOT NULL DEFAULT '',
	image_digest %[2]s NOT NULL DEFAULT '',
	state %[1]s NOT NULL DEFAULT '',
	cpu_millicores %[3]s NOT NULL DEFAULT 0,
	memory_bytes %[3]s NOT NULL DEFAULT 0,
	disk_bytes %[3]s NOT NULL DEFAULT 0,
	created_at %[3]s NOT NULL DEFAULT 0,
	updated_at %[3]s NOT NULL DEFAULT 0,
	removed_at %[3]s NOT NULL DEFAULT 0,
	version %[3]s NOT NULL DEFAULT 1
)`, d.Key, d.Text, d.Int, tableWorkloads),
		database.CreateIndex(driver, "idx_orch_workloads_tenant", tableWorkloads, "tenant_id"),
		database.CreateIndex(driver, "idx_orch_workloads_backend", tableWorkloads, "backend"),
		database.CreateIndex(driver, "idx_orch_workloads_removed_at", tableWorkloads, "removed_at"),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %[4]s (
	snapshot_id %[1]s NOT NULL PRIMARY KEY,
	qualified_name %[1]s NOT NULL DEFAULT '',
	tenant_id %[1]s NOT NULL DEFAULT '',
	snapshot_name %[1]s NOT NULL DEFAULT '',
	backend %[1]s NOT NULL DEFAULT '',
	size_bytes %[3]s NOT NULL DEFAULT 0,
	stateful %[3]s NOT NULL DEFAULT 0,
	created_at %[3]s NOT NULL DEFAULT 0,
	removed_at %[3]s NOT NULL DEFAULT 0,
	note %[2]s NOT NULL DEFAULT ''
)`, d.Key, d.Text, d.Int, tableSnapshots),
		database.CreateIndex(driver, "idx_orch_snapshots_tenant", tableSnapshots, "tenant_id"),
		database.CreateIndex(driver, "idx_orch_snapshots_workload", tableSnapshots, "qualified_name"),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %[4]s (
	event_id %[1]s NOT NULL PRIMARY KEY,
	tenant_id %[1]s NOT NULL DEFAULT '',
	qualified_name %[1]s NOT NULL DEFAULT '',
	backend %[1]s NOT NULL DEFAULT '',
	action %[1]s NOT NULL DEFAULT '',
	actor_user_id %[1]s NOT NULL DEFAULT '',
	actor_role %[1]s NOT NULL DEFAULT '',
	request_id %[1]s NOT NULL DEFAULT '',
	outcome %[1]s NOT NULL DEFAULT '',
	detail %[2]s NOT NULL DEFAULT '',
	created_at %[3]s NOT NULL DEFAULT 0
)`, d.Key, d.Text, d.Int, tableEvents),
		database.CreateIndex(driver, "idx_orch_events_tenant", tableEvents, "tenant_id"),
		database.CreateIndex(driver, "idx_orch_events_workload", tableEvents, "qualified_name"),
		database.CreateIndex(driver, "idx_orch_events_created_at", tableEvents, "created_at"),
	}
}
