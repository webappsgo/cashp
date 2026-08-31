// Package middleware implements cashp's HTTP middleware chain: request IDs,
// structured request logging, panic recovery, real-IP resolution behind
// trusted proxies, security headers, CSRF, rate limiting, content
// negotiation, and CORS (AI.md PART 11, PART 12, PART 14).
package middleware

import (
	"bufio"
	"errors"
	"net"
	"net/http"
)

// recorder wraps an http.ResponseWriter to capture the status code and the
// byte count for the logging middleware, and to let the recovery middleware
// know whether a response has already started.
type recorder struct {
	http.ResponseWriter
	status  int
	written int64
	wrote   bool
}

// newRecorder wraps a response writer.
func newRecorder(w http.ResponseWriter) *recorder {
	return &recorder{ResponseWriter: w, status: http.StatusOK}
}

// WriteHeader records the status code once and forwards it.
func (rec *recorder) WriteHeader(status int) {
	if rec.wrote {
		return
	}
	rec.status = status
	rec.wrote = true
	rec.ResponseWriter.WriteHeader(status)
}

// Write records the byte count and forwards the body.
func (rec *recorder) Write(b []byte) (int, error) {
	if !rec.wrote {
		rec.WriteHeader(http.StatusOK)
	}
	n, err := rec.ResponseWriter.Write(b)
	rec.written += int64(n)
	return n, err
}

// Flush forwards a flush when the underlying writer supports it.
func (rec *recorder) Flush() {
	if f, ok := rec.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards a connection hijack, which WebSocket upgrades need.
func (rec *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := rec.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("response writer does not support hijacking")
}

// Unwrap exposes the wrapped writer to http.ResponseController.
func (rec *recorder) Unwrap() http.ResponseWriter {
	return rec.ResponseWriter
}
