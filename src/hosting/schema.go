package hosting

import (
	"github.com/webappsgo/cashp/src/database"
)

// SchemaName is the fragment name this package registers with src/database.
const SchemaName = "hosting"

// Schema for the hosting subsystem. Every statement is additive and
// idempotent per AI.md PART 10: no DROP, no DELETE, no rename, no migration
// files. Timestamps are Unix seconds. Every tenant-owned table carries
// tenant_id and every query filters on it.
func init() {
	database.RegisterSchema(SchemaName, hostingSchema)
}

// hostingSchema returns the DDL for the hosting tables in the dialect of the
// active driver.
func hostingSchema(driver string) []string {
	d := database.DialectFor(driver)

	stmts := []string{
		// One nginx virtual host. doc_root is relative to the tenant's site
		// directory; the absolute path is always re-derived via SafeJoin.
		`CREATE TABLE IF NOT EXISTS hosting_sites (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL,
			name ` + d.Key + ` NOT NULL,
			primary_domain ` + d.Key + ` NOT NULL,
			aliases ` + d.Text + ` NOT NULL DEFAULT '',
			doc_root ` + d.Text + ` NOT NULL DEFAULT '',
			php_version ` + d.Key + ` NOT NULL DEFAULT 'none',
			tls_enabled ` + d.Int + ` NOT NULL DEFAULT 0,
			enabled ` + d.Int + ` NOT NULL DEFAULT 0,
			disk_quota_mb ` + d.Int + ` NOT NULL DEFAULT 0,
			bandwidth_quota_mb ` + d.Int + ` NOT NULL DEFAULT 0,
			disk_used_mb ` + d.Int + ` NOT NULL DEFAULT 0,
			git_remote ` + d.Text + ` NOT NULL DEFAULT '',
			git_branch ` + d.Key + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// A BIND authoritative zone. serial is bumped on every change.
		`CREATE TABLE IF NOT EXISTS hosting_dns_zones (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL,
			name ` + d.Key + ` NOT NULL,
			primary_ns ` + d.Key + ` NOT NULL DEFAULT '',
			hostmaster ` + d.Key + ` NOT NULL DEFAULT '',
			serial ` + d.Int + ` NOT NULL DEFAULT 0,
			refresh ` + d.Int + ` NOT NULL DEFAULT 3600,
			retry ` + d.Int + ` NOT NULL DEFAULT 900,
			expire ` + d.Int + ` NOT NULL DEFAULT 604800,
			minimum ` + d.Int + ` NOT NULL DEFAULT 3600,
			default_ttl ` + d.Int + ` NOT NULL DEFAULT 3600,
			dnssec ` + d.Int + ` NOT NULL DEFAULT 0,
			enabled ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// One resource record. priority holds the MX preference, the SRV
		// priority, or the CAA flags byte depending on type.
		`CREATE TABLE IF NOT EXISTS hosting_dns_records (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			zone_id ` + d.Key + ` NOT NULL,
			tenant_id ` + d.Key + ` NOT NULL,
			name ` + d.Key + ` NOT NULL DEFAULT '@',
			type ` + d.Key + ` NOT NULL,
			value ` + d.Text + ` NOT NULL DEFAULT '',
			ttl ` + d.Int + ` NOT NULL DEFAULT 3600,
			priority ` + d.Int + ` NOT NULL DEFAULT 0,
			weight ` + d.Int + ` NOT NULL DEFAULT 0,
			port ` + d.Int + ` NOT NULL DEFAULT 0,
			managed ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// A mail-hosting domain. dkim_private holds base64 AES-GCM
		// ciphertext and is never returned through an API surface.
		`CREATE TABLE IF NOT EXISTS hosting_mail_domains (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL,
			domain ` + d.Key + ` NOT NULL,
			dkim_selector ` + d.Key + ` NOT NULL DEFAULT '',
			dkim_private ` + d.Text + ` NOT NULL DEFAULT '',
			dkim_public ` + d.Text + ` NOT NULL DEFAULT '',
			enabled ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// A virtual mailbox. password_hash is an Argon2id PHC string.
		`CREATE TABLE IF NOT EXISTS hosting_mailboxes (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL,
			domain_id ` + d.Key + ` NOT NULL,
			domain ` + d.Key + ` NOT NULL,
			local_part ` + d.Key + ` NOT NULL,
			password_hash ` + d.Text + ` NOT NULL DEFAULT '',
			quota_mb ` + d.Int + ` NOT NULL DEFAULT 0,
			enabled ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// A virtual alias mapping one address to another.
		`CREATE TABLE IF NOT EXISTS hosting_mail_aliases (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL,
			domain_id ` + d.Key + ` NOT NULL,
			domain ` + d.Key + ` NOT NULL,
			source ` + d.Key + ` NOT NULL,
			destination ` + d.Key + ` NOT NULL,
			enabled ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// A PaaS application and the workload currently serving it.
		`CREATE TABLE IF NOT EXISTS hosting_apps (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL,
			name ` + d.Key + ` NOT NULL,
			runtime ` + d.Key + ` NOT NULL DEFAULT 'static',
			git_remote ` + d.Text + ` NOT NULL DEFAULT '',
			git_branch ` + d.Key + ` NOT NULL DEFAULT '',
			domain ` + d.Key + ` NOT NULL DEFAULT '',
			port ` + d.Int + ` NOT NULL DEFAULT 0,
			replicas ` + d.Int + ` NOT NULL DEFAULT 1,
			memory_mb ` + d.Int + ` NOT NULL DEFAULT 0,
			cpu_shares ` + d.Int + ` NOT NULL DEFAULT 0,
			state ` + d.Key + ` NOT NULL DEFAULT 'created',
			workload_id ` + d.Key + ` NOT NULL DEFAULT '',
			release_id ` + d.Key + ` NOT NULL DEFAULT '',
			database_ref ` + d.Key + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Environment entries. A secret value lives only in enc_value as
		// base64 AES-GCM ciphertext; plain_value stays empty for secrets.
		`CREATE TABLE IF NOT EXISTS hosting_app_env (
			app_id ` + d.Key + ` NOT NULL,
			tenant_id ` + d.Key + ` NOT NULL,
			env_key ` + d.Key + ` NOT NULL,
			plain_value ` + d.Text + ` NOT NULL DEFAULT '',
			enc_value ` + d.Text + ` NOT NULL DEFAULT '',
			is_secret ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0,
			PRIMARY KEY (app_id, env_key)
		)`,

		// An immutable deploy attempt with its rollback lineage.
		`CREATE TABLE IF NOT EXISTS hosting_app_releases (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL,
			app_id ` + d.Key + ` NOT NULL,
			number ` + d.Int + ` NOT NULL DEFAULT 0,
			source ` + d.Text + ` NOT NULL DEFAULT '',
			image ` + d.Text + ` NOT NULL DEFAULT '',
			command ` + d.Text + ` NOT NULL DEFAULT '',
			state ` + d.Key + ` NOT NULL DEFAULT 'pending',
			workload_id ` + d.Key + ` NOT NULL DEFAULT '',
			log ` + d.Text + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Proof that a tenant controls a domain. A vhost or a zone is only
		// activated for a domain whose ownership row is verified.
		`CREATE TABLE IF NOT EXISTS hosting_domain_ownership (
			domain ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL,
			token ` + d.Key + ` NOT NULL DEFAULT '',
			method ` + d.Key + ` NOT NULL DEFAULT 'dns',
			verified ` + d.Int + ` NOT NULL DEFAULT 0,
			verified_at ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,
	}

	indexes := [][]string{
		{"idx_hosting_sites_tenant", "hosting_sites", "tenant_id"},
		{"idx_hosting_sites_domain", "hosting_sites", "primary_domain"},
		{"idx_hosting_zones_tenant", "hosting_dns_zones", "tenant_id"},
		{"idx_hosting_zones_name", "hosting_dns_zones", "name"},
		{"idx_hosting_records_zone", "hosting_dns_records", "zone_id"},
		{"idx_hosting_records_tenant", "hosting_dns_records", "tenant_id"},
		{"idx_hosting_maildomains_tenant", "hosting_mail_domains", "tenant_id"},
		{"idx_hosting_maildomains_domain", "hosting_mail_domains", "domain"},
		{"idx_hosting_mailboxes_tenant", "hosting_mailboxes", "tenant_id"},
		{"idx_hosting_mailboxes_domain", "hosting_mailboxes", "domain_id"},
		{"idx_hosting_aliases_tenant", "hosting_mail_aliases", "tenant_id"},
		{"idx_hosting_aliases_domain", "hosting_mail_aliases", "domain_id"},
		{"idx_hosting_apps_tenant", "hosting_apps", "tenant_id"},
		{"idx_hosting_env_app", "hosting_app_env", "app_id"},
		{"idx_hosting_releases_app", "hosting_app_releases", "app_id"},
		{"idx_hosting_releases_tenant", "hosting_app_releases", "tenant_id"},
		{"idx_hosting_ownership_tenant", "hosting_domain_ownership", "tenant_id"},
	}
	for _, idx := range indexes {
		stmts = append(stmts, database.CreateIndex(driver, idx[0], idx[1], idx[2:]...))
	}

	return stmts
}
