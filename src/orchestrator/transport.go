package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Doer is the HTTP contract every socket-backed engine client is built on.
// It exists so the Docker, Podman, and Incus backends can be exercised
// against an in-process test server: nothing in this package ever reaches
// for http.DefaultClient or opens a socket implicitly.
type Doer interface {
	// Do performs one HTTP round trip.
	Do(req *http.Request) (*http.Response, error)
}

// Transport limits. An engine API answering over a local socket has no
// business returning more than a few megabytes to a control-plane call, and
// an unbounded read of an attacker-influenced response is a memory
// exhaustion surface on a shared node.
const (
	// DefaultMaxBodyBytes caps a decoded JSON response.
	DefaultMaxBodyBytes int64 = 8 << 20
	// DefaultMaxLogBytes caps a decoded log payload.
	DefaultMaxLogBytes int64 = 1 << 20
	// MaxLogBytesCeiling is the largest log payload any caller may ask for.
	MaxLogBytesCeiling int64 = 8 << 20
	// DefaultLogTail is the line count used when a caller asks for none.
	DefaultLogTail = 200
	// MaxLogTail is the largest line count any caller may ask for.
	MaxLogTail = 10000
	// DefaultExecOutputBytes caps captured exec output per stream.
	DefaultExecOutputBytes int64 = 256 << 10
	// MaxExecOutputBytes is the largest exec capture any caller may ask for.
	MaxExecOutputBytes int64 = 4 << 20
)

// UnixTransport speaks HTTP over a unix domain socket. It is the real
// implementation behind Doer for Docker, Podman, and Incus, all three of
// which expose an ordinary REST API on a local socket.
type UnixTransport struct {
	socket string
	client *http.Client
}

// NewUnixTransport builds a transport bound to one engine socket. The
// socket path comes from operator configuration and is shape-checked here
// so a misconfiguration surfaces at construction rather than on first use.
func NewUnixTransport(socket string, timeout time.Duration) (*UnixTransport, error) {
	if err := ValidateSocketPath(socket); err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}
	dialer := &net.Dialer{Timeout: timeout}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		},
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: timeout,
		DisableCompression:    true,
	}
	return &UnixTransport{socket: socket, client: &http.Client{Transport: tr}}, nil
}

// Do performs one round trip over the engine socket.
func (t *UnixTransport) Do(req *http.Request) (*http.Response, error) {
	return t.client.Do(req)
}

// Close releases pooled connections held by the transport.
func (t *UnixTransport) Close() {
	if tr, ok := t.client.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}

// apiClient is the shared JSON plumbing over a Doer. Each backend supplies
// its own base URL and version prefix; the encoding, size capping, status
// handling, and error redaction are identical for all of them.
type apiClient struct {
	// doer performs the round trip.
	doer Doer
	// backend names the engine, for error details only.
	backend BackendName
	// base is the scheme and host prefix, e.g. "http://docker".
	base string
	// prefix is the API version path segment, e.g. "/v1.43".
	prefix string
	// maxBody caps a decoded JSON response.
	maxBody int64
}

// newAPIClient builds a client with the package response cap applied.
func newAPIClient(doer Doer, backend BackendName, host, prefix string) *apiClient {
	return &apiClient{
		doer:    doer,
		backend: backend,
		base:    "http://" + host,
		prefix:  prefix,
		maxBody: DefaultMaxBodyBytes,
	}
}

// endpoint builds the absolute URL for an API path. Callers assemble p
// from a constant route and identifiers passed through pathEscape, so a
// name can never break out of its path segment.
func (c *apiClient) endpoint(p string, q url.Values) string {
	var b strings.Builder
	b.WriteString(c.base)
	b.WriteString(c.prefix)
	b.WriteString(p)
	if len(q) > 0 {
		b.WriteString("?")
		b.WriteString(q.Encode())
	}
	return b.String()
}

// stream issues a request and returns the live response. The caller owns
// the body and must close it. Non-2xx responses are converted to a safe
// error and the body is drained and closed before returning.
func (c *apiClient) stream(ctx context.Context, method, p string, q url.Values, body any, headers map[string]string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, backendErr(c.backend, "encode_request", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(p, q), reader)
	if err != nil {
		return nil, backendErr(c.backend, "build_request", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.doer.Do(req)
	if err != nil {
		if stderrors.Is(err, context.DeadlineExceeded) {
			return nil, timeoutErr(c.backend, p, err)
		}
		if stderrors.Is(err, context.Canceled) {
			return nil, unavailableErr(c.backend, "socket", err)
		}
		return nil, unavailableErr(c.backend, "socket", err)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}

	detail := readCapped(resp.Body, 4096)
	resp.Body.Close()
	return nil, c.statusError(p, resp.StatusCode, detail)
}

// statusError converts a non-2xx engine response into a typed application
// error. The engine's own message is kept only in the wrapped cause, which
// is logged and never rendered into an HTTP response, because engine
// messages routinely quote host paths.
func (c *apiClient) statusError(op string, status int, detail []byte) error {
	cause := fmt.Errorf("%s: status %d: %s", c.backend, status, strings.TrimSpace(string(detail)))
	switch status {
	case http.StatusNotFound:
		return notFoundErr().WithCause(cause)
	case http.StatusConflict:
		return backendErr(c.backend, op, cause)
	case http.StatusNotImplemented:
		return unsupportedErr(c.backend, op).WithCause(cause)
	case http.StatusUnauthorized, http.StatusForbidden:
		return unavailableErr(c.backend, "permission", cause)
	default:
		return backendErr(c.backend, op, cause)
	}
}

// do issues a request and decodes a JSON response into out. A nil out
// discards the body after draining it so the connection can be reused.
func (c *apiClient) do(ctx context.Context, method, p string, q url.Values, body, out any) error {
	resp, err := c.stream(ctx, method, p, q, body, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	payload := readCapped(resp.Body, c.maxBody)
	if out == nil || len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return backendErr(c.backend, "decode_response", err)
	}
	return nil
}

// readCapped reads at most limit bytes and always drains what it can so the
// underlying connection stays reusable.
func readCapped(r io.Reader, limit int64) []byte {
	if limit <= 0 {
		limit = DefaultMaxBodyBytes
	}
	data, _ := io.ReadAll(io.LimitReader(r, limit))
	_, _ = io.Copy(io.Discard, io.LimitReader(r, limit))
	return data
}

// pathEscape escapes an already-validated identifier before it becomes a
// URL path segment. The allowlists make this a no-op in practice; it is
// kept so a future relaxation of an allowlist cannot silently create a path
// injection.
func pathEscape(s string) string { return url.PathEscape(s) }

// clampTail bounds a caller-supplied log line count.
func clampTail(tail int) int {
	if tail <= 0 {
		return DefaultLogTail
	}
	if tail > MaxLogTail {
		return MaxLogTail
	}
	return tail
}

// clampLogBytes bounds a caller-supplied log payload cap.
func clampLogBytes(n int64) int64 {
	if n <= 0 {
		return DefaultMaxLogBytes
	}
	if n > MaxLogBytesCeiling {
		return MaxLogBytesCeiling
	}
	return n
}

// clampExecBytes bounds a caller-supplied exec output cap.
func clampExecBytes(n int64) int64 {
	if n <= 0 {
		return DefaultExecOutputBytes
	}
	if n > MaxExecOutputBytes {
		return MaxExecOutputBytes
	}
	return n
}

// dockerFrameHeaderLen is the length of the 8-byte header Docker and the
// Podman compatibility API prepend to each chunk of a multiplexed stream.
const dockerFrameHeaderLen = 8

// demuxStream decodes Docker's multiplexed attach format into per-stream
// byte buffers. The reader is bounded by maxBytes; hitting the cap sets the
// truncated flag rather than growing the buffers without limit.
func demuxStream(r io.Reader, maxBytes int64) (stdout, stderr []byte, truncated bool, err error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxLogBytes
	}
	limited := io.LimitReader(r, maxBytes+1)
	var total int64
	header := make([]byte, dockerFrameHeaderLen)

	for {
		if _, rerr := io.ReadFull(limited, header); rerr != nil {
			if stderrors.Is(rerr, io.EOF) || stderrors.Is(rerr, io.ErrUnexpectedEOF) {
				return stdout, stderr, truncated, nil
			}
			return stdout, stderr, truncated, rerr
		}
		total += dockerFrameHeaderLen

		size := int64(header[4])<<24 | int64(header[5])<<16 | int64(header[6])<<8 | int64(header[7])
		if size < 0 || size > maxBytes {
			size = maxBytes
			truncated = true
		}
		if total+size > maxBytes {
			size = maxBytes - total
			truncated = true
		}
		if size <= 0 {
			return stdout, stderr, truncated, nil
		}

		chunk := make([]byte, size)
		n, rerr := io.ReadFull(limited, chunk)
		total += int64(n)
		switch header[0] {
		case 2:
			stderr = append(stderr, chunk[:n]...)
		default:
			stdout = append(stdout, chunk[:n]...)
		}
		if rerr != nil {
			if stderrors.Is(rerr, io.EOF) || stderrors.Is(rerr, io.ErrUnexpectedEOF) {
				return stdout, stderr, truncated, nil
			}
			return stdout, stderr, truncated, rerr
		}
		if total >= maxBytes {
			return stdout, stderr, true, nil
		}
	}
}

// splitLogLines turns a decoded stream buffer into bounded LogLine records,
// keeping only the trailing tail lines. A Docker log line may carry an
// RFC3339Nano timestamp prefix when timestamps were requested; it is parsed
// out when present.
func splitLogLines(stream string, data []byte, tail int) []LogLine {
	if len(data) == 0 {
		return nil
	}
	raw := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if tail > 0 && len(raw) > tail {
		raw = raw[len(raw)-tail:]
	}
	out := make([]LogLine, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSuffix(line, "\r")
		entry := LogLine{Stream: stream, Text: line}
		if head, rest, ok := strings.Cut(line, " "); ok {
			if ts, perr := time.Parse(time.RFC3339Nano, head); perr == nil {
				entry.Time = ts.UTC()
				entry.Text = rest
			}
		}
		out = append(out, entry)
	}
	return out
}

// mergeLogLines interleaves two decoded streams into one tail-bounded
// slice, ordered by timestamp where both sides carry one.
func mergeLogLines(stdout, stderr []LogLine, tail int) []LogLine {
	all := make([]LogLine, 0, len(stdout)+len(stderr))
	all = append(all, stdout...)
	all = append(all, stderr...)
	if len(all) > 1 {
		sortLogLines(all)
	}
	if tail > 0 && len(all) > tail {
		all = all[len(all)-tail:]
	}
	return all
}

// sortLogLines orders records by timestamp, leaving untimestamped records
// in their original relative position at the front.
func sortLogLines(lines []LogLine) {
	for i := 1; i < len(lines); i++ {
		for j := i; j > 0; j-- {
			if lines[j-1].Time.IsZero() || lines[j].Time.IsZero() {
				break
			}
			if lines[j-1].Time.Before(lines[j].Time) {
				break
			}
			lines[j-1], lines[j] = lines[j], lines[j-1]
		}
	}
}
