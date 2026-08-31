package orchestrator

import (
	stderrors "errors"
	"net/http"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// Machine-readable codes this package adds to the standard taxonomy in
// src/errors. They are stable API surface: the admin panel and the HTTP
// layer switch on them.
const (
	// CodeUnsupported marks an operation the selected backend cannot
	// perform. It is never returned for an operation that silently did
	// nothing — an unsupported operation always fails loudly.
	CodeUnsupported = "UNSUPPORTED_OPERATION"
	// CodeBackendUnavailable marks a backend whose socket or binary is not
	// reachable on this host.
	CodeBackendUnavailable = "BACKEND_UNAVAILABLE"
	// CodeIsolationViolation marks a spec that asked for something the
	// workload class's isolation profile forbids.
	CodeIsolationViolation = "ISOLATION_VIOLATION"
)

// Sentinels for errors.Is checks. Every *errors.Error this package returns
// wraps one of these, so callers can branch without string matching.
var (
	// ErrUnsupported is wrapped by every unsupported-operation error.
	ErrUnsupported = stderrors.New("orchestrator: operation not supported by this backend")
	// ErrBackendUnavailable is wrapped when a backend cannot be reached.
	ErrBackendUnavailable = stderrors.New("orchestrator: backend unavailable")
	// ErrIsolationViolation is wrapped when a spec breaches its profile.
	ErrIsolationViolation = stderrors.New("orchestrator: isolation profile violation")
	// ErrValidation is wrapped when an identifier or path fails its
	// allowlist check.
	ErrValidation = stderrors.New("orchestrator: validation failed")
	// ErrTenantMismatch is wrapped when an actor addresses a workload that
	// belongs to another tenant.
	ErrTenantMismatch = stderrors.New("orchestrator: tenant scope violation")
	// ErrNotFound is wrapped when a workload does not exist.
	ErrNotFound = stderrors.New("orchestrator: workload not found")
)

// unsupportedErr builds the typed error a backend returns for an operation
// it genuinely cannot perform. Backend and operation are safe to expose:
// neither names a socket, a host path, or a command line.
func unsupportedErr(backend BackendName, op string) *apperr.Error {
	e := apperr.Wrap(ErrUnsupported, CodeUnsupported, http.StatusNotImplemented,
		"That operation is not available on this virtualization backend")
	return e.WithDetails(map[string]any{"backend": string(backend), "operation": op})
}

// unavailableErr builds the typed error returned when a backend's socket or
// binary is missing. The cause is kept for the log only; reason is a short
// non-leaking token such as "socket" or "binary".
func unavailableErr(backend BackendName, reason string, cause error) *apperr.Error {
	e := apperr.Wrap(stderrors.Join(ErrBackendUnavailable, cause), CodeBackendUnavailable,
		http.StatusServiceUnavailable, "The virtualization backend is not available on this node")
	return e.WithDetails(map[string]any{"backend": string(backend), "reason": reason})
}

// isolationErr builds the typed error returned when a spec asks for
// something the class profile forbids, such as a privileged tenant
// container or a host-network attachment.
func isolationErr(class Class, field, detail string) *apperr.Error {
	e := apperr.Wrap(ErrIsolationViolation, CodeIsolationViolation, http.StatusForbidden,
		"That workload configuration is not permitted")
	return e.WithDetails(map[string]any{"class": string(class), "field": field, "rule": detail})
}

// validationErr builds the typed error returned when an identifier, image
// reference, or path fails its allowlist check. The rejected value is never
// echoed back: a caller that supplied an injection attempt does not get it
// reflected into a response.
func validationErr(field, rule string) *apperr.Error {
	e := apperr.Wrap(ErrValidation, apperr.CodeValidation, http.StatusBadRequest,
		"One or more workload settings are not valid")
	return e.WithDetails(map[string]any{"field": field, "rule": rule})
}

// tenantErr builds the typed error returned when an actor addresses a
// workload outside their tenant. The response is deliberately the same
// generic permission failure regardless of whether the target exists, so it
// cannot be used to enumerate other tenants' workloads.
func tenantErr() *apperr.Error {
	return apperr.Wrap(ErrTenantMismatch, apperr.CodeForbidden, http.StatusForbidden,
		"Permission denied")
}

// notFoundErr builds the typed error returned when a workload does not
// exist for the requesting tenant.
func notFoundErr() *apperr.Error {
	return apperr.Wrap(ErrNotFound, apperr.CodeNotFound, http.StatusNotFound,
		"Workload not found")
}

// backendErr converts a raw backend failure into a safe application error.
// The cause is preserved for logging only; op names the logical operation
// so an operator can find the matching log line without the message ever
// carrying a socket path, host path, or command line.
func backendErr(backend BackendName, op string, cause error) *apperr.Error {
	e := apperr.Wrap(cause, apperr.CodeUnavailable, http.StatusBadGateway,
		"The virtualization backend rejected the request")
	return e.WithDetails(map[string]any{"backend": string(backend), "operation": op})
}

// timeoutErr converts a deadline overrun into the standard timeout error.
func timeoutErr(backend BackendName, op string, cause error) *apperr.Error {
	e := apperr.Wrap(cause, apperr.CodeTimeout, http.StatusGatewayTimeout, "")
	return e.WithDetails(map[string]any{"backend": string(backend), "operation": op})
}

// IsUnsupported reports whether err is (or wraps) an unsupported-operation
// error from any backend.
func IsUnsupported(err error) bool { return stderrors.Is(err, ErrUnsupported) }

// IsUnavailable reports whether err is (or wraps) a backend-unavailable
// error.
func IsUnavailable(err error) bool { return stderrors.Is(err, ErrBackendUnavailable) }

// IsIsolationViolation reports whether err is (or wraps) an isolation
// profile violation.
func IsIsolationViolation(err error) bool { return stderrors.Is(err, ErrIsolationViolation) }

// IsValidation reports whether err is (or wraps) a validation failure.
func IsValidation(err error) bool { return stderrors.Is(err, ErrValidation) }

// IsTenantMismatch reports whether err is (or wraps) a tenant scope
// violation.
func IsTenantMismatch(err error) bool { return stderrors.Is(err, ErrTenantMismatch) }

// IsNotFound reports whether err is (or wraps) a missing-workload error.
func IsNotFound(err error) bool { return stderrors.Is(err, ErrNotFound) }
