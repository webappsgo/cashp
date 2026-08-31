package netinfo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNewRequestID checks that generated IDs are unique version 4 UUIDs.
func TestNewRequestID(t *testing.T) {
	seen := make(map[string]bool, 128)

	for i := 0; i < 128; i++ {
		id := NewRequestID()
		if !IsValidUUID(id) {
			t.Fatalf("NewRequestID returned %q, which is not a UUID", id)
		}
		if id[14] != '4' {
			t.Fatalf("NewRequestID returned %q, which is not version 4", id)
		}
		if strings.IndexByte("89ab", id[19]) < 0 {
			t.Fatalf("NewRequestID returned %q, which has the wrong variant", id)
		}
		if seen[id] {
			t.Fatalf("NewRequestID returned the duplicate %q", id)
		}
		seen[id] = true
	}
}

// TestIsValidUUID checks the accepted and rejected shapes.
func TestIsValidUUID(t *testing.T) {
	valid := []string{
		"550e8400-e29b-41d4-a716-446655440000",
		"550E8400-E29B-41D4-A716-446655440000",
	}
	for _, value := range valid {
		if !IsValidUUID(value) {
			t.Errorf("%q must be accepted", value)
		}
	}

	invalid := []string{
		"",
		"not-a-uuid",
		"550e8400e29b41d4a716446655440000",
		"550e8400-e29b-41d4-a716-44665544000",
		"550e8400-e29b-41d4-a716-4466554400000",
		"550e8400-e29b-41d4-a716-44665544zzzz",
		"550e8400_e29b_41d4_a716_446655440000",
	}
	for _, value := range invalid {
		if IsValidUUID(value) {
			t.Errorf("%q must be rejected", value)
		}
	}
}

// TestResolveRequestIDPassthrough checks each accepted header and that a
// valid client ID is preserved rather than replaced.
func TestResolveRequestIDPassthrough(t *testing.T) {
	const clientID = "550e8400-e29b-41d4-a716-446655440000"

	for _, header := range []string{HeaderRequestID, HeaderCorrelationID, HeaderTraceID} {
		r := httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil)
		r.Header.Set(header, clientID)

		requestID, generated := ResolveRequestID(r)
		if requestID != clientID {
			t.Errorf("with %s the request id = %q, want the client value", header, requestID)
		}
		if generated {
			t.Errorf("with %s a valid client id must not be regenerated", header)
		}
	}

	if got := RequestIDFromHeaders(httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil)); got != "" {
		t.Errorf("RequestIDFromHeaders = %q, want an empty string", got)
	}
}

// TestResolveRequestIDGenerates checks that a missing or malformed client ID
// is replaced with a fresh one.
func TestResolveRequestIDGenerates(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil)

	requestID, generated := ResolveRequestID(r)
	if !generated || !IsValidUUID(requestID) {
		t.Fatalf("a missing id must be generated, got %q generated=%v", requestID, generated)
	}

	r.Header.Set(HeaderRequestID, "'; DROP TABLE users; --")
	requestID, generated = ResolveRequestID(r)
	if !generated || !IsValidUUID(requestID) {
		t.Fatalf("a malformed id must be replaced, got %q generated=%v", requestID, generated)
	}
}

// TestRequestIDMiddleware checks the response echo and the context storage.
func TestRequestIDMiddleware(t *testing.T) {
	const clientID = "550e8400-e29b-41d4-a716-446655440000"

	var seen string
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestID(r)
	}))

	r := httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil)
	r.Header.Set(HeaderRequestID, clientID)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if seen != clientID {
		t.Errorf("the handler saw request id %q, want %q", seen, clientID)
	}
	if got := w.Header().Get(HeaderRequestID); got != clientID {
		t.Errorf("the response echoed %q, want %q", got, clientID)
	}

	r = httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	echoed := w.Header().Get(HeaderRequestID)
	if !IsValidUUID(echoed) {
		t.Fatalf("the response echoed %q, want a generated UUID", echoed)
	}
	if seen != echoed {
		t.Errorf("the handler saw %q but the response echoed %q", seen, echoed)
	}
}

// TestRequestIDContextHelpers checks storage, retrieval, and the empty cases.
func TestRequestIDContextHelpers(t *testing.T) {
	const requestID = "550e8400-e29b-41d4-a716-446655440000"

	ctx := WithRequestID(context.Background(), requestID)
	if got := RequestIDFromContext(ctx); got != requestID {
		t.Errorf("RequestIDFromContext = %q, want %q", got, requestID)
	}
	if got := RequestIDFromContext(context.Background()); got != "" {
		t.Errorf("an unset context returned %q, want an empty string", got)
	}
	if got := RequestID(nil); got != "" {
		t.Errorf("RequestID(nil) = %q, want an empty string", got)
	}
}

// TestPropagateRequestID checks that downstream calls join the same trace.
func TestPropagateRequestID(t *testing.T) {
	const requestID = "550e8400-e29b-41d4-a716-446655440000"

	outgoing := httptest.NewRequest(http.MethodGet, "http://service.internal/", nil)
	PropagateRequestID(WithRequestID(context.Background(), requestID), outgoing)
	if got := outgoing.Header.Get(HeaderRequestID); got != requestID {
		t.Errorf("the outgoing header = %q, want %q", got, requestID)
	}

	bare := httptest.NewRequest(http.MethodGet, "http://service.internal/", nil)
	PropagateRequestID(context.Background(), bare)
	if got := bare.Header.Get(HeaderRequestID); got != "" {
		t.Errorf("with no stored id the outgoing header = %q, want an empty string", got)
	}

	// A nil request must be tolerated rather than panic.
	PropagateRequestID(context.Background(), nil)
}

// TestTrimBearer checks scheme stripping.
func TestTrimBearer(t *testing.T) {
	cases := map[string]string{
		"Bearer abc123": "abc123",
		"token xyz":     "xyz",
		"abc123":        "abc123",
		"  abc123  ":    "abc123",
		"":              "",
	}

	for input, want := range cases {
		if got := TrimBearer(input); got != want {
			t.Errorf("TrimBearer(%q) = %q, want %q", input, got, want)
		}
	}
}
