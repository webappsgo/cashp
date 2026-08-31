package billing

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"strings"
)

// Export formats a tenant may ask for.
const (
	ExportJSON = "json"
	ExportCSV  = "csv"
)

// ExportBundle is everything cashp holds about a tenant's billing, in a form
// any other system can read. It exists so a tenant is never locked in: the
// whole financial record leaves in an open format on request.
type ExportBundle struct {
	GeneratedAt    int64               `json:"generated_at"`
	TenantID       string              `json:"tenant_id"`
	Account        Account             `json:"account"`
	Subscriptions  []Subscription      `json:"subscriptions"`
	Events         []SubscriptionEvent `json:"subscription_events"`
	Invoices       []Invoice           `json:"invoices"`
	CreditNotes    []CreditNote        `json:"credit_notes"`
	PaymentMethods []PaymentMethod     `json:"payment_methods"`
	Payments       []PaymentAttempt    `json:"payments"`
	Usage          []UsageRecord       `json:"usage"`
	Quotas         []QuotaStatus       `json:"quotas"`
}

// ExportTenant assembles a tenant's complete billing record. Every read is
// scoped to the tenant, so an export can only ever contain the caller's own
// data, and nothing in the bundle carries a card number, a security code or a
// provider secret: payment instruments export as brand, last four and expiry.
func (s *Service) ExportTenant(ctx context.Context, tenantID, actor, ip string) (ExportBundle, error) {
	if strings.TrimSpace(tenantID) == "" {
		return ExportBundle{}, ErrValidation("billing: a tenant is required")
	}
	bundle := ExportBundle{GeneratedAt: s.unix(), TenantID: tenantID}

	account, err := s.EnsureAccount(ctx, tenantID)
	if err != nil {
		return ExportBundle{}, err
	}
	bundle.Account = account

	if bundle.Subscriptions, err = s.ListSubscriptions(ctx, tenantID); err != nil {
		return ExportBundle{}, err
	}
	bundle.Events = []SubscriptionEvent{}
	for _, sub := range bundle.Subscriptions {
		events, eErr := s.SubscriptionEvents(ctx, tenantID, sub.ID)
		if eErr != nil {
			return ExportBundle{}, eErr
		}
		bundle.Events = append(bundle.Events, events...)
	}
	if bundle.Invoices, err = s.ListInvoices(ctx, tenantID, 1000, 0); err != nil {
		return ExportBundle{}, err
	}
	for i := range bundle.Invoices {
		lines, lErr := s.InvoiceLines(ctx, tenantID, bundle.Invoices[i].ID)
		if lErr != nil {
			return ExportBundle{}, lErr
		}
		bundle.Invoices[i].Lines = lines
	}
	if bundle.CreditNotes, err = s.ListCreditNotes(ctx, tenantID); err != nil {
		return ExportBundle{}, err
	}
	if bundle.PaymentMethods, err = s.ListPaymentMethods(ctx, tenantID); err != nil {
		return ExportBundle{}, err
	}
	if bundle.Payments, err = s.ListAttempts(ctx, tenantID, 200); err != nil {
		return ExportBundle{}, err
	}
	if bundle.Usage, err = s.ListUsage(ctx, tenantID); err != nil {
		return ExportBundle{}, err
	}
	if bundle.Quotas, err = s.QuotaStatuses(ctx, tenantID); err != nil {
		return ExportBundle{}, err
	}

	s.WriteAudit(ctx, AuditRecord{
		TenantID: tenantID, Actor: actor, Action: ActionDataExported,
		Target: "tenant:" + tenantID, Result: ResultSuccess, IP: ip,
		Detail: "invoices=" + itoa(int64(len(bundle.Invoices))) +
			" payments=" + itoa(int64(len(bundle.Payments))),
	})
	return bundle, nil
}

// ExportTenantJSON renders the bundle as indented JSON.
func (s *Service) ExportTenantJSON(ctx context.Context, tenantID, actor, ip string) ([]byte, error) {
	bundle, err := s.ExportTenant(ctx, tenantID, actor, ip)
	if err != nil {
		return nil, err
	}
	// The payment instrument fields that must never leave cashp are tagged
	// json:"-" on the model, so the marshaller drops them without a second
	// filtering pass that could fall out of step with the struct.
	out, mErr := json.MarshalIndent(bundle, "", "  ")
	if mErr != nil {
		return nil, ErrInternal(mErr, "Could not render the export.")
	}
	return append(out, '\n'), nil
}

// ExportTenantCSV renders the bundle as a single CSV with a section column,
// which is what a spreadsheet expects and what an accountant asks for.
func (s *Service) ExportTenantCSV(ctx context.Context, tenantID, actor, ip string) ([]byte, error) {
	bundle, err := s.ExportTenant(ctx, tenantID, actor, ip)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	sections := [][]string{
		{"section", "field_1", "field_2", "field_3", "field_4", "field_5", "field_6", "field_7"},
		{"account", "billing_email", "country", "currency", "tax_id", "balance_minor", "", ""},
		{"account", bundle.Account.BillingEmail, bundle.Account.Country,
			bundle.Account.Currency, bundle.Account.TaxID,
			itoa(bundle.Account.BalanceMinor), "", ""},
		{"subscription", "id", "plan_id", "state", "price_minor", "currency", "period_start", "period_end"},
	}
	for _, sub := range bundle.Subscriptions {
		sections = append(sections, []string{"subscription", sub.ID, sub.PlanID,
			sub.State, itoa(sub.PriceMinor), sub.Currency,
			timeText(sub.PeriodStart), timeText(sub.PeriodEnd)})
	}
	sections = append(sections, []string{"invoice", "number", "state", "currency",
		"total_minor", "paid_minor", "credited_minor", "issued_at"})
	for _, inv := range bundle.Invoices {
		sections = append(sections, []string{"invoice", inv.Number, inv.State,
			inv.Currency, itoa(inv.TotalMinor), itoa(inv.PaidMinor),
			itoa(inv.CreditedMinor), timeText(inv.IssuedAt)})
	}
	sections = append(sections, []string{"credit_note", "number", "invoice_id",
		"amount_minor", "currency", "reason", "issued_at", ""})
	for _, note := range bundle.CreditNotes {
		sections = append(sections, []string{"credit_note", note.Number,
			note.InvoiceID, itoa(note.AmountMinor), note.Currency, note.Reason,
			timeText(note.IssuedAt), ""})
	}
	sections = append(sections, []string{"payment_method", "provider", "kind",
		"brand", "last4", "exp_month", "exp_year", "state"})
	for _, m := range bundle.PaymentMethods {
		sections = append(sections, []string{"payment_method", m.Provider, m.Kind,
			m.Brand, m.Last4, itoa(m.ExpMonth), itoa(m.ExpYear), m.State})
	}
	sections = append(sections, []string{"payment", "attempted_at", "provider",
		"amount_minor", "currency", "state", "failure_code", "invoice_id"})
	for _, a := range bundle.Payments {
		sections = append(sections, []string{"payment", timeText(a.AttemptedAt),
			a.Provider, itoa(a.AmountMinor), a.Currency, a.State,
			a.FailureCode, a.InvoiceID})
	}
	sections = append(sections, []string{"usage", "meter", "value", "period_start",
		"period_end", "", "", ""})
	for _, u := range bundle.Usage {
		sections = append(sections, []string{"usage", u.MeterCode, itoa(u.Value),
			timeText(u.PeriodStart), timeText(u.PeriodEnd), "", "", ""})
	}

	for _, row := range sections {
		if err := w.Write(row); err != nil {
			return nil, ErrInternal(err, "Could not render the export.")
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, ErrInternal(err, "Could not render the export.")
	}
	return buf.Bytes(), nil
}

// ExportFilename is the download name for a tenant's export.
func ExportFilename(tenantID, format string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			return r
		default:
			return '-'
		}
	}, tenantID)
	if format != ExportCSV {
		format = ExportJSON
	}
	return "cashp-billing-" + safe + "." + format
}
