package billing

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/webappsgo/cashp/src/billing/provider"
	"github.com/webappsgo/cashp/src/database"
)

// maxWebhookBody caps an inbound delivery. A provider notification is small;
// anything larger is either a mistake or an attempt to exhaust memory.
const maxWebhookBody = 1 << 20

// webhookColumns is the explicit column list for billing_webhook_events.
const webhookColumns = `id, provider, event_id, event_type, state, payload_hash,
	detail, received_at, processed_at`

// WebhookResult reports what happened to one delivery.
type WebhookResult struct {
	Provider  string `json:"provider"`
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	State     string `json:"state"`
	Duplicate bool   `json:"duplicate"`
	Detail    string `json:"detail"`
}

// HandleWebhook authenticates and applies one inbound provider delivery.
//
// The signature is verified before the body is looked at, and a delivery from
// a provider that is not enabled is rejected outright — an attacker cannot
// reach billing logic through a provider an administrator never turned on.
func (s *Service) HandleWebhook(ctx context.Context, providerName string, header http.Header, body io.Reader, ip string) (WebhookResult, error) {
	result := WebhookResult{Provider: providerName, State: WebhookRejected}

	rec, err := s.ProviderByName(ctx, providerName)
	if err != nil {
		return result, err
	}
	if !rec.Enabled {
		s.WriteAudit(ctx, AuditRecord{
			Actor: ActorSystem, Action: ActionWebhookRejected, Provider: providerName,
			Result: ResultDenied, IP: ip, Detail: "provider is not enabled",
		})
		return result, ErrProviderDisabled(providerName)
	}

	raw, err := io.ReadAll(io.LimitReader(body, maxWebhookBody+1))
	if err != nil {
		return result, ErrValidation("The webhook body could not be read.")
	}
	if len(raw) > maxWebhookBody {
		s.rejectWebhook(ctx, providerName, "", ip, "body exceeds the size limit")
		return result, ErrValidation("The webhook body is too large.")
	}

	secret, err := s.WebhookSecret(ctx, providerName)
	if err != nil {
		return result, err
	}
	if secret == "" {
		s.rejectWebhook(ctx, providerName, "", ip, "no webhook secret is configured")
		return result, ErrValidation("No webhook secret is configured for this provider.")
	}

	instance, _, err := s.instance(ctx, rec)
	if err != nil {
		return result, err
	}
	event, err := instance.VerifyWebhook(ctx, header, raw, secret)
	if err != nil {
		s.rejectWebhook(ctx, providerName, "", ip, "signature verification failed")
		return result, ErrForbidden("The webhook signature could not be verified.")
	}

	result.EventID = event.ID
	result.EventType = event.Kind
	hash := sha256.Sum256(raw)
	ledgerID := providerName + ":" + event.ID

	existing, lookupErr := s.webhookEvent(ctx, ledgerID)
	if lookupErr != nil && !isNotFound(lookupErr) {
		return result, lookupErr
	}
	if lookupErr == nil {
		// A provider that never got a 2xx will resend. Replaying an already
		// applied event must not move money twice, so it is acknowledged and
		// dropped here.
		result.Duplicate = true
		result.State = existing.State
		result.Detail = "already processed"
		return result, nil
	}

	if err := s.recordWebhook(ctx, WebhookEvent{
		ID:          ledgerID,
		Provider:    providerName,
		EventID:     event.ID,
		EventType:   event.Kind,
		State:       WebhookReceived,
		PayloadHash: hex.EncodeToString(hash[:]),
		ReceivedAt:  s.unix(),
	}); err != nil {
		if isConflictErr(err) {
			result.Duplicate = true
			result.State = WebhookProcessed
			return result, nil
		}
		return result, err
	}

	s.WriteAudit(ctx, AuditRecord{
		Actor: ActorSystem, Action: ActionWebhookReceived, Provider: providerName,
		Target: "event:" + event.ID, Result: ResultSuccess, IP: ip,
		Detail: event.Kind,
	})

	state, detail, err := s.applyWebhook(ctx, providerName, event)
	if err != nil {
		s.finishWebhook(ctx, ledgerID, WebhookFailed, err.Error())
		result.State = WebhookFailed
		result.Detail = err.Error()
		return result, err
	}
	s.finishWebhook(ctx, ledgerID, state, detail)
	result.State = state
	result.Detail = detail
	return result, nil
}

// applyWebhook maps a normalized event onto cashp's own records. It never
// branches on a provider-specific event name: the driver has already
// translated, so a new provider adds no cases here.
func (s *Service) applyWebhook(ctx context.Context, providerName string, event provider.Event) (string, string, error) {
	switch event.Kind {
	case provider.EventPaymentSucceeded:
		return s.applyPaymentEvent(ctx, providerName, event, PaymentSucceeded)
	case provider.EventPaymentFailed:
		return s.applyPaymentEvent(ctx, providerName, event, PaymentFailed)
	case provider.EventRefunded:
		return s.applyRefundEvent(ctx, providerName, event)
	case provider.EventDisputed:
		return s.applyDisputeEvent(ctx, providerName, event)
	case provider.EventMethodExpiring:
		return s.applyMethodExpiring(ctx, providerName, event)
	default:
		return WebhookIgnored, "no action for " + event.Kind, nil
	}
}

// applyPaymentEvent settles or fails the attempt a provider reference points
// at. The provider is the authority on whether money moved, so an out-of-band
// success recorded here is applied exactly as an inline one would be.
func (s *Service) applyPaymentEvent(ctx context.Context, providerName string, event provider.Event, state string) (string, string, error) {
	attempt, err := s.attemptByReference(ctx, providerName, event.Reference)
	if err != nil {
		if isNotFound(err) {
			return WebhookIgnored, "no matching payment attempt", nil
		}
		return WebhookFailed, "", err
	}
	if attempt.State == state {
		return WebhookProcessed, "attempt already in " + state, nil
	}
	s.completeAttempt(ctx, attempt, state, event.Reference, "", event.Detail)

	if state != PaymentSucceeded {
		s.notify(ctx, attempt.TenantID, NotifyPaymentFailed, map[string]any{
			"amount": FormatMinor(attempt.AmountMinor, attempt.Currency),
			"detail": event.Detail,
		})
		return WebhookProcessed, "attempt marked failed", nil
	}

	amount := attempt.AmountMinor
	if event.AmountMinor > 0 {
		amount = event.AmountMinor
	}
	if _, err := s.RecordPayment(ctx, attempt.TenantID, attempt.InvoiceID, amount, ActorSystem, ""); err != nil {
		return WebhookFailed, "", err
	}
	if sub, sErr := s.ActiveSubscription(ctx, attempt.TenantID); sErr == nil && sub.State == StatePastDue {
		if _, tErr := s.Transition(ctx, sub, EventPaymentRecovered, ActorSystem, "webhook payment recovered"); tErr != nil {
			return WebhookFailed, "", tErr
		}
	}
	return WebhookProcessed, "payment recorded", nil
}

// applyRefundEvent books a credit note for a refund raised at the provider,
// for example from its own dashboard. The invoice itself is never altered.
func (s *Service) applyRefundEvent(ctx context.Context, providerName string, event provider.Event) (string, string, error) {
	attempt, err := s.attemptByReference(ctx, providerName, event.Reference)
	if err != nil {
		if isNotFound(err) {
			return WebhookIgnored, "no matching payment attempt", nil
		}
		return WebhookFailed, "", err
	}
	amount := event.AmountMinor
	if amount <= 0 {
		amount = attempt.AmountMinor
	}
	if s.refundAlreadyBooked(ctx, attempt.ID, amount) {
		return WebhookProcessed, "refund already booked", nil
	}
	note, err := s.IssueCreditNote(ctx, attempt.TenantID, attempt.InvoiceID, amount,
		CreditRefund, "refunded at the provider", ActorSystem, "")
	if err != nil {
		return WebhookFailed, "", err
	}
	kind := RefundPartial
	if amount >= attempt.AmountMinor {
		kind = RefundFull
	}
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO billing_refunds
		 (id, tenant_id, invoice_id, attempt_id, credit_note_id, provider,
		  provider_ref, amount_minor, currency, kind, state, reason, created_at,
		  completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		newID(), attempt.TenantID, attempt.InvoiceID, attempt.ID, note.ID,
		providerName, event.Reference, amount, attempt.Currency, kind,
		PaymentSucceeded, "provider refund", s.unix(), s.unix()); err != nil {
		return WebhookFailed, "", ErrInternal(err, "Could not record the refund.")
	}
	s.notify(ctx, attempt.TenantID, NotifyRefundIssued, map[string]any{
		"amount": FormatMinor(amount, attempt.Currency),
	})
	return WebhookProcessed, "credit note " + note.Number + " issued", nil
}

// applyDisputeEvent marks the disputed invoice and alerts the operator. A
// dispute is never auto-resolved: money is contested, so a person decides.
func (s *Service) applyDisputeEvent(ctx context.Context, providerName string, event provider.Event) (string, string, error) {
	attempt, err := s.attemptByReference(ctx, providerName, event.Reference)
	if err != nil {
		if isNotFound(err) {
			return WebhookIgnored, "no matching payment attempt", nil
		}
		return WebhookFailed, "", err
	}
	if _, err := s.MarkInvoiceState(ctx, attempt.TenantID, attempt.InvoiceID, InvoiceDisputed, ActorSystem, ""); err != nil {
		return WebhookFailed, "", err
	}
	s.WriteAudit(ctx, AuditRecord{
		TenantID: attempt.TenantID, Actor: ActorSystem, Action: ActionWebhookReceived,
		Target: "invoice:" + attempt.InvoiceID, Provider: providerName,
		Result: ResultFailure, Detail: "dispute opened: " + event.Detail,
	})
	return WebhookProcessed, "invoice marked disputed", nil
}

// applyMethodExpiring flags an instrument that is about to stop working, so
// the tenant can replace it before a renewal fails.
func (s *Service) applyMethodExpiring(ctx context.Context, providerName string, event provider.Event) (string, string, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+methodColumns+` FROM billing_payment_methods
		 WHERE provider = ? AND provider_token = ?`, providerName, event.Reference)
	method, err := scanMethod(row)
	if errors.Is(err, sql.ErrNoRows) {
		return WebhookIgnored, "no matching payment method", nil
	}
	if err != nil {
		return WebhookFailed, "", ErrInternal(err, "Could not read the payment method.")
	}
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_payment_methods SET state = ?, updated_at = ?
		 WHERE id = ?`, MethodExpired, s.unix(), method.ID); err != nil {
		return WebhookFailed, "", ErrInternal(err, "Could not flag the payment method.")
	}
	s.notify(ctx, method.TenantID, NotifyPaymentFailed, map[string]any{
		"detail": "the card ending " + method.Last4 + " is expiring",
	})
	return WebhookProcessed, "payment method flagged expiring", nil
}

// attemptByReference finds the charge a provider reference belongs to.
func (s *Service) attemptByReference(ctx context.Context, providerName, reference string) (PaymentAttempt, error) {
	if strings.TrimSpace(reference) == "" {
		return PaymentAttempt{}, ErrNotFound("payment attempt")
	}
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+attemptColumns+` FROM billing_payment_attempts
		 WHERE provider = ? AND provider_ref = ? ORDER BY attempted_at DESC LIMIT 1`,
		providerName, reference)
	a, err := scanAttempt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PaymentAttempt{}, ErrNotFound("payment attempt")
	}
	if err != nil {
		return PaymentAttempt{}, ErrInternal(err, "Could not read the payment attempt.")
	}
	return a, nil
}

// refundAlreadyBooked reports whether this refund has already been recorded,
// which keeps a resent provider notification from double-crediting.
func (s *Service) refundAlreadyBooked(ctx context.Context, attemptID string, amountMinor int64) bool {
	var count int64
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT COUNT(id) FROM billing_refunds
		 WHERE attempt_id = ? AND amount_minor = ?`, attemptID, amountMinor)
	if err := row.Scan(&count); err != nil {
		return false
	}
	return count > 0
}

// webhookEvent reads one ledger entry.
func (s *Service) webhookEvent(ctx context.Context, id string) (WebhookEvent, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+webhookColumns+` FROM billing_webhook_events WHERE id = ?`, id)
	var e WebhookEvent
	err := row.Scan(&e.ID, &e.Provider, &e.EventID, &e.EventType, &e.State,
		&e.PayloadHash, &e.Detail, &e.ReceivedAt, &e.ProcessedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return WebhookEvent{}, ErrNotFound("webhook event")
	}
	if err != nil {
		return WebhookEvent{}, ErrInternal(err, "Could not read the webhook ledger.")
	}
	return e, nil
}

// recordWebhook claims an event id. The primary key is what makes the claim
// atomic: two concurrent deliveries of the same event race here and exactly
// one wins.
func (s *Service) recordWebhook(ctx context.Context, e WebhookEvent) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO billing_webhook_events
		 (id, provider, event_id, event_type, state, payload_hash, detail,
		  received_at, processed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.Provider, e.EventID, e.EventType, e.State, e.PayloadHash,
		e.Detail, e.ReceivedAt, e.ProcessedAt)
	if err == nil {
		return nil
	}
	if database.IsAlreadyExistsError(err) {
		return ErrConflict("That webhook event was already received.")
	}
	return ErrInternal(err, "Could not record the webhook event.")
}

// finishWebhook closes out a ledger entry with its outcome.
func (s *Service) finishWebhook(ctx context.Context, id, state, detail string) {
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_webhook_events SET state = ?, detail = ?, processed_at = ?
		 WHERE id = ?`, state, detail, s.unix(), id); err != nil {
		s.WriteAudit(ctx, AuditRecord{
			Actor: ActorSystem, Action: ActionWebhookReceived, Target: "event:" + id,
			Result: ResultFailure, Detail: "ledger update failed: " + err.Error(),
		})
	}
}

// rejectWebhook audits a delivery that never got as far as the ledger.
func (s *Service) rejectWebhook(ctx context.Context, providerName, eventID, ip, detail string) {
	s.WriteAudit(ctx, AuditRecord{
		Actor: ActorSystem, Action: ActionWebhookRejected, Provider: providerName,
		Target: "event:" + eventID, Result: ResultDenied, IP: ip, Detail: detail,
	})
}

// ListWebhookEvents returns the most recent deliveries for the admin view.
func (s *Service) ListWebhookEvents(ctx context.Context, limit int) ([]WebhookEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+webhookColumns+` FROM billing_webhook_events
		 ORDER BY received_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the webhook ledger.")
	}
	defer func() { _ = rows.Close() }()

	out := []WebhookEvent{}
	for rows.Next() {
		var e WebhookEvent
		if err := rows.Scan(&e.ID, &e.Provider, &e.EventID, &e.EventType, &e.State,
			&e.PayloadHash, &e.Detail, &e.ReceivedAt, &e.ProcessedAt); err != nil {
			return nil, ErrInternal(err, "Could not read the webhook ledger.")
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the webhook ledger.")
	}
	return out, nil
}

// RetryFailedWebhooks reprocesses deliveries that failed to apply, and parks
// the ones that keep failing in the dead letter state for an administrator.
func (s *Service) RetryFailedWebhooks(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+webhookColumns+` FROM billing_webhook_events
		 WHERE state = ? ORDER BY received_at LIMIT 50`, WebhookFailed)
	if err != nil {
		return ErrInternal(err, "Could not read the webhook ledger.")
	}
	pending := []WebhookEvent{}
	for rows.Next() {
		var e WebhookEvent
		if err := rows.Scan(&e.ID, &e.Provider, &e.EventID, &e.EventType, &e.State,
			&e.PayloadHash, &e.Detail, &e.ReceivedAt, &e.ProcessedAt); err != nil {
			_ = rows.Close()
			return ErrInternal(err, "Could not read the webhook ledger.")
		}
		pending = append(pending, e)
	}
	closeErr := rows.Close()
	if err := rows.Err(); err != nil {
		return ErrInternal(err, "Could not read the webhook ledger.")
	}
	if closeErr != nil {
		return ErrInternal(closeErr, "Could not read the webhook ledger.")
	}

	// The original body is not kept — it may carry payer detail cashp has no
	// reason to retain — so a retry re-reads the authoritative state from the
	// provider rather than replaying a stored payload.
	for _, e := range pending {
		state, detail, aErr := s.replayFromProvider(ctx, e)
		if aErr != nil {
			s.finishWebhook(ctx, e.ID, WebhookDeadLetter, aErr.Error())
			continue
		}
		s.finishWebhook(ctx, e.ID, state, detail)
	}
	return nil
}

// replayFromProvider re-reads a payment's live state and applies it.
func (s *Service) replayFromProvider(ctx context.Context, e WebhookEvent) (string, string, error) {
	instance, err := s.ProviderInstance(ctx, e.Provider)
	if err != nil {
		return WebhookDeadLetter, "", err
	}
	attempt, err := s.attemptByEventProvider(ctx, e)
	if err != nil {
		return WebhookIgnored, "no matching payment attempt", nil
	}
	callCtx, cancel := context.WithTimeout(ctx, s.providerTimeout(ctx))
	defer cancel()
	live, err := instance.GetPayment(callCtx, attempt.ProviderRef)
	if err != nil {
		return WebhookDeadLetter, "", ErrUpstream(e.Provider, err)
	}
	return s.applyPaymentEvent(ctx, e.Provider, provider.Event{
		ID:          e.EventID,
		Kind:        e.EventType,
		Reference:   attempt.ProviderRef,
		AmountMinor: live.AmountMinor,
		Currency:    live.Currency,
		Detail:      live.FailureMessage,
	}, mapChargeState(live.State))
}

// attemptByEventProvider finds the most recent attempt a failed delivery for
// this provider could plausibly refer to.
func (s *Service) attemptByEventProvider(ctx context.Context, e WebhookEvent) (PaymentAttempt, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+attemptColumns+` FROM billing_payment_attempts
		 WHERE provider = ? AND provider_ref <> '' AND attempted_at >= ?
		 ORDER BY attempted_at DESC LIMIT 1`,
		e.Provider, e.ReceivedAt-secondsPerDay)
	a, err := scanAttempt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PaymentAttempt{}, ErrNotFound("payment attempt")
	}
	if err != nil {
		return PaymentAttempt{}, ErrInternal(err, "Could not read the payment attempt.")
	}
	return a, nil
}

// WebhookPath is where a provider should be told to deliver its events.
func WebhookPath(providerName string) string {
	return "/webhooks/billing/" + providerName
}
