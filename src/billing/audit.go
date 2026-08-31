package billing

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/logging"
)

// The billing audit trail is append-only. This file is the only place that
// writes to billing_audit and it contains no UPDATE and no DELETE, which is
// what makes the guarantee structural rather than a matter of discipline.

// Audit results.
const (
	ResultSuccess = "success"
	ResultFailure = "failure"
	ResultDenied  = "denied"
)

// Audit action names. Every financial operation in this package records one.
const (
	ActionAccountUpdated      = "billing.account.updated"
	ActionPlanCreated         = "billing.plan.created"
	ActionPlanUpdated         = "billing.plan.updated"
	ActionSubscriptionCreated = "billing.subscription.created"
	ActionSubscriptionChanged = "billing.subscription.changed"
	ActionSubscriptionCancel  = "billing.subscription.cancelled"
	ActionSubscriptionResumed = "billing.subscription.resumed"
	ActionInvoiceIssued       = "billing.invoice.issued"
	ActionInvoicePaid         = "billing.invoice.paid"
	ActionCreditNoteIssued    = "billing.credit_note.issued"
	ActionPaymentAttempted    = "billing.payment.attempted"
	ActionPaymentSucceeded    = "billing.payment.succeeded"
	ActionPaymentFailed       = "billing.payment.failed"
	ActionRefundIssued        = "billing.refund.issued"
	ActionMethodAdded         = "billing.payment_method.added"
	ActionMethodRemoved       = "billing.payment_method.removed"
	ActionProviderConfigured  = "billing.provider.configured"
	ActionProviderEnabled     = "billing.provider.enabled"
	ActionProviderDisabled    = "billing.provider.disabled"
	ActionProviderTested      = "billing.provider.tested"
	ActionWebhookReceived     = "billing.webhook.received"
	ActionWebhookRejected     = "billing.webhook.rejected"
	ActionQuotaDenied         = "billing.quota.denied"
	ActionQuotaOverride       = "billing.quota.override"
	ActionDunningStarted      = "billing.dunning.started"
	ActionDunningExhausted    = "billing.dunning.exhausted"
	ActionDataExported        = "billing.data.exported"
	ActionSettingChanged      = "billing.setting.changed"
)

// Audit actions raised by the background passes.
const (
	ActionSubscriptionSuspended = "billing.subscription.suspended"
	ActionRenewalRun            = "billing.renewal.run"
	ActionDunningRun            = "billing.dunning.run"
	ActionReconciled            = "billing.reconciliation.run"
	ActionReconcileMismatch     = "billing.reconciliation.discrepancy"
	ActionProviderHealth        = "billing.provider.health"
	ActionExportRequested       = "billing.data.export_requested"
)

// Well-known audit actors. A financial entry always names who caused it, and
// an unattended pass names itself rather than borrowing a user's identity.
const (
	ActorSystem         = "system"
	ActorReconciliation = "reconciliation-job"
)

// AuditRecord is one entry to append.
type AuditRecord struct {
	TenantID string
	Actor    string
	Action   string
	Target   string
	Provider string
	Result   string
	Code     string
	IP       string
	Detail   string
}

// WriteAudit appends one financial audit entry. It writes both the queryable
// table and the append-only JSON audit log so the two agree. An audit write
// never fails the caller's operation: a lost audit line is logged loudly
// rather than rolling back money that has already moved.
func (s *Service) WriteAudit(ctx context.Context, rec AuditRecord) {
	if rec.Actor == "" {
		rec.Actor = "system"
	}
	if rec.Result == "" {
		rec.Result = ResultSuccess
	}
	entry := AuditEntry{
		ID:         newID(),
		OccurredAt: s.unix(),
		TenantID:   rec.TenantID,
		Actor:      rec.Actor,
		Action:     rec.Action,
		Target:     rec.Target,
		Provider:   rec.Provider,
		Result:     rec.Result,
		Code:       rec.Code,
		IP:         rec.IP,
		Detail:     logging.MaskSecret(rec.Detail),
	}
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO billing_audit
		 (id, occurred_at, tenant_id, actor, action, target, provider, result, code, ip, detail)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.OccurredAt, entry.TenantID, entry.Actor, entry.Action,
		entry.Target, entry.Provider, entry.Result, entry.Code, entry.IP, entry.Detail)
	if err != nil {
		logging.L().Error("billing audit write failed",
			"action", entry.Action, "tenant_id", entry.TenantID, "error", err.Error())
	}
	logging.Audit().Info(entry.Action,
		"tenant_id", entry.TenantID,
		"actor", entry.Actor,
		"target", entry.Target,
		"provider", entry.Provider,
		"result", entry.Result,
		"code", entry.Code,
		"ip", entry.IP,
		"detail", entry.Detail)
}

// ListAudit returns audit entries for one tenant, newest first. Passing an
// empty tenant is a server-wide read and is only reachable from the admin
// routes, which are already gated on the global administrator role.
func (s *Service) ListAudit(ctx context.Context, tenantID string, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT id, occurred_at, tenant_id, actor, action, target, provider,
	                 result, code, ip, detail
	          FROM billing_audit`
	args := []any{}
	if tenantID != "" {
		query += ` WHERE tenant_id = ?`
		args = append(args, tenantID)
	}
	query += ` ORDER BY occurred_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect, query, args...)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the billing audit trail.")
	}
	defer func() { _ = rows.Close() }()

	out := make([]AuditEntry, 0, limit)
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.OccurredAt, &e.TenantID, &e.Actor, &e.Action,
			&e.Target, &e.Provider, &e.Result, &e.Code, &e.IP, &e.Detail); err != nil {
			return nil, ErrInternal(err, "Could not read the billing audit trail.")
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the billing audit trail.")
	}
	return out, nil
}

// ClientIP extracts the caller address for an audit entry. Only the socket
// peer is trusted: a forwarded header is attacker-controlled unless a proxy
// the operator configured set it, and billing never needs to believe one.
func ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}
