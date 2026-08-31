package billing

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

// subscriptionColumns is the explicit column list for billing_subscriptions.
const subscriptionColumns = `id, tenant_id, account_id, plan_id, state, cycle,
	currency, price_minor, quantity, trial_ends_at, period_start, period_end,
	grace_ends_at, cancel_at_period_end, cancelled_at, ended_at,
	pending_plan_id, pending_effective_at, provider, provider_ref,
	created_at, updated_at, version`

// scanSubscription reads one row in subscriptionColumns order.
func scanSubscription(sc interface{ Scan(...any) error }) (Subscription, error) {
	var s Subscription
	var cancelAtEnd int64
	if err := sc.Scan(&s.ID, &s.TenantID, &s.AccountID, &s.PlanID, &s.State,
		&s.Cycle, &s.Currency, &s.PriceMinor, &s.Quantity, &s.TrialEndsAt,
		&s.PeriodStart, &s.PeriodEnd, &s.GraceEndsAt, &cancelAtEnd,
		&s.CancelledAt, &s.EndedAt, &s.PendingPlanID, &s.PendingEffectiveAt,
		&s.Provider, &s.ProviderRef, &s.CreatedAt, &s.UpdatedAt,
		&s.Version); err != nil {
		return Subscription{}, err
	}
	s.CancelAtPeriodEnd = cancelAtEnd != 0
	return s, nil
}

// Subscription returns one subscription belonging to a tenant. The tenant is
// always in the predicate, so an identifier belonging to another tenant reads
// as not found rather than leaking that it exists.
func (s *Service) Subscription(ctx context.Context, tenantID, subID string) (Subscription, error) {
	if strings.TrimSpace(tenantID) == "" {
		return Subscription{}, ErrValidation("billing: a tenant is required")
	}
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+subscriptionColumns+` FROM billing_subscriptions
		 WHERE id = ? AND tenant_id = ?`, subID, tenantID)
	sub, err := scanSubscription(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Subscription{}, ErrNotFound("subscription")
	}
	if err != nil {
		return Subscription{}, ErrInternal(err, "Could not read the subscription.")
	}
	return sub, nil
}

// ActiveSubscription returns the subscription that currently entitles a
// tenant to its plan quotas, if any.
func (s *Service) ActiveSubscription(ctx context.Context, tenantID string) (Subscription, error) {
	if strings.TrimSpace(tenantID) == "" {
		return Subscription{}, ErrValidation("billing: a tenant is required")
	}
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+subscriptionColumns+` FROM billing_subscriptions
		 WHERE tenant_id = ? AND state IN (?, ?, ?, ?)
		 ORDER BY created_at DESC`,
		tenantID, StateActive, StateTrialing, StatePastDue, StateCancelled)
	if err != nil {
		return Subscription{}, ErrInternal(err, "Could not read the subscription.")
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		sub, sErr := scanSubscription(rows)
		if sErr != nil {
			return Subscription{}, ErrInternal(sErr, "Could not read the subscription.")
		}
		if sub.Active() {
			return sub, nil
		}
	}
	if err := rows.Err(); err != nil {
		return Subscription{}, ErrInternal(err, "Could not read the subscription.")
	}
	return Subscription{}, ErrNotFound("subscription")
}

// ListSubscriptions returns every subscription a tenant has ever had.
func (s *Service) ListSubscriptions(ctx context.Context, tenantID string) ([]Subscription, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+subscriptionColumns+` FROM billing_subscriptions
		 WHERE tenant_id = ? ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the subscriptions.")
	}
	defer func() { _ = rows.Close() }()

	out := []Subscription{}
	for rows.Next() {
		sub, sErr := scanSubscription(rows)
		if sErr != nil {
			return nil, ErrInternal(sErr, "Could not read the subscriptions.")
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the subscriptions.")
	}
	return out, nil
}

// Subscribe puts a tenant on a plan. A plan with a trial starts trialing; a
// free plan activates immediately; anything else waits for a payment method
// and stays pending until one is added or an invoice is settled.
func (s *Service) Subscribe(ctx context.Context, tenantID, planID, actor, ip string) (Subscription, error) {
	if _, err := s.ActiveSubscription(ctx, tenantID); err == nil {
		return Subscription{}, ErrConflict("This tenant already has an active subscription; change the plan instead.")
	} else if !isNotFound(err) {
		return Subscription{}, err
	}
	plan, err := s.Plan(ctx, planID)
	if err != nil {
		return Subscription{}, err
	}
	if !plan.Active {
		return Subscription{}, ErrValidation("That plan is not open for new subscriptions.")
	}
	account, err := s.EnsureAccount(ctx, tenantID)
	if err != nil {
		return Subscription{}, err
	}

	now := s.Now()
	sub := Subscription{
		ID:          newID(),
		TenantID:    tenantID,
		AccountID:   account.ID,
		PlanID:      plan.ID,
		State:       StatePendingActivation,
		Cycle:       plan.Cycle,
		Currency:    plan.Currency,
		PriceMinor:  plan.PriceMinor,
		Quantity:    1,
		PeriodStart: now.Unix(),
		CreatedAt:   now.Unix(),
		UpdatedAt:   now.Unix(),
		Version:     1,
	}
	periodEnd := AdvancePeriod(now, plan.Cycle)
	if !periodEnd.IsZero() {
		sub.PeriodEnd = periodEnd.Unix()
	}
	event := EventPaymentAdded
	switch {
	case plan.TrialDays > 0:
		sub.State = StateTrialing
		sub.TrialEndsAt = now.AddDate(0, 0, int(plan.TrialDays)).Unix()
		event = EventTrialStarted
	case plan.PriceMinor == 0:
		sub.State = StateActive
	default:
		sub.State = StatePendingActivation
		event = EventSubscriptionCreated
	}

	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO billing_subscriptions
		 (id, tenant_id, account_id, plan_id, state, cycle, currency, price_minor,
		  quantity, trial_ends_at, period_start, period_end, created_at,
		  updated_at, version)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, 1)`,
		sub.ID, sub.TenantID, sub.AccountID, sub.PlanID, sub.State, sub.Cycle,
		sub.Currency, sub.PriceMinor, sub.TrialEndsAt, sub.PeriodStart,
		sub.PeriodEnd, sub.CreatedAt, sub.UpdatedAt); err != nil {
		return Subscription{}, ErrInternal(err, "Could not create the subscription.")
	}

	s.recordSubscriptionEvent(ctx, sub, event, "", sub.State, actor, "plan="+plan.Code)
	s.WriteAudit(ctx, AuditRecord{
		TenantID: tenantID, Actor: actor, Action: ActionSubscriptionCreated,
		Target: "subscription:" + sub.ID, IP: ip, Detail: "plan=" + plan.Code,
	})
	if sub.State == StateTrialing {
		s.notify(ctx, tenantID, NotifyTrialEnding, map[string]any{
			"plan":          plan.Name,
			"trial_ends_at": sub.TrialEndsAt,
		})
	}
	return sub, nil
}

// ProrationPreview is what a plan change would cost right now, shown to the
// tenant before they confirm it.
type ProrationPreview struct {
	FromPlanID   string `json:"from_plan_id"`
	ToPlanID     string `json:"to_plan_id"`
	Currency     string `json:"currency"`
	CreditMinor  int64  `json:"credit_minor"`
	ChargeMinor  int64  `json:"charge_minor"`
	DueNowMinor  int64  `json:"due_now_minor"`
	RefundMinor  int64  `json:"refund_minor"`
	Immediate    bool   `json:"immediate"`
	EffectiveAt  int64  `json:"effective_at"`
	RemainingSec int64  `json:"remaining_seconds"`
	PeriodSec    int64  `json:"period_seconds"`
}

// PreviewPlanChange computes the proration for moving a tenant to another
// plan without changing anything. An upgrade is charged for the unused part
// of the period; a downgrade is credited for it and takes effect at the
// period end so the tenant keeps what they already paid for.
func (s *Service) PreviewPlanChange(ctx context.Context, tenantID, toPlanID string) (ProrationPreview, error) {
	sub, err := s.ActiveSubscription(ctx, tenantID)
	if err != nil {
		return ProrationPreview{}, err
	}
	target, err := s.Plan(ctx, toPlanID)
	if err != nil {
		return ProrationPreview{}, err
	}
	if target.ID == sub.PlanID {
		return ProrationPreview{}, ErrValidation("The tenant is already on that plan.")
	}
	if target.Currency != sub.Currency {
		return ProrationPreview{}, ErrValidation("A plan change cannot cross currencies.")
	}
	if !target.Active {
		return ProrationPreview{}, ErrValidation("That plan is not open for new subscriptions.")
	}
	return s.prorate(sub, target, s.unix()), nil
}

// prorate is the pure proration calculation, in integer minor units
// throughout. Credit and charge use the same rounding rule so an upgrade and
// the downgrade that undoes it agree to the cent.
func (s *Service) prorate(sub Subscription, target Plan, now int64) ProrationPreview {
	preview := ProrationPreview{
		FromPlanID:  sub.PlanID,
		ToPlanID:    target.ID,
		Currency:    sub.Currency,
		EffectiveAt: now,
	}
	period := sub.PeriodEnd - sub.PeriodStart
	remaining := sub.PeriodEnd - now
	if remaining < 0 {
		remaining = 0
	}
	preview.PeriodSec = period
	preview.RemainingSec = remaining

	upgrade := target.PriceMinor > sub.PriceMinor
	if !upgrade {
		// A downgrade never takes money back mid-period and never removes
		// capability early: it lands at the period boundary.
		preview.Immediate = false
		preview.EffectiveAt = sub.PeriodEnd
		return preview
	}
	if period <= 0 || remaining == 0 {
		preview.Immediate = true
		preview.ChargeMinor = target.PriceMinor
		preview.DueNowMinor = target.PriceMinor
		return preview
	}

	preview.Immediate = true
	preview.CreditMinor = Prorate(sub.PriceMinor, remaining, period)
	preview.ChargeMinor = Prorate(target.PriceMinor, remaining, period)
	delta := preview.ChargeMinor - preview.CreditMinor
	if delta >= 0 {
		preview.DueNowMinor = delta
	} else {
		preview.RefundMinor = -delta
	}
	return preview
}

// ChangePlan moves a tenant between plans. An upgrade applies at once and
// raises a prorated invoice; a downgrade is scheduled for the period end.
// Neither ever changes which features the tenant can reach, only the
// ceilings on how much of them they may consume.
func (s *Service) ChangePlan(ctx context.Context, tenantID, toPlanID, actor, ip string) (Subscription, error) {
	preview, err := s.PreviewPlanChange(ctx, tenantID, toPlanID)
	if err != nil {
		return Subscription{}, err
	}
	sub, err := s.ActiveSubscription(ctx, tenantID)
	if err != nil {
		return Subscription{}, err
	}
	target, err := s.Plan(ctx, toPlanID)
	if err != nil {
		return Subscription{}, err
	}
	now := s.unix()

	if !preview.Immediate {
		if err := s.db.UpdateVersioned(ctx,
			`UPDATE billing_subscriptions SET pending_plan_id = ?,
			   pending_effective_at = ?, updated_at = ?, version = version + 1
			 WHERE id = ? AND tenant_id = ? AND version = ?`,
			target.ID, preview.EffectiveAt, now, sub.ID, tenantID, sub.Version); err != nil {
			return Subscription{}, planChangeError(err)
		}
		s.recordSubscriptionEvent(ctx, sub, EventPlanChanged, sub.State, sub.State, actor,
			"scheduled plan="+target.Code)
		s.WriteAudit(ctx, AuditRecord{
			TenantID: tenantID, Actor: actor, Action: ActionSubscriptionChanged,
			Target: "subscription:" + sub.ID, IP: ip,
			Detail: "scheduled to " + target.Code,
		})
		return s.Subscription(ctx, tenantID, sub.ID)
	}

	nextState, err := NextSubscriptionState(sub.State, EventPlanChanged)
	if err != nil {
		return Subscription{}, ErrConflict("A plan change is not allowed while the subscription is " + sub.State + ".")
	}
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE billing_subscriptions SET plan_id = ?, price_minor = ?, cycle = ?,
		   state = ?, pending_plan_id = '', pending_effective_at = 0,
		   updated_at = ?, version = version + 1
		 WHERE id = ? AND tenant_id = ? AND version = ?`,
		target.ID, target.PriceMinor, target.Cycle, nextState, now,
		sub.ID, tenantID, sub.Version); err != nil {
		return Subscription{}, planChangeError(err)
	}

	if preview.DueNowMinor > 0 {
		if _, err := s.RaiseProrationInvoice(ctx, tenantID, sub, target, preview); err != nil {
			return Subscription{}, err
		}
	}
	if preview.RefundMinor > 0 {
		if err := s.creditAccountBalance(ctx, tenantID, preview.RefundMinor); err != nil {
			return Subscription{}, err
		}
	}

	s.recordSubscriptionEvent(ctx, sub, EventPlanChanged, sub.State, nextState, actor,
		"plan="+target.Code+" due_now_minor="+itoa(preview.DueNowMinor))
	s.WriteAudit(ctx, AuditRecord{
		TenantID: tenantID, Actor: actor, Action: ActionSubscriptionChanged,
		Target: "subscription:" + sub.ID, IP: ip,
		Detail: "plan=" + target.Code + " due_now_minor=" + itoa(preview.DueNowMinor),
	})
	return s.Subscription(ctx, tenantID, sub.ID)
}

// planChangeError renders a failed subscription write.
func planChangeError(err error) error {
	if database.IsConflict(err) {
		return ErrConflict("The subscription changed while you were editing it; reload and try again.")
	}
	return ErrInternal(err, "Could not change the subscription.")
}

// Cancel ends a subscription. The default is to run to the end of the period
// the tenant has already paid for; there is no retention interstitial, no
// extra confirmation step and no penalty, and the tenant keeps full access
// until the period actually ends.
func (s *Service) Cancel(ctx context.Context, tenantID, subID string, immediate bool, reason, actor, ip string) (Subscription, error) {
	sub, err := s.Subscription(ctx, tenantID, subID)
	if err != nil {
		return Subscription{}, err
	}
	nextState, err := NextSubscriptionState(sub.State, EventCancelled)
	if err != nil {
		return Subscription{}, ErrConflict("This subscription is already " + sub.State + ".")
	}

	now := s.unix()
	endedAt := int64(0)
	cancelAtEnd := true
	if immediate {
		endedAt = now
		cancelAtEnd = false
	}
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE billing_subscriptions SET state = ?, cancelled_at = ?, ended_at = ?,
		   cancel_at_period_end = ?, pending_plan_id = '', pending_effective_at = 0,
		   updated_at = ?, version = version + 1
		 WHERE id = ? AND tenant_id = ? AND version = ?`,
		nextState, now, endedAt, boolToInt(cancelAtEnd), now,
		sub.ID, tenantID, sub.Version); err != nil {
		return Subscription{}, planChangeError(err)
	}

	s.recordSubscriptionEvent(ctx, sub, EventCancelled, sub.State, nextState, actor, reason)
	s.WriteAudit(ctx, AuditRecord{
		TenantID: tenantID, Actor: actor, Action: ActionSubscriptionCancel,
		Target: "subscription:" + sub.ID, IP: ip,
		Detail: "immediate=" + boolText(immediate) + " reason=" + reason,
	})
	s.notify(ctx, tenantID, NotifyCancelled, map[string]any{
		"subscription_id": sub.ID,
		"immediate":       immediate,
		"access_until":    sub.PeriodEnd,
	})
	return s.Subscription(ctx, tenantID, sub.ID)
}

// Resume reverses a cancellation that has not yet taken effect, or restarts
// a paused subscription.
func (s *Service) Resume(ctx context.Context, tenantID, subID, actor, ip string) (Subscription, error) {
	sub, err := s.Subscription(ctx, tenantID, subID)
	if err != nil {
		return Subscription{}, err
	}
	if sub.State == StateCancelled && sub.EndedAt == 0 {
		if err := s.db.UpdateVersioned(ctx,
			`UPDATE billing_subscriptions SET state = ?, cancelled_at = 0,
			   cancel_at_period_end = 0, updated_at = ?, version = version + 1
			 WHERE id = ? AND tenant_id = ? AND version = ?`,
			StateActive, s.unix(), sub.ID, tenantID, sub.Version); err != nil {
			return Subscription{}, planChangeError(err)
		}
		s.recordSubscriptionEvent(ctx, sub, EventResumed, sub.State, StateActive, actor, "cancellation withdrawn")
		s.WriteAudit(ctx, AuditRecord{
			TenantID: tenantID, Actor: actor, Action: ActionSubscriptionResumed,
			Target: "subscription:" + sub.ID, IP: ip,
		})
		return s.Subscription(ctx, tenantID, sub.ID)
	}

	nextState, err := NextSubscriptionState(sub.State, EventResumed)
	if err != nil {
		return Subscription{}, ErrConflict("A subscription that is " + sub.State + " cannot be resumed.")
	}
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE billing_subscriptions SET state = ?, updated_at = ?, version = version + 1
		 WHERE id = ? AND tenant_id = ? AND version = ?`,
		nextState, s.unix(), sub.ID, tenantID, sub.Version); err != nil {
		return Subscription{}, planChangeError(err)
	}
	s.recordSubscriptionEvent(ctx, sub, EventResumed, sub.State, nextState, actor, "")
	s.WriteAudit(ctx, AuditRecord{
		TenantID: tenantID, Actor: actor, Action: ActionSubscriptionResumed,
		Target: "subscription:" + sub.ID, IP: ip,
	})
	return s.Subscription(ctx, tenantID, sub.ID)
}

// Transition applies one lifecycle event to a subscription, refusing any
// move the state machine does not name. Every state change in this package
// goes through here or through one of the explicit methods above.
func (s *Service) Transition(ctx context.Context, sub Subscription, event, actor, detail string) (Subscription, error) {
	nextState, err := NextSubscriptionState(sub.State, event)
	if err != nil {
		return Subscription{}, ErrConflict(err.Error())
	}
	now := s.unix()
	endedAt := sub.EndedAt
	if nextState == StateExpired && endedAt == 0 {
		endedAt = now
	}
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE billing_subscriptions SET state = ?, ended_at = ?, updated_at = ?,
		   version = version + 1
		 WHERE id = ? AND tenant_id = ? AND version = ?`,
		nextState, endedAt, now, sub.ID, sub.TenantID, sub.Version); err != nil {
		return Subscription{}, planChangeError(err)
	}
	s.recordSubscriptionEvent(ctx, sub, event, sub.State, nextState, actor, detail)
	return s.Subscription(ctx, sub.TenantID, sub.ID)
}

// StartGrace puts a subscription into arrears with a grace window during
// which the tenant keeps full service. cashp never suspends the moment a
// charge fails.
func (s *Service) StartGrace(ctx context.Context, sub Subscription, detail string) (Subscription, error) {
	plan, err := s.Plan(ctx, sub.PlanID)
	graceDays := int64(DefaultGraceDays)
	if err == nil && plan.GraceDays > 0 {
		graceDays = plan.GraceDays
	}
	graceEnds := s.Now().AddDate(0, 0, int(graceDays)).Unix()

	updated, err := s.Transition(ctx, sub, EventPaymentFailed, "system", detail)
	if err != nil {
		return Subscription{}, err
	}
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_subscriptions SET grace_ends_at = ?, updated_at = ?
		 WHERE id = ? AND tenant_id = ?`,
		graceEnds, s.unix(), updated.ID, updated.TenantID); err != nil {
		return Subscription{}, ErrInternal(err, "Could not start the grace period.")
	}
	s.notify(ctx, sub.TenantID, NotifyGraceStarted, map[string]any{
		"subscription_id": sub.ID,
		"grace_ends_at":   graceEnds,
		"days":            graceDays,
	})
	return s.Subscription(ctx, updated.TenantID, updated.ID)
}

// AdvanceRenewal moves a subscription into its next period after a
// successful renewal.
func (s *Service) AdvanceRenewal(ctx context.Context, sub Subscription) (Subscription, error) {
	start := time.Unix(sub.PeriodEnd, 0).UTC()
	if sub.PeriodEnd == 0 {
		start = s.Now()
	}
	end := AdvancePeriod(start, sub.Cycle)
	if end.IsZero() {
		return sub, nil
	}
	now := s.unix()
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE billing_subscriptions SET period_start = ?, period_end = ?,
		   grace_ends_at = 0, updated_at = ?, version = version + 1
		 WHERE id = ? AND tenant_id = ? AND version = ?`,
		start.Unix(), end.Unix(), now, sub.ID, sub.TenantID, sub.Version); err != nil {
		return Subscription{}, planChangeError(err)
	}
	s.recordSubscriptionEvent(ctx, sub, EventRenewed, sub.State, sub.State, "system",
		"period_end="+itoa(end.Unix()))
	return s.Subscription(ctx, sub.TenantID, sub.ID)
}

// ApplyPendingPlan promotes a scheduled downgrade once its effective time has
// arrived.
func (s *Service) ApplyPendingPlan(ctx context.Context, sub Subscription) (Subscription, error) {
	if sub.PendingPlanID == "" || sub.PendingEffectiveAt > s.unix() {
		return sub, nil
	}
	target, err := s.Plan(ctx, sub.PendingPlanID)
	if err != nil {
		return Subscription{}, err
	}
	now := s.unix()
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE billing_subscriptions SET plan_id = ?, price_minor = ?, cycle = ?,
		   pending_plan_id = '', pending_effective_at = 0, updated_at = ?,
		   version = version + 1
		 WHERE id = ? AND tenant_id = ? AND version = ?`,
		target.ID, target.PriceMinor, target.Cycle, now,
		sub.ID, sub.TenantID, sub.Version); err != nil {
		return Subscription{}, planChangeError(err)
	}
	s.recordSubscriptionEvent(ctx, sub, EventPlanChanged, sub.State, sub.State, "system",
		"scheduled change applied plan="+target.Code)
	return s.Subscription(ctx, sub.TenantID, sub.ID)
}

// recordSubscriptionEvent appends one lifecycle record. The log is
// append-only: nothing in this package updates or deletes a row in it.
func (s *Service) recordSubscriptionEvent(ctx context.Context, sub Subscription, event, from, to, actor, detail string) {
	if actor == "" {
		actor = "system"
	}
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO billing_subscription_events
		 (id, tenant_id, subscription_id, event, from_state, to_state, actor,
		  detail, occurred_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		newID(), sub.TenantID, sub.ID, event, from, to, actor, detail, s.unix())
	if err != nil {
		s.WriteAudit(ctx, AuditRecord{
			TenantID: sub.TenantID, Action: ActionSubscriptionChanged,
			Target: "subscription:" + sub.ID, Result: ResultFailure,
			Detail: "event log write failed: " + err.Error(),
		})
	}
}

// SubscriptionEvents returns a subscription's lifecycle history.
func (s *Service) SubscriptionEvents(ctx context.Context, tenantID, subID string) ([]SubscriptionEvent, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT id, tenant_id, subscription_id, event, from_state, to_state,
		        actor, detail, occurred_at
		 FROM billing_subscription_events
		 WHERE tenant_id = ? AND subscription_id = ?
		 ORDER BY occurred_at DESC`, tenantID, subID)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the subscription history.")
	}
	defer func() { _ = rows.Close() }()

	out := []SubscriptionEvent{}
	for rows.Next() {
		var e SubscriptionEvent
		if err := rows.Scan(&e.ID, &e.TenantID, &e.SubscriptionID, &e.Event,
			&e.FromState, &e.ToState, &e.Actor, &e.Detail,
			&e.OccurredAt); err != nil {
			return nil, ErrInternal(err, "Could not read the subscription history.")
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the subscription history.")
	}
	return out, nil
}

// DueSubscriptions returns subscriptions whose period ends at or before a
// cutoff and which are still entitled, for the renewal task.
func (s *Service) DueSubscriptions(ctx context.Context, cutoff int64) ([]Subscription, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutReport,
		`SELECT `+subscriptionColumns+` FROM billing_subscriptions
		 WHERE period_end > 0 AND period_end <= ? AND state IN (?, ?, ?)
		 ORDER BY period_end`,
		cutoff, StateActive, StateTrialing, StateCancelled)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the due subscriptions.")
	}
	defer func() { _ = rows.Close() }()

	out := []Subscription{}
	for rows.Next() {
		sub, sErr := scanSubscription(rows)
		if sErr != nil {
			return nil, ErrInternal(sErr, "Could not read the due subscriptions.")
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the due subscriptions.")
	}
	return out, nil
}

// creditAccountBalance adds a downgrade credit to a tenant's account balance
// so it is consumed by the next invoice instead of being refunded to a card.
func (s *Service) creditAccountBalance(ctx context.Context, tenantID string, amountMinor int64) error {
	if amountMinor <= 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_accounts SET balance_minor = balance_minor + ?, updated_at = ?
		 WHERE tenant_id = ?`, amountMinor, s.unix(), tenantID); err != nil {
		return ErrInternal(err, "Could not credit the account balance.")
	}
	return nil
}

// boolText renders a bool for an audit detail string.
func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
