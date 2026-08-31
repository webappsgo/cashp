package billing

import (
	"context"
	"sort"
	"strings"

	"github.com/webappsgo/cashp/src/database"
)

// Reconciliation verdicts for one provider's day.
const (
	ReconcileClean       = "clean"
	ReconcileDiscrepancy = "discrepancies"
	ReconcileFailed      = "failed"
)

// DefaultDiscrepancyThreshold is how many mismatches a single day may carry
// before the operator is alerted rather than merely informed.
const DefaultDiscrepancyThreshold = 1

// Discrepancy is one disagreement between cashp's ledger and a provider's.
type Discrepancy struct {
	Kind        string `json:"kind"`
	Reference   string `json:"reference"`
	LocalMinor  int64  `json:"local_minor"`
	RemoteMinor int64  `json:"remote_minor"`
	Currency    string `json:"currency"`
	Detail      string `json:"detail"`
}

// Discrepancy kinds.
const (
	DiscrepancyAmount      = "amount_mismatch"
	DiscrepancyState       = "state_mismatch"
	DiscrepancyMissingHere = "missing_locally"
	DiscrepancyMissingAway = "missing_at_provider"
)

// ReconciliationReport is one provider's daily reconciliation result.
type ReconciliationReport struct {
	Provider         string        `json:"provider"`
	Day              string        `json:"day"`
	Since            int64         `json:"since"`
	Until            int64         `json:"until"`
	Checked          int64         `json:"checked"`
	Matched          int64         `json:"matched"`
	ReconciledMinor  int64         `json:"reconciled_minor"`
	DiscrepancyMinor int64         `json:"discrepancy_minor"`
	Currency         string        `json:"currency"`
	Status           string        `json:"status"`
	Detail           string        `json:"detail"`
	CompletedAt      int64         `json:"completed_at"`
	Discrepancies    []Discrepancy `json:"discrepancies"`
}

// RunReconciliation compares cashp's ledger against every enabled provider's
// own record for the previous day. It never changes a financial record: money
// only ever moves through an explicit billing operation, so a disagreement is
// reported for a person to settle and is never silently patched.
func (s *Service) RunReconciliation(ctx context.Context) error {
	if !s.Enabled(ctx) {
		return nil
	}
	providers, err := s.EnabledProviders(ctx)
	if err != nil {
		return err
	}
	now := s.unix()
	until := now - (now % secondsPerDay)
	since := until - secondsPerDay

	for _, rec := range providers {
		report := s.reconcileProvider(ctx, rec, since, until)
		s.recordReconciliation(ctx, report)
	}
	return nil
}

// reconcileProvider reconciles one provider over one window.
func (s *Service) reconcileProvider(ctx context.Context, rec ProviderRecord, since, until int64) ReconciliationReport {
	report := ReconciliationReport{
		Provider:      rec.Name,
		Day:           timeText(since),
		Since:         since,
		Until:         until,
		Status:        ReconcileClean,
		CompletedAt:   s.unix(),
		Discrepancies: []Discrepancy{},
	}

	instance, _, err := s.instance(ctx, rec)
	if err != nil {
		report.Status = ReconcileFailed
		report.Detail = err.Error()
		return report
	}
	callCtx, cancel := context.WithTimeout(ctx, s.providerTimeout(ctx))
	remote, err := instance.ListPayments(callCtx, since, until)
	cancel()
	if err != nil {
		report.Status = ReconcileFailed
		report.Detail = err.Error()
		return report
	}

	local, err := s.attemptsInWindow(ctx, rec.Name, since, until)
	if err != nil {
		report.Status = ReconcileFailed
		report.Detail = err.Error()
		return report
	}

	byReference := map[string]PaymentAttempt{}
	for _, a := range local {
		if a.ProviderRef != "" {
			byReference[a.ProviderRef] = a
		}
	}
	seen := map[string]bool{}

	for _, r := range remote {
		report.Checked++
		if report.Currency == "" {
			report.Currency = r.Currency
		}
		attempt, ok := byReference[r.Reference]
		if !ok {
			report.Discrepancies = append(report.Discrepancies, Discrepancy{
				Kind: DiscrepancyMissingHere, Reference: r.Reference,
				RemoteMinor: r.AmountMinor, Currency: r.Currency,
				Detail: "the provider has a charge cashp has no attempt for",
			})
			report.DiscrepancyMinor += r.AmountMinor
			continue
		}
		seen[r.Reference] = true
		remoteState := mapChargeState(r.State)
		switch {
		case attempt.AmountMinor != r.AmountMinor:
			report.Discrepancies = append(report.Discrepancies, Discrepancy{
				Kind: DiscrepancyAmount, Reference: r.Reference,
				LocalMinor: attempt.AmountMinor, RemoteMinor: r.AmountMinor,
				Currency: r.Currency, Detail: "the amounts disagree",
			})
			report.DiscrepancyMinor += absInt64(attempt.AmountMinor - r.AmountMinor)
		case attempt.State != remoteState:
			report.Discrepancies = append(report.Discrepancies, Discrepancy{
				Kind: DiscrepancyState, Reference: r.Reference,
				LocalMinor: attempt.AmountMinor, RemoteMinor: r.AmountMinor,
				Currency: r.Currency,
				Detail:   "cashp says " + attempt.State + ", the provider says " + remoteState,
			})
		default:
			report.Matched++
			report.ReconciledMinor += r.AmountMinor
		}
	}

	for _, a := range local {
		if a.State != PaymentSucceeded || a.ProviderRef == "" || seen[a.ProviderRef] {
			continue
		}
		report.Checked++
		report.Discrepancies = append(report.Discrepancies, Discrepancy{
			Kind: DiscrepancyMissingAway, Reference: a.ProviderRef,
			LocalMinor: a.AmountMinor, Currency: a.Currency,
			Detail: "cashp recorded a settled charge the provider does not list",
		})
		report.DiscrepancyMinor += a.AmountMinor
	}

	if len(report.Discrepancies) > 0 {
		report.Status = ReconcileDiscrepancy
	}
	return report
}

// absInt64 is the magnitude of a signed minor-unit difference.
func absInt64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// attemptsInWindow returns one provider's attempts inside a time window.
func (s *Service) attemptsInWindow(ctx context.Context, providerName string, since, until int64) ([]PaymentAttempt, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutReport,
		`SELECT `+attemptColumns+` FROM billing_payment_attempts
		 WHERE provider = ? AND attempted_at >= ? AND attempted_at < ?
		 ORDER BY attempted_at`, providerName, since, until)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the payment attempts.")
	}
	defer func() { _ = rows.Close() }()

	out := []PaymentAttempt{}
	for rows.Next() {
		a, sErr := scanAttempt(rows)
		if sErr != nil {
			return nil, ErrInternal(sErr, "Could not read the payment attempts.")
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the payment attempts.")
	}
	return out, nil
}

// recordReconciliation writes the result into the append-only audit trail and
// alerts the operator when the day is over the discrepancy threshold.
func (s *Service) recordReconciliation(ctx context.Context, report ReconciliationReport) {
	summary := "day=" + report.Day +
		" checked=" + itoa(report.Checked) +
		" matched=" + itoa(report.Matched) +
		" reconciled_minor=" + itoa(report.ReconciledMinor) +
		" discrepancy_minor=" + itoa(report.DiscrepancyMinor) +
		" status=" + report.Status
	if report.Detail != "" {
		summary += " detail=" + report.Detail
	}

	result := ResultSuccess
	if report.Status != ReconcileClean {
		result = ResultFailure
	}
	s.WriteAudit(ctx, AuditRecord{
		Actor: ActorReconciliation, Action: ActionReconciled,
		Provider: report.Provider, Target: "day:" + report.Day,
		Result: result, Code: report.Status, Detail: summary,
	})

	for _, d := range report.Discrepancies {
		s.WriteAudit(ctx, AuditRecord{
			Actor: ActorReconciliation, Action: ActionReconcileMismatch,
			Provider: report.Provider, Target: "reference:" + d.Reference,
			Result: ResultFailure, Code: d.Kind,
			Detail: d.Detail + " local_minor=" + itoa(d.LocalMinor) +
				" remote_minor=" + itoa(d.RemoteMinor),
		})
	}

	threshold := s.SettingInt(ctx, SettingDiscrepancyThreshold, DefaultDiscrepancyThreshold)
	if threshold > 0 && int64(len(report.Discrepancies)) >= threshold {
		s.notify(ctx, "", NotifyReconcileAlert, map[string]any{
			"provider":      report.Provider,
			"day":           report.Day,
			"discrepancies": len(report.Discrepancies),
			"amount":        FormatMinor(report.DiscrepancyMinor, report.Currency),
		})
	}
}

// ReconcileSummary is what the admin dashboard shows per provider.
type ReconcileSummary struct {
	Provider      string `json:"provider"`
	LastRun       int64  `json:"last_run"`
	LastRunText   string `json:"last_run_text"`
	Status        string `json:"status"`
	Detail        string `json:"detail"`
	Unresolved    int64  `json:"unresolved"`
	NeverRun      bool   `json:"never_run"`
	AlertOperator bool   `json:"alert_operator"`
}

// ReconciliationStatus reports the latest reconciliation for every enabled
// provider, so an operator can see at a glance whether the books agree.
func (s *Service) ReconciliationStatus(ctx context.Context) ([]ReconcileSummary, error) {
	providers, err := s.EnabledProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ReconcileSummary, 0, len(providers))
	for _, rec := range providers {
		summary := ReconcileSummary{Provider: rec.Name, NeverRun: true, Status: "never run"}
		row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
			`SELECT occurred_at, code, detail FROM billing_audit
			 WHERE action = ? AND provider = ?
			 ORDER BY occurred_at DESC LIMIT 1`, ActionReconciled, rec.Name)
		var occurred int64
		var code, detail string
		if err := row.Scan(&occurred, &code, &detail); err == nil {
			summary.NeverRun = false
			summary.LastRun = occurred
			summary.LastRunText = timeText(occurred)
			summary.Status = code
			summary.Detail = detail
		}

		var unresolved int64
		countRow := s.db.QueryRowContext(ctx, database.TimeoutSelect,
			`SELECT COUNT(id) FROM billing_audit
			 WHERE action = ? AND provider = ? AND occurred_at >= ?`,
			ActionReconcileMismatch, rec.Name, s.unix()-30*secondsPerDay)
		if err := countRow.Scan(&unresolved); err == nil {
			summary.Unresolved = unresolved
		}
		summary.AlertOperator = summary.Unresolved > 0 || summary.Status == ReconcileFailed
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.Compare(out[i].Provider, out[j].Provider) < 0
	})
	return out, nil
}

// ReconcileNow runs reconciliation for one provider over one window on
// demand, which is what the admin dashboard's re-check control calls.
func (s *Service) ReconcileNow(ctx context.Context, providerName string, since, until int64, actor, ip string) (ReconciliationReport, error) {
	rec, err := s.ProviderByName(ctx, providerName)
	if err != nil {
		return ReconciliationReport{}, err
	}
	if !rec.Enabled {
		return ReconciliationReport{}, ErrProviderDisabled(providerName)
	}
	if until <= since {
		now := s.unix()
		until = now - (now % secondsPerDay)
		since = until - secondsPerDay
	}
	report := s.reconcileProvider(ctx, rec, since, until)
	s.recordReconciliation(ctx, report)
	s.WriteAudit(ctx, AuditRecord{
		Actor: actor, Action: ActionReconciled, Provider: providerName,
		Target: "day:" + report.Day, Result: ResultSuccess, IP: ip,
		Detail: "operator requested reconciliation",
	})
	return report, nil
}
