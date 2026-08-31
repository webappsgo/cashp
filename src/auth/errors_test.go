package auth

import (
	"testing"

	apperr "github.com/webappsgo/cashp/src/errors"
)

func TestErrorConstructorsCodeAndStatus(t *testing.T) {
	cases := []struct {
		name       string
		err        *apperr.Error
		wantCode   string
		wantStatus int
	}{
		{"InvalidCredentials", ErrInvalidCredentials(), apperr.CodeUnauthorized, 401},
		{"TwoFactorRequired", ErrTwoFactorRequired(), apperr.CodeTwoFactorRequired, 401},
		{"TwoFactorInvalid", ErrTwoFactorInvalid(), apperr.CodeTwoFactorInvalid, 401},
		{"Unauthenticated", ErrUnauthenticated(), apperr.CodeUnauthorized, 401},
		{"SessionExpired", ErrSessionExpired(), apperr.CodeTokenExpired, 401},
		{"Forbidden", ErrForbidden(), apperr.CodeForbidden, 403},
		{"CSRF", ErrCSRF(), apperr.CodeForbidden, 403},
		{"RegistrationClosed", ErrRegistrationClosed(), apperr.CodeForbidden, 403},
		{"InviteRequired", ErrInviteRequired(), apperr.CodeForbidden, 403},
		{"InviteInvalid", ErrInviteInvalid(), apperr.CodeValidation, 400},
		{"OrgCreationClosed", ErrOrgCreationClosed(), apperr.CodeForbidden, 403},
		{"LastOwner", ErrLastOwner(), apperr.CodeConflict, 409},
		{"DomainTaken", ErrDomainTaken(), apperr.CodeConflict, 409},
		{"DomainNotVerified", ErrDomainNotVerified(), apperr.CodeConflict, 409},
		{"DomainVerificationFailed", ErrDomainVerificationFailed(), apperr.CodeValidation, 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.err.Code != c.wantCode {
				t.Errorf("Code = %q, want %q", c.err.Code, c.wantCode)
			}
			if c.err.HTTPStatus != c.wantStatus {
				t.Errorf("HTTPStatus = %d, want %d", c.err.HTTPStatus, c.wantStatus)
			}
			if c.err.Message == "" {
				t.Error("Message must not be empty")
			}
		})
	}
}

func TestErrNotFoundIncludesWhat(t *testing.T) {
	err := ErrNotFound("Organization")
	if err.Message != "Organization not found" {
		t.Errorf("Message = %q, want %q", err.Message, "Organization not found")
	}
	if err.Code != apperr.CodeNotFound || err.HTTPStatus != 404 {
		t.Errorf("got code=%s status=%d, want %s/404", err.Code, err.HTTPStatus, apperr.CodeNotFound)
	}
}

func TestErrRateLimitedCarriesRetryAfter(t *testing.T) {
	err := ErrRateLimited(42)
	if err.Code != apperr.CodeRateLimited || err.HTTPStatus != 429 {
		t.Fatalf("got code=%s status=%d", err.Code, err.HTTPStatus)
	}
	if got, ok := err.Details["retry_after"]; !ok || got != 42 {
		t.Errorf("Details[retry_after] = %v (ok=%v), want 42", got, ok)
	}
}

func TestErrValidationCarriesField(t *testing.T) {
	err := ErrValidation("email", "must be a valid address")
	if err.Message != "must be a valid address" {
		t.Errorf("Message = %q", err.Message)
	}
	if got := err.Details["field"]; got != "email" {
		t.Errorf("Details[field] = %v, want email", got)
	}
}

func TestErrNameUnavailableAndReservedCarryField(t *testing.T) {
	for _, fn := range []func(string) *apperr.Error{ErrNameUnavailable, ErrNameReserved} {
		err := fn("username")
		if err.Code != apperr.CodeConflict || err.HTTPStatus != 409 {
			t.Errorf("got code=%s status=%d, want %s/409", err.Code, err.HTTPStatus, apperr.CodeConflict)
		}
		if got := err.Details["field"]; got != "username" {
			t.Errorf("Details[field] = %v, want username", got)
		}
	}
}

func TestErrQuotaUsesProvidedMessage(t *testing.T) {
	err := ErrQuota("You have reached the maximum number of organizations")
	if err.Code != apperr.CodeQuotaExceeded {
		t.Errorf("Code = %q, want %q", err.Code, apperr.CodeQuotaExceeded)
	}
	if err.Message != "You have reached the maximum number of organizations" {
		t.Errorf("Message = %q", err.Message)
	}
}

func TestErrFeatureDisabledIncludesFeatureName(t *testing.T) {
	err := ErrFeatureDisabled("Custom domains")
	if err.Message != "Custom domains is not enabled on this server" {
		t.Errorf("Message = %q", err.Message)
	}
}

func TestErrDomainInvalidUsesProvidedMessage(t *testing.T) {
	err := ErrDomainInvalid("domain contains invalid characters")
	if err.Code != apperr.CodeValidation || err.HTTPStatus != 400 {
		t.Errorf("got code=%s status=%d", err.Code, err.HTTPStatus)
	}
	if err.Message != "domain contains invalid characters" {
		t.Errorf("Message = %q", err.Message)
	}
}

// TestErrInternalNeverLeaksCauseInMessage is a regression guard for the
// no-internal-leak rule (backend-rules.md): the cause is attached via
// WithCause for logging only, and Message must stay a generic string.
func TestErrInternalNeverLeaksCauseInMessage(t *testing.T) {
	cause := &apperr.Error{Message: "db dsn=postgres://user:pass@10.0.0.5/db"}
	err := ErrInternal(cause)
	if err.Code != apperr.CodeInternal || err.HTTPStatus != 500 {
		t.Errorf("got code=%s status=%d", err.Code, err.HTTPStatus)
	}
	if err.Message == "" || err.Message == cause.Message {
		t.Errorf("Message must be a safe generic string, got %q", err.Message)
	}
	if err.Err != cause {
		t.Error("ErrInternal must attach the cause via Err for logging")
	}
}
