// Package errors defines cashp's canonical application error type, the
// standard error code taxonomy, the HTTP status mapping, and the wire
// format used by every handler, per AI.md PART 9 and PART 14.
//
// Error values carry two distinct payloads: a safe, user-facing Message
// that may be sent over HTTP, and a wrapped cause (Err) that is only ever
// logged. Stack traces, database DSNs, internal hostnames, IP addresses,
// and filesystem paths must never appear in Message or Details.
package errors

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"log/slog"
	"net/http"
	"strings"
)

// Standard machine-readable error codes. Values are the exact strings
// placed in the "error" field of an error response; they are stable API
// surface and must never change without an API version bump.
const (
	// CodeBadRequest marks a malformed or unparseable request.
	CodeBadRequest = "BAD_REQUEST"
	// CodeValidation marks a well-formed request that failed field validation.
	CodeValidation = "VALIDATION_FAILED"
	// CodeUnauthorized marks a request with missing or unusable credentials.
	CodeUnauthorized = "UNAUTHORIZED"
	// CodeTokenExpired marks an expired session or API token.
	CodeTokenExpired = "TOKEN_EXPIRED"
	// CodeTokenInvalid marks a syntactically or cryptographically invalid token.
	CodeTokenInvalid = "TOKEN_INVALID"
	// CodeTwoFactorRequired marks a login that still needs a second factor.
	CodeTwoFactorRequired = "2FA_REQUIRED"
	// CodeTwoFactorInvalid marks a rejected second-factor code.
	CodeTwoFactorInvalid = "2FA_INVALID"
	// CodeForbidden marks an authenticated caller lacking permission.
	CodeForbidden = "FORBIDDEN"
	// CodeAccountLocked marks an account locked by policy or an administrator.
	CodeAccountLocked = "ACCOUNT_LOCKED"
	// CodeNotFound marks a missing resource.
	CodeNotFound = "NOT_FOUND"
	// CodeMethodNotAllowed marks a route matched with an unsupported method.
	CodeMethodNotAllowed = "METHOD_NOT_ALLOWED"
	// CodeConflict marks a uniqueness or concurrent-update conflict.
	CodeConflict = "CONFLICT"
	// CodePayloadTooLarge marks a body larger than the configured limit.
	CodePayloadTooLarge = "PAYLOAD_TOO_LARGE"
	// CodeRateLimited marks a caller that exceeded a rate limit window.
	CodeRateLimited = "RATE_LIMITED"
	// CodeQuotaExceeded marks a caller that exhausted an allowance or plan quota.
	CodeQuotaExceeded = "QUOTA_EXCEEDED"
	// CodeInternal marks an unexpected server-side failure.
	CodeInternal = "SERVER_ERROR"
	// CodeUnavailable marks a dependency or subsystem that is temporarily down.
	CodeUnavailable = "UNAVAILABLE"
	// CodeMaintenance marks a deliberate maintenance window.
	CodeMaintenance = "MAINTENANCE"
	// CodeTimeout marks an operation that exceeded its deadline.
	CodeTimeout = "TIMEOUT"
)

// Short aliases for the standard codes. They name the same wire values as
// the Code* constants above and exist so call sites can read as
// errors.NotFound instead of errors.CodeNotFound.
const (
	BadRequest        = CodeBadRequest
	Validation        = CodeValidation
	ValidationFailed  = CodeValidation
	Unauthorized      = CodeUnauthorized
	TokenExpired      = CodeTokenExpired
	TokenInvalid      = CodeTokenInvalid
	TwoFactorRequired = CodeTwoFactorRequired
	TwoFactorInvalid  = CodeTwoFactorInvalid
	Forbidden         = CodeForbidden
	AccountLocked     = CodeAccountLocked
	NotFound          = CodeNotFound
	MethodNotAllowed  = CodeMethodNotAllowed
	Conflict          = CodeConflict
	PayloadTooLarge   = CodePayloadTooLarge
	RateLimited       = CodeRateLimited
	QuotaExceeded     = CodeQuotaExceeded
	Internal          = CodeInternal
	ServerError       = CodeInternal
	Unavailable       = CodeUnavailable
	Maintenance       = CodeMaintenance
	Timeout           = CodeTimeout
)

// codeSpec is the HTTP status and default user-facing message registered
// for a standard error code.
type codeSpec struct {
	status  int
	message string
}

// codeTable is the single source of truth for status and default message
// per code, per the standard error code table in AI.md PART 9.
var codeTable = map[string]codeSpec{
	CodeBadRequest:        {http.StatusBadRequest, "Invalid request format"},
	CodeValidation:        {http.StatusBadRequest, "Validation failed"},
	CodeUnauthorized:      {http.StatusUnauthorized, "Authentication required"},
	CodeTokenExpired:      {http.StatusUnauthorized, "Token has expired"},
	CodeTokenInvalid:      {http.StatusUnauthorized, "Invalid token"},
	CodeTwoFactorRequired: {http.StatusUnauthorized, "Two-factor authentication required"},
	CodeTwoFactorInvalid:  {http.StatusUnauthorized, "Invalid 2FA code"},
	CodeForbidden:         {http.StatusForbidden, "Permission denied"},
	CodeAccountLocked:     {http.StatusForbidden, "Account locked"},
	CodeNotFound:          {http.StatusNotFound, "Resource not found"},
	CodeMethodNotAllowed:  {http.StatusMethodNotAllowed, "Method not allowed"},
	CodeConflict:          {http.StatusConflict, "Resource already exists"},
	CodePayloadTooLarge:   {http.StatusRequestEntityTooLarge, "Request payload too large"},
	CodeRateLimited:       {http.StatusTooManyRequests, "Too many requests"},
	CodeQuotaExceeded:     {http.StatusTooManyRequests, "Quota exceeded"},
	CodeInternal:          {http.StatusInternalServerError, "Internal server error"},
	CodeUnavailable:       {http.StatusServiceUnavailable, "Service unavailable"},
	CodeMaintenance:       {http.StatusServiceUnavailable, "Service unavailable"},
	CodeTimeout:           {http.StatusGatewayTimeout, "Request timed out"},
}

// Error is cashp's canonical application error. Code and Message are safe
// to send to a client; Err is the wrapped cause and is never exposed over
// HTTP, only logged.
type Error struct {
	// Code is the machine-readable code, e.g. "NOT_FOUND".
	Code string
	// Message is the safe, user-facing message; it never leaks internals.
	Message string
	// HTTPStatus is the response status carrying this error.
	HTTPStatus int
	// Details is optional structured context, e.g. {"field":"email"}.
	Details map[string]any
	// Err is the wrapped cause, never exposed over HTTP.
	Err error
	// RequestID correlates the error with the request log entry.
	RequestID string
}

// New builds an error from a code, an HTTP status, and a user-facing
// message. An empty message falls back to the code's default message; an
// out-of-range status falls back to the code's registered status.
func New(code string, httpStatus int, message string) *Error {
	c := normalizeCode(code)
	return &Error{
		Code:       c,
		Message:    resolveMessage(c, message),
		HTTPStatus: resolveStatus(c, httpStatus),
	}
}

// Wrap builds an error that carries err as its cause. The cause is kept
// for logging only and is never rendered into the HTTP response.
func Wrap(err error, code string, httpStatus int, message string) *Error {
	e := New(code, httpStatus, message)
	e.Err = err
	return e
}

// Error renders the error for logs. It includes the wrapped cause and is
// therefore never safe to place in an HTTP response body — use Message or
// Payload for anything that reaches a client.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	var b strings.Builder
	b.WriteString(e.Code)
	b.WriteString(": ")
	b.WriteString(e.Message)
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

// Unwrap returns the wrapped cause, satisfying the standard errors chain.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// WithDetails returns a copy of the error with d merged into Details. The
// receiver is left untouched so shared error values cannot be mutated by
// a caller.
func (e *Error) WithDetails(d map[string]any) *Error {
	if e == nil {
		return nil
	}
	c := *e
	if len(d) > 0 {
		merged := make(map[string]any, len(e.Details)+len(d))
		for k, v := range e.Details {
			merged[k] = v
		}
		for k, v := range d {
			merged[k] = v
		}
		c.Details = merged
	}
	return &c
}

// WithRequestID returns a copy of the error tagged with a request id so
// the log entry and the client-visible response can be correlated.
func (e *Error) WithRequestID(id string) *Error {
	if e == nil {
		return nil
	}
	c := *e
	c.RequestID = id
	return &c
}

// WithCause returns a copy of the error carrying err as its cause.
func (e *Error) WithCause(err error) *Error {
	if e == nil {
		return nil
	}
	c := *e
	c.Err = err
	return &c
}

// From converts any error into an *Error. An error that already is (or
// wraps) an *Error is returned as-is; a deadline or cancellation maps to
// the matching code; anything else becomes an opaque internal error whose
// Message reveals nothing about the cause.
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if stderrors.As(err, &e) {
		return e
	}
	switch {
	case stderrors.Is(err, context.DeadlineExceeded):
		return Wrap(err, CodeTimeout, 0, "")
	case stderrors.Is(err, context.Canceled):
		return Wrap(err, CodeUnavailable, 0, "")
	default:
		return Wrap(err, CodeInternal, 0, "")
	}
}

// Is reports whether err, or any error it wraps, is an *Error with the
// given code.
func Is(err error, code string) bool {
	target := normalizeCode(code)
	for err != nil {
		var e *Error
		if stderrors.As(err, &e) {
			if e.Code == target {
				return true
			}
			err = e.Unwrap()
			continue
		}
		err = stderrors.Unwrap(err)
	}
	return false
}

// CodeOf returns the machine-readable code for any error. A nil error
// has no code and returns the empty string; an unrecognized error maps to
// CodeInternal.
func CodeOf(err error) string {
	if e := From(err); e != nil {
		return e.Code
	}
	return ""
}

// StatusOf returns the HTTP status for any error. A nil error is not a
// failure and returns 200; an unrecognized error maps to 500.
func StatusOf(err error) int {
	if e := From(err); e != nil {
		return e.HTTPStatus
	}
	return http.StatusOK
}

// StatusForCode maps a machine-readable code to its HTTP status. Unknown
// codes map to 500 so an unmapped failure is never reported as success.
func StatusForCode(code string) int {
	if spec, ok := codeTable[normalizeCode(code)]; ok {
		return spec.status
	}
	return http.StatusInternalServerError
}

// DefaultMessage returns the standard user-facing message for a code.
// Unknown codes fall back to the generic internal-error message so no
// internal detail can leak through an unmapped code.
func DefaultMessage(code string) string {
	if spec, ok := codeTable[normalizeCode(code)]; ok {
		return spec.message
	}
	return codeTable[CodeInternal].message
}

// Payload is the canonical error response body defined in AI.md PART 14.
// The HTTP status carries the status; it is never duplicated in the body.
type Payload struct {
	OK      bool           `json:"ok"`
	Error   string         `json:"error"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Payload renders the client-visible body for the error. The wrapped
// cause and the request id are deliberately excluded.
func (e *Error) Payload() Payload {
	if e == nil {
		return Payload{OK: false, Error: CodeInternal, Message: DefaultMessage(CodeInternal)}
	}
	return Payload{OK: false, Error: e.Code, Message: e.Message, Details: e.Details}
}

// WriteJSON writes the canonical error response. Error responses are
// never cached, per the HTTP cache header table in AI.md PART 9.
func (e *Error) WriteJSON(w http.ResponseWriter) error {
	if e == nil {
		e = New(CodeInternal, 0, "")
	}
	status := e.HTTPStatus
	body, err := json.Marshal(e.Payload())
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"ok":false,"error":"` + CodeInternal + `","message":"` + DefaultMessage(CodeInternal) + `"}`)
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, werr := w.Write(body)
	return werr
}

// LogAttrs returns the structured attributes every error log entry must
// carry. The wrapped cause is included here — logs are the only place it
// may appear.
func (e *Error) LogAttrs() []slog.Attr {
	if e == nil {
		return nil
	}
	attrs := []slog.Attr{
		slog.String("error_code", e.Code),
		slog.Int("http_status", e.HTTPStatus),
	}
	if e.RequestID != "" {
		attrs = append(attrs, slog.String("request_id", e.RequestID))
	}
	if e.Err != nil {
		attrs = append(attrs, slog.String("internal", e.Err.Error()))
	}
	return attrs
}

// Log emits the error at ERROR for 5xx and WARN for 4xx, per AI.md PART
// 9. A nil logger falls back to the default slog logger.
func (e *Error) Log(ctx context.Context, logger *slog.Logger) {
	if e == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	level := slog.LevelWarn
	if e.HTTPStatus >= http.StatusInternalServerError {
		level = slog.LevelError
	} else if e.HTTPStatus < http.StatusBadRequest {
		level = slog.LevelInfo
	}
	logger.LogAttrs(ctx, level, e.Message, e.LogAttrs()...)
}

// normalizeCode upper-cases and trims a code so lookups and comparisons
// are stable regardless of how a call site spelled it.
func normalizeCode(code string) string {
	c := strings.ToUpper(strings.TrimSpace(code))
	c = strings.ReplaceAll(c, " ", "_")
	c = strings.ReplaceAll(c, "-", "_")
	if c == "" {
		return CodeInternal
	}
	return c
}

// resolveMessage falls back to the code's default message when the call
// site supplied none.
func resolveMessage(code, message string) string {
	if m := strings.TrimSpace(message); m != "" {
		return m
	}
	return DefaultMessage(code)
}

// resolveStatus falls back to the code's registered status when the call
// site supplied one outside the valid HTTP range.
func resolveStatus(code string, status int) int {
	if status >= 100 && status <= 599 {
		return status
	}
	return StatusForCode(code)
}
