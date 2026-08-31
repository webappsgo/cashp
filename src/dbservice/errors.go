package dbservice

import (
	"fmt"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// Every failure leaving this package is an *errors.Error so the API layer
// renders the project's standard envelope. Messages here are written for a
// tenant: they never carry a host path, a socket path, a DSN, a container
// identifier or a command line. The cause travels in the wrapped error for
// the log and is stripped from the response body by errors.Payload.

// ErrNotFound reports a managed instance, database, user or backup that does
// not exist for the calling tenant. A record owned by another tenant is
// reported the same way so ownership cannot be probed.
func ErrNotFound(kind string) *apperr.Error {
	return apperr.New(apperr.CodeNotFound, 404, fmt.Sprintf("No such %s.", kind))
}

// ErrValidation reports rejected input such as an identifier that failed the
// allowlist or an engine version that is not offered.
func ErrValidation(message string) *apperr.Error {
	return apperr.New(apperr.CodeValidation, 422, message)
}

// ErrConflict reports a uniqueness collision or an operation attempted
// against an instance whose current state does not allow it.
func ErrConflict(message string) *apperr.Error {
	return apperr.New(apperr.CodeConflict, 409, message)
}

// ErrForbidden reports a caller reaching for another tenant's instance or an
// operation their role does not permit.
func ErrForbidden(message string) *apperr.Error {
	return apperr.New(apperr.CodeForbidden, 403, message)
}

// ErrConfirmationRequired reports a destructive operation invoked without its
// explicit confirmation flag. Destroy is the only caller: an accidental API
// call must never be able to delete a tenant's data.
func ErrConfirmationRequired(name string) *apperr.Error {
	return apperr.New(apperr.CodeValidation, 422,
		fmt.Sprintf("Destroying %q deletes its data permanently; resend the request with confirmation.", name)).
		WithDetails(map[string]any{"confirmation_required": true, "instance": name})
}

// ErrUnsupported reports an operation an engine genuinely cannot perform, for
// example creating a named database on Valkey. It is always an explicit typed
// error and never a silent no-op, so a caller can tell "cannot" from "did
// nothing".
func ErrUnsupported(engine Engine, operation string) *apperr.Error {
	return apperr.New(apperr.CodeBadRequest, 400,
		fmt.Sprintf("%s does not support %s.", EngineDisplayName(engine), operation)).
		WithDetails(map[string]any{
			"engine":      string(engine),
			"operation":   operation,
			"unsupported": true,
		})
}

// ErrUnknownEngine reports an engine name that is not managed by cashp.
func ErrUnknownEngine(name string) *apperr.Error {
	return apperr.New(apperr.CodeValidation, 422,
		fmt.Sprintf("%q is not a database engine this server manages.", name)).
		WithDetails(map[string]any{"engine": name})
}

// ErrQuota reports a per-tenant ceiling that the request would cross.
func ErrQuota(resource string, limit, used, requested int64) *apperr.Error {
	return apperr.New(apperr.CodeQuotaExceeded, 429,
		fmt.Sprintf("Your plan allows %d %s and %d are already in use.", limit, resource, used)).
		WithDetails(map[string]any{
			"resource":  resource,
			"limit":     limit,
			"used":      used,
			"requested": requested,
		})
}

// ErrUnavailable reports an instance that is not currently reachable, for
// example a stopped instance that a query needs running.
func ErrUnavailable(message string) *apperr.Error {
	return apperr.New(apperr.CodeUnavailable, 503, message)
}

// ErrInternal wraps an unexpected failure. The cause is kept for the log and
// deliberately never rendered into the response body.
func ErrInternal(cause error, message string) *apperr.Error {
	return apperr.Wrap(cause, apperr.CodeInternal, 500, message)
}

// ErrNotConfigured reports the service being called before an orchestrator or
// backup repository has been wired in.
func ErrNotConfigured(component string) *apperr.Error {
	return apperr.New(apperr.CodeUnavailable, 503,
		fmt.Sprintf("Managed databases are not available: %s is not configured.", component))
}

// IsNotFound reports whether err is the typed not-found error, so a caller can
// tell "no such record" from a real failure without string matching.
func IsNotFound(err error) bool {
	e := apperr.From(err)
	return e != nil && e.Code == apperr.CodeNotFound
}

// IsUnsupported reports whether err is the typed "unsupported for this
// engine" error, so callers can branch without string matching.
func IsUnsupported(err error) bool {
	e := apperr.From(err)
	if e == nil {
		return false
	}
	unsupported, _ := e.Details["unsupported"].(bool)
	return unsupported
}
