package auth

import (
	"net/http"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// Anti-enumeration responses. Every failed credential check, unknown account, locked
// account and disabled account funnels into ErrInvalidCredentials so that the response
// body, the error code and the HTTP status are byte-identical in all four cases.
// Nothing here ever names an account, an email address, or a reason.
func ErrInvalidCredentials() *apperr.Error {
	return apperr.New(apperr.CodeUnauthorized, http.StatusUnauthorized, "Invalid credentials")
}

// ErrTwoFactorRequired asks for the second factor without confirming anything else.
func ErrTwoFactorRequired() *apperr.Error {
	return apperr.New(apperr.CodeTwoFactorRequired, http.StatusUnauthorized, "Two-factor authentication code required")
}

// ErrTwoFactorInvalid is returned for a wrong or replayed TOTP code.
func ErrTwoFactorInvalid() *apperr.Error {
	return apperr.New(apperr.CodeTwoFactorInvalid, http.StatusUnauthorized, "Invalid authentication code")
}

// ErrUnauthenticated is returned when no usable session or token was presented.
func ErrUnauthenticated() *apperr.Error {
	return apperr.New(apperr.CodeUnauthorized, http.StatusUnauthorized, "Authentication required")
}

// ErrSessionExpired is returned when a session or token existed but is past its lifetime.
func ErrSessionExpired() *apperr.Error {
	return apperr.New(apperr.CodeTokenExpired, http.StatusUnauthorized, "Your session has expired, please sign in again")
}

// ErrForbidden is the single response for every authorization failure. It is also the
// response for a resource that exists but belongs to another tenant, so that probing
// cannot distinguish "not yours" from "does not exist" on shared-namespace routes.
func ErrForbidden() *apperr.Error {
	return apperr.New(apperr.CodeForbidden, http.StatusForbidden, "You do not have permission to perform this action")
}

// ErrNotFound is the generic missing-resource response.
func ErrNotFound(what string) *apperr.Error {
	return apperr.New(apperr.CodeNotFound, http.StatusNotFound, what+" not found")
}

// ErrCSRF is returned when a state-changing request carries a missing or invalid token.
func ErrCSRF() *apperr.Error {
	return apperr.New(apperr.CodeForbidden, http.StatusForbidden, "Your session could not be verified, please reload the page and try again")
}

// ErrRateLimited is returned once a limiter rejects the caller.
func ErrRateLimited(retryAfterSeconds int) *apperr.Error {
	return apperr.New(apperr.CodeRateLimited, http.StatusTooManyRequests, "Too many attempts, please try again later").
		WithDetails(map[string]any{"retry_after": retryAfterSeconds})
}

// ErrValidation reports a syntactic problem with a named field.
func ErrValidation(field, message string) *apperr.Error {
	return apperr.New(apperr.CodeValidation, http.StatusBadRequest, message).
		WithDetails(map[string]any{"field": field})
}

// ErrNameUnavailable is the deliberately vague answer for a username or org slug that is
// taken, tombstoned, or otherwise unusable. It never distinguishes those cases.
func ErrNameUnavailable(field string) *apperr.Error {
	return apperr.New(apperr.CodeConflict, http.StatusConflict, "That name is unavailable").
		WithDetails(map[string]any{"field": field})
}

// ErrNameReserved is the only specific availability answer, given for blocklisted names.
// Revealing this is safe because the blocklist is static and public.
func ErrNameReserved(field string) *apperr.Error {
	return apperr.New(apperr.CodeConflict, http.StatusConflict, "That name is reserved").
		WithDetails(map[string]any{"field": field})
}

// ErrRegistrationClosed is returned when the registration mode forbids self-signup.
func ErrRegistrationClosed() *apperr.Error {
	return apperr.New(apperr.CodeForbidden, http.StatusForbidden, "Registration is currently closed")
}

// ErrInviteRequired is returned when registration requires a valid invite code.
func ErrInviteRequired() *apperr.Error {
	return apperr.New(apperr.CodeForbidden, http.StatusForbidden, "An invitation is required to register")
}

// ErrInviteInvalid covers unknown, expired, revoked and exhausted invite codes alike.
func ErrInviteInvalid() *apperr.Error {
	return apperr.New(apperr.CodeValidation, http.StatusBadRequest, "That invitation is no longer valid")
}

// ErrOrgCreationClosed is returned when the org creation mode forbids the request.
func ErrOrgCreationClosed() *apperr.Error {
	return apperr.New(apperr.CodeForbidden, http.StatusForbidden, "Organization creation is currently closed")
}

// ErrLastOwner prevents an organization from being left without an owner.
func ErrLastOwner() *apperr.Error {
	return apperr.New(apperr.CodeConflict, http.StatusConflict, "An organization must always have at least one owner")
}

// ErrQuota is returned when a per-owner limit is reached.
func ErrQuota(message string) *apperr.Error {
	return apperr.New(apperr.CodeQuotaExceeded, http.StatusForbidden, message)
}

// ErrFeatureDisabled is returned when a route is reachable but its feature is off.
func ErrFeatureDisabled(feature string) *apperr.Error {
	return apperr.New(apperr.CodeForbidden, http.StatusForbidden, feature+" is not enabled on this server")
}

// ErrDomainInvalid rejects a syntactically bad domain.
func ErrDomainInvalid(message string) *apperr.Error {
	return apperr.New(apperr.CodeValidation, http.StatusBadRequest, message)
}

// ErrDomainTaken is the generic answer for a domain already claimed anywhere on the
// server. It never says which tenant holds it.
func ErrDomainTaken() *apperr.Error {
	return apperr.New(apperr.CodeConflict, http.StatusConflict, "That domain is unavailable")
}

// ErrDomainNotVerified blocks activation until ownership has been proven.
func ErrDomainNotVerified() *apperr.Error {
	return apperr.New(apperr.CodeConflict, http.StatusConflict, "Domain ownership has not been verified yet")
}

// ErrDomainVerificationFailed reports a DNS lookup that did not find the expected record.
func ErrDomainVerificationFailed() *apperr.Error {
	return apperr.New(apperr.CodeValidation, http.StatusBadRequest, "The verification record could not be found, check your DNS settings and try again")
}

// ErrInternal is the only path by which an unexpected failure reaches a client. The
// underlying cause is attached for the logger and never rendered into the response, so
// stack traces, DSNs, credentials, internal addresses and filesystem paths cannot leak.
func ErrInternal(cause error) *apperr.Error {
	return apperr.New(apperr.CodeInternal, http.StatusInternalServerError, "Something went wrong, please try again").
		WithCause(cause)
}
