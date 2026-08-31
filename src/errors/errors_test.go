package errors

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewDefaults(t *testing.T) {
	e := New(CodeNotFound, 0, "")
	if e.Code != CodeNotFound {
		t.Fatalf("code = %q, want %q", e.Code, CodeNotFound)
	}
	if e.HTTPStatus != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", e.HTTPStatus, http.StatusNotFound)
	}
	if e.Message != "Resource not found" {
		t.Fatalf("message = %q", e.Message)
	}
}

func TestNewNormalizesCodeAndStatus(t *testing.T) {
	e := New("  not-found  ", 999, "Gone missing")
	if e.Code != CodeNotFound {
		t.Fatalf("code = %q, want %q", e.Code, CodeNotFound)
	}
	if e.HTTPStatus != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", e.HTTPStatus, http.StatusNotFound)
	}
	if e.Message != "Gone missing" {
		t.Fatalf("message = %q", e.Message)
	}
}

func TestNewEmptyCodeIsInternal(t *testing.T) {
	e := New("", 0, "")
	if e.Code != CodeInternal || e.HTTPStatus != http.StatusInternalServerError {
		t.Fatalf("got %q/%d", e.Code, e.HTTPStatus)
	}
}

func TestWrapAndUnwrap(t *testing.T) {
	cause := stderrors.New("pq: connection refused at 10.0.0.5")
	e := Wrap(cause, CodeInternal, 0, "")

	if !stderrors.Is(e, cause) {
		t.Fatal("wrapped cause not reachable via errors.Is")
	}
	if e.Unwrap() != cause {
		t.Fatal("Unwrap did not return the cause")
	}
	if strings.Contains(e.Message, "10.0.0.5") {
		t.Fatal("internal detail leaked into user-facing message")
	}
	if !strings.Contains(e.Error(), "connection refused") {
		t.Fatalf("Error() should include the cause for logs: %q", e.Error())
	}
}

func TestErrorNilReceiver(t *testing.T) {
	var e *Error
	if got := e.Error(); got != "<nil>" {
		t.Fatalf("Error() = %q", got)
	}
	if e.Unwrap() != nil {
		t.Fatal("Unwrap on nil should be nil")
	}
	if e.WithDetails(map[string]any{"a": 1}) != nil {
		t.Fatal("WithDetails on nil should be nil")
	}
	if e.WithRequestID("x") != nil {
		t.Fatal("WithRequestID on nil should be nil")
	}
	if e.WithCause(stderrors.New("x")) != nil {
		t.Fatal("WithCause on nil should be nil")
	}
	if e.LogAttrs() != nil {
		t.Fatal("LogAttrs on nil should be nil")
	}
}

func TestWithDetailsCopiesAndMerges(t *testing.T) {
	base := New(CodeValidation, 0, "Invalid email address").WithDetails(map[string]any{"field": "email"})
	derived := base.WithDetails(map[string]any{"rule": "format"})

	if len(base.Details) != 1 {
		t.Fatalf("base mutated: %v", base.Details)
	}
	if derived.Details["field"] != "email" || derived.Details["rule"] != "format" {
		t.Fatalf("details not merged: %v", derived.Details)
	}
	if base == derived {
		t.Fatal("WithDetails must return a copy")
	}
}

func TestWithRequestIDAndCause(t *testing.T) {
	cause := stderrors.New("boom")
	e := New(CodeInternal, 0, "").WithRequestID("req_abc123").WithCause(cause)
	if e.RequestID != "req_abc123" {
		t.Fatalf("request id = %q", e.RequestID)
	}
	if !stderrors.Is(e, cause) {
		t.Fatal("cause not attached")
	}
}

func TestFrom(t *testing.T) {
	if From(nil) != nil {
		t.Fatal("From(nil) must be nil")
	}

	app := New(CodeForbidden, 0, "")
	if got := From(fmt.Errorf("layer: %w", app)); got != app {
		t.Fatal("From must return the existing *Error from the chain")
	}

	if got := From(context.DeadlineExceeded); got.Code != CodeTimeout || got.HTTPStatus != http.StatusGatewayTimeout {
		t.Fatalf("deadline mapped to %q/%d", got.Code, got.HTTPStatus)
	}
	if got := From(context.Canceled); got.Code != CodeUnavailable {
		t.Fatalf("cancel mapped to %q", got.Code)
	}

	opaque := From(stderrors.New("dial tcp 127.0.0.1:5432: refused"))
	if opaque.Code != CodeInternal || opaque.HTTPStatus != http.StatusInternalServerError {
		t.Fatalf("generic mapped to %q/%d", opaque.Code, opaque.HTTPStatus)
	}
	if strings.Contains(opaque.Message, "127.0.0.1") {
		t.Fatalf("message leaked internals: %q", opaque.Message)
	}
}

func TestIs(t *testing.T) {
	inner := New(CodeConflict, 0, "")
	outer := Wrap(fmt.Errorf("save: %w", inner), CodeInternal, 0, "")

	if !Is(outer, CodeInternal) {
		t.Fatal("outer code not matched")
	}
	if !Is(outer, CodeConflict) {
		t.Fatal("inner code not matched through the chain")
	}
	if !Is(outer, "conflict") {
		t.Fatal("code comparison should be normalized")
	}
	if Is(outer, CodeNotFound) {
		t.Fatal("unrelated code matched")
	}
	if Is(nil, CodeNotFound) {
		t.Fatal("nil error matched")
	}
	if Is(stderrors.New("plain"), CodeInternal) {
		t.Fatal("plain error matched a code")
	}
}

func TestCodeOfAndStatusOf(t *testing.T) {
	if CodeOf(nil) != "" {
		t.Fatal("nil error has no code")
	}
	if StatusOf(nil) != http.StatusOK {
		t.Fatal("nil error is not a failure")
	}
	if CodeOf(New(CodeRateLimited, 0, "")) != CodeRateLimited {
		t.Fatal("code lost")
	}
	if StatusOf(stderrors.New("x")) != http.StatusInternalServerError {
		t.Fatal("unknown error should be 500")
	}
}

func TestStatusForCodeAndDefaultMessage(t *testing.T) {
	cases := map[string]int{
		CodeBadRequest:        400,
		CodeValidation:        400,
		CodeUnauthorized:      401,
		CodeTokenExpired:      401,
		CodeTokenInvalid:      401,
		CodeTwoFactorRequired: 401,
		CodeTwoFactorInvalid:  401,
		CodeForbidden:         403,
		CodeAccountLocked:     403,
		CodeNotFound:          404,
		CodeMethodNotAllowed:  405,
		CodeConflict:          409,
		CodePayloadTooLarge:   413,
		CodeRateLimited:       429,
		CodeQuotaExceeded:     429,
		CodeInternal:          500,
		CodeUnavailable:       503,
		CodeMaintenance:       503,
		CodeTimeout:           504,
	}
	for code, want := range cases {
		if got := StatusForCode(code); got != want {
			t.Errorf("StatusForCode(%s) = %d, want %d", code, got, want)
		}
		if DefaultMessage(code) == "" {
			t.Errorf("DefaultMessage(%s) is empty", code)
		}
	}

	if StatusForCode("SOMETHING_ELSE") != http.StatusInternalServerError {
		t.Error("unknown code must default to 500")
	}
	if DefaultMessage("SOMETHING_ELSE") != DefaultMessage(CodeInternal) {
		t.Error("unknown code must use the generic internal message")
	}
}

func TestAliasesMatchCanonicalCodes(t *testing.T) {
	pairs := [][2]string{
		{BadRequest, CodeBadRequest},
		{Validation, CodeValidation},
		{ValidationFailed, CodeValidation},
		{Unauthorized, CodeUnauthorized},
		{TokenExpired, CodeTokenExpired},
		{TokenInvalid, CodeTokenInvalid},
		{TwoFactorRequired, CodeTwoFactorRequired},
		{TwoFactorInvalid, CodeTwoFactorInvalid},
		{Forbidden, CodeForbidden},
		{AccountLocked, CodeAccountLocked},
		{NotFound, CodeNotFound},
		{MethodNotAllowed, CodeMethodNotAllowed},
		{Conflict, CodeConflict},
		{PayloadTooLarge, CodePayloadTooLarge},
		{RateLimited, CodeRateLimited},
		{QuotaExceeded, CodeQuotaExceeded},
		{Internal, CodeInternal},
		{ServerError, CodeInternal},
		{Unavailable, CodeUnavailable},
		{Maintenance, CodeMaintenance},
		{Timeout, CodeTimeout},
	}
	for _, p := range pairs {
		if p[0] != p[1] {
			t.Errorf("alias %q != canonical %q", p[0], p[1])
		}
	}
}

func TestPayloadOmitsInternals(t *testing.T) {
	e := Wrap(stderrors.New("pq: relation \"users\" does not exist"), CodeNotFound, 0, "").WithRequestID("req_1")
	body, err := json.Marshal(e.Payload())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(body)
	if strings.Contains(s, "pq:") || strings.Contains(s, "req_1") {
		t.Fatalf("payload leaked internals: %s", s)
	}
	if !strings.Contains(s, `"ok":false`) || !strings.Contains(s, `"error":"NOT_FOUND"`) {
		t.Fatalf("unexpected payload: %s", s)
	}
	if strings.Contains(s, `"details"`) {
		t.Fatalf("empty details must be omitted: %s", s)
	}

	var nilErr *Error
	if p := nilErr.Payload(); p.Error != CodeInternal || p.OK {
		t.Fatalf("nil payload = %+v", p)
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	e := New(CodeValidation, 0, "Invalid email address").WithDetails(map[string]any{"field": "email", "rule": "format"})
	if err := e.WriteJSON(rec); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("content-type = %q", got)
	}

	var p Payload
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.OK || p.Error != CodeValidation || p.Details["field"] != "email" {
		t.Fatalf("payload = %+v", p)
	}
}

func TestWriteJSONNilReceiver(t *testing.T) {
	rec := httptest.NewRecorder()
	var e *Error
	if err := e.WriteJSON(rec); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestLogAttrs(t *testing.T) {
	e := Wrap(stderrors.New("boom"), CodeInternal, 0, "").WithRequestID("req_9")
	attrs := e.LogAttrs()

	found := map[string]bool{}
	for _, a := range attrs {
		found[a.Key] = true
	}
	for _, key := range []string{"error_code", "http_status", "request_id", "internal"} {
		if !found[key] {
			t.Errorf("missing log attribute %q", key)
		}
	}

	plain := New(CodeNotFound, 0, "")
	for _, a := range plain.LogAttrs() {
		if a.Key == "internal" || a.Key == "request_id" {
			t.Errorf("unexpected attribute %q when unset", a.Key)
		}
	}
}

func TestLogLevels(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	New(CodeInternal, 0, "").Log(context.Background(), logger)
	New(CodeNotFound, 0, "").Log(context.Background(), logger)

	out := buf.String()
	if !strings.Contains(out, "level=ERROR") {
		t.Fatalf("5xx must log at ERROR: %s", out)
	}
	if !strings.Contains(out, "level=WARN") {
		t.Fatalf("4xx must log at WARN: %s", out)
	}

	var nilErr *Error
	nilErr.Log(context.Background(), logger)
}
