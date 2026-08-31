package nodes

import (
	"github.com/webappsgo/cashp/src/database"
)

// SchemaName is the fragment name this package registers its DDL under.
const SchemaName = "nodes"

// Tables owned by the node subsystem. Every statement is additive and
// idempotent per AI.md PART 10: no DROP, no rename, no migration files.
// Timestamps are Unix seconds; booleans are integers so the DDL is portable
// across every supported driver. No secret is stored in plaintext: an
// enrollment token and a node credential are held as their SHA-256 hex
// digest plus a short non-reversible display prefix.
func init() {
	database.RegisterSchema(SchemaName, nodesSchema)
}

// nodesSchema returns the DDL for the node tables in the dialect of the
// active driver.
func nodesSchema(driver string) []string {
	d := database.DialectFor(driver)

	return []string{
		// The fleet register. A cluster node also appears in the control
		// plane's own nodes table (src/database) once it heartbeats; a
		// managed node never does.
		`CREATE TABLE IF NOT EXISTS node_registry (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			name ` + d.Key + ` NOT NULL DEFAULT '',
			role ` + d.Key + ` NOT NULL DEFAULT 'managed',
			state ` + d.Key + ` NOT NULL DEFAULT 'pending',
			state_reason ` + d.Text + ` NOT NULL DEFAULT '',
			address ` + d.Text + ` NOT NULL DEFAULT '',
			callback_url ` + d.Text + ` NOT NULL DEFAULT '',
			agent_version ` + d.Key + ` NOT NULL DEFAULT '',
			cordoned ` + d.Int + ` NOT NULL DEFAULT 0,
			maintenance ` + d.Int + ` NOT NULL DEFAULT 0,
			enrolled_at ` + d.Int + ` NOT NULL DEFAULT 0,
			last_seen ` + d.Int + ` NOT NULL DEFAULT 0,
			state_changed_at ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0,
			version ` + d.Int + ` NOT NULL DEFAULT 1
		)`,
		database.CreateIndex(driver, "idx_node_registry_role_state", "node_registry", "role", "state"),
		database.CreateIndex(driver, "idx_node_registry_name", "node_registry", "name"),

		// Enrollment tokens. token_hash is the only stored form; max_uses
		// bounds redemption and node_id binds a token to an existing node so
		// a node can rejoin or re-key without being deleted first.
		`CREATE TABLE IF NOT EXISTS node_enrollment_tokens (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			token_hash ` + d.Key + ` NOT NULL UNIQUE,
			display_prefix ` + d.Key + ` NOT NULL DEFAULT '',
			role ` + d.Key + ` NOT NULL DEFAULT 'managed',
			node_id ` + d.Key + ` NOT NULL DEFAULT '',
			max_uses ` + d.Int + ` NOT NULL DEFAULT 1,
			uses ` + d.Int + ` NOT NULL DEFAULT 0,
			expires_at ` + d.Int + ` NOT NULL DEFAULT 0,
			revoked_at ` + d.Int + ` NOT NULL DEFAULT 0,
			created_by ` + d.Key + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,
		database.CreateIndex(driver, "idx_node_enrollment_tokens_expiry", "node_enrollment_tokens", "expires_at"),
		database.CreateIndex(driver, "idx_node_enrollment_tokens_node", "node_enrollment_tokens", "node_id"),

		// Long-lived credentials a node presents on every call. Re-keying
		// issues a new row and revokes the previous ones; the plaintext is
		// shown exactly once at issue time and never persisted or logged.
		`CREATE TABLE IF NOT EXISTS node_credentials (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			node_id ` + d.Key + ` NOT NULL DEFAULT '',
			token_hash ` + d.Key + ` NOT NULL UNIQUE,
			display_prefix ` + d.Key + ` NOT NULL DEFAULT '',
			issued_at ` + d.Int + ` NOT NULL DEFAULT 0,
			expires_at ` + d.Int + ` NOT NULL DEFAULT 0,
			revoked_at ` + d.Int + ` NOT NULL DEFAULT 0,
			last_used_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,
		database.CreateIndex(driver, "idx_node_credentials_node", "node_credentials", "node_id"),

		// Capability inventory reported by the node. One validated key per
		// row keeps hostile input out of any structured blob parser and
		// makes every value individually size-bounded.
		`CREATE TABLE IF NOT EXISTS node_facts (
			node_id ` + d.Key + ` NOT NULL,
			fact_key ` + d.Key + ` NOT NULL,
			fact_value ` + d.Text + ` NOT NULL DEFAULT '',
			reported_at ` + d.Int + ` NOT NULL DEFAULT 0,
			PRIMARY KEY (node_id, fact_key)
		)`,

		// Work the control plane assigned to a node, with its retry and
		// timeout accounting. payload is opaque JSON handed back to the node
		// verbatim through a parameterized statement; it is never expanded
		// into a command line, a path or a config file by this package.
		`CREATE TABLE IF NOT EXISTS node_tasks (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			node_id ` + d.Key + ` NOT NULL DEFAULT '',
			action ` + d.Key + ` NOT NULL DEFAULT '',
			payload ` + d.Text + ` NOT NULL DEFAULT '',
			state ` + d.Key + ` NOT NULL DEFAULT 'queued',
			attempts ` + d.Int + ` NOT NULL DEFAULT 0,
			max_attempts ` + d.Int + ` NOT NULL DEFAULT 3,
			timeout_seconds ` + d.Int + ` NOT NULL DEFAULT 300,
			created_by ` + d.Key + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			next_attempt_at ` + d.Int + ` NOT NULL DEFAULT 0,
			claimed_at ` + d.Int + ` NOT NULL DEFAULT 0,
			deadline_at ` + d.Int + ` NOT NULL DEFAULT 0,
			finished_at ` + d.Int + ` NOT NULL DEFAULT 0,
			exit_code ` + d.Int + ` NOT NULL DEFAULT 0,
			result ` + d.Text + ` NOT NULL DEFAULT '',
			error ` + d.Text + ` NOT NULL DEFAULT '',
			version ` + d.Int + ` NOT NULL DEFAULT 1
		)`,
		database.CreateIndex(driver, "idx_node_tasks_queue", "node_tasks", "node_id", "state", "next_attempt_at"),
		database.CreateIndex(driver, "idx_node_tasks_deadline", "node_tasks", "state", "deadline_at"),
	}
}
