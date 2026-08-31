package netinfo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
)

// Request ID headers, checked in order.
const (
	HeaderRequestID     = "X-Request-ID"
	HeaderCorrelationID = "X-Correlation-ID"
	HeaderTraceID       = "X-Trace-ID"
)

// requestIDKey holds the request ID in the request context.
const requestIDKey contextKey = "request_id"

// NewRequestID returns a random UUID v4 built from crypto/rand.
func NewRequestID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand never fails on the supported platforms; a failure
		// here means the system entropy source is gone, and an empty ID
		// would break tracing, so fall back to a fixed-shape nil UUID.
		return "00000000-0000-4000-8000-000000000000"
	}

	// Version 4 and the RFC 4122 variant.
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80

	encoded := hex.EncodeToString(buf)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

// IsValidUUID reports whether a string is a canonical 8-4-4-4-12 UUID.
func IsValidUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	if value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		isDigit := r >= '0' && r <= '9'
		isLower := r >= 'a' && r <= 'f'
		isUpper := r >= 'A' && r <= 'F'
		if !isDigit && !isLower && !isUpper {
			return false
		}
	}
	return true
}

// RequestIDFromHeaders returns the client-supplied request ID, or an empty
// string when none of the accepted headers carries one.
func RequestIDFromHeaders(r *http.Request) string {
	return headerValue(r, HeaderRequestID, HeaderCorrelationID, HeaderTraceID)
}

// ResolveRequestID returns the request ID to use: the client's when it is a
// valid UUID, otherwise a freshly generated one. The second result reports
// whether the ID was generated, so the caller can log the reason.
func ResolveRequestID(r *http.Request) (string, bool) {
	requestID := RequestIDFromHeaders(r)
	if requestID == "" || !IsValidUUID(requestID) {
		return NewRequestID(), true
	}
	return requestID, false
}

// WithRequestID stores a request ID in the context.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

// RequestIDFromContext returns the stored request ID, or an empty string.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

// RequestID returns the request ID carried by a request.
func RequestID(r *http.Request) string {
	if r == nil {
		return ""
	}
	return RequestIDFromContext(r.Context())
}

// RequestIDMiddleware guarantees every request has a request ID: it accepts
// a valid client ID, generates one otherwise, echoes it in the response,
// and stores it in the context for logging and downstream calls.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID, generated := ResolveRequestID(r)

		if generated {
			if supplied := RequestIDFromHeaders(r); supplied != "" {
				Logf("invalid request id %q replaced with %s", supplied, requestID)
			}
		}

		w.Header().Set(HeaderRequestID, requestID)

		ctx := WithRequestID(r.Context(), requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// PropagateRequestID copies the request ID onto an outgoing request so
// downstream services join the same trace.
func PropagateRequestID(ctx context.Context, outgoing *http.Request) {
	if outgoing == nil {
		return
	}
	if requestID := RequestIDFromContext(ctx); requestID != "" {
		outgoing.Header.Set(HeaderRequestID, requestID)
	}
}

// TrimBearer strips a scheme prefix such as "Bearer " from a credential.
func TrimBearer(value string) string {
	parts := strings.SplitN(strings.TrimSpace(value), " ", 2)
	if len(parts) != 2 {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(parts[1])
}
