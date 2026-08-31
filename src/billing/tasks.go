package billing

import (
	"context"

	"github.com/webappsgo/cashp/src/scheduler"
)

// Scheduled task names. They are stable identifiers because the scheduler
// persists per-task enabled state and run history against them.
const (
	TaskRenewals      = "billing_renewals"
	TaskDunning       = "billing_dunning"
	TaskReminders     = "billing_reminders"
	TaskProviderCheck = "billing_provider_health"
	TaskReconcile     = "billing_reconciliation"
	TaskWebhookRetry  = "billing_webhook_retry"
)

// Tasks returns every background pass the billing subsystem needs, for the
// server to hand to the built-in scheduler. Each one is cluster-wide, because
// charging a card twice because two nodes both woke up is not recoverable, and
// each one returns immediately when billing is switched off, so a fresh
// install runs them as no-ops rather than needing them disabled by hand.
func (s *Service) Tasks() []scheduler.Task {
	return []scheduler.Task{
		{
			Name:        TaskRenewals,
			Title:       "Billing renewals",
			Description: "Raises the next invoice for every subscription whose period has ended and attempts the charge.",
			Schedule:    "0 * * * *",
			CatchUp:     true,
			ClusterWide: true,
			Run:         s.RunRenewals,
		},
		{
			Name:        TaskDunning,
			Title:       "Billing dunning",
			Description: "Retries unpaid invoices on the configured schedule and suspends only after the grace period has run out.",
			Schedule:    "0 3 * * *",
			CatchUp:     true,
			ClusterWide: true,
			Run:         s.RunDunning,
		},
		{
			Name:        TaskReminders,
			Title:       "Billing reminders",
			Description: "Warns tenants of an upcoming renewal charge and of a trial that is about to end.",
			Schedule:    "0 9 * * *",
			CatchUp:     false,
			ClusterWide: true,
			Run:         s.SendRenewalReminders,
		},
		{
			Name:        TaskProviderCheck,
			Title:       "Payment provider health",
			Description: "Validates the credentials of every enabled payment provider. Disabled providers are never contacted.",
			Schedule:    "@every 15m",
			CatchUp:     false,
			ClusterWide: true,
			Run:         s.CheckProviderHealth,
		},
		{
			Name:        TaskReconcile,
			Title:       "Billing reconciliation",
			Description: "Compares the previous day's charges against each enabled provider's own record and reports any disagreement.",
			Schedule:    "0 2 * * *",
			CatchUp:     true,
			ClusterWide: true,
			Run:         s.RunReconciliation,
		},
		{
			Name:        TaskWebhookRetry,
			Title:       "Billing webhook retry",
			Description: "Re-reads provider state for webhooks that failed to process, and dead-letters the ones that keep failing.",
			Schedule:    "@every 10m",
			CatchUp:     false,
			ClusterWide: true,
			Run:         s.RetryFailedWebhooks,
		},
	}
}

// RegisterTasks registers every billing task with a scheduler. It is the one
// call the server makes; the task list itself stays private to this package so
// a new pass never needs a change outside it.
func (s *Service) RegisterTasks(sched *scheduler.Scheduler) error {
	if sched == nil {
		return ErrValidation("billing: a scheduler is required")
	}
	for _, task := range s.Tasks() {
		if err := sched.Register(task); err != nil {
			return err
		}
	}
	return nil
}

// Bootstrap prepares a fresh database for billing. It seeds the meters the
// quota engine measures against and nothing else: no plan is created, no
// provider is enabled and billing itself stays switched off until an operator
// turns it on, which is what makes first run work with no configuration.
func (s *Service) Bootstrap(ctx context.Context) error {
	return s.EnsureDefaultMeters(ctx)
}
