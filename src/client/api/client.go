// Package api is the cashp-cli HTTP client. It speaks the versioned panel
// API described in AI.md PART 33, decodes the {ok,data} / {ok,error,message}
// envelope, and fails over to cluster members on connection-level errors
// only.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/client/urlutil"
	"github.com/webappsgo/cashp/src/config"
)

// MaxResponseBytes caps how much of a response body the client will read so
// a hostile or broken endpoint cannot exhaust CLI memory.
const MaxResponseBytes int64 = 32 << 20

// AutodiscoverPath is unversioned by design: the CLI calls it before it
// knows which API version the server speaks.
const AutodiscoverPath = "/api/autodiscover"

// Envelope is the standard API response wrapper. Health endpoints return
// bare documents and are decoded directly instead.
type Envelope struct {
	OK      bool            `json:"ok"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
	Details map[string]any  `json:"details"`
}

// Options configures a Client.
type Options struct {
	// Primary is the configured server base URL.
	Primary string
	// Cluster holds additional base URLs used only for failover.
	Cluster []string
	// Token is the bearer credential; an empty token sends no header.
	Token string
	// Version is the CLI build version used in the User-Agent.
	Version string
	// APIVersion is the path segment used by VersionedPath.
	APIVersion string
	// Timeout bounds a single HTTP attempt.
	Timeout time.Duration
	// Retry is the number of attempts per host, minimum 1.
	Retry int
	// RetryDelay is the pause between attempts against the same host.
	RetryDelay time.Duration
	// Debug enables request tracing through DebugLog.
	Debug bool
	// DebugLog receives trace lines; it never receives credentials.
	DebugLog func(format string, args ...any)
	// HTTPClient overrides the transport, used by tests.
	HTTPClient *http.Client
}

// Client performs authenticated requests against the panel API.
type Client struct {
	primary    string
	cluster    []string
	active     string
	token      string
	userAgent  string
	apiVersion string
	retry      int
	retryDelay time.Duration
	debug      bool
	debugLog   func(format string, args ...any)
	http       *http.Client
}

// ValidateServerURL checks an operator-supplied panel URL.
//
// security.ValidateOutboundURL is deliberately NOT used here: it is the
// server-side SSRF guard and rejects localhost, loopback and RFC1918 hosts,
// which are the normal deployment shape for a self-hosted panel. The CLI's
// server URL comes from the operator, not from remote data, so the correct
// gate is a syntactic one.
func ValidateServerURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return newError(KindConfig, "server URL is not configured", nil)
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return newError(KindConfig, "server URL is not a valid URL", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return newError(KindConfig, "server URL must use http or https", nil)
	}
	if u.Host == "" {
		return newError(KindConfig, "server URL is missing a host", nil)
	}
	if u.User != nil {
		return newError(KindConfig, "server URL must not embed credentials", nil)
	}
	return nil
}

// IsValidServerURL reports whether raw passes ValidateServerURL.
func IsValidServerURL(raw string) bool {
	return ValidateServerURL(raw) == nil
}

// New builds a Client from opts, validating every candidate base URL.
func New(opts Options) (*Client, error) {
	if err := ValidateServerURL(opts.Primary); err != nil {
		return nil, err
	}

	cluster := make([]string, 0, len(opts.Cluster))
	for _, member := range opts.Cluster {
		normalized := urlutil.NormalizeBase(member)
		if normalized == "" || !IsValidServerURL(normalized) {
			continue
		}
		cluster = append(cluster, normalized)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	retry := opts.Retry
	if retry < 1 {
		retry = 1
	}
	delay := opts.RetryDelay
	if delay < 0 {
		delay = 0
	}
	apiVersion := strings.TrimSpace(opts.APIVersion)
	if apiVersion == "" {
		apiVersion = config.DefaultAPIVersion
	}
	version := strings.TrimSpace(opts.Version)
	if version == "" {
		version = "devel"
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}

	primary := urlutil.NormalizeBase(opts.Primary)
	return &Client{
		primary:    primary,
		cluster:    cluster,
		active:     primary,
		token:      opts.Token,
		userAgent:  "cashp-cli/" + version,
		apiVersion: apiVersion,
		retry:      retry,
		retryDelay: delay,
		debug:      opts.Debug,
		debugLog:   opts.DebugLog,
		http:       httpClient,
	}, nil
}

// UserAgent returns the fixed User-Agent string. It is hardcoded to
// cashp-cli regardless of how the binary was renamed on disk.
func (c *Client) UserAgent() string {
	return c.userAgent
}

// APIVersion returns the API version segment in use.
func (c *Client) APIVersion() string {
	return c.apiVersion
}

// ActiveServer returns the base URL currently serving requests.
func (c *Client) ActiveServer() string {
	return c.active
}

// Primary returns the configured primary base URL.
func (c *Client) Primary() string {
	return c.primary
}

// SetToken replaces the bearer credential for subsequent requests.
func (c *Client) SetToken(token string) {
	c.token = token
}

// HasToken reports whether a bearer credential is configured.
func (c *Client) HasToken() bool {
	return strings.TrimSpace(c.token) != ""
}

// VersionedPath prefixes a resource path with /api/{api_version}. The
// version is never hardcoded at a call site.
func (c *Client) VersionedPath(resourcePath string) string {
	trimmed := strings.TrimPrefix(resourcePath, "/")
	return "/api/" + c.apiVersion + "/" + trimmed
}

// candidates returns the ordered list of base URLs to try: the active host
// first, then the primary, then every cluster member, without duplicates.
func (c *Client) candidates() []string {
	ordered := []string{c.active, c.primary}
	ordered = append(ordered, c.cluster...)

	seen := make(map[string]bool, len(ordered))
	unique := make([]string, 0, len(ordered))
	for _, base := range ordered {
		if base == "" || seen[base] {
			continue
		}
		seen[base] = true
		unique = append(unique, base)
	}
	return unique
}

// Request describes one API call.
type Request struct {
	// Method is the HTTP method; empty means GET.
	Method string
	// Path is a path template that may contain {name} placeholders.
	Path string
	// PathParams substitutes placeholders, path-escaped.
	PathParams map[string]string
	// Query holds query parameters, query-escaped.
	Query map[string]string
	// Body is JSON-encoded when non-nil.
	Body any
	// Raw skips envelope validation, for endpoints such as /healthz that
	// return a bare document.
	Raw bool
}

// Do performs req with retry and cluster failover, returning the decoded
// envelope on success.
func (c *Client) Do(ctx context.Context, req Request) (*Envelope, error) {
	body, err := encodeBody(req.Body)
	if err != nil {
		return nil, err
	}

	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}

	var lastErr error
	for _, base := range c.candidates() {
		endpoint := urlutil.BuildAPIURL(base, req.Path, req.PathParams, req.Query)
		if endpoint == "" {
			lastErr = newError(KindConfig, "server URL is not a valid URL", nil)
			continue
		}

		env, err := c.attempt(ctx, method, endpoint, body, req.Raw)
		if err == nil {
			c.active = base
			return env, nil
		}
		lastErr = err

		// Only a transport-level failure justifies trying another cluster
		// member. A 4xx is the server's considered answer and re-asking a
		// different node would just replay a rejected request.
		if KindOf(err) != KindConnection {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, err
		}
	}

	if lastErr == nil {
		lastErr = newError(KindConnection, "no server URL available", nil)
	}
	return nil, lastErr
}

// Get performs a GET request against a versioned resource path.
func (c *Client) Get(ctx context.Context, path string, query map[string]string) (*Envelope, error) {
	return c.Do(ctx, Request{Method: http.MethodGet, Path: c.VersionedPath(path), Query: query})
}

// Post performs a POST request against a versioned resource path.
func (c *Client) Post(ctx context.Context, path string, body any) (*Envelope, error) {
	return c.Do(ctx, Request{Method: http.MethodPost, Path: c.VersionedPath(path), Body: body})
}

// Put performs a PUT request against a versioned resource path.
func (c *Client) Put(ctx context.Context, path string, body any) (*Envelope, error) {
	return c.Do(ctx, Request{Method: http.MethodPut, Path: c.VersionedPath(path), Body: body})
}

// Patch performs a PATCH request against a versioned resource path.
func (c *Client) Patch(ctx context.Context, path string, body any) (*Envelope, error) {
	return c.Do(ctx, Request{Method: http.MethodPatch, Path: c.VersionedPath(path), Body: body})
}

// Delete performs a DELETE request against a versioned resource path.
func (c *Client) Delete(ctx context.Context, path string, query map[string]string) (*Envelope, error) {
	return c.Do(ctx, Request{Method: http.MethodDelete, Path: c.VersionedPath(path), Query: query})
}

// attempt runs a single endpoint up to c.retry times, retrying only on
// transport failures and 5xx responses.
func (c *Client) attempt(ctx context.Context, method, endpoint string, body []byte, raw bool) (*Envelope, error) {
	var lastErr error
	for try := 0; try < c.retry; try++ {
		if try > 0 {
			if err := sleepCtx(ctx, c.retryDelay); err != nil {
				return nil, newError(KindConnection, "request cancelled", err)
			}
		}

		env, retryable, err := c.roundTrip(ctx, method, endpoint, body, raw)
		if err == nil {
			return env, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
	}
	return nil, lastErr
}

// roundTrip performs one HTTP exchange and reports whether the failure is
// worth retrying against the same host.
func (c *Client) roundTrip(ctx context.Context, method, endpoint string, body []byte, raw bool) (*Envelope, bool, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, false, newError(KindConfig, "could not build request", err)
	}
	httpReq.Header.Set("User-Agent", c.userAgent)
	httpReq.Header.Set("Accept", "application/json")
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if c.HasToken() {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}

	c.trace("%s %s", method, endpoint)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, true, newError(KindConnection, "could not reach "+hostOf(endpoint), err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, MaxResponseBytes))
		_ = resp.Body.Close()
	}()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes))
	if err != nil {
		return nil, true, newError(KindConnection, "could not read response body", err)
	}

	c.trace("-> %d (%d bytes)", resp.StatusCode, len(payload))

	if resp.StatusCode >= 500 {
		return nil, true, decodeFailure(resp.StatusCode, payload)
	}
	if resp.StatusCode >= 400 {
		return nil, false, decodeFailure(resp.StatusCode, payload)
	}

	if raw {
		return &Envelope{OK: true, Data: json.RawMessage(payload)}, false, nil
	}

	env := &Envelope{}
	if err := json.Unmarshal(payload, env); err != nil {
		return nil, false, newError(KindGeneral, "server returned a malformed response", err)
	}
	if !env.OK {
		return nil, false, envelopeError(resp.StatusCode, env)
	}
	return env, false, nil
}

// trace emits a debug line when --debug is active.
func (c *Client) trace(format string, args ...any) {
	if !c.debug || c.debugLog == nil {
		return
	}
	c.debugLog(format, args...)
}

// Decode unmarshals the envelope's data field into out.
func (e *Envelope) Decode(out any) error {
	if len(e.Data) == 0 {
		return newError(KindGeneral, "server returned an empty response body", nil)
	}
	if err := json.Unmarshal(e.Data, out); err != nil {
		return newError(KindGeneral, "server returned an unexpected response shape", err)
	}
	return nil
}

// encodeBody JSON-encodes a request body, returning nil for a nil body.
func encodeBody(body any) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	if raw, ok := body.([]byte); ok {
		return raw, nil
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, newError(KindUsage, "request body could not be encoded", err)
	}
	return encoded, nil
}

// decodeFailure turns a non-2xx response into a classified error, using the
// envelope fields when the body is a well-formed envelope.
func decodeFailure(status int, payload []byte) *Error {
	env := &Envelope{}
	if err := json.Unmarshal(payload, env); err == nil && (env.Error != "" || env.Message != "") {
		return envelopeError(status, env)
	}
	return &Error{
		Kind:    kindForStatus(status, ""),
		Status:  status,
		Message: fmt.Sprintf("server returned HTTP %d", status),
	}
}

// envelopeError builds an Error from a decoded error envelope.
func envelopeError(status int, env *Envelope) *Error {
	message := env.Message
	if message == "" {
		message = fmt.Sprintf("server returned HTTP %d", status)
	}
	return &Error{
		Kind:    kindForStatus(status, env.Error),
		Code:    env.Error,
		Message: message,
		Status:  status,
	}
}

// kindForStatus maps an HTTP status and error code to a failure Kind.
func kindForStatus(status int, code string) Kind {
	switch code {
	case CodeTokenRevoked, CodeTokenExpired, CodeTokenInvalid:
		return KindAuth
	case "NOT_FOUND":
		return KindNotFound
	}
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return KindAuth
	case status == http.StatusNotFound:
		return KindNotFound
	case status == http.StatusBadRequest, status == http.StatusUnprocessableEntity:
		return KindUsage
	default:
		return KindGeneral
	}
}

// hostOf returns the host portion of an endpoint for error messages, so a
// full URL with query parameters is never echoed back to the user.
func hostOf(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return "server"
	}
	return u.Scheme + "://" + u.Host
}

// sleepCtx pauses for d unless ctx is cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ExitCode maps err to the CLI exit code table in AI.md PART 33.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		return 1
	}
	switch apiErr.Kind {
	case KindConfig:
		return 2
	case KindConnection:
		return 3
	case KindAuth:
		return 4
	case KindNotFound:
		return 5
	case KindUsage:
		return 64
	default:
		return 1
	}
}
