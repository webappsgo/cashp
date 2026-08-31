package notify

import (
	"fmt"

	"github.com/webappsgo/cashp/src/database"
)

// Table names owned by this package.
const (
	// TableAdminNotifications stores the server admin notification center.
	TableAdminNotifications = "admin_notifications"
	// TableUserNotifications stores the per-user notification center.
	TableUserNotifications = "user_notifications"
	// TableDeliveries tracks every outbound delivery attempt.
	TableDeliveries = "notification_deliveries"
	// TableDedup holds claimed deduplication keys.
	TableDedup = "notification_dedup"
	// TablePreferences holds per-recipient channel opt-in state.
	TablePreferences = "notification_preferences"
	// TableAudit is the append-only record of configuration changes and
	// delivery outcomes.
	TableAudit = "notification_audit"
)

// Retention limits from AI.md PART 18 -> "Notification Storage".
const (
	// RetentionDays is how long a stored notification is kept.
	RetentionDays = 30
	// MaxPerOwner is how many notifications one admin or user keeps.
	MaxPerOwner = 100
)

// init registers this package's self-creating schema. Every statement is a
// CREATE TABLE IF NOT EXISTS or an index creation, so applying it on every
// startup and on every cluster node concurrently is safe.
func init() {
	database.RegisterSchema("notify", schemaDDL)
}

// bodyColumn returns a column type able to hold a rendered notification
// body. Dialect.Text caps at 255 characters on MySQL, which is far too
// short for an email body.
func bodyColumn(driver string) string {
	switch driver {
	case database.DriverMySQL:
		return "TEXT"
	case database.DriverSQLServer:
		return "NVARCHAR(MAX)"
	default:
		return "TEXT"
	}
}

// schemaDDL returns the idempotent DDL for one driver.
func schemaDDL(driver string) []string {
	d := database.DialectFor(driver)
	body := bodyColumn(driver)

	stmts := []string{
		notificationTable(TableAdminNotifications, "admin_id", d, body),
		notificationTable(TableUserNotifications, "user_id", d, body),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
	id %s NOT NULL PRIMARY KEY,
	notification_id %s NOT NULL DEFAULT '',
	event %s NOT NULL DEFAULT '',
	channel %s NOT NULL DEFAULT '',
	role %s NOT NULL DEFAULT '',
	recipient %s NOT NULL DEFAULT '',
	status %s NOT NULL DEFAULT 'pending',
	attempt %s NOT NULL DEFAULT 0,
	next_attempt_at %s NOT NULL DEFAULT 0,
	last_error %s NOT NULL DEFAULT '',
	payload %s,
	created_at %s NOT NULL DEFAULT 0,
	updated_at %s NOT NULL DEFAULT 0
)`, TableDeliveries, d.Key, d.Key, d.Key, d.Key, d.Key, d.Text, d.Key, d.Int, d.Int, d.Text, body, d.Int, d.Int),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
	dedup_key %s NOT NULL PRIMARY KEY,
	event %s NOT NULL DEFAULT '',
	claimed_at %s NOT NULL DEFAULT 0,
	expires_at %s NOT NULL DEFAULT 0
)`, TableDedup, d.Key, d.Key, d.Int, d.Int),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
	audience %s NOT NULL,
	owner_id %s NOT NULL,
	event %s NOT NULL,
	webui %s NOT NULL DEFAULT 1,
	email %s NOT NULL DEFAULT 1,
	updated_at %s NOT NULL DEFAULT 0,
	PRIMARY KEY (audience, owner_id, event)
)`, TablePreferences, d.Key, d.Key, d.Key, d.Int, d.Int, d.Int),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
	id %s NOT NULL PRIMARY KEY,
	occurred_at %s NOT NULL DEFAULT 0,
	actor %s NOT NULL DEFAULT '',
	action %s NOT NULL DEFAULT '',
	channel %s NOT NULL DEFAULT '',
	event %s NOT NULL DEFAULT '',
	result %s NOT NULL DEFAULT '',
	detail %s
)`, TableAudit, d.Key, d.Int, d.Key, d.Key, d.Key, d.Key, d.Key, body),
	}

	stmts = append(stmts,
		database.CreateIndex(driver, "idx_admin_notifications_owner", TableAdminNotifications, "admin_id", "created_at"),
		database.CreateIndex(driver, "idx_admin_notifications_unread", TableAdminNotifications, "admin_id", "read_at"),
		database.CreateIndex(driver, "idx_user_notifications_owner", TableUserNotifications, "user_id", "created_at"),
		database.CreateIndex(driver, "idx_user_notifications_unread", TableUserNotifications, "user_id", "read_at"),
		database.CreateIndex(driver, "idx_notification_deliveries_due", TableDeliveries, "status", "next_attempt_at"),
		database.CreateIndex(driver, "idx_notification_deliveries_channel", TableDeliveries, "channel", "created_at"),
		database.CreateIndex(driver, "idx_notification_dedup_expiry", TableDedup, "expires_at"),
		database.CreateIndex(driver, "idx_notification_audit_time", TableAudit, "occurred_at"),
	)
	return stmts
}

// notificationTable builds the DDL for one notification store. The two
// stores are structurally identical and differ only in the owner column.
func notificationTable(table, owner string, d database.Dialect, body string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
	id %s NOT NULL PRIMARY KEY,
	%s %s NOT NULL DEFAULT '',
	event %s NOT NULL DEFAULT '',
	type %s NOT NULL DEFAULT 'info',
	surfaces %s NOT NULL DEFAULT 0,
	title %s NOT NULL DEFAULT '',
	body %s,
	link %s NOT NULL DEFAULT '',
	read_at %s NOT NULL DEFAULT 0,
	dismissed_at %s NOT NULL DEFAULT 0,
	created_at %s NOT NULL DEFAULT 0
)`, table, d.Key, owner, d.Key, d.Key, d.Key, d.Int, d.Text, body, d.Text, d.Int, d.Int, d.Int)
}
