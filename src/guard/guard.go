// Package guard implements cashp's cross-cutting security enforcement: the
// tenant-isolation authorization primitive, the untrusted-workload posture
// validator, argv-only command execution, strict identifier allowlists,
// abuse and quota control, secret redaction, anti-enumeration helpers, and
// the request-layer hardening handlers that src/server/middleware does not
// already provide.
//
// Every decision in this package is deny-by-default: an input the guard
// does not positively recognize is refused, never passed through. Denials
// carry an internal Reason for the log and map to a generic src/errors code
// for the wire, so a caller can never leak why a guard refused.
package guard

import (
	stderrors "errors"
	"strings"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// Reason is the internal, log-only explanation of a guard denial. It is
// never rendered into an HTTP response; only the mapped error code and its
// generic default message reach a client.
type Reason string

// Denial reasons. Each maps to a generic wire code through reasonCodes.
const (
	// ReasonSubjectInvalid marks a subject with no identity or an unusable role.
	ReasonSubjectInvalid Reason = "subject_invalid"
	// ReasonSubjectInactive marks a suspended, locked, or deleted subject.
	ReasonSubjectInactive Reason = "subject_inactive"
	// ReasonRoleUnknown marks a role string outside the defined RBAC set.
	ReasonRoleUnknown Reason = "role_unknown"
	// ReasonActionUnknown marks an action outside the defined action set.
	ReasonActionUnknown Reason = "action_unknown"
	// ReasonResourceInvalid marks a resource with no type or no tenant scope.
	ReasonResourceInvalid Reason = "resource_invalid"
	// ReasonCrossTenant marks an attempt to touch another tenant's resource.
	ReasonCrossTenant Reason = "cross_tenant"
	// ReasonNoGrant marks an end user acting without an explicit matching grant.
	ReasonNoGrant Reason = "no_grant"
	// ReasonPlatformControlled marks an attempt to read or change platform-owned state.
	ReasonPlatformControlled Reason = "platform_controlled"
	// ReasonPrimaryAdminImmutable marks an attempt to change the tamper-proof primary-admin flag.
	ReasonPrimaryAdminImmutable Reason = "primary_admin_immutable"
	// ReasonPeerAdminCredential marks an attempt to read or change another administrator's credentials.
	ReasonPeerAdminCredential Reason = "peer_admin_credential"
	// ReasonQuotaExceeded marks a resource request beyond the tenant's plan allowance.
	ReasonQuotaExceeded Reason = "quota_exceeded"
	// ReasonRateLimited marks an abuse-control window that is already full.
	ReasonRateLimited Reason = "rate_limited"
	// ReasonLockedOut marks an identity in authentication lockout or backoff.
	ReasonLockedOut Reason = "locked_out"
	// ReasonWorkloadUnsafe marks a container or VM spec that fails the isolation posture.
	ReasonWorkloadUnsafe Reason = "workload_unsafe"
	// ReasonInvalidInput marks input rejected by a strict allowlist validator.
	ReasonInvalidInput Reason = "invalid_input"
	// ReasonOutboundBlocked marks an outbound destination refused by abuse control.
	ReasonOutboundBlocked Reason = "outbound_blocked"
	// ReasonBodyTooLarge marks a request body over the configured ceiling.
	ReasonBodyTooLarge Reason = "body_too_large"
	// ReasonUnsupportedMediaType marks a request whose Content-Type is not accepted.
	ReasonUnsupportedMediaType Reason = "unsupported_media_type"
	// ReasonHostNotAllowed marks a request whose Host header is not a configured host.
	ReasonHostNotAllowed Reason = "host_not_allowed"
	// ReasonSignatureInvalid marks a webhook whose signature did not verify.
	ReasonSignatureInvalid Reason = "signature_invalid"
	// ReasonReplay marks a webhook event identifier that has already been processed.
	ReasonReplay Reason = "replay"
)

// reasonCodes maps every denial reason to the generic src/errors code sent
// to the client. A reason that is absent falls through to CodeForbidden, so
// an unmapped reason denies rather than leaks.
//
// ReasonCrossTenant deliberately maps to NOT_FOUND: telling a caller that a
// resource exists but belongs to someone else is itself a cross-tenant
// disclosure, so a foreign resource is indistinguishable from a missing one.
var reasonCodes = map[Reason]string{
	ReasonSubjectInvalid:        apperr.CodeUnauthorized,
	ReasonSubjectInactive:       apperr.CodeAccountLocked,
	ReasonRoleUnknown:           apperr.CodeForbidden,
	ReasonActionUnknown:         apperr.CodeForbidden,
	ReasonResourceInvalid:       apperr.CodeForbidden,
	ReasonCrossTenant:           apperr.CodeNotFound,
	ReasonNoGrant:               apperr.CodeForbidden,
	ReasonPlatformControlled:    apperr.CodeForbidden,
	ReasonPrimaryAdminImmutable: apperr.CodeForbidden,
	ReasonPeerAdminCredential:   apperr.CodeForbidden,
	ReasonQuotaExceeded:         apperr.CodeQuotaExceeded,
	ReasonRateLimited:           apperr.CodeRateLimited,
	ReasonLockedOut:             apperr.CodeAccountLocked,
	ReasonWorkloadUnsafe:        apperr.CodeValidation,
	ReasonInvalidInput:          apperr.CodeValidation,
	ReasonOutboundBlocked:       apperr.CodeForbidden,
	ReasonBodyTooLarge:          apperr.CodePayloadTooLarge,
	ReasonUnsupportedMediaType:  apperr.CodeBadRequest,
	ReasonHostNotAllowed:        apperr.CodeBadRequest,
	ReasonSignatureInvalid:      apperr.CodeUnauthorized,
	ReasonReplay:                apperr.CodeConflict,
}

// CodeForReason returns the client-visible error code for a denial reason.
// An unrecognized reason denies with FORBIDDEN rather than succeeding.
func CodeForReason(reason Reason) string {
	if code, ok := reasonCodes[reason]; ok {
		return code
	}
	return apperr.CodeForbidden
}

// DenyError is the typed decision error every guard returns on refusal.
// Reason and Detail are diagnostic and log-only; only Code and the code's
// generic default message may be shown to a caller.
type DenyError struct {
	// Reason is the internal category of the denial.
	Reason Reason
	// Code is the client-visible src/errors code the reason maps to.
	Code string
	// Detail is a log-only explanation. It may name internal state and must never be returned over HTTP.
	Detail string
}

// Deny builds a denial for a reason, with a log-only detail string. Detail
// is not sanitized here because it never leaves the process; callers must
// route it through Error() or AppError().Err, both of which are log paths.
func Deny(reason Reason, detail string) *DenyError {
	return &DenyError{Reason: reason, Code: CodeForReason(reason), Detail: detail}
}

// Error renders the denial for a log line. It includes Detail and is
// therefore never safe to place in an HTTP response body.
func (e *DenyError) Error() string {
	if e == nil {
		return "<nil>"
	}
	var b strings.Builder
	b.WriteString("guard: denied (")
	b.WriteString(string(e.Reason))
	b.WriteString(")")
	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}
	return b.String()
}

// AppError converts the denial into the canonical application error. The
// user-facing message is always the code's generic default; the reason and
// detail travel only in the wrapped cause, which src/errors logs and never
// serializes.
func (e *DenyError) AppError() *apperr.Error {
	if e == nil {
		return nil
	}
	return apperr.Wrap(e, e.Code, 0, apperr.DefaultMessage(e.Code))
}

// IsDenied reports whether err is, or wraps, a guard denial.
func IsDenied(err error) bool {
	var d *DenyError
	return stderrors.As(err, &d)
}

// DenialReason returns the internal reason carried by err, or the empty
// string when err is not a guard denial. It exists for log and audit call
// sites; it must never be used to build a client-visible message.
func DenialReason(err error) Reason {
	var d *DenyError
	if stderrors.As(err, &d) {
		return d.Reason
	}
	return ""
}

// AppErrorFor converts any error into the canonical application error,
// preferring a guard denial's generic mapping when one is present.
func AppErrorFor(err error) *apperr.Error {
	if err == nil {
		return nil
	}
	var d *DenyError
	if stderrors.As(err, &d) {
		return d.AppError()
	}
	return apperr.From(err)
}
