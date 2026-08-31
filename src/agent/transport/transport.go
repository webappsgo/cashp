// Package transport is the agent's outbound-only link to the panel. The
// agent never listens for connections: every exchange is a request the
// agent itself makes, authenticated with its own agent token, so a managed
// node exposes no new attack surface. See AI.md PART 33 "Agent Cluster
// Failover" and "Agent Registration API".
package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/webappsgo/cashp/src/client/urlutil"
	"github.com/webappsgo/cashp/src/security"
)

// MaxResponseBytes caps a panel response so a hostile or broken server
// cannot exhaust the agent's memory.
const MaxResponseBytes int64 = 8 << 20

// AutodiscoverPath is the unversioned cluster-discovery endpoint.
const AutodiscoverPath = "/api/autodiscover"

// Scope is the owner a set of agent credentials belongs to. It is derived
// from the token prefix and selects the API route family.
type Scope string

// The three agent scopes from AI.md PART 33 "Agent Token Format".
const (
	ScopeAdmin Scope = "admin"
	ScopeUser  Scope = "user"
	ScopeOrg   Scope = "org"
)

// ErrUnknownScope is returned when a token carries no recognised agent
// prefix. The agent refuses to talk to the panel with such a credential
// rather than guessing a route.
var ErrUnknownScope = errors.New("token is not an agent token")

// ErrOrgSlugRequired is returned when an org-scoped token is used without
// the owning org being known.
var ErrOrgSlugRequired = errors.New("org-scoped agent tokens require the owning org slug")

// ErrUnauthorized reports a rejected credential. The agent stops rather
// than retrying: a revoked token never becomes valid again by repetition.
var ErrUnauthorized = errors.New("the panel rejected this agent token")

// Envelope is the standard API response envelope.
type Envelope struct {
	OK      bool            `json:"ok"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
	Message string          `json:"message,omitempty"`
}

// Decode unmarshals the data member into out.
func (e *Envelope) Decode(out any) error {
	if out == nil {
		return nil
	}
	if len(e.Data) == 0 {
		return errors.New("the panel returned no data")
	}
	if err := json.Unmarshal(e.Data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// Options configures a Client.
type Options struct {
	Primary    string
	Cluster    []string
	Token      string
	Version    string
	APIVersion string
	AdminPath  string
	OrgSlug    string
	Timeout    time.Duration
	Retry      int
	RetryDelay time.Duration
	HTTPClient *http.Client
}

// Client performs authenticated outbound requests with cluster failover.
type Client struct {
	mu         sync.Mutex
	primary    string
	active     string
	cluster    []string
	token      string
	userAgent  string
	apiVersion string
	adminPath  string
	orgSlug    string
	scope      Scope
	retry      int
	retryDelay time.Duration
	http       *http.Client
}

// New validates the options and builds a Client. The token is required:
// the agent has no anonymous mode.
func New(opts Options) (*Client, error) {
	primary := strings.TrimSpace(opts.Primary)
	if err := ValidateServerURL(primary); err != nil {
		return nil, err
	}

	scope, err := ScopeOf(opts.Token)
	if err != nil {
		return nil, err
	}
	if scope == ScopeOrg && strings.TrimSpace(opts.OrgSlug) == "" {
		return nil, ErrOrgSlugRequired
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	retry := opts.Retry
	if retry < 0 {
		retry = 0
	}
	retryDelay := opts.RetryDelay
	if retryDelay <= 0 {
		retryDelay = 5 * time.Second
	}
	apiVersion := strings.Trim(strings.TrimSpace(opts.APIVersion), "/")
	if apiVersion == "" {
		return nil, errors.New("no API version configured")
	}
	adminPath := strings.Trim(strings.TrimSpace(opts.AdminPath), "/")
	if adminPath == "" {
		adminPath = "administration"
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}

	version := strings.TrimSpace(opts.Version)
	if version == "" {
		version = "devel"
	}

	return &Client{
		primary:    primary,
		active:     primary,
		cluster:    cleanURLs(opts.Cluster),
		token:      strings.TrimSpace(opts.Token),
		userAgent:  "cashp-agent/" + version,
		apiVersion: apiVersion,
		adminPath:  adminPath,
		orgSlug:    strings.TrimSpace(opts.OrgSlug),
		scope:      scope,
		retry:      retry,
		retryDelay: retryDelay,
		http:       httpClient,
	}, nil
}

// ScopeOf derives the owner scope from an agent token prefix. Anything
// that is not an agent token is rejected outright.
func ScopeOf(token string) (Scope, error) {
	prefix, _, err := security.ParseToken(strings.TrimSpace(token))
	if err != nil {
		return "", ErrUnknownScope
	}
	switch prefix {
	case security.PrefixAdminAgent:
		return ScopeAdmin, nil
	case security.PrefixUserAgent:
		return ScopeUser, nil
	case security.PrefixOrgAgent:
		return ScopeOrg, nil
	default:
		return "", ErrUnknownScope
	}
}

// ValidateServerURL accepts only an absolute http/https URL with a host and
// no embedded credentials. The panel is routinely on a private address or
// on localhost, so the outbound SSRF guard used for user-supplied remote
// URLs would reject every normal deployment; this check is the appropriate
// one for a configured control-plane endpoint.
func ValidateServerURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return errors.New("no server URL configured")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("server URL must start with http:// or https://")
	}
	if parsed.Host == "" {
		return errors.New("server URL is missing a host")
	}
	if parsed.User != nil {
		return errors.New("server URL must not contain embedded credentials")
	}
	return nil
}

// Scope reports the owner scope of the configured token.
func (c *Client) Scope() Scope {
	return c.scope
}

// ActiveServer reports the node currently being used.
func (c *Client) ActiveServer() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}

// Primary reports the configured primary node.
func (c *Client) Primary() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.primary
}

// Cluster reports the current failover list.
func (c *Client) Cluster() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.cluster...)
}

// SetCluster replaces the failover list, normally from autodiscover.
func (c *Client) SetCluster(primary string, nodes []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ValidateServerURL(primary) == nil {
		c.primary = primary
	}
	c.cluster = cleanURLs(nodes)
}

// BasePath is the scope-specific agent route family, without a leading or
// trailing slash.
func (c *Client) BasePath() string {
	switch c.scope {
	case ScopeAdmin:
		return "server/" + c.adminPath + "/config/agents"
	case ScopeUser:
		return "users/agents"
	default:
		return "orgs/" + urlutil.EncodePathSegment(c.orgSlug) + "/agents"
	}
}

// VersionedPath prefixes a resource path with the configured API version.
// The version is never hardcoded anywhere else in the agent.
func (c *Client) VersionedPath(resourcePath string) string {
	return "/api/" + c.apiVersion + "/" + strings.TrimPrefix(resourcePath, "/")
}

// Request describes one outbound call.
type Request struct {
	Method     string
	Path       string
	PathParams map[string]string
	Query      map[string]string
	Body       any
	// SkipEnvelope returns the raw body instead of decoding an envelope,
	// used for the unversioned health probes.
	SkipEnvelope bool
}

// Do performs a request, retrying transient failures and failing over to
// cluster nodes when a node is unreachable. Authentication failures never
// trigger failover or a retry.
func (c *Client) Do(ctx context.Context, req Request) (*Envelope, error) {
	var lastErr error

	for _, node := range c.candidates() {
		env, err := c.attempt(ctx, node, req)
		if err == nil {
			c.mu.Lock()
			c.active = node
			c.mu.Unlock()
			return env, nil
		}
		if !isFailover(err) {
			return nil, err
		}
		lastErr = err
	}

	if lastErr == nil {
		lastErr = errors.New("no server nodes are configured")
	}
	return nil, lastErr
}

// candidates lists the nodes to try, active first, then the primary, then
// the discovered cluster, without duplicates.
func (c *Client) candidates() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	ordered := append([]string{c.active, c.primary}, c.cluster...)
	seen := map[string]bool{}
	nodes := make([]string, 0, len(ordered))
	for _, node := range ordered {
		trimmed := strings.TrimRight(strings.TrimSpace(node), "/")
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		nodes = append(nodes, trimmed)
	}
	return nodes
}

// attempt runs one node's request with the configured retry budget.
func (c *Client) attempt(ctx context.Context, node string, req Request) (*Envelope, error) {
	var lastErr error

	for try := 0; try <= c.retry; try++ {
		if try > 0 {
			timer := time.NewTimer(c.retryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}

		env, retryable, err := c.roundTrip(ctx, node, req)
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

// roundTrip performs a single HTTP exchange.
func (c *Client) roundTrip(ctx context.Context, node string, req Request) (env *Envelope, retryable bool, err error) {
	endpoint := urlutil.BuildAPIURL(node, req.Path, req.PathParams, req.Query)
	if endpoint == "" {
		return nil, false, fmt.Errorf("could not build a request URL for %s", req.Path)
	}

	var body io.Reader
	if req.Body != nil {
		encoded, marshalErr := json.Marshal(req.Body)
		if marshalErr != nil {
			return nil, false, fmt.Errorf("encode request body: %w", marshalErr)
		}
		body = bytes.NewReader(encoded)
	}

	method := req.Method
	if method == "" {
		method = http.MethodGet
	}

	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, false, fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return nil, true, &ConnectionError{Node: node, cause: err}
	}
	defer func() {
		_ = response.Body.Close()
	}()

	payload, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes))
	if err != nil {
		return nil, true, &ConnectionError{Node: node, cause: err}
	}

	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, false, ErrUnauthorized
	}

	if req.SkipEnvelope {
		if response.StatusCode >= http.StatusInternalServerError {
			return nil, true, &ServerError{Status: response.StatusCode}
		}
		if response.StatusCode >= http.StatusBadRequest {
			return nil, false, statusError(response.StatusCode)
		}
		return &Envelope{OK: true, Data: json.RawMessage(payload)}, false, nil
	}

	decoded := &Envelope{}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, decoded); err != nil {
			if response.StatusCode >= http.StatusInternalServerError {
				return nil, true, &ServerError{Status: response.StatusCode}
			}
			return nil, false, fmt.Errorf("the panel returned a malformed response (HTTP %d)", response.StatusCode)
		}
	}

	if !decoded.OK || response.StatusCode >= http.StatusBadRequest {
		message := decoded.Message
		if message == "" {
			message = statusError(response.StatusCode).Error()
		}
		if decoded.Error != "" {
			message = decoded.Error + ": " + message
		}
		if response.StatusCode >= http.StatusInternalServerError {
			return nil, true, &ServerError{Status: response.StatusCode, Message: message}
		}
		return nil, false, errors.New(message)
	}

	return decoded, false, nil
}

// ConnectionError marks a failure to reach a node, which is the only class
// of failure that triggers cluster failover.
type ConnectionError struct {
	Node  string
	cause error
}

// Error implements the error interface without leaking the full URL of the
// node, which may embed a private hostname.
func (e *ConnectionError) Error() string {
	return "could not reach " + security.RedactURL(e.Node)
}

// Unwrap exposes the underlying transport failure.
func (e *ConnectionError) Unwrap() error {
	return e.cause
}

// ServerError marks a 5xx answer from a node. Like an unreachable node it
// is a reason to try the next cluster member; a 4xx never is, because the
// next node would reject the same request for the same reason.
type ServerError struct {
	Status  int
	Message string
}

// Error prefers the panel's own message when it supplied one.
func (e *ServerError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return statusError(e.Status).Error()
}

// isConnectionError reports whether err means "this node is unreachable".
func isConnectionError(err error) bool {
	var connErr *ConnectionError
	return errors.As(err, &connErr)
}

// isFailover reports whether err justifies trying the next cluster node.
func isFailover(err error) bool {
	var serverErr *ServerError
	return isConnectionError(err) || errors.As(err, &serverErr)
}

// statusError renders an HTTP status as an error.
func statusError(status int) error {
	return errors.New("the panel returned HTTP " + strconv.Itoa(status))
}

// cleanURLs keeps only well-formed absolute node URLs. The cluster list
// arrives from the panel, so it is validated before it is ever used.
func cleanURLs(values []string) []string {
	nodes := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimRight(strings.TrimSpace(value), "/")
		if trimmed == "" || ValidateServerURL(trimmed) != nil {
			continue
		}
		nodes = append(nodes, trimmed)
	}
	return nodes
}
