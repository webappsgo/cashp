package billing

import "context"

// Billing notification events. The names are stable strings so an operator can
// route or silence any one of them without a code change, and so a
// notification subsystem can template them independently of this package.
const (
	// NotifyPaymentFailed is sent when a charge fails and a retry is queued.
	NotifyPaymentFailed = "billing.payment.failed"
	// NotifyPaymentSucceeded confirms a settled charge.
	NotifyPaymentSucceeded = "billing.payment.succeeded"
	// NotifyTrialEnding warns that a trial is about to end.
	NotifyTrialEnding = "billing.trial.ending"
	// NotifyRenewalUpcoming warns that a renewal charge is about to be taken.
	NotifyRenewalUpcoming = "billing.renewal.upcoming"
	// NotifySuspended reports service suspension after the grace period.
	NotifySuspended = "billing.subscription.suspended"
	// NotifyCancelled confirms a cancellation.
	NotifyCancelled = "billing.subscription.cancelled"
	// NotifyInvoiceIssued announces a new invoice.
	NotifyInvoiceIssued = "billing.invoice.issued"
	// NotifyGraceStarted reports that a payment failed and the tenant is in a
	// grace period rather than suspended.
	NotifyGraceStarted = "billing.grace.period.started"
	// NotifyQuotaWarning reports a soft ceiling crossed with overage tracked.
	NotifyQuotaWarning = "billing.quota.warning"
	// NotifyCreditIssued announces a credit note.
	NotifyCreditIssued = "billing.credit.issued"
	// NotifyRefundIssued confirms money returned to the payer.
	NotifyRefundIssued = "billing.refund.issued"
	// NotifyExportReady tells a tenant their billing data export is ready.
	NotifyExportReady = "billing.export.ready"
	// NotifyReconcileAlert warns the operator that a provider's books and
	// cashp's disagree. It carries no tenant, because it is about the install.
	NotifyReconcileAlert = "billing.reconciliation.alert"
)

// NotifyEvents lists every event this package can emit, so an administration
// page can render the routing matrix without hardcoding the list.
func NotifyEvents() []string {
	return []string{
		NotifyPaymentFailed,
		NotifyPaymentSucceeded,
		NotifyTrialEnding,
		NotifyRenewalUpcoming,
		NotifySuspended,
		NotifyCancelled,
		NotifyInvoiceIssued,
		NotifyGraceStarted,
		NotifyQuotaWarning,
		NotifyCreditIssued,
		NotifyRefundIssued,
		NotifyExportReady,
		NotifyReconcileAlert,
	}
}

// NotifyTenant sends one billing notification on behalf of another package.
// It never returns an error, because a notification failure must not undo a
// financial operation that already succeeded.
func (s *Service) NotifyTenant(ctx context.Context, tenantID, event string, data map[string]any) {
	s.notify(ctx, tenantID, event, data)
}
