package billing

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/billing/provider"
	"github.com/webappsgo/cashp/src/database"
)

// Payment method states.
const (
	MethodActive  = "active"
	MethodExpired = "expired"
	MethodRemoved = "removed"
)

// methodColumns is the explicit column list for billing_payment_methods.
const methodColumns = `id, tenant_id, account_id, provider, provider_token,
	provider_customer, kind, brand, last4, exp_month, exp_year, holder_name,
	country, is_default, state, created_at, updated_at`

// attemptColumns is the explicit column list for billing_payment_attempts.
const attemptColumns = `id, tenant_id, account_id, invoice_id, method_id, provider,
	idempotency_key, amount_minor, currency, state, provider_ref, failure_code,
	failure_message, attempt_number, attempted_at, completed_at`

// scanMethod reads one row in methodColumns order.
func scanMethod(sc interface{ Scan(...any) error }) (PaymentMethod, error) {
	var m PaymentMethod
	var isDefault int64
	if err := sc.Scan(&m.ID, &m.TenantID, &m.AccountID, &m.Provider,
		&m.ProviderToken, &m.ProviderCustomer, &m.Kind, &m.Brand, &m.Last4,
		&m.ExpMonth, &m.ExpYear, &m.HolderName, &m.Country, &isDefault,
		&m.State, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return PaymentMethod{}, err
	}
	m.IsDefault = isDefault != 0
	return m, nil
}

// scanAttempt reads one row in attemptColumns order.
func scanAttempt(sc interface{ Scan(...any) error }) (PaymentAttempt, error) {
	var a PaymentAttempt
	if err := sc.Scan(&a.ID, &a.TenantID, &a.AccountID, &a.InvoiceID, &a.MethodID,
		&a.Provider, &a.IdempotencyKey, &a.AmountMinor, &a.Currency, &a.State,
		&a.ProviderRef, &a.FailureCode, &a.FailureMessage, &a.AttemptNumber,
		&a.AttemptedAt, &a.CompletedAt); err != nil {
		return PaymentAttempt{}, err
	}
	return a, nil
}

// PaymentMethod returns one stored instrument belonging to a tenant.
func (s *Service) PaymentMethod(ctx context.Context, tenantID, methodID string) (PaymentMethod, error) {
	if strings.TrimSpace(tenantID) == "" {
		return PaymentMethod{}, ErrValidation("billing: a tenant is required")
	}
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+methodColumns+` FROM billing_payment_methods
		 WHERE id = ? AND tenant_id = ?`, methodID, tenantID)
	m, err := scanMethod(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PaymentMethod{}, ErrNotFound("payment method")
	}
	if err != nil {
		return PaymentMethod{}, ErrInternal(err, "Could not read the payment method.")
	}
	return m, nil
}

// ListPaymentMethods returns a tenant's usable instruments.
func (s *Service) ListPaymentMethods(ctx context.Context, tenantID string) ([]PaymentMethod, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+methodColumns+` FROM billing_payment_methods
		 WHERE tenant_id = ? AND state <> ? ORDER BY is_default DESC, created_at`,
		tenantID, MethodRemoved)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the payment methods.")
	}
	defer func() { _ = rows.Close() }()

	out := []PaymentMethod{}
	for rows.Next() {
		m, sErr := scanMethod(rows)
		if sErr != nil {
			return nil, ErrInternal(sErr, "Could not read the payment methods.")
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the payment methods.")
	}
	return out, nil
}

// AddPaymentMethod stores an instrument for a tenant. The token comes from
// the provider's own browser element: the card number and security code go
// from the tenant's browser to the provider and never touch this server, so
// there is nothing here to leak.
func (s *Service) AddPaymentMethod(ctx context.Context, tenantID, providerName, token, holderName, actor, ip string) (PaymentMethod, error) {
	if strings.TrimSpace(token) == "" {
		return PaymentMethod{}, ErrValidation("A payment method token is required.")
	}
	instance, err := s.ProviderInstance(ctx, providerName)
	if err != nil {
		return PaymentMethod{}, err
	}
	account, err := s.EnsureAccount(ctx, tenantID)
	if err != nil {
		return PaymentMethod{}, err
	}

	customerRef := ""
	existing, err := s.ListPaymentMethods(ctx, tenantID)
	if err != nil {
		return PaymentMethod{}, err
	}
	for _, m := range existing {
		if m.Provider == providerName && m.ProviderCustomer != "" {
			customerRef = m.ProviderCustomer
			break
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, s.providerTimeout(ctx))
	defer cancel()
	stored, err := instance.StoreMethod(callCtx, provider.MethodRequest{
		TenantID:     tenantID,
		CustomerRef:  customerRef,
		Token:        token,
		HolderName:   holderName,
		BillingEmail: account.BillingEmail,
		Country:      account.Country,
	})
	if err != nil {
		return PaymentMethod{}, ErrUpstream(providerName, err)
	}

	now := s.unix()
	method := PaymentMethod{
		ID:               newID(),
		TenantID:         tenantID,
		AccountID:        account.ID,
		Provider:         providerName,
		ProviderToken:    stored.Token,
		ProviderCustomer: stored.CustomerRef,
		Kind:             stored.Kind,
		Brand:            stored.Brand,
		Last4:            stored.Last4,
		ExpMonth:         stored.ExpMonth,
		ExpYear:          stored.ExpYear,
		HolderName:       holderName,
		Country:          stored.Country,
		IsDefault:        len(existing) == 0,
		State:            MethodActive,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if method.Kind == "" {
		method.Kind = "card"
	}
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO billing_payment_methods
		 (id, tenant_id, account_id, provider, provider_token, provider_customer,
		  kind, brand, last4, exp_month, exp_year, holder_name, country,
		  is_default, state, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		method.ID, method.TenantID, method.AccountID, method.Provider,
		method.ProviderToken, method.ProviderCustomer, method.Kind, method.Brand,
		method.Last4, method.ExpMonth, method.ExpYear, method.HolderName,
		method.Country, boolToInt(method.IsDefault), method.State,
		method.CreatedAt, method.UpdatedAt); err != nil {
		return PaymentMethod{}, ErrInternal(err, "Could not store the payment method.")
	}
	if method.IsDefault {
		if err := s.SetDefaultMethod(ctx, tenantID, method.ID); err != nil {
			return PaymentMethod{}, err
		}
	}

	s.WriteAudit(ctx, AuditRecord{
		TenantID: tenantID, Actor: actor, Action: ActionMethodAdded,
		Target: "method:" + method.ID, Provider: providerName, IP: ip,
		Detail: method.Brand + " ending " + method.Last4,
	})
	return method, nil
}

// RemovePaymentMethod detaches an instrument at the provider and marks it
// removed here. The row is kept so historical payment attempts still resolve.
func (s *Service) RemovePaymentMethod(ctx context.Context, tenantID, methodID, actor, ip string) error {
	method, err := s.PaymentMethod(ctx, tenantID, methodID)
	if err != nil {
		return err
	}
	if instance, iErr := s.ProviderInstance(ctx, method.Provider); iErr == nil {
		callCtx, cancel := context.WithTimeout(ctx, s.providerTimeout(ctx))
		// A provider-side failure must not strand the instrument in cashp: the
		// tenant asked for it to go, so it goes either way and the failure is
		// audited.
		dErr := instance.DeleteMethod(callCtx, method.ProviderToken)
		cancel()
		if dErr != nil {
			s.WriteAudit(ctx, AuditRecord{
				TenantID: tenantID, Actor: actor, Action: ActionMethodRemoved,
				Target: "method:" + methodID, Provider: method.Provider, IP: ip,
				Result: ResultFailure, Detail: dErr.Error(),
			})
		}
	}
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_payment_methods SET state = ?, provider_token = '',
		   is_default = 0, updated_at = ?
		 WHERE id = ? AND tenant_id = ?`,
		MethodRemoved, s.unix(), methodID, tenantID); err != nil {
		return ErrInternal(err, "Could not remove the payment method.")
	}
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_accounts SET default_method_id = '', updated_at = ?
		 WHERE tenant_id = ? AND default_method_id = ?`,
		s.unix(), tenantID, methodID); err != nil {
		return ErrInternal(err, "Could not clear the default payment method.")
	}
	s.WriteAudit(ctx, AuditRecord{
		TenantID: tenantID, Actor: actor, Action: ActionMethodRemoved,
		Target: "method:" + methodID, Provider: method.Provider, IP: ip,
	})
	return nil
}

// providerTimeout is how long any single provider call may take.
func (s *Service) providerTimeout(ctx context.Context) time.Duration {
	seconds := s.SettingInt(ctx, SettingProviderTimeout, int64(DefaultProviderTimeout/time.Second))
	if seconds <= 0 {
		return DefaultProviderTimeout
	}
	return time.Duration(seconds) * time.Second
}

// AttemptByKey returns a previous attempt with the same idempotency key.
func (s *Service) AttemptByKey(ctx context.Context, key string) (PaymentAttempt, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+attemptColumns+` FROM billing_payment_attempts
		 WHERE idempotency_key = ?`, key)
	a, err := scanAttempt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PaymentAttempt{}, ErrNotFound("payment attempt")
	}
	if err != nil {
		return PaymentAttempt{}, ErrInternal(err, "Could not read the payment attempt.")
	}
	return a, nil
}

// ListAttempts returns a tenant's charge history, newest first.
func (s *Service) ListAttempts(ctx context.Context, tenantID string, limit int) ([]PaymentAttempt, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+attemptColumns+` FROM billing_payment_attempts
		 WHERE tenant_id = ? ORDER BY attempted_at DESC LIMIT ?`, tenantID, limit)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the payment history.")
	}
	defer func() { _ = rows.Close() }()

	out := []PaymentAttempt{}
	for rows.Next() {
		a, sErr := scanAttempt(rows)
		if sErr != nil {
			return nil, ErrInternal(sErr, "Could not read the payment history.")
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the payment history.")
	}
	return out, nil
}

// ChargeInvoice attempts to settle an invoice through the tenant's stored
// instruments, walking the enabled providers in failover order. It is
// idempotent on idempotencyKey: replaying the same key returns the original
// attempt instead of taking the money twice.
//
// With no provider enabled the invoice is left issued and unpaid, which is a
// supported way to run cashp: the tenant pays by whatever means the operator
// arranges and an administrator records the payment.
func (s *Service) ChargeInvoice(ctx context.Context, tenantID, invoiceID, idempotencyKey, actor, ip string) (PaymentAttempt, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return PaymentAttempt{}, ErrValidation("An idempotency key is required for a charge.")
	}
	if prior, err := s.AttemptByKey(ctx, idempotencyKey); err == nil {
		if prior.TenantID != tenantID {
			return PaymentAttempt{}, ErrForbidden("That idempotency key belongs to another account.")
		}
		return prior, nil
	} else if !isNotFound(err) {
		return PaymentAttempt{}, err
	}

	inv, err := s.Invoice(ctx, tenantID, invoiceID)
	if err != nil {
		return PaymentAttempt{}, err
	}
	amount := inv.BalanceDueMinor()
	if amount <= 0 {
		return PaymentAttempt{}, ErrValidation("That invoice has nothing left to pay.")
	}
	if inv.State == InvoiceDraft {
		return PaymentAttempt{}, ErrValidation("A draft invoice cannot be charged; issue it first.")
	}

	methods, err := s.ListPaymentMethods(ctx, tenantID)
	if err != nil {
		return PaymentAttempt{}, err
	}
	if len(methods) == 0 {
		return PaymentAttempt{}, ErrValidation("No payment method is stored for this account.")
	}
	enabled, err := s.EnabledProviders(ctx)
	if err != nil {
		return PaymentAttempt{}, err
	}
	if len(enabled) == 0 {
		return PaymentAttempt{}, ErrNoProvider()
	}

	attemptNumber := s.nextAttemptNumber(ctx, inv.ID)
	var last PaymentAttempt
	var lastErr error
	failoverAllowed := s.Setting(ctx, SettingFailoverMode, DefaultFailoverMode) != FailoverManual

	for index, rec := range enabled {
		method := pickMethod(methods, rec.Name)
		if method.ID == "" {
			continue
		}
		key := idempotencyKey
		if index > 0 {
			// A retry through a different provider is a distinct charge and
			// needs its own key, or the second provider would be handed a key
			// that means nothing to it.
			key = idempotencyKey + ":" + rec.Name
		}
		attempt, err := s.chargeThrough(ctx, rec, inv, method, key, amount, attemptNumber, actor, ip)
		last = attempt
		lastErr = err
		if err == nil && attempt.State == PaymentSucceeded {
			if _, pErr := s.RecordPayment(ctx, tenantID, inv.ID, amount, actor, ip); pErr != nil {
				return attempt, pErr
			}
			return attempt, nil
		}
		if !failoverAllowed {
			break
		}
		attemptNumber++
	}

	s.notify(ctx, tenantID, NotifyPaymentFailed, map[string]any{
		"invoice_id": inv.ID,
		"number":     inv.Number,
		"amount":     FormatMinor(amount, inv.Currency),
	})
	if lastErr != nil {
		return last, lastErr
	}
	return last, nil
}

// pickMethod returns the tenant's preferred instrument for one provider.
func pickMethod(methods []PaymentMethod, providerName string) PaymentMethod {
	for _, m := range methods {
		if m.Provider == providerName && m.IsDefault && m.State == MethodActive {
			return m
		}
	}
	for _, m := range methods {
		if m.Provider == providerName && m.State == MethodActive {
			return m
		}
	}
	return PaymentMethod{}
}

// nextAttemptNumber counts how many times an invoice has already been tried.
func (s *Service) nextAttemptNumber(ctx context.Context, invoiceID string) int64 {
	var count int64
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT COUNT(id) FROM billing_payment_attempts WHERE invoice_id = ?`, invoiceID)
	if err := row.Scan(&count); err != nil {
		return 1
	}
	return count + 1
}

// chargeThrough runs one charge against one provider and records the attempt
// whatever the outcome, so the ledger shows every try.
func (s *Service) chargeThrough(ctx context.Context, rec ProviderRecord, inv Invoice, method PaymentMethod, key string, amount, attemptNumber int64, actor, ip string) (PaymentAttempt, error) {
	attempt := PaymentAttempt{
		ID:             newID(),
		TenantID:       inv.TenantID,
		AccountID:      inv.AccountID,
		InvoiceID:      inv.ID,
		MethodID:       method.ID,
		Provider:       rec.Name,
		IdempotencyKey: key,
		AmountMinor:    amount,
		Currency:       inv.Currency,
		State:          PaymentPending,
		AttemptNumber:  attemptNumber,
		AttemptedAt:    s.unix(),
	}
	if err := s.insertAttempt(ctx, attempt); err != nil {
		return PaymentAttempt{}, err
	}
	s.WriteAudit(ctx, AuditRecord{
		TenantID: inv.TenantID, Actor: actor, Action: ActionPaymentAttempted,
		Target: "invoice:" + inv.Number, Provider: rec.Name, IP: ip,
		Detail: "amount_minor=" + itoa(amount) + " attempt=" + itoa(attemptNumber),
	})

	instance, _, err := s.instance(ctx, rec)
	if err != nil {
		s.completeAttempt(ctx, attempt, PaymentFailed, "", "provider_unavailable", err.Error())
		return attempt, err
	}
	if !instance.Capabilities().Supports(inv.Currency) {
		detail := "This provider does not accept " + inv.Currency + "."
		s.completeAttempt(ctx, attempt, PaymentFailed, "", "currency_unsupported", detail)
		return attempt, ErrValidation(detail)
	}

	callCtx, cancel := context.WithTimeout(ctx, s.providerTimeout(ctx))
	defer cancel()
	result, err := instance.Charge(callCtx, provider.ChargeRequest{
		IdempotencyKey: key,
		AmountMinor:    amount,
		Currency:       inv.Currency,
		MethodToken:    method.ProviderToken,
		CustomerRef:    method.ProviderCustomer,
		Description:    "cashp invoice " + inv.Number,
		InvoiceNumber:  inv.Number,
		Capture:        true,
	})
	if err != nil {
		s.completeAttempt(ctx, attempt, PaymentFailed, "", "provider_error", err.Error())
		s.recordProviderHealth(ctx, rec, HealthDegraded, err.Error())
		s.WriteAudit(ctx, AuditRecord{
			TenantID: inv.TenantID, Actor: actor, Action: ActionPaymentFailed,
			Target: "invoice:" + inv.Number, Provider: rec.Name, IP: ip,
			Result: ResultFailure, Detail: err.Error(),
		})
		return attempt, ErrUpstream(rec.Name, err)
	}

	state := mapChargeState(result.State)
	attempt.State = state
	attempt.ProviderRef = result.Reference
	attempt.FailureCode = result.FailureCode
	attempt.FailureMessage = result.FailureMessage
	s.completeAttempt(ctx, attempt, state, result.Reference, result.FailureCode, result.FailureMessage)

	action := ActionPaymentSucceeded
	auditResult := ResultSuccess
	if state != PaymentSucceeded {
		action = ActionPaymentFailed
		auditResult = ResultFailure
	}
	s.WriteAudit(ctx, AuditRecord{
		TenantID: inv.TenantID, Actor: actor, Action: action,
		Target: "invoice:" + inv.Number, Provider: rec.Name, IP: ip,
		Result: auditResult, Code: result.FailureCode,
		Detail: "amount_minor=" + itoa(amount),
	})
	if state == PaymentSucceeded {
		s.recordProviderHealth(ctx, rec, HealthHealthy, "")
	}
	return attempt, nil
}

// mapChargeState translates a driver's outcome into a stored attempt state.
func mapChargeState(state string) string {
	switch state {
	case provider.StateSucceeded:
		return PaymentSucceeded
	case provider.StateAuthorized:
		return PaymentAuthorized
	case provider.StatePending:
		return PaymentPending
	default:
		return PaymentFailed
	}
}

// insertAttempt writes a new attempt row. A collision on the idempotency key
// means a concurrent request already claimed it, which is exactly the
// double-charge this key exists to prevent.
func (s *Service) insertAttempt(ctx context.Context, a PaymentAttempt) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO billing_payment_attempts
		 (id, tenant_id, account_id, invoice_id, method_id, provider,
		  idempotency_key, amount_minor, currency, state, attempt_number,
		  attempted_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.TenantID, a.AccountID, a.InvoiceID, a.MethodID, a.Provider,
		a.IdempotencyKey, a.AmountMinor, a.Currency, a.State, a.AttemptNumber,
		a.AttemptedAt)
	if err == nil {
		return nil
	}
	if database.IsAlreadyExistsError(err) {
		return ErrConflict("That payment is already in progress.")
	}
	return ErrInternal(err, "Could not record the payment attempt.")
}

// completeAttempt closes out an attempt row with its outcome.
func (s *Service) completeAttempt(ctx context.Context, a PaymentAttempt, state, reference, code, message string) {
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_payment_attempts SET state = ?, provider_ref = ?,
		   failure_code = ?, failure_message = ?, completed_at = ?
		 WHERE id = ?`,
		state, reference, code, message, s.unix(), a.ID); err != nil {
		s.WriteAudit(ctx, AuditRecord{
			TenantID: a.TenantID, Action: ActionPaymentAttempted,
			Target: "attempt:" + a.ID, Result: ResultFailure,
			Detail: "attempt write failed: " + err.Error(),
		})
	}
}

// RefundPayment returns money to the payer and books the matching credit
// note. The original invoice is never altered.
func (s *Service) RefundPayment(ctx context.Context, tenantID, invoiceID string, amountMinor int64, reason, actor, ip string) (Refund, error) {
	inv, err := s.Invoice(ctx, tenantID, invoiceID)
	if err != nil {
		return Refund{}, err
	}
	if amountMinor <= 0 || amountMinor > inv.PaidMinor {
		return Refund{}, ErrValidation("A refund cannot exceed what was actually paid.")
	}
	attempt, err := s.settledAttempt(ctx, tenantID, inv.ID)
	if err != nil {
		return Refund{}, err
	}
	instance, err := s.ProviderInstance(ctx, attempt.Provider)
	if err != nil {
		return Refund{}, err
	}

	callCtx, cancel := context.WithTimeout(ctx, s.providerTimeout(ctx))
	defer cancel()
	result, err := instance.Refund(callCtx, provider.RefundRequest{
		IdempotencyKey: "refund:" + attempt.ID + ":" + itoa(amountMinor),
		Reference:      attempt.ProviderRef,
		AmountMinor:    amountMinor,
		Currency:       inv.Currency,
		Reason:         reason,
	})
	if err != nil {
		return Refund{}, ErrUpstream(attempt.Provider, err)
	}

	note, err := s.IssueCreditNote(ctx, tenantID, inv.ID, amountMinor, CreditRefund, reason, actor, ip)
	if err != nil {
		return Refund{}, err
	}
	kind := RefundPartial
	if amountMinor == inv.TotalMinor {
		kind = RefundFull
	}
	refund := Refund{
		ID:           newID(),
		TenantID:     tenantID,
		InvoiceID:    inv.ID,
		AttemptID:    attempt.ID,
		CreditNoteID: note.ID,
		Provider:     attempt.Provider,
		ProviderRef:  result.Reference,
		AmountMinor:  amountMinor,
		Currency:     inv.Currency,
		Kind:         kind,
		State:        result.State,
		Reason:       reason,
		CreatedAt:    s.unix(),
	}
	if result.State == provider.StateSucceeded {
		refund.State = PaymentSucceeded
		refund.CompletedAt = s.unix()
	}
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO billing_refunds
		 (id, tenant_id, invoice_id, attempt_id, credit_note_id, provider,
		  provider_ref, amount_minor, currency, kind, state, reason, created_at,
		  completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		refund.ID, refund.TenantID, refund.InvoiceID, refund.AttemptID,
		refund.CreditNoteID, refund.Provider, refund.ProviderRef,
		refund.AmountMinor, refund.Currency, refund.Kind, refund.State,
		refund.Reason, refund.CreatedAt, refund.CompletedAt); err != nil {
		return Refund{}, ErrInternal(err, "Could not record the refund.")
	}

	s.WriteAudit(ctx, AuditRecord{
		TenantID: tenantID, Actor: actor, Action: ActionRefundIssued,
		Target: "invoice:" + inv.Number, Provider: attempt.Provider, IP: ip,
		Detail: "amount_minor=" + itoa(amountMinor),
	})
	s.notify(ctx, tenantID, NotifyRefundIssued, map[string]any{
		"invoice_number": inv.Number,
		"amount":         FormatMinor(amountMinor, inv.Currency),
	})
	return refund, nil
}

// settledAttempt finds the successful charge behind an invoice.
func (s *Service) settledAttempt(ctx context.Context, tenantID, invoiceID string) (PaymentAttempt, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+attemptColumns+` FROM billing_payment_attempts
		 WHERE tenant_id = ? AND invoice_id = ? AND state = ?
		 ORDER BY completed_at DESC LIMIT 1`,
		tenantID, invoiceID, PaymentSucceeded)
	a, err := scanAttempt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PaymentAttempt{}, ErrValidation("That invoice has no settled payment to refund.")
	}
	if err != nil {
		return PaymentAttempt{}, ErrInternal(err, "Could not read the payment attempt.")
	}
	return a, nil
}

// AttemptsForInvoice returns every attempt made against one invoice.
func (s *Service) AttemptsForInvoice(ctx context.Context, tenantID, invoiceID string) ([]PaymentAttempt, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+attemptColumns+` FROM billing_payment_attempts
		 WHERE tenant_id = ? AND invoice_id = ? ORDER BY attempted_at`,
		tenantID, invoiceID)
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

// PaymentCSV renders a tenant's payment history for export.
func (s *Service) PaymentCSV(ctx context.Context, tenantID string) ([][]string, error) {
	attempts, err := s.ListAttempts(ctx, tenantID, 200)
	if err != nil {
		return nil, err
	}
	rows := [][]string{{
		"attempted_at", "provider", "amount_minor", "currency", "state",
		"failure_code", "invoice_id",
	}}
	for _, a := range attempts {
		rows = append(rows, []string{
			timeText(a.AttemptedAt), a.Provider, itoa(a.AmountMinor), a.Currency,
			a.State, a.FailureCode, a.InvoiceID,
		})
	}
	return rows, nil
}
