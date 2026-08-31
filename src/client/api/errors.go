package api

import (
	"errors"
	"fmt"
)

// Kind classifies an API failure so the caller can map it to the CLI exit
// codes defined in AI.md PART 33 "Exit Codes".
type Kind int

// Failure kinds returned by the API client.
const (
	// KindGeneral is any failure without a more specific classification.
	KindGeneral Kind = iota
	// KindConfig is a missing or invalid local configuration value.
	KindConfig
	// KindConnection is a transport-level failure against every candidate URL.
	KindConnection
	// KindAuth is a 401/403 response, including revoked and expired tokens.
	KindAuth
	// KindNotFound is a 404 response or a NOT_FOUND error code.
	KindNotFound
	// KindUsage is a 400/422 response caused by bad arguments.
	KindUsage
)

// Server error codes the CLI reacts to specifically.
const (
	// CodeTokenRevoked means the token was revoked server-side.
	CodeTokenRevoked = "TOKEN_REVOKED"
	// CodeTokenExpired means the token passed its expiry.
	CodeTokenExpired = "TOKEN_EXPIRED"
	// CodeTokenInvalid means the token was never valid.
	CodeTokenInvalid = "TOKEN_INVALID"
)

// Error is a classified API failure carrying the server's error envelope
// fields. It never carries a token, DSN or filesystem path.
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Status  int
	cause   error
}

// Error implements the error interface.
func (e *Error) Error() string {
	switch {
	case e.Message != "" && e.Code != "":
		return fmt.Sprintf("%s (%s)", e.Message, e.Code)
	case e.Message != "":
		return e.Message
	case e.cause != nil:
		return e.cause.Error()
	default:
		return "request failed"
	}
}

// Unwrap exposes the underlying transport error where one exists.
func (e *Error) Unwrap() error {
	return e.cause
}

// IsTokenRejected reports whether the failure means the stored credential
// must be discarded and the user must re-authenticate deliberately.
func (e *Error) IsTokenRejected() bool {
	switch e.Code {
	case CodeTokenRevoked, CodeTokenExpired, CodeTokenInvalid:
		return true
	}
	return e.Kind == KindAuth && e.Status == 401
}

// KindOf returns the Kind of err, or KindGeneral when err is not an API
// error.
func KindOf(err error) Kind {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr.Kind
	}
	return KindGeneral
}

// AsError extracts the *Error from err when present.
func AsError(err error) (*Error, bool) {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

// newError builds a classified error without a server envelope.
func newError(kind Kind, message string, cause error) *Error {
	return &Error{Kind: kind, Message: message, cause: cause}
}
