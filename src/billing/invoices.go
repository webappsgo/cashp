package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

// Invoice line kinds.
const (
	LineSubscription = "subscription"
	LineProration    = "proration"
	LineOverage      = "overage"
	LineAdjustment   = "adjustment"
	LineCredit       = "credit"
)

// Credit note reasons.
const (
	CreditAdjustment = "adjustment"
	CreditRefund     = "refund"
	CreditGoodwill   = "goodwill"
	CreditError      = "error"
)

// invoiceColumns is the explicit column list for billing_invoices.
const invoiceColumns = `id, tenant_id, account_id, subscription_id, number, state,
	currency, subtotal_minor, discount_minor, tax_minor, total_minor, paid_minor,
	credited_minor, tax_rate_bps, tax_kind, tax_jurisdiction, reverse_charge,
	period_start, period_end, issued_at, due_at, paid_at, voided_at,
	buyer_snapshot, notes, created_at, updated_at, version`

// invoiceLineColumns is the explicit column list for billing_invoice_lines.
const invoiceLineColumns = `id, invoice_id, tenant_id, position, kind, description,
	quantity_milli, unit_price_minor, amount_minor, tax_rate_bps, tax_minor,
	meter_code, period_start, period_end`

// scanInvoice reads one row in invoiceColumns order.
func scanInvoice(sc interface{ Scan(...any) error }) (Invoice, error) {
	var inv Invoice
	var reverse int64
	if err := sc.Scan(&inv.ID, &inv.TenantID, &inv.AccountID, &inv.SubscriptionID,
		&inv.Number, &inv.State, &inv.Currency, &inv.SubtotalMinor,
		&inv.DiscountMinor, &inv.TaxMinor, &inv.TotalMinor, &inv.PaidMinor,
		&inv.CreditedMinor, &inv.TaxRateBPS, &inv.TaxKind, &inv.TaxJurisdiction,
		&reverse, &inv.PeriodStart, &inv.PeriodEnd, &inv.IssuedAt, &inv.DueAt,
		&inv.PaidAt, &inv.VoidedAt, &inv.BuyerSnapshot, &inv.Notes,
		&inv.CreatedAt, &inv.UpdatedAt, &inv.Version); err != nil {
		return Invoice{}, err
	}
	inv.ReverseCharge = reverse != 0
	return inv, nil
}

// Invoice returns one invoice with its lines. The tenant is always part of
// the predicate, so one tenant can never read another's invoice.
func (s *Service) Invoice(ctx context.Context, tenantID, invoiceID string) (Invoice, error) {
	if strings.TrimSpace(tenantID) == "" {
		return Invoice{}, ErrValidation("billing: a tenant is required")
	}
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+invoiceColumns+` FROM billing_invoices
		 WHERE id = ? AND tenant_id = ?`, invoiceID, tenantID)
	inv, err := scanInvoice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Invoice{}, ErrNotFound("invoice")
	}
	if err != nil {
		return Invoice{}, ErrInternal(err, "Could not read the invoice.")
	}
	lines, err := s.InvoiceLines(ctx, tenantID, invoiceID)
	if err != nil {
		return Invoice{}, err
	}
	inv.Lines = lines
	return inv, nil
}

// InvoiceLines returns an invoice's line items in display order.
func (s *Service) InvoiceLines(ctx context.Context, tenantID, invoiceID string) ([]InvoiceLine, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+invoiceLineColumns+` FROM billing_invoice_lines
		 WHERE invoice_id = ? AND tenant_id = ? ORDER BY position`,
		invoiceID, tenantID)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the invoice lines.")
	}
	defer func() { _ = rows.Close() }()

	out := []InvoiceLine{}
	for rows.Next() {
		var l InvoiceLine
		if err := rows.Scan(&l.ID, &l.InvoiceID, &l.TenantID, &l.Position,
			&l.Kind, &l.Description, &l.QuantityMilli, &l.UnitPriceMinor,
			&l.AmountMinor, &l.TaxRateBPS, &l.TaxMinor, &l.MeterCode,
			&l.LinePeriodStart, &l.LinePeriodEnd); err != nil {
			return nil, ErrInternal(err, "Could not read the invoice lines.")
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the invoice lines.")
	}
	return out, nil
}

// ListInvoices returns a tenant's invoices, newest first.
func (s *Service) ListInvoices(ctx context.Context, tenantID string, limit, offset int) ([]Invoice, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+invoiceColumns+` FROM billing_invoices
		 WHERE tenant_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		tenantID, limit, offset)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the invoices.")
	}
	defer func() { _ = rows.Close() }()

	out := []Invoice{}
	for rows.Next() {
		inv, sErr := scanInvoice(rows)
		if sErr != nil {
			return nil, ErrInternal(sErr, "Could not read the invoices.")
		}
		out = append(out, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the invoices.")
	}
	return out, nil
}

// CreateDraft opens a new draft invoice for a tenant. Nothing is numbered
// until the draft is issued, so an abandoned draft never consumes a number.
func (s *Service) CreateDraft(ctx context.Context, tenantID, subscriptionID string, periodStart, periodEnd int64) (Invoice, error) {
	account, err := s.EnsureAccount(ctx, tenantID)
	if err != nil {
		return Invoice{}, err
	}
	now := s.unix()
	inv := Invoice{
		ID:             newID(),
		TenantID:       tenantID,
		AccountID:      account.ID,
		SubscriptionID: subscriptionID,
		State:          InvoiceDraft,
		Currency:       account.Currency,
		PeriodStart:    periodStart,
		PeriodEnd:      periodEnd,
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}
	if inv.Currency == "" {
		inv.Currency = s.BaseCurrency(ctx)
	}
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO billing_invoices
		 (id, tenant_id, account_id, subscription_id, state, currency,
		  period_start, period_end, created_at, updated_at, version)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
		inv.ID, inv.TenantID, inv.AccountID, inv.SubscriptionID, inv.State,
		inv.Currency, inv.PeriodStart, inv.PeriodEnd, inv.CreatedAt,
		inv.UpdatedAt); err != nil {
		return Invoice{}, ErrInternal(err, "Could not create the invoice.")
	}
	return inv, nil
}

// AddLine appends a line item to a draft invoice and refreshes its totals. An
// issued invoice is refused: the correct adjustment is a credit note.
func (s *Service) AddLine(ctx context.Context, tenantID, invoiceID string, line InvoiceLine) (Invoice, error) {
	inv, err := s.Invoice(ctx, tenantID, invoiceID)
	if err != nil {
		return Invoice{}, err
	}
	if InvoiceFrozen(inv.State) {
		return Invoice{}, ErrImmutable(inv.Number)
	}
	if line.Kind == "" {
		line.Kind = LineAdjustment
	}
	if line.QuantityMilli == 0 {
		line.QuantityMilli = 1000
	}
	if line.AmountMinor == 0 {
		line.AmountMinor = Prorate(line.UnitPriceMinor, line.QuantityMilli, 1000)
	}
	line.ID = newID()
	line.InvoiceID = inv.ID
	line.TenantID = tenantID
	line.Position = int64(len(inv.Lines)) + 1

	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO billing_invoice_lines
		 (id, invoice_id, tenant_id, position, kind, description, quantity_milli,
		  unit_price_minor, amount_minor, tax_rate_bps, tax_minor, meter_code,
		  period_start, period_end)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		line.ID, line.InvoiceID, line.TenantID, line.Position, line.Kind,
		line.Description, line.QuantityMilli, line.UnitPriceMinor,
		line.AmountMinor, line.TaxRateBPS, line.TaxMinor, line.MeterCode,
		line.LinePeriodStart, line.LinePeriodEnd); err != nil {
		return Invoice{}, ErrInternal(err, "Could not add the invoice line.")
	}
	return s.RecalculateInvoice(ctx, tenantID, inv.ID)
}

// RecalculateInvoice recomputes a draft's subtotal, tax and total from its
// lines. All arithmetic is in integer minor units.
func (s *Service) RecalculateInvoice(ctx context.Context, tenantID, invoiceID string) (Invoice, error) {
	inv, err := s.Invoice(ctx, tenantID, invoiceID)
	if err != nil {
		return Invoice{}, err
	}
	if InvoiceFrozen(inv.State) {
		return inv, nil
	}
	account, err := s.EnsureAccount(ctx, tenantID)
	if err != nil {
		return Invoice{}, err
	}

	subtotal := int64(0)
	for _, l := range inv.Lines {
		subtotal += l.AmountMinor
	}
	net := ClampNonNegative(subtotal - inv.DiscountMinor)
	tax := s.CalculateTax(ctx, account, net)
	total := net + tax.AmountMinor

	now := s.unix()
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE billing_invoices SET subtotal_minor = ?, tax_minor = ?,
		   total_minor = ?, tax_rate_bps = ?, tax_kind = ?, tax_jurisdiction = ?,
		   reverse_charge = ?, updated_at = ?, version = version + 1
		 WHERE id = ? AND tenant_id = ? AND version = ?`,
		subtotal, tax.AmountMinor, total, tax.RateBPS, tax.Kind,
		tax.Jurisdiction, boolToInt(tax.ReverseCharge), now,
		inv.ID, tenantID, inv.Version); err != nil {
		return Invoice{}, invoiceWriteError(err)
	}
	if tax.RateBPS > 0 {
		if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
			`UPDATE billing_invoice_lines SET tax_rate_bps = ?
			 WHERE invoice_id = ? AND tenant_id = ?`,
			tax.RateBPS, inv.ID, tenantID); err != nil {
			return Invoice{}, ErrInternal(err, "Could not apply tax to the invoice lines.")
		}
	}
	return s.Invoice(ctx, tenantID, inv.ID)
}

// IssueInvoice numbers a draft and freezes it. From this point the record is
// immutable: payments and credit notes change the balance, never the invoice.
// An invoice is always issued, even when no payment provider is enabled — the
// tenant can then pay it manually.
func (s *Service) IssueInvoice(ctx context.Context, tenantID, invoiceID, actor, ip string) (Invoice, error) {
	inv, err := s.Invoice(ctx, tenantID, invoiceID)
	if err != nil {
		return Invoice{}, err
	}
	if InvoiceFrozen(inv.State) {
		return Invoice{}, ErrImmutable(inv.Number)
	}
	if !CanTransitionInvoice(inv.State, InvoiceIssued) {
		return Invoice{}, ErrConflict("This invoice cannot be issued from state " + inv.State + ".")
	}
	account, err := s.EnsureAccount(ctx, tenantID)
	if err != nil {
		return Invoice{}, err
	}
	number, err := s.nextDocumentNumber(ctx, "invoice", s.Setting(ctx, SettingInvoicePrefix, DefaultInvoicePrefix))
	if err != nil {
		return Invoice{}, err
	}
	snapshot, err := json.Marshal(buyerSnapshot(account))
	if err != nil {
		return Invoice{}, ErrInternal(err, "Could not record the buyer details.")
	}

	now := s.unix()
	dueDays := s.SettingInt(ctx, SettingDueDays, DefaultDueDays)
	dueAt := s.Now().AddDate(0, 0, int(dueDays)).Unix()
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE billing_invoices SET number = ?, state = ?, issued_at = ?,
		   due_at = ?, buyer_snapshot = ?, updated_at = ?, version = version + 1
		 WHERE id = ? AND tenant_id = ? AND version = ?`,
		number, InvoiceIssued, now, dueAt, string(snapshot), now,
		inv.ID, tenantID, inv.Version); err != nil {
		return Invoice{}, invoiceWriteError(err)
	}

	s.WriteAudit(ctx, AuditRecord{
		TenantID: tenantID, Actor: actor, Action: ActionInvoiceIssued,
		Target: "invoice:" + number, IP: ip,
		Detail: "total_minor=" + itoa(inv.TotalMinor) + " currency=" + inv.Currency,
	})
	s.notify(ctx, tenantID, NotifyInvoiceIssued, map[string]any{
		"invoice_id":  inv.ID,
		"number":      number,
		"total_minor": inv.TotalMinor,
		"total":       FormatMinor(inv.TotalMinor, inv.Currency),
		"currency":    inv.Currency,
		"due_at":      dueAt,
	})
	return s.Invoice(ctx, tenantID, inv.ID)
}

// buyerSnapshot is the frozen copy of the buyer's details stored on an issued
// invoice, so a later address change never rewrites financial history.
func buyerSnapshot(a Account) map[string]any {
	return map[string]any{
		"legal_name":    a.LegalName,
		"billing_email": a.BillingEmail,
		"address_line1": a.AddressLine1,
		"address_line2": a.AddressLine2,
		"city":          a.City,
		"region":        a.Region,
		"postal_code":   a.PostalCode,
		"country":       a.Country,
		"tax_id":        a.TaxID,
		"is_business":   a.IsBusiness,
	}
}

// invoiceWriteError renders a failed invoice write.
func invoiceWriteError(err error) error {
	if database.IsConflict(err) {
		return ErrConflict("The invoice changed while you were editing it; reload and try again.")
	}
	return ErrInternal(err, "Could not update the invoice.")
}

// nextDocumentNumber allocates the next number in a numbering scope. The
// allocation is a compare-and-set against the stored counter, so two
// concurrent issuers can never take the same number.
func (s *Service) nextDocumentNumber(ctx context.Context, scope, prefix string) (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		var next int64
		row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
			`SELECT next_value FROM billing_document_sequences WHERE scope = ?`, scope)
		err := row.Scan(&next)
		if errors.Is(err, sql.ErrNoRows) {
			if _, iErr := s.db.ExecContext(ctx, database.TimeoutWrite,
				`INSERT INTO billing_document_sequences (scope, next_value, updated_at)
				 VALUES (?, 1, ?)`, scope, s.unix()); iErr != nil {
				if !database.IsAlreadyExistsError(iErr) {
					return "", ErrInternal(iErr, "Could not allocate a document number.")
				}
			}
			continue
		}
		if err != nil {
			return "", ErrInternal(err, "Could not allocate a document number.")
		}
		res, err := s.db.ExecContext(ctx, database.TimeoutWrite,
			`UPDATE billing_document_sequences SET next_value = ?, updated_at = ?
			 WHERE scope = ? AND next_value = ?`,
			next+1, s.unix(), scope, next)
		if err != nil {
			return "", ErrInternal(err, "Could not allocate a document number.")
		}
		affected, err := res.RowsAffected()
		if err != nil || affected == 0 {
			continue
		}
		return fmt.Sprintf("%s%06d", prefix, next), nil
	}
	return "", ErrConflict("Could not allocate a document number; try again.")
}

// RaiseProrationInvoice bills the difference owed for an immediate upgrade.
func (s *Service) RaiseProrationInvoice(ctx context.Context, tenantID string, sub Subscription, target Plan, preview ProrationPreview) (Invoice, error) {
	inv, err := s.CreateDraft(ctx, tenantID, sub.ID, s.unix(), sub.PeriodEnd)
	if err != nil {
		return Invoice{}, err
	}
	description := fmt.Sprintf("Prorated change to %s for the remainder of the period", target.Name)
	if _, err := s.AddLine(ctx, tenantID, inv.ID, InvoiceLine{
		Kind:            LineProration,
		Description:     description,
		QuantityMilli:   1000,
		UnitPriceMinor:  preview.DueNowMinor,
		AmountMinor:     preview.DueNowMinor,
		LinePeriodStart: preview.EffectiveAt,
		LinePeriodEnd:   sub.PeriodEnd,
	}); err != nil {
		return Invoice{}, err
	}
	return s.IssueInvoice(ctx, tenantID, inv.ID, "system", "")
}

// GenerateSubscriptionInvoice raises the invoice for one subscription period:
// the plan charge plus any metered overage rolled up for that period. It is
// generated whether or not any payment provider is enabled.
func (s *Service) GenerateSubscriptionInvoice(ctx context.Context, sub Subscription) (Invoice, error) {
	plan, err := s.Plan(ctx, sub.PlanID)
	if err != nil {
		return Invoice{}, err
	}
	inv, err := s.CreateDraft(ctx, sub.TenantID, sub.ID, sub.PeriodStart, sub.PeriodEnd)
	if err != nil {
		return Invoice{}, err
	}
	if sub.PriceMinor > 0 {
		if _, err := s.AddLine(ctx, sub.TenantID, inv.ID, InvoiceLine{
			Kind:            LineSubscription,
			Description:     plan.Name + " subscription",
			QuantityMilli:   sub.Quantity * 1000,
			UnitPriceMinor:  sub.PriceMinor,
			AmountMinor:     sub.PriceMinor * maxInt64(sub.Quantity, 1),
			LinePeriodStart: sub.PeriodStart,
			LinePeriodEnd:   sub.PeriodEnd,
		}); err != nil {
			return Invoice{}, err
		}
	}

	rollups, err := s.PendingRollups(ctx, sub.TenantID, sub.PeriodStart, sub.PeriodEnd)
	if err != nil {
		return Invoice{}, err
	}
	billed := []string{}
	for _, r := range rollups {
		if r.OverageValue <= 0 {
			billed = append(billed, r.ID)
			continue
		}
		meter, mErr := s.Meter(ctx, r.MeterCode)
		if mErr != nil {
			continue
		}
		quota, qErr := s.effectiveQuotaFor(ctx, sub.TenantID, meter.Resource)
		if qErr != nil || quota.unitPrice <= 0 {
			billed = append(billed, r.ID)
			continue
		}
		amount := quota.unitPrice * r.OverageValue
		if _, err := s.AddLine(ctx, sub.TenantID, inv.ID, InvoiceLine{
			Kind:            LineOverage,
			Description:     fmt.Sprintf("%s overage: %d %s", meter.Name, r.OverageValue, meter.Unit),
			QuantityMilli:   r.OverageValue * 1000,
			UnitPriceMinor:  quota.unitPrice,
			AmountMinor:     amount,
			MeterCode:       r.MeterCode,
			LinePeriodStart: r.PeriodStart,
			LinePeriodEnd:   r.PeriodEnd,
		}); err != nil {
			return Invoice{}, err
		}
		billed = append(billed, r.ID)
	}
	if err := s.markRollupsInvoiced(ctx, billed); err != nil {
		return Invoice{}, err
	}

	issued, err := s.IssueInvoice(ctx, sub.TenantID, inv.ID, "system", "")
	if err != nil {
		return Invoice{}, err
	}
	return s.applyAccountBalance(ctx, issued)
}

// applyAccountBalance consumes any credit a tenant is carrying against a
// freshly issued invoice, so a downgrade credit reduces the next bill.
func (s *Service) applyAccountBalance(ctx context.Context, inv Invoice) (Invoice, error) {
	account, err := s.EnsureAccount(ctx, inv.TenantID)
	if err != nil {
		return Invoice{}, err
	}
	credit := account.BalanceMinor
	if credit <= 0 || inv.BalanceDueMinor() <= 0 {
		return inv, nil
	}
	if credit > inv.BalanceDueMinor() {
		credit = inv.BalanceDueMinor()
	}
	now := s.unix()
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_invoices SET credited_minor = credited_minor + ?,
		   updated_at = ?, version = version + 1
		 WHERE id = ? AND tenant_id = ?`, credit, now, inv.ID, inv.TenantID); err != nil {
		return Invoice{}, invoiceWriteError(err)
	}
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_accounts SET balance_minor = balance_minor - ?, updated_at = ?
		 WHERE tenant_id = ?`, credit, now, inv.TenantID); err != nil {
		return Invoice{}, ErrInternal(err, "Could not apply the account balance.")
	}
	return s.settleIfPaid(ctx, inv.TenantID, inv.ID)
}

// RecordPayment adds a settled amount to an invoice and closes it when the
// balance reaches zero. It never mutates the invoice's own figures — only the
// paid total, which is what the state machine reads.
func (s *Service) RecordPayment(ctx context.Context, tenantID, invoiceID string, amountMinor int64, actor, ip string) (Invoice, error) {
	if amountMinor <= 0 {
		return Invoice{}, ErrValidation("A payment amount must be positive.")
	}
	inv, err := s.Invoice(ctx, tenantID, invoiceID)
	if err != nil {
		return Invoice{}, err
	}
	if inv.State == InvoiceDraft {
		return Invoice{}, ErrValidation("A draft invoice cannot be paid; issue it first.")
	}
	now := s.unix()
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE billing_invoices SET paid_minor = paid_minor + ?, updated_at = ?,
		   version = version + 1
		 WHERE id = ? AND tenant_id = ? AND version = ?`,
		amountMinor, now, inv.ID, tenantID, inv.Version); err != nil {
		return Invoice{}, invoiceWriteError(err)
	}
	s.WriteAudit(ctx, AuditRecord{
		TenantID: tenantID, Actor: actor, Action: ActionInvoicePaid,
		Target: "invoice:" + inv.Number, IP: ip,
		Detail: "amount_minor=" + itoa(amountMinor),
	})
	return s.settleIfPaid(ctx, tenantID, inv.ID)
}

// settleIfPaid moves an invoice to paid or partial according to its balance.
func (s *Service) settleIfPaid(ctx context.Context, tenantID, invoiceID string) (Invoice, error) {
	inv, err := s.Invoice(ctx, tenantID, invoiceID)
	if err != nil {
		return Invoice{}, err
	}
	target := InvoicePartial
	paidAt := int64(0)
	if inv.BalanceDueMinor() == 0 {
		target = InvoicePaid
		paidAt = s.unix()
	} else if inv.PaidMinor == 0 && inv.CreditedMinor == 0 {
		return inv, nil
	}
	if inv.State == target || !CanTransitionInvoice(inv.State, target) {
		return inv, nil
	}
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE billing_invoices SET state = ?, paid_at = ?, updated_at = ?,
		   version = version + 1
		 WHERE id = ? AND tenant_id = ? AND version = ?`,
		target, paidAt, s.unix(), inv.ID, tenantID, inv.Version); err != nil {
		return Invoice{}, invoiceWriteError(err)
	}
	if target == InvoicePaid {
		s.notify(ctx, tenantID, NotifyPaymentSucceeded, map[string]any{
			"invoice_id": inv.ID,
			"number":     inv.Number,
			"total":      FormatMinor(inv.TotalMinor, inv.Currency),
		})
	}
	return s.Invoice(ctx, tenantID, inv.ID)
}

// MarkInvoiceState moves an issued invoice between the states the machine
// permits. Draft is deliberately unreachable from here: an issued invoice can
// never be reopened for editing.
func (s *Service) MarkInvoiceState(ctx context.Context, tenantID, invoiceID, target, actor, ip string) (Invoice, error) {
	inv, err := s.Invoice(ctx, tenantID, invoiceID)
	if err != nil {
		return Invoice{}, err
	}
	if target == InvoiceDraft {
		return Invoice{}, ErrImmutable(inv.Number)
	}
	if !CanTransitionInvoice(inv.State, target) {
		return Invoice{}, ErrConflict("An invoice cannot move from " + inv.State + " to " + target + ".")
	}
	voidedAt := inv.VoidedAt
	if target == InvoiceCancelled && voidedAt == 0 {
		voidedAt = s.unix()
	}
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE billing_invoices SET state = ?, voided_at = ?, updated_at = ?,
		   version = version + 1
		 WHERE id = ? AND tenant_id = ? AND version = ?`,
		target, voidedAt, s.unix(), inv.ID, tenantID, inv.Version); err != nil {
		return Invoice{}, invoiceWriteError(err)
	}
	s.WriteAudit(ctx, AuditRecord{
		TenantID: tenantID, Actor: actor, Action: ActionInvoiceIssued,
		Target: "invoice:" + inv.Number, IP: ip,
		Detail: "state=" + target,
	})
	return s.Invoice(ctx, tenantID, inv.ID)
}

// IssueCreditNote adjusts an issued invoice downward. The invoice itself is
// left exactly as it was issued; the credit note carries the adjustment.
func (s *Service) IssueCreditNote(ctx context.Context, tenantID, invoiceID string, amountMinor int64, reason, note, actor, ip string) (CreditNote, error) {
	inv, err := s.Invoice(ctx, tenantID, invoiceID)
	if err != nil {
		return CreditNote{}, err
	}
	if inv.State == InvoiceDraft {
		return CreditNote{}, ErrValidation("A draft invoice is edited directly; a credit note applies only once it is issued.")
	}
	if amountMinor <= 0 {
		return CreditNote{}, ErrValidation("A credit note amount must be positive.")
	}
	outstanding := inv.TotalMinor - inv.CreditedMinor
	if amountMinor > outstanding {
		return CreditNote{}, ErrValidation("A credit note cannot exceed the uncredited amount of the invoice.")
	}
	if reason == "" {
		reason = CreditAdjustment
	}
	number, err := s.nextDocumentNumber(ctx, "credit_note", s.Setting(ctx, SettingCreditPrefix, DefaultCreditPrefix))
	if err != nil {
		return CreditNote{}, err
	}

	taxMinor := int64(0)
	if inv.SubtotalMinor > 0 {
		taxMinor = Prorate(inv.TaxMinor, amountMinor, inv.TotalMinor)
	}
	cn := CreditNote{
		ID:          newID(),
		TenantID:    tenantID,
		InvoiceID:   inv.ID,
		Number:      number,
		Currency:    inv.Currency,
		AmountMinor: amountMinor,
		TaxMinor:    taxMinor,
		Reason:      reason,
		Note:        note,
		IssuedAt:    s.unix(),
		CreatedBy:   actor,
	}
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO billing_credit_notes
		 (id, tenant_id, invoice_id, number, currency, amount_minor, tax_minor,
		  reason, note, issued_at, created_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cn.ID, cn.TenantID, cn.InvoiceID, cn.Number, cn.Currency, cn.AmountMinor,
		cn.TaxMinor, cn.Reason, cn.Note, cn.IssuedAt, cn.CreatedBy); err != nil {
		return CreditNote{}, ErrInternal(err, "Could not issue the credit note.")
	}
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_invoices SET credited_minor = credited_minor + ?,
		   updated_at = ?, version = version + 1
		 WHERE id = ? AND tenant_id = ?`,
		amountMinor, s.unix(), inv.ID, tenantID); err != nil {
		return CreditNote{}, invoiceWriteError(err)
	}

	s.WriteAudit(ctx, AuditRecord{
		TenantID: tenantID, Actor: actor, Action: ActionCreditNoteIssued,
		Target: "credit_note:" + number, IP: ip,
		Detail: "invoice=" + inv.Number + " amount_minor=" + itoa(amountMinor),
	})
	s.notify(ctx, tenantID, NotifyCreditIssued, map[string]any{
		"credit_note_id": cn.ID,
		"number":         number,
		"invoice_number": inv.Number,
		"amount":         FormatMinor(amountMinor, inv.Currency),
	})
	if _, err := s.settleIfPaid(ctx, tenantID, inv.ID); err != nil {
		return CreditNote{}, err
	}
	return cn, nil
}

// ListCreditNotes returns the credit notes raised against a tenant's
// invoices, newest first.
func (s *Service) ListCreditNotes(ctx context.Context, tenantID string) ([]CreditNote, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT id, tenant_id, invoice_id, number, currency, amount_minor,
		        tax_minor, reason, note, issued_at, created_by
		 FROM billing_credit_notes WHERE tenant_id = ? ORDER BY issued_at DESC`,
		tenantID)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the credit notes.")
	}
	defer func() { _ = rows.Close() }()

	out := []CreditNote{}
	for rows.Next() {
		var cn CreditNote
		if err := rows.Scan(&cn.ID, &cn.TenantID, &cn.InvoiceID, &cn.Number,
			&cn.Currency, &cn.AmountMinor, &cn.TaxMinor, &cn.Reason, &cn.Note,
			&cn.IssuedAt, &cn.CreatedBy); err != nil {
			return nil, ErrInternal(err, "Could not read the credit notes.")
		}
		out = append(out, cn)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the credit notes.")
	}
	return out, nil
}

// OpenInvoices returns every unpaid issued invoice across all tenants, for
// the dunning and reminder tasks.
func (s *Service) OpenInvoices(ctx context.Context, dueBefore int64) ([]Invoice, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutReport,
		`SELECT `+invoiceColumns+` FROM billing_invoices
		 WHERE state IN (?, ?, ?, ?) AND due_at > 0 AND due_at <= ?
		 ORDER BY due_at`,
		InvoiceIssued, InvoiceDue, InvoiceOverdue, InvoicePartial, dueBefore)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the open invoices.")
	}
	defer func() { _ = rows.Close() }()

	out := []Invoice{}
	for rows.Next() {
		inv, sErr := scanInvoice(rows)
		if sErr != nil {
			return nil, ErrInternal(sErr, "Could not read the open invoices.")
		}
		out = append(out, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the open invoices.")
	}
	return out, nil
}

// InvoiceCSV renders a tenant's invoice list as CSV rows for export. The
// tenant owns this data and may take it elsewhere at any time.
func (s *Service) InvoiceCSV(ctx context.Context, tenantID string) ([][]string, error) {
	invoices, err := s.ListInvoices(ctx, tenantID, 200, 0)
	if err != nil {
		return nil, err
	}
	rows := [][]string{{
		"number", "state", "currency", "subtotal_minor", "tax_minor",
		"total_minor", "paid_minor", "credited_minor", "issued_at", "due_at",
		"paid_at",
	}}
	for _, inv := range invoices {
		rows = append(rows, []string{
			inv.Number, inv.State, inv.Currency, itoa(inv.SubtotalMinor),
			itoa(inv.TaxMinor), itoa(inv.TotalMinor), itoa(inv.PaidMinor),
			itoa(inv.CreditedMinor), timeText(inv.IssuedAt), timeText(inv.DueAt),
			timeText(inv.PaidAt),
		})
	}
	return rows, nil
}

// timeText renders a Unix second as RFC 3339, or empty for a zero stamp.
func timeText(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

// maxInt64 returns the larger of two values.
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
