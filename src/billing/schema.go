package billing

import (
	"github.com/webappsgo/cashp/src/database"
)

// Schema tables owned by the billing subsystem. Every statement is additive
// and idempotent per AI.md PART 10: no DROP, no DELETE, no rename, no
// migration files. Timestamps are Unix seconds and every monetary column is
// an integer count of the currency's minor unit, never a float.
func init() {
	database.RegisterSchema("billing", billingSchema)
}

// billingSchema returns the DDL for the billing tables in the dialect of the
// active driver.
func billingSchema(driver string) []string {
	d := database.DialectFor(driver)

	stmts := []string{
		// Billing profile for one tenant. Card data never lands here: only
		// the provider's own token, held on billing_payment_methods.
		`CREATE TABLE IF NOT EXISTS billing_accounts (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL UNIQUE,
			currency ` + d.Key + ` NOT NULL DEFAULT 'USD',
			billing_email ` + d.Text + ` NOT NULL DEFAULT '',
			legal_name ` + d.Text + ` NOT NULL DEFAULT '',
			address_line1 ` + d.Text + ` NOT NULL DEFAULT '',
			address_line2 ` + d.Text + ` NOT NULL DEFAULT '',
			city ` + d.Text + ` NOT NULL DEFAULT '',
			region ` + d.Text + ` NOT NULL DEFAULT '',
			postal_code ` + d.Key + ` NOT NULL DEFAULT '',
			country ` + d.Key + ` NOT NULL DEFAULT '',
			tax_id ` + d.Text + ` NOT NULL DEFAULT '',
			tax_id_status ` + d.Key + ` NOT NULL DEFAULT 'none',
			is_business ` + d.Int + ` NOT NULL DEFAULT 0,
			balance_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			auto_recharge ` + d.Int + ` NOT NULL DEFAULT 0,
			auto_recharge_threshold_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			auto_recharge_amount_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			default_method_id ` + d.Key + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0,
			version ` + d.Int + ` NOT NULL DEFAULT 1
		)`,

		// Plan catalogue. A plan sets prices and quota ceilings only; it can
		// never switch a product feature off (IDEA.md non-goals).
		`CREATE TABLE IF NOT EXISTS billing_plans (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			code ` + d.Key + ` NOT NULL UNIQUE,
			name ` + d.Text + ` NOT NULL DEFAULT '',
			description ` + d.Text + ` NOT NULL DEFAULT '',
			currency ` + d.Key + ` NOT NULL DEFAULT 'USD',
			price_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			cycle ` + d.Key + ` NOT NULL DEFAULT 'monthly',
			trial_days ` + d.Int + ` NOT NULL DEFAULT 0,
			grace_days ` + d.Int + ` NOT NULL DEFAULT 7,
			visibility ` + d.Key + ` NOT NULL DEFAULT 'public',
			overage_policy ` + d.Key + ` NOT NULL DEFAULT 'block',
			active ` + d.Int + ` NOT NULL DEFAULT 1,
			sort_order ` + d.Int + ` NOT NULL DEFAULT 0,
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0,
			version ` + d.Int + ` NOT NULL DEFAULT 1
		)`,

		// Quota ceilings attached to a plan. limit_value -1 means unlimited.
		`CREATE TABLE IF NOT EXISTS billing_plan_quotas (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			plan_id ` + d.Key + ` NOT NULL,
			resource ` + d.Key + ` NOT NULL,
			limit_value ` + d.Int + ` NOT NULL DEFAULT 0,
			enforcement ` + d.Key + ` NOT NULL DEFAULT 'hard',
			burst_value ` + d.Int + ` NOT NULL DEFAULT 0,
			overage_unit_price_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// One subscription per tenant per plan. price_minor is the price
		// captured at subscribe time so a later catalogue edit never silently
		// re-prices an existing customer.
		`CREATE TABLE IF NOT EXISTS billing_subscriptions (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL,
			account_id ` + d.Key + ` NOT NULL DEFAULT '',
			plan_id ` + d.Key + ` NOT NULL,
			state ` + d.Key + ` NOT NULL DEFAULT 'pending_activation',
			cycle ` + d.Key + ` NOT NULL DEFAULT 'monthly',
			currency ` + d.Key + ` NOT NULL DEFAULT 'USD',
			price_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			quantity ` + d.Int + ` NOT NULL DEFAULT 1,
			trial_ends_at ` + d.Int + ` NOT NULL DEFAULT 0,
			period_start ` + d.Int + ` NOT NULL DEFAULT 0,
			period_end ` + d.Int + ` NOT NULL DEFAULT 0,
			grace_ends_at ` + d.Int + ` NOT NULL DEFAULT 0,
			cancel_at_period_end ` + d.Int + ` NOT NULL DEFAULT 0,
			cancelled_at ` + d.Int + ` NOT NULL DEFAULT 0,
			ended_at ` + d.Int + ` NOT NULL DEFAULT 0,
			pending_plan_id ` + d.Key + ` NOT NULL DEFAULT '',
			pending_effective_at ` + d.Int + ` NOT NULL DEFAULT 0,
			provider ` + d.Key + ` NOT NULL DEFAULT '',
			provider_ref ` + d.Text + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0,
			version ` + d.Int + ` NOT NULL DEFAULT 1
		)`,

		// Append-only lifecycle log for a subscription. Rows are never
		// updated or deleted.
		`CREATE TABLE IF NOT EXISTS billing_subscription_events (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL DEFAULT '',
			subscription_id ` + d.Key + ` NOT NULL DEFAULT '',
			event ` + d.Key + ` NOT NULL DEFAULT '',
			from_state ` + d.Key + ` NOT NULL DEFAULT '',
			to_state ` + d.Key + ` NOT NULL DEFAULT '',
			actor ` + d.Key + ` NOT NULL DEFAULT 'system',
			detail ` + d.Text + ` NOT NULL DEFAULT '',
			occurred_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Per-tenant quota overrides granted by a global admin. They win over
		// the plan ceiling for as long as they are unexpired.
		`CREATE TABLE IF NOT EXISTS billing_quota_overrides (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL,
			resource ` + d.Key + ` NOT NULL,
			limit_value ` + d.Int + ` NOT NULL DEFAULT 0,
			enforcement ` + d.Key + ` NOT NULL DEFAULT 'hard',
			reason ` + d.Text + ` NOT NULL DEFAULT '',
			created_by ` + d.Key + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			expires_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Meter catalogue: what is measured, how it aggregates, when it
		// resets.
		`CREATE TABLE IF NOT EXISTS billing_usage_meters (
			code ` + d.Key + ` NOT NULL PRIMARY KEY,
			name ` + d.Text + ` NOT NULL DEFAULT '',
			meter_type ` + d.Key + ` NOT NULL DEFAULT 'counter',
			resource ` + d.Key + ` NOT NULL DEFAULT '',
			unit ` + d.Key + ` NOT NULL DEFAULT '',
			reset_policy ` + d.Key + ` NOT NULL DEFAULT 'hard_reset',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Raw measurements. Counters accumulate, gauges keep the last
		// observation, histograms keep a sum and a sample count.
		`CREATE TABLE IF NOT EXISTS billing_usage_records (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL,
			meter_code ` + d.Key + ` NOT NULL,
			period_start ` + d.Int + ` NOT NULL DEFAULT 0,
			period_end ` + d.Int + ` NOT NULL DEFAULT 0,
			value ` + d.Int + ` NOT NULL DEFAULT 0,
			samples ` + d.Int + ` NOT NULL DEFAULT 0,
			state ` + d.Key + ` NOT NULL DEFAULT 'included',
			dimensions ` + d.Text + ` NOT NULL DEFAULT '',
			recorded_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// One rollup row per tenant, meter and billing period. This is what
		// the invoice generator reads; raw records stay for audit.
		`CREATE TABLE IF NOT EXISTS billing_usage_rollups (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL,
			meter_code ` + d.Key + ` NOT NULL,
			period_start ` + d.Int + ` NOT NULL DEFAULT 0,
			period_end ` + d.Int + ` NOT NULL DEFAULT 0,
			total_value ` + d.Int + ` NOT NULL DEFAULT 0,
			included_value ` + d.Int + ` NOT NULL DEFAULT 0,
			overage_value ` + d.Int + ` NOT NULL DEFAULT 0,
			invoiced ` + d.Int + ` NOT NULL DEFAULT 0,
			rolled_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Invoices are immutable once issued; every later adjustment is a
		// credit note pointing back at the original row.
		`CREATE TABLE IF NOT EXISTS billing_invoices (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL,
			account_id ` + d.Key + ` NOT NULL DEFAULT '',
			subscription_id ` + d.Key + ` NOT NULL DEFAULT '',
			number ` + d.Key + ` NOT NULL DEFAULT '',
			state ` + d.Key + ` NOT NULL DEFAULT 'draft',
			currency ` + d.Key + ` NOT NULL DEFAULT 'USD',
			subtotal_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			discount_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			tax_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			total_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			paid_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			credited_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			tax_rate_bps ` + d.Int + ` NOT NULL DEFAULT 0,
			tax_kind ` + d.Key + ` NOT NULL DEFAULT 'none',
			tax_jurisdiction ` + d.Key + ` NOT NULL DEFAULT '',
			reverse_charge ` + d.Int + ` NOT NULL DEFAULT 0,
			period_start ` + d.Int + ` NOT NULL DEFAULT 0,
			period_end ` + d.Int + ` NOT NULL DEFAULT 0,
			issued_at ` + d.Int + ` NOT NULL DEFAULT 0,
			due_at ` + d.Int + ` NOT NULL DEFAULT 0,
			paid_at ` + d.Int + ` NOT NULL DEFAULT 0,
			voided_at ` + d.Int + ` NOT NULL DEFAULT 0,
			buyer_snapshot ` + d.Text + ` NOT NULL DEFAULT '',
			notes ` + d.Text + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0,
			version ` + d.Int + ` NOT NULL DEFAULT 1
		)`,

		// Invoice line items. quantity_milli is the quantity times 1000 so
		// fractional quantities stay integral.
		`CREATE TABLE IF NOT EXISTS billing_invoice_lines (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			invoice_id ` + d.Key + ` NOT NULL,
			tenant_id ` + d.Key + ` NOT NULL DEFAULT '',
			position ` + d.Int + ` NOT NULL DEFAULT 0,
			kind ` + d.Key + ` NOT NULL DEFAULT 'subscription',
			description ` + d.Text + ` NOT NULL DEFAULT '',
			quantity_milli ` + d.Int + ` NOT NULL DEFAULT 1000,
			unit_price_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			amount_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			tax_rate_bps ` + d.Int + ` NOT NULL DEFAULT 0,
			tax_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			meter_code ` + d.Key + ` NOT NULL DEFAULT '',
			period_start ` + d.Int + ` NOT NULL DEFAULT 0,
			period_end ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Credit notes: the only legal way to adjust an issued invoice.
		`CREATE TABLE IF NOT EXISTS billing_credit_notes (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL,
			invoice_id ` + d.Key + ` NOT NULL,
			number ` + d.Key + ` NOT NULL DEFAULT '',
			currency ` + d.Key + ` NOT NULL DEFAULT 'USD',
			amount_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			tax_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			reason ` + d.Key + ` NOT NULL DEFAULT 'adjustment',
			note ` + d.Text + ` NOT NULL DEFAULT '',
			issued_at ` + d.Int + ` NOT NULL DEFAULT 0,
			created_by ` + d.Key + ` NOT NULL DEFAULT ''
		)`,

		// Monotonic document counters, one row per numbering scope.
		`CREATE TABLE IF NOT EXISTS billing_document_sequences (
			scope ` + d.Key + ` NOT NULL PRIMARY KEY,
			next_value ` + d.Int + ` NOT NULL DEFAULT 1,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Payment provider registry. Every provider is disabled until an
		// admin configures it, and credentials are AES-256-GCM ciphertext.
		`CREATE TABLE IF NOT EXISTS billing_providers (
			name ` + d.Key + ` NOT NULL PRIMARY KEY,
			display_name ` + d.Text + ` NOT NULL DEFAULT '',
			category ` + d.Key + ` NOT NULL DEFAULT '',
			enabled ` + d.Int + ` NOT NULL DEFAULT 0,
			test_mode ` + d.Int + ` NOT NULL DEFAULT 1,
			state ` + d.Key + ` NOT NULL DEFAULT 'unconfigured',
			priority ` + d.Int + ` NOT NULL DEFAULT 100,
			credentials_enc ` + d.Text + ` NOT NULL DEFAULT '',
			credentials_test_enc ` + d.Text + ` NOT NULL DEFAULT '',
			health_state ` + d.Key + ` NOT NULL DEFAULT 'unknown',
			health_detail ` + d.Text + ` NOT NULL DEFAULT '',
			health_checked_at ` + d.Int + ` NOT NULL DEFAULT 0,
			configured_at ` + d.Int + ` NOT NULL DEFAULT 0,
			configured_by ` + d.Key + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0,
			version ` + d.Int + ` NOT NULL DEFAULT 1
		)`,

		// Tokenized payment instruments. provider_token is the provider's own
		// opaque reference; no PAN, no CVV, ever.
		`CREATE TABLE IF NOT EXISTS billing_payment_methods (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL,
			account_id ` + d.Key + ` NOT NULL DEFAULT '',
			provider ` + d.Key + ` NOT NULL,
			provider_token ` + d.Text + ` NOT NULL DEFAULT '',
			provider_customer ` + d.Text + ` NOT NULL DEFAULT '',
			kind ` + d.Key + ` NOT NULL DEFAULT 'card',
			brand ` + d.Key + ` NOT NULL DEFAULT '',
			last4 ` + d.Key + ` NOT NULL DEFAULT '',
			exp_month ` + d.Int + ` NOT NULL DEFAULT 0,
			exp_year ` + d.Int + ` NOT NULL DEFAULT 0,
			holder_name ` + d.Text + ` NOT NULL DEFAULT '',
			country ` + d.Key + ` NOT NULL DEFAULT '',
			is_default ` + d.Int + ` NOT NULL DEFAULT 0,
			state ` + d.Key + ` NOT NULL DEFAULT 'active',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Every charge attempt, successful or not. idempotency_key is unique
		// so a replayed request can never double-charge.
		`CREATE TABLE IF NOT EXISTS billing_payment_attempts (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL,
			account_id ` + d.Key + ` NOT NULL DEFAULT '',
			invoice_id ` + d.Key + ` NOT NULL DEFAULT '',
			method_id ` + d.Key + ` NOT NULL DEFAULT '',
			provider ` + d.Key + ` NOT NULL DEFAULT '',
			idempotency_key ` + d.Key + ` NOT NULL UNIQUE,
			amount_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			currency ` + d.Key + ` NOT NULL DEFAULT 'USD',
			state ` + d.Key + ` NOT NULL DEFAULT 'pending',
			provider_ref ` + d.Text + ` NOT NULL DEFAULT '',
			failure_code ` + d.Key + ` NOT NULL DEFAULT '',
			failure_message ` + d.Text + ` NOT NULL DEFAULT '',
			attempt_number ` + d.Int + ` NOT NULL DEFAULT 1,
			attempted_at ` + d.Int + ` NOT NULL DEFAULT 0,
			completed_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Refunds always carry the credit note they were booked against.
		`CREATE TABLE IF NOT EXISTS billing_refunds (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL,
			invoice_id ` + d.Key + ` NOT NULL DEFAULT '',
			attempt_id ` + d.Key + ` NOT NULL DEFAULT '',
			credit_note_id ` + d.Key + ` NOT NULL DEFAULT '',
			provider ` + d.Key + ` NOT NULL DEFAULT '',
			provider_ref ` + d.Text + ` NOT NULL DEFAULT '',
			amount_minor ` + d.Int + ` NOT NULL DEFAULT 0,
			currency ` + d.Key + ` NOT NULL DEFAULT 'USD',
			kind ` + d.Key + ` NOT NULL DEFAULT 'full',
			state ` + d.Key + ` NOT NULL DEFAULT 'pending',
			reason ` + d.Text + ` NOT NULL DEFAULT '',
			created_at ` + d.Int + ` NOT NULL DEFAULT 0,
			completed_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Webhook idempotency ledger. The primary key is
		// "{provider}:{event_id}" so a replayed delivery collides and is
		// ignored instead of being applied twice.
		`CREATE TABLE IF NOT EXISTS billing_webhook_events (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			provider ` + d.Key + ` NOT NULL DEFAULT '',
			event_id ` + d.Text + ` NOT NULL DEFAULT '',
			event_type ` + d.Key + ` NOT NULL DEFAULT '',
			state ` + d.Key + ` NOT NULL DEFAULT 'received',
			payload_hash ` + d.Key + ` NOT NULL DEFAULT '',
			detail ` + d.Text + ` NOT NULL DEFAULT '',
			received_at ` + d.Int + ` NOT NULL DEFAULT 0,
			processed_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Dunning state for a subscription whose payment failed.
		`CREATE TABLE IF NOT EXISTS billing_dunning (
			subscription_id ` + d.Key + ` NOT NULL PRIMARY KEY,
			tenant_id ` + d.Key + ` NOT NULL DEFAULT '',
			invoice_id ` + d.Key + ` NOT NULL DEFAULT '',
			state ` + d.Key + ` NOT NULL DEFAULT 'idle',
			attempt ` + d.Int + ` NOT NULL DEFAULT 0,
			next_attempt_at ` + d.Int + ` NOT NULL DEFAULT 0,
			started_at ` + d.Int + ` NOT NULL DEFAULT 0,
			last_error ` + d.Text + ` NOT NULL DEFAULT '',
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Append-only financial audit trail. Rows are inserted and never
		// updated or deleted; logging.Audit() receives the same record.
		`CREATE TABLE IF NOT EXISTS billing_audit (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			occurred_at ` + d.Int + ` NOT NULL DEFAULT 0,
			tenant_id ` + d.Key + ` NOT NULL DEFAULT '',
			actor ` + d.Key + ` NOT NULL DEFAULT 'system',
			action ` + d.Key + ` NOT NULL DEFAULT '',
			target ` + d.Text + ` NOT NULL DEFAULT '',
			provider ` + d.Key + ` NOT NULL DEFAULT '',
			result ` + d.Key + ` NOT NULL DEFAULT '',
			code ` + d.Key + ` NOT NULL DEFAULT '',
			ip ` + d.Key + ` NOT NULL DEFAULT '',
			detail ` + d.Text + ` NOT NULL DEFAULT ''
		)`,

		// Operator-maintained tax rate table. Rates are basis points so no
		// rate is stored as a float either.
		`CREATE TABLE IF NOT EXISTS billing_tax_rates (
			id ` + d.Key + ` NOT NULL PRIMARY KEY,
			country ` + d.Key + ` NOT NULL DEFAULT '',
			region ` + d.Key + ` NOT NULL DEFAULT '',
			kind ` + d.Key + ` NOT NULL DEFAULT 'vat',
			name ` + d.Text + ` NOT NULL DEFAULT '',
			rate_bps ` + d.Int + ` NOT NULL DEFAULT 0,
			reverse_charge_b2b ` + d.Int + ` NOT NULL DEFAULT 0,
			active ` + d.Int + ` NOT NULL DEFAULT 1,
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0
		)`,

		// Global billing settings edited through the admin panel.
		`CREATE TABLE IF NOT EXISTS billing_settings (
			setting_key ` + d.Key + ` NOT NULL PRIMARY KEY,
			setting_value ` + d.Text + ` NOT NULL DEFAULT '',
			updated_at ` + d.Int + ` NOT NULL DEFAULT 0,
			updated_by ` + d.Key + ` NOT NULL DEFAULT ''
		)`,
	}

	return append(stmts,
		database.CreateIndex(driver, "idx_billing_plan_quotas_plan", "billing_plan_quotas", "plan_id"),
		database.CreateIndex(driver, "idx_billing_subs_tenant", "billing_subscriptions", "tenant_id"),
		database.CreateIndex(driver, "idx_billing_subs_state", "billing_subscriptions", "state"),
		database.CreateIndex(driver, "idx_billing_subs_period_end", "billing_subscriptions", "period_end"),
		database.CreateIndex(driver, "idx_billing_sub_events_sub", "billing_subscription_events", "subscription_id"),
		database.CreateIndex(driver, "idx_billing_overrides_tenant", "billing_quota_overrides", "tenant_id"),
		database.CreateIndex(driver, "idx_billing_usage_tenant", "billing_usage_records", "tenant_id", "meter_code"),
		database.CreateIndex(driver, "idx_billing_usage_period", "billing_usage_records", "period_start"),
		database.CreateIndex(driver, "idx_billing_rollups_tenant", "billing_usage_rollups", "tenant_id", "meter_code"),
		database.CreateIndex(driver, "idx_billing_invoices_tenant", "billing_invoices", "tenant_id"),
		database.CreateIndex(driver, "idx_billing_invoices_state", "billing_invoices", "state"),
		database.CreateIndex(driver, "idx_billing_invoices_number", "billing_invoices", "number"),
		database.CreateIndex(driver, "idx_billing_lines_invoice", "billing_invoice_lines", "invoice_id"),
		database.CreateIndex(driver, "idx_billing_credits_invoice", "billing_credit_notes", "invoice_id"),
		database.CreateIndex(driver, "idx_billing_methods_tenant", "billing_payment_methods", "tenant_id"),
		database.CreateIndex(driver, "idx_billing_attempts_tenant", "billing_payment_attempts", "tenant_id"),
		database.CreateIndex(driver, "idx_billing_attempts_invoice", "billing_payment_attempts", "invoice_id"),
		database.CreateIndex(driver, "idx_billing_refunds_tenant", "billing_refunds", "tenant_id"),
		database.CreateIndex(driver, "idx_billing_dunning_next", "billing_dunning", "next_attempt_at"),
		database.CreateIndex(driver, "idx_billing_audit_time", "billing_audit", "occurred_at"),
		database.CreateIndex(driver, "idx_billing_audit_tenant", "billing_audit", "tenant_id"),
		database.CreateIndex(driver, "idx_billing_tax_country", "billing_tax_rates", "country"),
	)
}
