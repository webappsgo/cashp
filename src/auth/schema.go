package auth

import (
	"github.com/webappsgo/cashp/src/database"
)

// SchemaName is the key this package registers its DDL under.
const SchemaName = "auth"

func init() {
	database.RegisterSchema(SchemaName, schemaDDL)
}

// schemaDDL returns every statement needed to bring the auth tables up to date.
// Every statement is additive and idempotent: no DROP, no DELETE, no rename, no
// migration files, and no schema-version table, per the backend rules.
func schemaDDL(driver string) []string {
	d := database.DialectFor(driver)
	stmts := []string{
		// Server Admin credentials. Never stored in the config file.
		`CREATE TABLE IF NOT EXISTS admins (
			id ` + d.AutoIncrementPK + `,
			username ` + d.Text + ` NOT NULL,
			email ` + d.Text + ` NOT NULL,
			password_hash ` + d.Text + ` NOT NULL,
			token_hash ` + d.Text + `,
			token_prefix ` + d.Text + `,
			totp_secret ` + d.Text + `,
			totp_enabled ` + d.Int + ` NOT NULL DEFAULT 0,
			is_primary ` + d.Int + ` NOT NULL DEFAULT 0,
			failed_logins ` + d.Int + ` NOT NULL DEFAULT 0,
			locked_until ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0,
			last_login_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS admin_sessions (
			id ` + d.AutoIncrementPK + `,
			admin_id ` + d.Int + ` NOT NULL,
			token_hash ` + d.Text + ` NOT NULL,
			ip_address ` + d.Text + ` NOT NULL DEFAULT '',
			user_agent ` + d.Text + ` NOT NULL DEFAULT '',
			location ` + d.Text + ` NOT NULL DEFAULT '',
			expires_at ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// One-time setup tokens. Redeeming one creates the tamper-proof primary global admin.
		`CREATE TABLE IF NOT EXISTS setup_tokens (
			id ` + d.AutoIncrementPK + `,
			token_hash ` + d.Text + ` NOT NULL,
			purpose ` + d.Text + ` NOT NULL DEFAULT 'primary_admin',
			used ` + d.Int + ` NOT NULL DEFAULT 0,
			used_at ` + d.Int + ` NOT NULL DEFAULT 0,
			expires_at ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Regular end-user accounts.
		`CREATE TABLE IF NOT EXISTS users (
			id ` + d.AutoIncrementPK + `,
			username ` + d.Text + ` NOT NULL,
			email ` + d.Text + ` NOT NULL,
			password_hash ` + d.Text + ` NOT NULL DEFAULT '',
			display_name ` + d.Text + ` NOT NULL DEFAULT '',
			avatar_url ` + d.Text + ` NOT NULL DEFAULT '',
			bio ` + d.Text + ` NOT NULL DEFAULT '',
			location ` + d.Text + ` NOT NULL DEFAULT '',
			website ` + d.Text + ` NOT NULL DEFAULT '',
			visibility ` + d.Text + ` NOT NULL DEFAULT 'public',
			role ` + d.Text + ` NOT NULL DEFAULT 'user',
			source ` + d.Text + ` NOT NULL DEFAULT 'local',
			external_id ` + d.Text + ` NOT NULL DEFAULT '',
			groups ` + d.Text + ` NOT NULL DEFAULT '[]',
			last_sync ` + d.Int + ` NOT NULL DEFAULT 0,
			email_verified ` + d.Int + ` NOT NULL DEFAULT 0,
			approved ` + d.Int + ` NOT NULL DEFAULT 1,
			disabled ` + d.Int + ` NOT NULL DEFAULT 0,
			totp_secret ` + d.Text + ` NOT NULL DEFAULT '',
			totp_enabled ` + d.Int + ` NOT NULL DEFAULT 0,
			timezone ` + d.Text + ` NOT NULL DEFAULT 'UTC',
			language ` + d.Text + ` NOT NULL DEFAULT 'en',
			failed_logins ` + d.Int + ` NOT NULL DEFAULT 0,
			locked_until ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0,
			last_login_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Permanent record of every username and org slug that has ever existed.
		// A tombstoned name is never reusable, so a former tenant cannot be impersonated.
		`CREATE TABLE IF NOT EXISTS name_tombstones (
			id ` + d.AutoIncrementPK + `,
			name ` + d.Text + ` NOT NULL,
			kind ` + d.Text + ` NOT NULL DEFAULT 'user',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS user_sessions (
			id ` + d.AutoIncrementPK + `,
			user_id ` + d.Int + ` NOT NULL,
			token_hash ` + d.Text + ` NOT NULL,
			ip_address ` + d.Text + ` NOT NULL DEFAULT '',
			user_agent ` + d.Text + ` NOT NULL DEFAULT '',
			location ` + d.Text + ` NOT NULL DEFAULT '',
			expires_at ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS user_tokens (
			id ` + d.AutoIncrementPK + `,
			user_id ` + d.Int + ` NOT NULL,
			name ` + d.Text + ` NOT NULL DEFAULT '',
			token_hash ` + d.Text + ` NOT NULL,
			token_prefix ` + d.Text + ` NOT NULL DEFAULT '',
			scopes ` + d.Text + ` NOT NULL DEFAULT '[]',
			expires_at ` + d.Int + ` NOT NULL DEFAULT 0,
			last_used_at ` + d.Int + ` NOT NULL DEFAULT 0,
			revoked ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS org_tokens (
			id ` + d.AutoIncrementPK + `,
			org_id ` + d.Int + ` NOT NULL,
			created_by ` + d.Int + ` NOT NULL DEFAULT 0,
			name ` + d.Text + ` NOT NULL DEFAULT '',
			token_hash ` + d.Text + ` NOT NULL,
			token_prefix ` + d.Text + ` NOT NULL DEFAULT '',
			scopes ` + d.Text + ` NOT NULL DEFAULT '[]',
			expires_at ` + d.Int + ` NOT NULL DEFAULT 0,
			last_used_at ` + d.Int + ` NOT NULL DEFAULT 0,
			revoked ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Account invitations issued by a Server Admin, and org membership invitations.
		`CREATE TABLE IF NOT EXISTS invites (
			id ` + d.AutoIncrementPK + `,
			kind ` + d.Text + ` NOT NULL DEFAULT 'user',
			code_hash ` + d.Text + ` NOT NULL,
			email ` + d.Text + ` NOT NULL DEFAULT '',
			org_id ` + d.Int + ` NOT NULL DEFAULT 0,
			role ` + d.Text + ` NOT NULL DEFAULT 'user',
			max_uses ` + d.Int + ` NOT NULL DEFAULT 1,
			use_count ` + d.Int + ` NOT NULL DEFAULT 0,
			expires_at ` + d.Int + ` NOT NULL DEFAULT 0,
			revoked ` + d.Int + ` NOT NULL DEFAULT 0,
			created_by ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS password_resets (
			id ` + d.AutoIncrementPK + `,
			user_id ` + d.Int + ` NOT NULL,
			token_hash ` + d.Text + ` NOT NULL,
			used ` + d.Int + ` NOT NULL DEFAULT 0,
			expires_at ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS email_verifications (
			id ` + d.AutoIncrementPK + `,
			user_id ` + d.Int + ` NOT NULL,
			email ` + d.Text + ` NOT NULL,
			token_hash ` + d.Text + ` NOT NULL,
			used ` + d.Int + ` NOT NULL DEFAULT 0,
			expires_at ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS orgs (
			id ` + d.AutoIncrementPK + `,
			slug ` + d.Text + ` NOT NULL,
			name ` + d.Text + ` NOT NULL DEFAULT '',
			description ` + d.Text + ` NOT NULL DEFAULT '',
			avatar_type ` + d.Text + ` NOT NULL DEFAULT 'gravatar',
			avatar_url ` + d.Text + ` NOT NULL DEFAULT '',
			website ` + d.Text + ` NOT NULL DEFAULT '',
			location ` + d.Text + ` NOT NULL DEFAULT '',
			visibility ` + d.Text + ` NOT NULL DEFAULT 'public',
			owner_id ` + d.Int + ` NOT NULL,
			suspended ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS org_members (
			id ` + d.AutoIncrementPK + `,
			org_id ` + d.Int + ` NOT NULL,
			user_id ` + d.Int + ` NOT NULL,
			role ` + d.Text + ` NOT NULL DEFAULT 'member',
			joined_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Append-only record of security-relevant organization events.
		`CREATE TABLE IF NOT EXISTS org_audit (
			id ` + d.AutoIncrementPK + `,
			org_id ` + d.Int + ` NOT NULL,
			action ` + d.Text + ` NOT NULL,
			actor_type ` + d.Text + ` NOT NULL DEFAULT 'user',
			actor_id ` + d.Int + ` NOT NULL DEFAULT 0,
			details ` + d.Text + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS custom_domains (
			id ` + d.AutoIncrementPK + `,
			owner_type ` + d.Text + ` NOT NULL,
			owner_id ` + d.Int + ` NOT NULL,
			domain ` + d.Text + ` NOT NULL,
			is_apex ` + d.Int + ` NOT NULL DEFAULT 0,
			is_wildcard ` + d.Int + ` NOT NULL DEFAULT 0,
			verification_status ` + d.Text + ` NOT NULL DEFAULT 'pending',
			verification_token ` + d.Text + ` NOT NULL,
			verified_at ` + d.Int + ` NOT NULL DEFAULT 0,
			last_check_at ` + d.Int + ` NOT NULL DEFAULT 0,
			check_count ` + d.Int + ` NOT NULL DEFAULT 0,
			ssl_enabled ` + d.Int + ` NOT NULL DEFAULT 0,
			ssl_status ` + d.Text + ` NOT NULL DEFAULT 'none',
			ssl_challenge ` + d.Text + ` NOT NULL DEFAULT '',
			ssl_provider ` + d.Text + ` NOT NULL DEFAULT '',
			ssl_credentials ` + d.Text + ` NOT NULL DEFAULT '',
			ssl_cert_pem ` + d.Text + ` NOT NULL DEFAULT '',
			ssl_key_pem ` + d.Text + ` NOT NULL DEFAULT '',
			ssl_issued_at ` + d.Int + ` NOT NULL DEFAULT 0,
			ssl_expires_at ` + d.Int + ` NOT NULL DEFAULT 0,
			ssl_last_error ` + d.Text + ` NOT NULL DEFAULT '',
			status ` + d.Text + ` NOT NULL DEFAULT 'pending',
			suspended_reason ` + d.Text + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS custom_domain_audit (
			id ` + d.AutoIncrementPK + `,
			domain_id ` + d.Int + ` NOT NULL,
			action ` + d.Text + ` NOT NULL,
			actor_type ` + d.Text + ` NOT NULL DEFAULT 'system',
			actor_id ` + d.Int + ` NOT NULL DEFAULT 0,
			details ` + d.Text + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,
	}

	indexes := [][]string{
		{"idx_admins_username", "admins", "username"},
		{"idx_admins_token_hash", "admins", "token_hash"},
		{"idx_admin_sessions_token", "admin_sessions", "token_hash"},
		{"idx_admin_sessions_expires", "admin_sessions", "expires_at"},
		{"idx_setup_tokens_hash", "setup_tokens", "token_hash"},
		{"idx_users_username", "users", "username"},
		{"idx_users_email", "users", "email"},
		{"idx_name_tombstones_name", "name_tombstones", "name"},
		{"idx_user_sessions_token", "user_sessions", "token_hash"},
		{"idx_user_sessions_user", "user_sessions", "user_id"},
		{"idx_user_sessions_expires", "user_sessions", "expires_at"},
		{"idx_user_tokens_hash", "user_tokens", "token_hash"},
		{"idx_user_tokens_user", "user_tokens", "user_id"},
		{"idx_org_tokens_hash", "org_tokens", "token_hash"},
		{"idx_org_tokens_org", "org_tokens", "org_id"},
		{"idx_invites_code_hash", "invites", "code_hash"},
		{"idx_invites_org", "invites", "org_id"},
		{"idx_password_resets_hash", "password_resets", "token_hash"},
		{"idx_password_resets_user", "password_resets", "user_id"},
		{"idx_email_verifications_hash", "email_verifications", "token_hash"},
		{"idx_email_verifications_user", "email_verifications", "user_id"},
		{"idx_orgs_slug", "orgs", "slug"},
		{"idx_orgs_owner", "orgs", "owner_id"},
		{"idx_org_members_org", "org_members", "org_id"},
		{"idx_org_members_user", "org_members", "user_id"},
		{"idx_org_audit_org", "org_audit", "org_id"},
		{"idx_custom_domains_domain", "custom_domains", "domain"},
		{"idx_custom_domains_owner", "custom_domains", "owner_type", "owner_id"},
		{"idx_custom_domains_status", "custom_domains", "status"},
		{"idx_custom_domains_ssl_expires", "custom_domains", "ssl_expires_at"},
		{"idx_domain_audit_domain", "custom_domain_audit", "domain_id"},
	}
	for _, idx := range indexes {
		stmts = append(stmts, database.CreateIndex(driver, idx[0], idx[1], idx[2:]...))
	}
	return stmts
}
