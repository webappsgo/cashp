package billing

import (
	"context"
	"sort"

	"github.com/webappsgo/cashp/src/database"
)

// PlanMetric is one plan's contribution to recurring revenue.
type PlanMetric struct {
	PlanID    string `json:"plan_id"`
	PlanName  string `json:"plan_name"`
	Tenants   int64  `json:"tenants"`
	MRRMinor  int64  `json:"mrr_minor"`
	Currency  string `json:"currency"`
	SharePerc int64  `json:"share_percent"`
}

// ProviderMetric is one provider's measured performance.
type ProviderMetric struct {
	Provider      string `json:"provider"`
	Enabled       bool   `json:"enabled"`
	TestMode      bool   `json:"test_mode"`
	Health        string `json:"health"`
	Attempts      int64  `json:"attempts"`
	Succeeded     int64  `json:"succeeded"`
	Failed        int64  `json:"failed"`
	SuccessPerc   int64  `json:"success_percent"`
	CapturedMinor int64  `json:"captured_minor"`
	RefundedMinor int64  `json:"refunded_minor"`
}

// Dashboard is the financial picture the administration panel renders. Every
// figure is in integer minor units of the base currency, and every percentage
// is a whole number so nothing here is a float either.
type Dashboard struct {
	Currency          string           `json:"currency"`
	GeneratedAt       int64            `json:"generated_at"`
	BillingEnabled    bool             `json:"billing_enabled"`
	ActiveTenants     int64            `json:"active_tenants"`
	TrialingTenants   int64            `json:"trialing_tenants"`
	PastDueTenants    int64            `json:"past_due_tenants"`
	CancelledTenants  int64            `json:"cancelled_tenants"`
	MRRMinor          int64            `json:"mrr_minor"`
	ARRMinor          int64            `json:"arr_minor"`
	ARPUMinor         int64            `json:"arpu_minor"`
	LTVMinor          int64            `json:"ltv_minor"`
	ChurnPerc         int64            `json:"churn_percent"`
	OutstandingMinor  int64            `json:"outstanding_minor"`
	OverdueMinor      int64            `json:"overdue_minor"`
	CollectedMinor    int64            `json:"collected_minor"`
	CreditedMinor     int64            `json:"credited_minor"`
	OpenInvoiceCount  int64            `json:"open_invoice_count"`
	Plans             []PlanMetric     `json:"plans"`
	Providers         []ProviderMetric `json:"providers"`
	EnabledProviders  int              `json:"enabled_providers"`
	RegisteredDrivers int              `json:"registered_drivers"`
}

// monthlyEquivalent converts a cycle price to its monthly share, which is
// what makes plans on different cycles comparable in one MRR figure. A
// lifetime plan contributes nothing recurring: it was a single payment.
func monthlyEquivalent(priceMinor int64, cycle string) int64 {
	months := CycleMonths(cycle)
	if months <= 0 {
		return 0
	}
	return divRoundHalfAway(priceMinor, int64(months))
}

// FinancialDashboard assembles every operator-facing billing figure.
func (s *Service) FinancialDashboard(ctx context.Context) (Dashboard, error) {
	d := Dashboard{
		Currency:       s.Setting(ctx, SettingBaseCurrency, DefaultCurrency),
		GeneratedAt:    s.unix(),
		BillingEnabled: s.Enabled(ctx),
		Plans:          []PlanMetric{},
		Providers:      []ProviderMetric{},
	}

	if err := s.subscriptionMetrics(ctx, &d); err != nil {
		return Dashboard{}, err
	}
	if err := s.invoiceMetrics(ctx, &d); err != nil {
		return Dashboard{}, err
	}
	if err := s.providerMetrics(ctx, &d); err != nil {
		return Dashboard{}, err
	}

	d.ARRMinor = d.MRRMinor * 12
	if d.ActiveTenants > 0 {
		d.ARPUMinor = divRoundHalfAway(d.MRRMinor, d.ActiveTenants)
	}
	total := d.ActiveTenants + d.TrialingTenants + d.PastDueTenants + d.CancelledTenants
	if total > 0 {
		d.ChurnPerc = divRoundHalfAway(d.CancelledTenants*100, total)
	}
	if d.ChurnPerc > 0 {
		// Lifetime value is average revenue divided by the churn rate, which
		// is the standard estimate of how long an account stays.
		d.LTVMinor = divRoundHalfAway(d.ARPUMinor*100, d.ChurnPerc)
	} else {
		// With no churn measured yet a lifetime figure would be meaningless,
		// so a conservative twelve-month value is shown instead of infinity.
		d.LTVMinor = d.ARPUMinor * 12
	}
	for i := range d.Plans {
		if d.MRRMinor > 0 {
			d.Plans[i].SharePerc = divRoundHalfAway(d.Plans[i].MRRMinor*100, d.MRRMinor)
		}
		d.Plans[i].Currency = d.Currency
	}
	return d, nil
}

// subscriptionMetrics counts tenants by state and builds per-plan revenue.
func (s *Service) subscriptionMetrics(ctx context.Context, d *Dashboard) error {
	rows, err := s.db.QueryContext(ctx, database.TimeoutReport,
		`SELECT state, plan_id, cycle, price_minor, quantity
		 FROM billing_subscriptions`)
	if err != nil {
		return ErrInternal(err, "Could not read the subscriptions.")
	}
	defer func() { _ = rows.Close() }()

	planTotals := map[string]*PlanMetric{}
	for rows.Next() {
		var state, planID, cycle string
		var priceMinor, quantity int64
		if err := rows.Scan(&state, &planID, &cycle, &priceMinor, &quantity); err != nil {
			return ErrInternal(err, "Could not read the subscriptions.")
		}
		if quantity <= 0 {
			quantity = 1
		}
		switch state {
		case StateActive:
			d.ActiveTenants++
		case StateTrialing:
			d.TrialingTenants++
			continue
		case StatePastDue:
			d.PastDueTenants++
		case StateCancelled, StateExpired:
			d.CancelledTenants++
			continue
		default:
			continue
		}
		monthly := monthlyEquivalent(priceMinor*quantity, cycle)
		d.MRRMinor += monthly
		metric, ok := planTotals[planID]
		if !ok {
			metric = &PlanMetric{PlanID: planID}
			planTotals[planID] = metric
		}
		metric.Tenants++
		metric.MRRMinor += monthly
	}
	if err := rows.Err(); err != nil {
		return ErrInternal(err, "Could not read the subscriptions.")
	}

	for planID, metric := range planTotals {
		if plan, pErr := s.Plan(ctx, planID); pErr == nil {
			metric.PlanName = plan.Name
		}
		d.Plans = append(d.Plans, *metric)
	}
	sort.Slice(d.Plans, func(i, j int) bool {
		if d.Plans[i].MRRMinor == d.Plans[j].MRRMinor {
			return d.Plans[i].PlanID < d.Plans[j].PlanID
		}
		return d.Plans[i].MRRMinor > d.Plans[j].MRRMinor
	})
	return nil
}

// invoiceMetrics totals what has been collected and what is still owed.
func (s *Service) invoiceMetrics(ctx context.Context, d *Dashboard) error {
	rows, err := s.db.QueryContext(ctx, database.TimeoutReport,
		`SELECT state, total_minor, paid_minor, credited_minor
		 FROM billing_invoices WHERE state <> ?`, InvoiceDraft)
	if err != nil {
		return ErrInternal(err, "Could not read the invoices.")
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var state string
		var total, paid, credited int64
		if err := rows.Scan(&state, &total, &paid, &credited); err != nil {
			return ErrInternal(err, "Could not read the invoices.")
		}
		d.CollectedMinor += paid
		d.CreditedMinor += credited
		outstanding := ClampNonNegative(total - paid - credited)
		if outstanding == 0 {
			continue
		}
		switch state {
		case InvoicePaid, InvoiceCancelled, InvoiceRefunded:
			continue
		case InvoiceOverdue:
			d.OverdueMinor += outstanding
		}
		d.OutstandingMinor += outstanding
		d.OpenInvoiceCount++
	}
	if err := rows.Err(); err != nil {
		return ErrInternal(err, "Could not read the invoices.")
	}
	return nil
}

// providerMetrics measures every configured provider, enabled or not, so an
// operator can see that a disabled provider is genuinely doing nothing.
func (s *Service) providerMetrics(ctx context.Context, d *Dashboard) error {
	records, err := s.ListProviderRecords(ctx)
	if err != nil {
		return err
	}
	enabled, total, err := s.EnabledProviderCount(ctx)
	if err != nil {
		return err
	}
	d.EnabledProviders = enabled
	d.RegisteredDrivers = total

	for _, rec := range records {
		metric := ProviderMetric{
			Provider: rec.Name,
			Enabled:  rec.Enabled,
			TestMode: rec.TestMode,
			Health:   rec.HealthState,
		}
		row := s.db.QueryRowContext(ctx, database.TimeoutReport,
			`SELECT COUNT(id) FROM billing_payment_attempts WHERE provider = ?`, rec.Name)
		if err := row.Scan(&metric.Attempts); err != nil {
			return ErrInternal(err, "Could not read the payment attempts.")
		}
		okRow := s.db.QueryRowContext(ctx, database.TimeoutReport,
			`SELECT COUNT(id), COALESCE(SUM(amount_minor), 0)
			 FROM billing_payment_attempts WHERE provider = ? AND state = ?`,
			rec.Name, PaymentSucceeded)
		if err := okRow.Scan(&metric.Succeeded, &metric.CapturedMinor); err != nil {
			return ErrInternal(err, "Could not read the payment attempts.")
		}
		refundRow := s.db.QueryRowContext(ctx, database.TimeoutReport,
			`SELECT COALESCE(SUM(amount_minor), 0) FROM billing_refunds WHERE provider = ?`,
			rec.Name)
		if err := refundRow.Scan(&metric.RefundedMinor); err != nil {
			return ErrInternal(err, "Could not read the refunds.")
		}
		metric.Failed = metric.Attempts - metric.Succeeded
		if metric.Attempts > 0 {
			metric.SuccessPerc = divRoundHalfAway(metric.Succeeded*100, metric.Attempts)
		}
		d.Providers = append(d.Providers, metric)
	}
	return nil
}

// TenantSummary is the billing picture one tenant sees on its own page.
type TenantSummary struct {
	TenantID         string        `json:"tenant_id"`
	Account          Account       `json:"account"`
	Subscription     Subscription  `json:"subscription"`
	Plan             Plan          `json:"plan"`
	HasSubscription  bool          `json:"has_subscription"`
	Quotas           []QuotaStatus `json:"quotas"`
	OutstandingMinor int64         `json:"outstanding_minor"`
	NextInvoiceAt    int64         `json:"next_invoice_at"`
	NextAmountMinor  int64         `json:"next_amount_minor"`
	Currency         string        `json:"currency"`
}

// TenantDashboard assembles what a tenant is shown about its own billing.
func (s *Service) TenantDashboard(ctx context.Context, tenantID string) (TenantSummary, error) {
	summary := TenantSummary{TenantID: tenantID, Quotas: []QuotaStatus{}}
	account, err := s.EnsureAccount(ctx, tenantID)
	if err != nil {
		return TenantSummary{}, err
	}
	summary.Account = account
	summary.Currency = account.Currency

	sub, err := s.ActiveSubscription(ctx, tenantID)
	if err == nil {
		summary.Subscription = sub
		summary.HasSubscription = true
		summary.NextInvoiceAt = sub.PeriodEnd
		summary.NextAmountMinor = sub.PriceMinor * maxInt64(sub.Quantity, 1)
		summary.Currency = sub.Currency
		if plan, pErr := s.Plan(ctx, sub.PlanID); pErr == nil {
			summary.Plan = plan
		}
	} else if !isNotFound(err) {
		return TenantSummary{}, err
	}

	if summary.Quotas, err = s.QuotaStatuses(ctx, tenantID); err != nil {
		return TenantSummary{}, err
	}
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT COALESCE(SUM(total_minor - paid_minor - credited_minor), 0)
		 FROM billing_invoices
		 WHERE tenant_id = ? AND state NOT IN (?, ?, ?, ?)`,
		tenantID, InvoiceDraft, InvoicePaid, InvoiceCancelled, InvoiceRefunded)
	if err := row.Scan(&summary.OutstandingMinor); err != nil {
		return TenantSummary{}, ErrInternal(err, "Could not read the invoices.")
	}
	summary.OutstandingMinor = ClampNonNegative(summary.OutstandingMinor)
	return summary, nil
}
