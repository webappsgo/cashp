package billing

import (
	"fmt"
	"sort"
	"time"
)

// Subscription states, spelled exactly as they are persisted.
const (
	StatePendingActivation = "pending_activation"
	StateTrialing          = "trialing"
	StateActive            = "active"
	StatePastDue           = "past_due"
	StatePaused            = "paused"
	StateCancelled         = "cancelled"
	StateExpired           = "expired"
)

// Subscription lifecycle events. Each names exactly one legal transition out
// of a given state; nothing else may move a subscription.
const (
	EventSubscriptionCreated = "subscription_created"
	EventTrialStarted        = "trial_started"
	EventTrialConverted      = "trial_converted"
	EventTrialExpired        = "trial_expired"
	EventPaymentAdded        = "payment_method_added"
	EventPaymentFailed       = "payment_failed"
	EventPaymentRecovered    = "payment_recovered"
	EventRenewed             = "renewed"
	EventUserPaused          = "user_paused"
	EventResumed             = "resumed"
	EventCancelled           = "cancelled"
	EventGraceExpired        = "grace_expired"
	EventAutoExpired         = "auto_expired"
	EventPeriodEnded         = "period_ended"
	EventPlanChanged         = "subscription_modified"
)

// NextSubscriptionState resolves the state a subscription moves to for an
// event. The switch below is the complete, authoritative edge set: a move it
// does not name is rejected, so no code path can invent a state change.
func NextSubscriptionState(from, event string) (string, error) {
	switch from {
	case StatePendingActivation:
		switch event {
		case EventPaymentAdded:
			return StateActive, nil
		case EventTrialStarted:
			return StateTrialing, nil
		case EventCancelled:
			return StateCancelled, nil
		}
	case StateTrialing:
		switch event {
		case EventTrialConverted:
			return StateActive, nil
		case EventTrialExpired:
			return StateExpired, nil
		case EventCancelled:
			return StateCancelled, nil
		}
	case StateActive:
		switch event {
		case EventPaymentFailed:
			return StatePastDue, nil
		case EventUserPaused:
			return StatePaused, nil
		case EventCancelled:
			return StateCancelled, nil
		case EventRenewed, EventPlanChanged:
			return StateActive, nil
		}
	case StatePastDue:
		switch event {
		case EventPaymentRecovered:
			return StateActive, nil
		case EventGraceExpired:
			return StateExpired, nil
		case EventCancelled:
			return StateCancelled, nil
		}
	case StatePaused:
		switch event {
		case EventResumed:
			return StateActive, nil
		case EventCancelled:
			return StateCancelled, nil
		case EventAutoExpired:
			return StateExpired, nil
		}
	case StateCancelled:
		if event == EventPeriodEnded {
			return StateExpired, nil
		}
	}
	return "", fmt.Errorf("billing: %q is not a legal event in state %q", event, from)
}

// SubscriptionStates lists every state, sorted, for validation and UI.
func SubscriptionStates() []string {
	return []string{
		StateActive, StateCancelled, StateExpired, StatePastDue,
		StatePaused, StatePendingActivation, StateTrialing,
	}
}

// ValidSubscriptionState reports whether a name is a known subscription
// state.
func ValidSubscriptionState(state string) bool {
	for _, s := range SubscriptionStates() {
		if s == state {
			return true
		}
	}
	return false
}

// Invoice states.
const (
	InvoiceDraft      = "draft"
	InvoiceIssued     = "issued"
	InvoiceDue        = "due"
	InvoiceOverdue    = "overdue"
	InvoiceProcessing = "processing"
	InvoicePartial    = "partial"
	InvoicePaid       = "paid"
	InvoiceDisputed   = "disputed"
	InvoiceCancelled  = "cancelled"
	InvoiceRefunded   = "refunded"
)

// CanTransitionInvoice reports whether an invoice may move between two
// states. No edge leads back into draft: once issued, an invoice's figures
// are frozen and every correction is a credit note instead.
func CanTransitionInvoice(from, to string) bool {
	var allowed []string
	switch from {
	case InvoiceDraft:
		allowed = []string{InvoiceIssued, InvoiceCancelled}
	case InvoiceIssued:
		allowed = []string{InvoiceDue, InvoiceProcessing, InvoiceDisputed, InvoiceCancelled}
	case InvoiceDue:
		allowed = []string{InvoiceOverdue, InvoiceProcessing, InvoicePartial, InvoiceDisputed, InvoiceCancelled}
	case InvoiceOverdue:
		allowed = []string{InvoiceProcessing, InvoicePartial, InvoiceDisputed, InvoiceCancelled}
	case InvoiceProcessing:
		allowed = []string{InvoicePaid, InvoicePartial, InvoiceDue, InvoiceDisputed}
	case InvoicePartial:
		allowed = []string{InvoiceProcessing, InvoicePaid, InvoiceOverdue, InvoiceDisputed, InvoiceCancelled}
	case InvoicePaid:
		allowed = []string{InvoiceRefunded, InvoiceDisputed}
	case InvoiceDisputed:
		allowed = []string{InvoicePaid, InvoiceRefunded, InvoiceCancelled, InvoiceDue}
	}
	for _, state := range allowed {
		if state == to {
			return true
		}
	}
	return false
}

// InvoiceFrozen reports whether an invoice's monetary figures and line items
// are immutable. Everything past draft is frozen.
func InvoiceFrozen(state string) bool {
	return state != InvoiceDraft
}

// Billing cycles.
const (
	CycleMonthly    = "monthly"
	CycleQuarterly  = "quarterly"
	CycleSemiAnnual = "semi_annual"
	CycleAnnual     = "annual"
	CycleBiennial   = "biennial"
	CycleTriennial  = "triennial"
	CycleLifetime   = "lifetime"
)

// CycleMonths returns the whole-month length of a cycle. Lifetime has no
// length and returns zero; an unknown cycle returns minus one.
func CycleMonths(cycle string) int {
	switch cycle {
	case CycleMonthly:
		return 1
	case CycleQuarterly:
		return 3
	case CycleSemiAnnual:
		return 6
	case CycleAnnual:
		return 12
	case CycleBiennial:
		return 24
	case CycleTriennial:
		return 36
	case CycleLifetime:
		return 0
	}
	return -1
}

// ValidCycle reports whether a cycle name is one this package bills.
func ValidCycle(cycle string) bool {
	return CycleMonths(cycle) >= 0
}

// AdvancePeriod returns the end of the billing period that starts at the
// given instant. Calendar month arithmetic is used so a period beginning on
// the 31st lands on the last day of a shorter month rather than spilling into
// the following one, and so leap years bill correctly. A lifetime or unknown
// cycle returns the zero time, meaning "never renews".
func AdvancePeriod(start time.Time, cycle string) time.Time {
	months := CycleMonths(cycle)
	if months <= 0 {
		return time.Time{}
	}
	start = start.UTC()
	y, m, d := start.Date()
	target := time.Date(y, m+time.Month(months), 1, start.Hour(), start.Minute(), start.Second(), 0, time.UTC)
	if last := daysInMonth(target.Year(), target.Month()); d > last {
		d = last
	}
	return time.Date(target.Year(), target.Month(), d, start.Hour(), start.Minute(), start.Second(), 0, time.UTC)
}

// daysInMonth returns the length of a calendar month, leap years included.
func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// Plan visibility levels.
const (
	VisibilityPublic        = "public"
	VisibilityAuthenticated = "authenticated"
	VisibilityInternal      = "internal"
	VisibilityHidden        = "hidden"
	VisibilityDeprecated    = "deprecated"
)

// ValidVisibility reports whether a visibility level is known.
func ValidVisibility(v string) bool {
	switch v {
	case VisibilityPublic, VisibilityAuthenticated, VisibilityInternal, VisibilityHidden, VisibilityDeprecated:
		return true
	}
	return false
}

// Quota enforcement strategies.
const (
	// EnforceHard denies the provisioning request at the ceiling.
	EnforceHard = "hard"
	// EnforceSoft allows the request and records billable overage.
	EnforceSoft = "soft"
	// EnforceBurst allows the request while burst headroom remains.
	EnforceBurst = "burst"
)

// ValidEnforcement reports whether an enforcement strategy is known.
func ValidEnforcement(e string) bool {
	switch e {
	case EnforceHard, EnforceSoft, EnforceBurst:
		return true
	}
	return false
}

// Overage policies applied when a soft ceiling is passed.
const (
	OverageBlock            = "block"
	OverageAllowWithCharge  = "allow_with_charge"
	OverageAllowWithWarning = "allow_with_warning"
	OverageThrottle         = "throttle"
)

// Unlimited is the sentinel ceiling meaning "no limit at all".
const Unlimited int64 = -1

// Meter types and reset policies.
const (
	MeterCounter   = "counter"
	MeterGauge     = "gauge"
	MeterHistogram = "histogram"

	ResetHard       = "hard_reset"
	ResetRolling    = "rolling_window"
	ResetCarry      = "carry_forward"
	ResetAccumulate = "accumulating"
)

// Usage record states.
const (
	UsageIncluded = "included"
	UsageOverage  = "overage"
	UsageExempt   = "exempt"
)

// Payment attempt states.
const (
	PaymentPending    = "pending"
	PaymentAuthorized = "authorized"
	PaymentSucceeded  = "succeeded"
	PaymentFailed     = "failed"
	PaymentVoided     = "voided"
	PaymentRefunded   = "refunded"
)

// Refund kinds.
const (
	RefundFull      = "full"
	RefundPartial   = "partial"
	RefundCredit    = "credit"
	RefundProration = "proration"
)

// Dunning states.
const (
	DunningIdle      = "idle"
	DunningRetrying  = "retrying"
	DunningExhausted = "exhausted"
	DunningRecovered = "recovered"
)

// Inbound webhook delivery states. A delivery is recorded before it is acted
// on, so a replay of the same provider event is recognised and dropped rather
// than applied a second time.
const (
	WebhookReceived   = "received"
	WebhookProcessed  = "processed"
	WebhookIgnored    = "ignored"
	WebhookRejected   = "rejected"
	WebhookFailed     = "failed"
	WebhookDeadLetter = "dead_letter"
)

// WebhookMaxAttempts is how many times a delivery may fail processing before
// it is parked in the dead letter state for an administrator to look at.
const WebhookMaxAttempts = 5

// Provider lifecycle states.
const (
	ProviderUnconfigured = "unconfigured"
	ProviderTesting      = "testing"
	ProviderActive       = "active"
	ProviderDegraded     = "degraded"
	ProviderFailed       = "failed"
	ProviderDisabled     = "disabled"
)

// Provider health states.
const (
	HealthUnknown   = "unknown"
	HealthHealthy   = "healthy"
	HealthDegraded  = "degraded"
	HealthUnhealthy = "unhealthy"
)

// Quota resources. These are the only things a billing plan may cap, and
// they are all quantitative: cashp ships every product feature to every
// tenant on every tier, so no plan can switch a capability off, only bound
// how much of it is consumed (IDEA.md, "Non-goals" and "Constraints").
const (
	ResourceSites       = "sites"
	ResourceDomains     = "domains"
	ResourceApps        = "apps"
	ResourceContainers  = "containers"
	ResourceVMs         = "vms"
	ResourceDatabases   = "databases"
	ResourceMailboxes   = "mailboxes"
	ResourceDNSZones    = "dns_zones"
	ResourceUsers       = "users"
	ResourceBackupJobs  = "backup_jobs"
	ResourceStorageGB   = "storage_gb"
	ResourceBandwidthGB = "bandwidth_gb"
	ResourceBackupGB    = "backup_gb"
	ResourceCPUCores    = "cpu_cores"
	ResourceMemoryMB    = "memory_mb"
)

// resourceUnits names the unit each resource is counted in, for display.
var resourceUnits = map[string]string{
	ResourceApps:        "apps",
	ResourceBackupGB:    "GB",
	ResourceBackupJobs:  "jobs",
	ResourceBandwidthGB: "GB",
	ResourceCPUCores:    "cores",
	ResourceContainers:  "containers",
	ResourceDNSZones:    "zones",
	ResourceDatabases:   "databases",
	ResourceDomains:     "domains",
	ResourceMailboxes:   "mailboxes",
	ResourceMemoryMB:    "MB",
	ResourceSites:       "sites",
	ResourceStorageGB:   "GB",
	ResourceUsers:       "users",
	ResourceVMs:         "VMs",
}

// Resources lists every quota-governed resource, sorted for stable output.
func Resources() []string {
	out := make([]string, 0, len(resourceUnits))
	for name := range resourceUnits {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ValidResource reports whether a name is a known quota resource.
func ValidResource(name string) bool {
	_, ok := resourceUnits[name]
	return ok
}

// ResourceUnit returns the display unit for a resource.
func ResourceUnit(name string) string {
	return resourceUnits[name]
}
