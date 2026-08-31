package api

import (
	"context"
	"net/http"
	"sync"
)

// errorRecorder captures the machine-readable error code of a response so
// the logging middleware can include it in the log record without parsing
// the response body.
type errorRecorder struct {
	mu   sync.Mutex
	code string
}

// set stores the first error code seen for a request.
func (rec *errorRecorder) set(code string) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.code == "" {
		rec.code = code
	}
}

// get returns the recorded error code, empty when the request succeeded.
func (rec *errorRecorder) get() string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.code
}

// WithErrorRecorder returns a context carrying a fresh error recorder. The
// logging middleware installs one per request.
func WithErrorRecorder(ctx context.Context) context.Context {
	return context.WithValue(ctx, errorRecorderKey, &errorRecorder{})
}

// RecordErrorCode notes the error code of the response being written. It is
// a no-op when no recorder is installed.
func RecordErrorCode(r *http.Request, code string) {
	if r == nil || code == "" {
		return
	}
	if rec, ok := r.Context().Value(errorRecorderKey).(*errorRecorder); ok {
		rec.set(code)
	}
}

// RecordedErrorCode returns the error code recorded for a request context.
func RecordedErrorCode(ctx context.Context) string {
	if rec, ok := ctx.Value(errorRecorderKey).(*errorRecorder); ok {
		return rec.get()
	}
	return ""
}
