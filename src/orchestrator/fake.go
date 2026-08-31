package orchestrator

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
)

// The fakes in this file are ordinary working implementations of the two
// package interfaces, not test scaffolding: they live outside _test.go so
// the hosting layer and the admin panel can exercise their own code paths
// against a scripted orchestrator without a container engine, a hypervisor,
// root privileges, or a socket of any kind.

// DoerFunc adapts a plain function to the Doer interface.
type DoerFunc func(req *http.Request) (*http.Response, error)

// Do performs one round trip by calling the wrapped function.
func (f DoerFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

// HandlerDoer serves engine API requests from an in-process http.Handler.
// No socket is opened and no listener is bound, so a caller can drive a
// backend end to end on a host with nothing installed.
type HandlerDoer struct {
	// Handler answers every request.
	Handler http.Handler

	mu       sync.Mutex
	requests []RecordedRequest
}

// RecordedRequest is one request a fake observed, kept so a caller can
// assert on exactly what a backend sent.
type RecordedRequest struct {
	// Method is the HTTP method.
	Method string
	// Path is the request path.
	Path string
	// RawQuery is the encoded query string.
	RawQuery string
	// Body is the request body.
	Body []byte
	// Header is the request header set.
	Header http.Header
}

// NewHandlerDoer builds an in-process Doer over h.
func NewHandlerDoer(h http.Handler) *HandlerDoer { return &HandlerDoer{Handler: h} }

// Do records the request and serves it from the wrapped handler.
func (d *HandlerDoer) Do(req *http.Request) (*http.Response, error) {
	record := RecordedRequest{
		Method:   req.Method,
		Path:     req.URL.Path,
		RawQuery: req.URL.RawQuery,
		Header:   req.Header.Clone(),
	}
	if req.Body != nil {
		record.Body = readCapped(req.Body, DefaultMaxBodyBytes)
		req.Body.Close()
		req.Body = http.NoBody
	}

	d.mu.Lock()
	d.requests = append(d.requests, record)
	d.mu.Unlock()

	if err := req.Context().Err(); err != nil {
		return nil, err
	}

	rec := &responseRecorder{status: http.StatusOK, header: make(http.Header)}
	d.Handler.ServeHTTP(rec, req)
	return rec.result(req), nil
}

// responseRecorder is a minimal http.ResponseWriter that buffers a
// response. It exists instead of net/http/httptest so this package's
// production build never links the testing flag machinery.
type responseRecorder struct {
	status  int
	header  http.Header
	body    bytes.Buffer
	written bool
}

// Header returns the response header map.
func (r *responseRecorder) Header() http.Header { return r.header }

// WriteHeader records the status code of the first call.
func (r *responseRecorder) WriteHeader(status int) {
	if !r.written {
		r.status = status
		r.written = true
	}
}

// Write buffers response bytes, defaulting the status to 200.
func (r *responseRecorder) Write(p []byte) (int, error) {
	r.WriteHeader(r.status)
	return r.body.Write(p)
}

// result materializes the buffered response.
func (r *responseRecorder) result(req *http.Request) *http.Response {
	body := r.body.Bytes()
	return &http.Response{
		Status:        http.StatusText(r.status),
		StatusCode:    r.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        r.header.Clone(),
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

// Requests returns a copy of everything the fake observed.
func (d *HandlerDoer) Requests() []RecordedRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]RecordedRequest, len(d.requests))
	copy(out, d.requests)
	return out
}

// RecordedCommand is one external command a FakeRunner observed.
type RecordedCommand struct {
	// Bin is the resolved binary path.
	Bin string
	// Args is the argv slice, excluding the binary itself.
	Args []string
	// Stdin is the input that was fed to the command.
	Stdin []byte
}

// FakeRunner is a Runner that never spawns a process. It answers from a
// caller-supplied function and records every invocation, so a test can
// assert on the exact argv a backend built.
type FakeRunner struct {
	// Respond returns the scripted result for one command. A nil Respond
	// succeeds with empty output.
	Respond func(bin string, args []string, stdin []byte) (RunResult, error)

	mu       sync.Mutex
	commands []RecordedCommand
}

// Run records the invocation and returns the scripted result.
func (f *FakeRunner) Run(ctx context.Context, bin string, args []string, stdin []byte) (RunResult, error) {
	f.mu.Lock()
	f.commands = append(f.commands, RecordedCommand{
		Bin:   bin,
		Args:  append([]string(nil), args...),
		Stdin: append([]byte(nil), stdin...),
	})
	f.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return RunResult{}, timeoutErr(BackendLibvirt, "exec", err)
	}
	if f.Respond == nil {
		return RunResult{}, nil
	}
	return f.Respond(bin, args, stdin)
}

// Commands returns a copy of everything the fake observed.
func (f *FakeRunner) Commands() []RecordedCommand {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]RecordedCommand, len(f.commands))
	copy(out, f.commands)
	return out
}
