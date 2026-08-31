package admin

import (
	"github.com/webappsgo/cashp/src/database"
)

// Schema tables owned by the admin panel. Every statement is additive and
// idempotent per AI.md PART 10: no DROP, no DELETE, no rename, no migration
// files. Timestamps are stored as Unix seconds so the DDL stays portable
// across every supported driver.
func init() {
	database.RegisterSchema("admin", adminSchema)
}

// adminSchema returns the DDL for the admin panel tables in the dialect of the
// active driver.
func adminSchema(driver string) []string {
	d := database.DialectFor(driver)

	stmts := []string{
		// Server administrator accounts. These are a separate account type
		// from application users and never share the users table.
		`CREATE TABLE IF NOT EXISTS admins (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			username ` + d.Key + ` NOT NULL UNIQUE,
			password_hash ` + d.Text + ` NOT NULL,
			account_email ` + d.Text + ` NOT NULL DEFAULT '',
			notification_email ` + d.Text + ` NOT NULL DEFAULT '',
			is_primary ` + d.Int + ` NOT NULL DEFAULT 0,
			disabled ` + d.Int + ` NOT NULL DEFAULT 0,
			source ` + d.Key + ` NOT NULL DEFAULT 'local',
			external_id ` + d.Key + ` NOT NULL DEFAULT '',
			group_names ` + d.Text + ` NOT NULL DEFAULT '',
			totp_secret ` + d.Text + ` NOT NULL DEFAULT '',
			totp_enabled ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0,
			last_login_at ` + d.Int + ` NOT NULL DEFAULT 0,
			last_sync_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// One-time recovery codes issued when an admin enables TOTP.
		`CREATE TABLE IF NOT EXISTS admin_recovery_codes (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			admin_id ` + d.Key + ` NOT NULL,
			code_hash ` + d.Text + ` NOT NULL,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			used_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Admin sessions, kept separate from user sessions. The primary key is
		// the SHA-256 of the cookie value; the plaintext is never stored.
		`CREATE TABLE IF NOT EXISTS admin_sessions (
			token_hash ` + d.Key + ` NOT NULL PRIMARY KEY,
			admin_id ` + d.Key + ` NOT NULL DEFAULT '',
			kind ` + d.Key + ` NOT NULL DEFAULT 'session',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			expires_at ` + d.Int + ` NOT NULL DEFAULT 0,
			last_seen_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Invites for additional server admins. Only the hash is stored.
		`CREATE TABLE IF NOT EXISTS admin_invites (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			username ` + d.Key + ` NOT NULL DEFAULT '',
			token_hash ` + d.Text + ` NOT NULL,
			created_by ` + d.Key + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			expires_at ` + d.Int + ` NOT NULL DEFAULT 0,
			max_uses ` + d.Int + ` NOT NULL DEFAULT 1,
			uses ` + d.Int + ` NOT NULL DEFAULT 0,
			revoked_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// The first-run bootstrap token. Only the SHA-256 hash is persisted and
		// the row is consumed atomically the first time it is redeemed.
		`CREATE TABLE IF NOT EXISTS admin_setup_tokens (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			token_hash ` + d.Text + ` NOT NULL,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			expires_at ` + d.Int + ` NOT NULL DEFAULT 0,
			used_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// API tokens belonging to an admin account.
		`CREATE TABLE IF NOT EXISTS admin_api_tokens (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			admin_id ` + d.Key + ` NOT NULL,
			name ` + d.Text + ` NOT NULL DEFAULT '',
			token_hash ` + d.Text + ` NOT NULL,
			display_prefix ` + d.Key + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			last_used_at ` + d.Int + ` NOT NULL DEFAULT 0,
			revoked_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Per-admin preferences (appearance, notification categories). The
		// primary key is "{admin_id}:{pref_key}" so it stays a single column.
		`CREATE TABLE IF NOT EXISTS admin_preferences (
			pref_id ` + d.Key + ` NOT NULL PRIMARY KEY,
			admin_id ` + d.Key + ` NOT NULL,
			pref_key ` + d.Key + ` NOT NULL,
			pref_value ` + d.Text + ` NOT NULL DEFAULT '',
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Server settings edited through the admin panel.
		`CREATE TABLE IF NOT EXISTS admin_settings (
			setting_key ` + d.Key + ` NOT NULL PRIMARY KEY,
			setting_value ` + d.Text + ` NOT NULL DEFAULT '',
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_by ` + d.Key + ` NOT NULL DEFAULT ''
		)`,

		// Queryable copy of the admin audit trail. The append-only JSON audit
		// log written by logging.Audit() remains the system of record; this
		// table is what the panel reads to render activity and log views.
		`CREATE TABLE IF NOT EXISTS admin_audit (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			occurred_at ` + d.Int + ` NOT NULL DEFAULT 0,
			category ` + d.Key + ` NOT NULL DEFAULT 'audit',
			event ` + d.Key + ` NOT NULL DEFAULT '',
			actor ` + d.Key + ` NOT NULL DEFAULT '',
			target ` + d.Text + ` NOT NULL DEFAULT '',
			detail ` + d.Text + ` NOT NULL DEFAULT ''
		)`,

		// Key material owned by the panel, currently the AES-256-GCM key that
		// wraps stored TOTP secrets so they are never at rest in the clear.
		`CREATE TABLE IF NOT EXISTS admin_secrets (
			name ` + d.Key + ` NOT NULL PRIMARY KEY,
			secret_value ` + d.Text + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Managed nodes enrolled into this server's cluster. Only the hash of a
		// join token is stored, never the token itself.
		`CREATE TABLE IF NOT EXISTS admin_cluster_nodes (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			name ` + d.Key + ` NOT NULL,
			address ` + d.Text + ` NOT NULL DEFAULT '',
			port ` + d.Int + ` NOT NULL DEFAULT 0,
			join_token_hash ` + d.Key + ` NOT NULL DEFAULT '',
			status ` + d.Key + ` NOT NULL DEFAULT 'pending',
			labels ` + d.Text + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			created_by ` + d.Key + ` NOT NULL DEFAULT '',
			last_seen_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,
	}

	return append(stmts,
		database.CreateIndex(driver, "idx_admin_cluster_nodes_name", "admin_cluster_nodes", "name"),
		database.CreateIndex(driver, "idx_admin_sessions_admin", "admin_sessions", "admin_id"),
		database.CreateIndex(driver, "idx_admin_sessions_expires", "admin_sessions", "expires_at"),
		database.CreateIndex(driver, "idx_admin_recovery_admin", "admin_recovery_codes", "admin_id"),
		database.CreateIndex(driver, "idx_admin_api_tokens_admin", "admin_api_tokens", "admin_id"),
		database.CreateIndex(driver, "idx_admin_preferences_admin", "admin_preferences", "admin_id"),
		database.CreateIndex(driver, "idx_admin_audit_time", "admin_audit", "occurred_at"),
		database.CreateIndex(driver, "idx_admin_audit_category", "admin_audit", "category"),
		database.CreateIndex(driver, "idx_admin_invites_expires", "admin_invites", "expires_at"),
	)
}
