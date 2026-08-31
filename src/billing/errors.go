package billing

import (
	"fmt"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// Billing errors are always returned as *errors.Error so the API layer
// renders them with the project's standard envelope, status code and machine
// code. Nothing in this package returns a bare string error to a handler.

// ErrDisabled reports that billing itself is not configured. It is not a
// failure: a cashp install with no billing configured serves every feature
// with no quotas at all.
func ErrDisabled() *apperr.Error {
	return apperr.New(apperr.CodeUnavailable, 503, "Billing is not configured on this server.")
}

// ErrProviderDisabled reports an operation attempted against a payment
// provider that an administrator has not enabled.
func ErrProviderDisabled(name string) *apperr.Error {
	return apperr.New(apperr.CodeUnavailable, 503,
		fmt.Sprintf("Payment provider %q is not enabled.", name)).
		WithDetails(map[string]any{"provider": name})
}

// ErrNoProvider reports that no payment provider is enabled at all, so the
// invoice was still raised but cannot be charged automatically.
func ErrNoProvider() *apperr.Error {
	return apperr.New(apperr.CodeUnavailable, 503,
		"No payment provider is enabled; the invoice was issued for manual payment.")
}

// ErrNotFound reports a missing billing record. The kind is echoed so the
// caller can tell an unknown invoice from an unknown plan.
func ErrNotFound(kind string) *apperr.Error {
	return apperr.New(apperr.CodeNotFound, 404, fmt.Sprintf("No such %s.", kind))
}

// ErrValidation reports rejected input.
func ErrValidation(message string) *apperr.Error {
	return apperr.New(apperr.CodeValidation, 422, message)
}

// ErrConflict reports a uniqueness or concurrent-update collision.
func ErrConflict(message string) *apperr.Error {
	return apperr.New(apperr.CodeConflict, 409, message)
}

// ErrForbidden reports a caller reaching for another tenant's records or an
// operation their role does not permit.
func ErrForbidden(message string) *apperr.Error {
	return apperr.New(apperr.CodeForbidden, 403, message)
}

// ErrUnauthorized reports a request that carries no usable identity.
func ErrUnauthorized(message string) *apperr.Error {
	return apperr.New(apperr.CodeUnauthorized, 401, message)
}

// ErrImmutable reports an attempt to change an invoice that has been issued.
// The correct action is always a credit note instead.
func ErrImmutable(number string) *apperr.Error {
	return apperr.New(apperr.CodeConflict, 409,
		fmt.Sprintf("Invoice %s has been issued and cannot be changed; raise a credit note instead.", number)).
		WithDetails(map[string]any{"invoice_number": number})
}

// ErrQuota reports a resource ceiling that would be crossed. The details map
// carries the numbers the UI needs to explain the refusal, and never any
// wording that implies a product feature is unavailable: cashp caps how much
// a tenant may use, never which features exist.
func ErrQuota(resource string, limit, used, requested int64) *apperr.Error {
	return apperr.New(apperr.CodeQuotaExceeded, 429,
		fmt.Sprintf("Your plan allows %d %s and %d %s are already in use.",
			limit, ResourceUnit(resource), used, ResourceUnit(resource))).
		WithDetails(map[string]any{
			"resource":  resource,
			"limit":     limit,
			"used":      used,
			"requested": requested,
			"unit":      ResourceUnit(resource),
		})
}

// ErrInternal wraps an unexpected failure, keeping the cause for the log and
// out of the response body.
func ErrInternal(cause error, message string) *apperr.Error {
	return apperr.Wrap(cause, apperr.CodeInternal, 500, message)
}

// isNotFound reports whether an error is a billing not-found error.
func isNotFound(err error) bool {
	return apperr.Is(err, apperr.CodeNotFound)
}

// isConflictErr reports whether an error is a billing conflict, which is how
// a lost race on a unique key surfaces to the caller.
func isConflictErr(err error) bool {
	return apperr.Is(err, apperr.CodeConflict)
}

// ErrUpstream reports a payment provider that failed or timed out. Billing
// never suspends a tenant because of one of these: the caller records the
// failure and lets dunning retry.
func ErrUpstream(provider string, cause error) *apperr.Error {
	return apperr.Wrap(cause, apperr.CodeUnavailable, 503,
		fmt.Sprintf("Payment provider %q is not responding.", provider)).
		WithDetails(map[string]any{"provider": provider})
}
