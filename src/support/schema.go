package support

import (
	"github.com/webappsgo/cashp/src/database"
)

// SchemaName is the registration name of this package's DDL fragment.
const SchemaName = "support"

// init registers the support schema so EnsureSchema creates it on startup.
// Every statement is additive and idempotent: there are no migration files, no
// schema version table, and no destructive statement anywhere in this file.
func init() {
	database.RegisterSchema(SchemaName, schemaDDL)
}

// schemaDDL returns the support tables and indexes for the active driver.
func schemaDDL(driver string) []string {
	d := database.DialectFor(driver)

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS support_departments (
			id ` + d.Key + ` NOT NULL,
			name ` + d.Text + ` NOT NULL,
			description ` + d.Text + ` NOT NULL DEFAULT '',
			enabled ` + d.Int + ` NOT NULL DEFAULT 1,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS support_agents (
			id ` + d.Key + ` NOT NULL,
			user_id ` + d.Int + ` NOT NULL,
			display_name ` + d.Text + ` NOT NULL,
			department_id ` + d.Key + ` NOT NULL DEFAULT '',
			max_concurrent_chats ` + d.Int + ` NOT NULL DEFAULT 3,
			enabled ` + d.Int + ` NOT NULL DEFAULT 1,
			last_activity_at ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS support_categories (
			id ` + d.Key + ` NOT NULL,
			parent_id ` + d.Key + ` NOT NULL DEFAULT '',
			name ` + d.Text + ` NOT NULL,
			slug ` + d.Key + ` NOT NULL,
			position ` + d.Int + ` NOT NULL DEFAULT 0,
			enabled ` + d.Int + ` NOT NULL DEFAULT 1
		)`,

		`CREATE TABLE IF NOT EXISTS support_sla_policies (
			id ` + d.Key + ` NOT NULL,
			priority ` + d.Key + ` NOT NULL,
			first_response_mins ` + d.Int + ` NOT NULL DEFAULT 1440,
			resolution_mins ` + d.Int + ` NOT NULL DEFAULT 4320,
			escalate_percent ` + d.Int + ` NOT NULL DEFAULT 80,
			enabled ` + d.Int + ` NOT NULL DEFAULT 1,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS support_tickets (
			id ` + d.Key + ` NOT NULL,
			org_id ` + d.Int + ` NOT NULL,
			number ` + d.Key + ` NOT NULL,
			title ` + d.Text + ` NOT NULL,
			description ` + d.Text + ` NOT NULL DEFAULT '',
			category_id ` + d.Key + ` NOT NULL DEFAULT '',
			priority ` + d.Key + ` NOT NULL DEFAULT 'NORMAL',
			status ` + d.Key + ` NOT NULL DEFAULT 'DRAFT',
			user_id ` + d.Int + ` NOT NULL,
			assigned_to ` + d.Int + ` NOT NULL DEFAULT 0,
			bot_context ` + d.Text + ` NOT NULL DEFAULT '',
			sla_policy_id ` + d.Key + ` NOT NULL DEFAULT '',
			first_response_at ` + d.Int + ` NOT NULL DEFAULT 0,
			resolved_at ` + d.Int + ` NOT NULL DEFAULT 0,
			closed_at ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0,
			version ` + d.Int + ` NOT NULL DEFAULT 1
		)`,

		`CREATE TABLE IF NOT EXISTS support_ticket_messages (
			id ` + d.Key + ` NOT NULL,
			ticket_id ` + d.Key + ` NOT NULL,
			org_id ` + d.Int + ` NOT NULL,
			author_id ` + d.Int + ` NOT NULL DEFAULT 0,
			author_role ` + d.Key + ` NOT NULL DEFAULT 'user',
			author_name ` + d.Text + ` NOT NULL DEFAULT '',
			body ` + d.Text + ` NOT NULL DEFAULT '',
			internal ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS support_ticket_attachments (
			id ` + d.Key + ` NOT NULL,
			ticket_id ` + d.Key + ` NOT NULL,
			org_id ` + d.Int + ` NOT NULL,
			message_id ` + d.Key + ` NOT NULL DEFAULT '',
			original_name ` + d.Text + ` NOT NULL DEFAULT '',
			stored_name ` + d.Key + ` NOT NULL,
			content_type ` + d.Key + ` NOT NULL DEFAULT 'application/octet-stream',
			size_bytes ` + d.Int + ` NOT NULL DEFAULT 0,
			uploaded_by ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS support_assignments (
			id ` + d.Key + ` NOT NULL,
			ticket_id ` + d.Key + ` NOT NULL,
			org_id ` + d.Int + ` NOT NULL,
			from_agent_id ` + d.Int + ` NOT NULL DEFAULT 0,
			to_agent_id ` + d.Int + ` NOT NULL DEFAULT 0,
			actor_id ` + d.Int + ` NOT NULL DEFAULT 0,
			reason ` + d.Text + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS support_audit_logs (
			id ` + d.Key + ` NOT NULL,
			org_id ` + d.Int + ` NOT NULL DEFAULT 0,
			actor_id ` + d.Int + ` NOT NULL DEFAULT 0,
			on_behalf_of ` + d.Int + ` NOT NULL DEFAULT 0,
			action ` + d.Key + ` NOT NULL,
			entity_type ` + d.Key + ` NOT NULL DEFAULT '',
			entity_id ` + d.Key + ` NOT NULL DEFAULT '',
			from_state ` + d.Key + ` NOT NULL DEFAULT '',
			to_state ` + d.Key + ` NOT NULL DEFAULT '',
			detail ` + d.Text + ` NOT NULL DEFAULT '',
			support_mode ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS support_settings (
			setting_key ` + d.Key + ` NOT NULL,
			setting_value ` + d.Text + ` NOT NULL DEFAULT '',
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS support_counters (
			name ` + d.Key + ` NOT NULL,
			value ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS support_kb_articles (
			id ` + d.Key + ` NOT NULL,
			org_id ` + d.Int + ` NOT NULL DEFAULT 0,
			slug ` + d.Key + ` NOT NULL,
			title ` + d.Text + ` NOT NULL,
			body ` + d.Text + ` NOT NULL DEFAULT '',
			category_id ` + d.Key + ` NOT NULL DEFAULT '',
			tags ` + d.Text + ` NOT NULL DEFAULT '',
			status ` + d.Key + ` NOT NULL DEFAULT 'DRAFT',
			author_id ` + d.Int + ` NOT NULL DEFAULT 0,
			helpful_count ` + d.Int + ` NOT NULL DEFAULT 0,
			not_helpful_count ` + d.Int + ` NOT NULL DEFAULT 0,
			view_count ` + d.Int + ` NOT NULL DEFAULT 0,
			published_at ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0,
			version ` + d.Int + ` NOT NULL DEFAULT 1
		)`,

		`CREATE TABLE IF NOT EXISTS support_chat_sessions (
			id ` + d.Key + ` NOT NULL,
			org_id ` + d.Int + ` NOT NULL,
			user_id ` + d.Int + ` NOT NULL,
			agent_id ` + d.Int + ` NOT NULL DEFAULT 0,
			status ` + d.Key + ` NOT NULL DEFAULT 'QUEUED',
			subject ` + d.Text + ` NOT NULL DEFAULT '',
			ticket_id ` + d.Key + ` NOT NULL DEFAULT '',
			queued_at ` + d.Int + ` NOT NULL DEFAULT 0,
			started_at ` + d.Int + ` NOT NULL DEFAULT 0,
			ended_at ` + d.Int + ` NOT NULL DEFAULT 0,
			rating ` + d.Int + ` NOT NULL DEFAULT 0,
			last_event_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS support_chat_messages (
			id ` + d.Key + ` NOT NULL,
			session_id ` + d.Key + ` NOT NULL,
			org_id ` + d.Int + ` NOT NULL,
			author_id ` + d.Int + ` NOT NULL DEFAULT 0,
			author_role ` + d.Key + ` NOT NULL DEFAULT 'user',
			author_name ` + d.Text + ` NOT NULL DEFAULT '',
			body ` + d.Text + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS support_canned_responses (
			id ` + d.Key + ` NOT NULL,
			scope ` + d.Key + ` NOT NULL DEFAULT 'PERSONAL',
			department_id ` + d.Key + ` NOT NULL DEFAULT '',
			agent_user_id ` + d.Int + ` NOT NULL DEFAULT 0,
			title ` + d.Text + ` NOT NULL,
			body ` + d.Text + ` NOT NULL DEFAULT '',
			tags ` + d.Text + ` NOT NULL DEFAULT '',
			usage_count ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS support_bot_sessions (
			id ` + d.Key + ` NOT NULL,
			org_id ` + d.Int + ` NOT NULL DEFAULT 0,
			user_id ` + d.Int + ` NOT NULL DEFAULT 0,
			attempts ` + d.Int + ` NOT NULL DEFAULT 0,
			resolved ` + d.Int + ` NOT NULL DEFAULT 0,
			escalated ` + d.Int + ` NOT NULL DEFAULT 0,
			transcript ` + d.Text + ` NOT NULL DEFAULT '',
			last_category ` + d.Key + ` NOT NULL DEFAULT '',
			last_priority ` + d.Key + ` NOT NULL DEFAULT 'NORMAL',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,
	}

	indexes := [][]string{
		{"ix_support_tickets_id", "support_tickets", "id"},
		{"ix_support_tickets_number", "support_tickets", "number"},
		{"ix_support_tickets_user_status", "support_tickets", "user_id", "status"},
		{"ix_support_tickets_assigned_status", "support_tickets", "assigned_to", "status"},
		{"ix_support_tickets_org_status", "support_tickets", "org_id", "status"},
		{"ix_support_tickets_created", "support_tickets", "created_at"},
		{"ix_support_tickets_search", "support_tickets", "org_id", "title"},
		{"ix_support_messages_ticket", "support_ticket_messages", "ticket_id", "created_at"},
		{"ix_support_messages_org", "support_ticket_messages", "org_id", "ticket_id"},
		{"ix_support_attachments_ticket", "support_ticket_attachments", "ticket_id"},
		{"ix_support_attachments_stored", "support_ticket_attachments", "stored_name"},
		{"ix_support_assignments_ticket", "support_assignments", "ticket_id", "created_at"},
		{"ix_support_audit_entity", "support_audit_logs", "entity_type", "entity_id"},
		{"ix_support_audit_created", "support_audit_logs", "created_at"},
		{"ix_support_settings_key", "support_settings", "setting_key"},
		{"ix_support_counters_name", "support_counters", "name"},
		{"ix_support_agents_user", "support_agents", "user_id"},
		{"ix_support_agents_enabled", "support_agents", "enabled", "last_activity_at"},
		{"ix_support_departments_id", "support_departments", "id"},
		{"ix_support_categories_slug", "support_categories", "slug"},
		{"ix_support_sla_priority", "support_sla_policies", "priority"},
		{"ix_support_kb_slug", "support_kb_articles", "slug"},
		{"ix_support_kb_status", "support_kb_articles", "status", "org_id"},
		{"ix_support_kb_title", "support_kb_articles", "title"},
		{"ix_support_chat_org_status", "support_chat_sessions", "org_id", "status"},
		{"ix_support_chat_agent", "support_chat_sessions", "agent_id", "status"},
		{"ix_support_chat_messages_session", "support_chat_messages", "session_id", "created_at"},
		{"ix_support_canned_scope", "support_canned_responses", "scope", "department_id"},
		{"ix_support_canned_agent", "support_canned_responses", "agent_user_id"},
		{"ix_support_bot_sessions_user", "support_bot_sessions", "user_id", "updated_at"},
	}
	for _, idx := range indexes {
		stmts = append(stmts, database.CreateIndex(driver, idx[0], idx[1], idx[2:]...))
	}

	return stmts
}
