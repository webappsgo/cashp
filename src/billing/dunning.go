package billing

import (
	"context"
	"strconv"
	"strings"
)

// secondsPerDay is the dunning calendar's unit.
const secondsPerDay = int64(86400)

// RetrySchedule returns the days after an invoice falls due on which a failed
// charge is retried. Operators tune it through the retry_schedule_days
// setting as a comma separated day list.
func (s *Service) RetrySchedule(ctx context.Context) []int64 {
	raw := s.Setting(ctx, SettingRetrySchedule, DefaultRetryScheduleRaw)
	days := parseDayList(raw)
	if len(days) == 0 {
		return parseDayList(DefaultRetryScheduleRaw)
	}
	return days
}

// parseDayList turns "1,3,5,7" into ascending, de-duplicated day offsets.
func parseDayList(raw string) []int64 {
	seen := map[int64]bool{}
	out := []int64{}
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || n <= 0 || seen[n] {
			continue
		}
		seen[n] = true
		// Keep the list ordered by inserting in place rather than sorting
		// afterwards, since an operator may type the days out of order.
		inserted := false
		for i, existing := range out {
			if n < existing {
				out = append(out[:i], append([]int64{n}, out[i:]...)...)
				inserted = true
				break
			}
		}
		if !inserted {
			out = append(out, n)
		}
	}
	return out
}

// GraceDays is how long a tenant keeps service after a charge fails.
func (s *Service) GraceDays(ctx context.Context) int64 {
	days := s.SettingInt(ctx, SettingGraceDays, DefaultGraceDays)
	if days < 0 {
		return DefaultGraceDays
	}
	return days
}

// DunningStep is one scheduled attempt against an overdue invoice.
type DunningStep struct {
	InvoiceID   string `json:"invoice_id"`
	Number      string `json:"number"`
	TenantID    string `json:"tenant_id"`
	DayOffset   int64  `json:"day_offset"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Due         bool   `json:"due"`
}

// dueRetryOffset reports which scheduled retry, if any, is owed for an
// invoice right now. It returns the day offset and whether a retry is due.
// An offset only fires once because the attempt count for the invoice tells
// us how many have already run.
func dueRetryOffset(schedule []int64, dueAt, now, attemptsMade int64) (int64, bool) {
	if dueAt <= 0 || now < dueAt {
		return 0, false
	}
	elapsedDays := (now - dueAt) / secondsPerDay
	for index, offset := range schedule {
		if int64(index) < attemptsMade {
			continue
		}
		if elapsedDays >= offset {
			return offset, true
		}
		return 0, false
	}
	return 0, false
}

// RunDunning walks every open invoice, retries the ones a scheduled attempt is
// owed on, moves the subscription behind an unpaid invoice into its grace
// period, and only suspends once that grace has fully run out. Service is
// never cut off the moment a charge fails.
func (s *Service) RunDunning(ctx context.Context) error {
	if !s.Enabled(ctx) {
		return nil
	}
	now := s.unix()
	invoices, err := s.OpenInvoices(ctx, now)
	if err != nil {
		return err
	}
	schedule := s.RetrySchedule(ctx)
	graceSeconds := s.GraceDays(ctx) * secondsPerDay

	for _, inv := range invoices {
		if inv.BalanceDueMinor() <= 0 {
			continue
		}
		attempts, aErr := s.AttemptsForInvoice(ctx, inv.TenantID, inv.ID)
		if aErr != nil {
			s.logDunning(ctx, inv, "could not read attempts: "+aErr.Error())
			continue
		}
		offset, due := dueRetryOffset(schedule, inv.DueAt, now, int64(len(attempts)))
		if due {
			key := "dunning:" + inv.ID + ":" + itoa(offset)
			if _, cErr := s.ChargeInvoice(ctx, inv.TenantID, inv.ID, key, ActorSystem, ""); cErr != nil {
				s.logDunning(ctx, inv, "retry failed: "+cErr.Error())
			}
			// Re-read so the rest of this pass sees a settled invoice as paid.
			if fresh, rErr := s.Invoice(ctx, inv.TenantID, inv.ID); rErr == nil {
				inv = fresh
			}
		}
		if inv.BalanceDueMinor() <= 0 {
			continue
		}
		if err := s.escalate(ctx, inv, graceSeconds, now); err != nil {
			s.logDunning(ctx, inv, "escalation failed: "+err.Error())
		}
	}
	return nil
}

// escalate moves the subscription behind an unpaid invoice one step further
// down the dunning path: past due first, suspension only after grace expiry.
func (s *Service) escalate(ctx context.Context, inv Invoice, graceSeconds, now int64) error {
	if inv.SubscriptionID == "" {
		if inv.State == InvoiceDue && now > inv.DueAt {
			if _, err := s.MarkInvoiceState(ctx, inv.TenantID, inv.ID, InvoiceOverdue, ActorSystem, ""); err != nil {
				return err
			}
		}
		return nil
	}
	sub, err := s.Subscription(ctx, inv.TenantID, inv.SubscriptionID)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}

	if sub.State == StateActive || sub.State == StateTrialing {
		moved, sErr := s.Transition(ctx, sub, EventPaymentFailed, ActorSystem,
			"invoice "+inv.Number+" unpaid")
		if sErr != nil {
			return sErr
		}
		sub = moved
		graced, gErr := s.StartGrace(ctx, sub, "invoice "+inv.Number)
		if gErr != nil {
			return gErr
		}
		sub = graced
		s.notify(ctx, sub.TenantID, NotifyGraceStarted, map[string]any{
			"invoice_number": inv.Number,
			"amount":         FormatMinor(inv.BalanceDueMinor(), inv.Currency),
			"grace_ends_at":  timeText(sub.GraceEndsAt),
		})
		return nil
	}

	if sub.State != StatePastDue {
		return nil
	}
	graceEnd := sub.GraceEndsAt
	if graceEnd <= 0 {
		graceEnd = inv.DueAt + graceSeconds
	}
	if now < graceEnd {
		s.notify(ctx, sub.TenantID, NotifyPaymentFailed, map[string]any{
			"invoice_number": inv.Number,
			"amount":         FormatMinor(inv.BalanceDueMinor(), inv.Currency),
			"grace_ends_at":  timeText(graceEnd),
		})
		return nil
	}

	suspended, err := s.Transition(ctx, sub, EventGraceExpired, ActorSystem,
		"grace expired with invoice "+inv.Number+" unpaid")
	if err != nil {
		return err
	}
	s.WriteAudit(ctx, AuditRecord{
		TenantID: sub.TenantID, Actor: ActorSystem, Action: ActionSubscriptionSuspended,
		Target: "subscription:" + suspended.ID, Result: ResultSuccess,
		Detail: "invoice " + inv.Number + " unpaid past grace",
	})
	s.notify(ctx, sub.TenantID, NotifySuspended, map[string]any{
		"invoice_number": inv.Number,
		"amount":         FormatMinor(inv.BalanceDueMinor(), inv.Currency),
	})
	if inv.State != InvoiceOverdue {
		if _, err := s.MarkInvoiceState(ctx, inv.TenantID, inv.ID, InvoiceOverdue, ActorSystem, ""); err != nil {
			return err
		}
	}
	return nil
}

// logDunning records a dunning problem without aborting the whole pass: one
// bad invoice must never stop the others from being worked.
func (s *Service) logDunning(ctx context.Context, inv Invoice, detail string) {
	s.WriteAudit(ctx, AuditRecord{
		TenantID: inv.TenantID, Actor: ActorSystem, Action: ActionDunningRun,
		Target: "invoice:" + inv.Number, Result: ResultFailure, Detail: detail,
	})
}

// SendRenewalReminders warns tenants ahead of a renewal charge so nothing is
// ever taken by surprise, and tells trialing tenants their trial is ending.
func (s *Service) SendRenewalReminders(ctx context.Context) error {
	if !s.Enabled(ctx) {
		return nil
	}
	now := s.unix()
	horizon := now + 7*secondsPerDay
	subs, err := s.DueSubscriptions(ctx, horizon)
	if err != nil {
		return err
	}
	for _, sub := range subs {
		switch {
		case sub.State == StateTrialing && sub.TrialEndsAt > now:
			s.notify(ctx, sub.TenantID, NotifyTrialEnding, map[string]any{
				"trial_ends_at": timeText(sub.TrialEndsAt),
				"amount":        FormatMinor(sub.PriceMinor, sub.Currency),
			})
		case sub.State == StateActive && !sub.CancelAtPeriodEnd && sub.PeriodEnd > now:
			s.notify(ctx, sub.TenantID, NotifyRenewalUpcoming, map[string]any{
				"renews_at": timeText(sub.PeriodEnd),
				"amount":    FormatMinor(sub.PriceMinor, sub.Currency),
			})
		}
	}
	return nil
}

// RunRenewals raises the next invoice for every subscription whose period has
// ended, applies any scheduled downgrade, and attempts the charge. A failure
// here never suspends anything: that is the dunning pass's job, after grace.
func (s *Service) RunRenewals(ctx context.Context) error {
	if !s.Enabled(ctx) {
		return nil
	}
	now := s.unix()
	subs, err := s.DueSubscriptions(ctx, now)
	if err != nil {
		return err
	}
	for _, sub := range subs {
		if err := s.renewOne(ctx, sub, now); err != nil {
			s.WriteAudit(ctx, AuditRecord{
				TenantID: sub.TenantID, Actor: ActorSystem, Action: ActionRenewalRun,
				Target: "subscription:" + sub.ID, Result: ResultFailure,
				Detail: err.Error(),
			})
		}
	}
	return nil
}

// renewOne carries a single subscription across its period boundary.
func (s *Service) renewOne(ctx context.Context, sub Subscription, now int64) error {
	if sub.State == StateTrialing && sub.TrialEndsAt > 0 && now >= sub.TrialEndsAt {
		converted, err := s.Transition(ctx, sub, EventTrialConverted, ActorSystem, "trial ended")
		if err != nil {
			return err
		}
		sub = converted
	}
	if sub.PeriodEnd > now {
		return nil
	}
	if sub.CancelAtPeriodEnd {
		ended, err := s.Transition(ctx, sub, EventPeriodEnded, ActorSystem, "cancellation took effect")
		if err != nil {
			return err
		}
		s.notify(ctx, ended.TenantID, NotifyCancelled, map[string]any{
			"ended_at": timeText(now),
		})
		return nil
	}
	if sub.PendingPlanID != "" && sub.PendingEffectiveAt > 0 && now >= sub.PendingEffectiveAt {
		applied, err := s.ApplyPendingPlan(ctx, sub)
		if err != nil {
			return err
		}
		sub = applied
	}

	inv, err := s.GenerateSubscriptionInvoice(ctx, sub)
	if err != nil {
		return err
	}
	advanced, err := s.AdvanceRenewal(ctx, sub)
	if err != nil {
		return err
	}
	sub = advanced

	if inv.BalanceDueMinor() > 0 {
		key := "renewal:" + sub.ID + ":" + itoa(inv.PeriodStart)
		if _, cErr := s.ChargeInvoice(ctx, sub.TenantID, inv.ID, key, ActorSystem, ""); cErr != nil {
			// A failed renewal charge is expected traffic, not an error in the
			// renewal itself: dunning picks the invoice up on its schedule.
			s.WriteAudit(ctx, AuditRecord{
				TenantID: sub.TenantID, Actor: ActorSystem, Action: ActionRenewalRun,
				Target: "invoice:" + inv.Number, Result: ResultFailure,
				Detail: cErr.Error(),
			})
		}
	}
	return nil
}
