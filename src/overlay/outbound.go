package overlay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// App-Scoped-Only errors (AI.md PART 32.1 "App-Scoped Only — No Relay, Exit,
// or Proxy Abuse"). Both are refusals, never transport failures.
var (
	// ErrDestinationNotAllowed means the destination is not on the
	// app-owned allowlist, so it was not app/config-determined.
	ErrDestinationNotAllowed = errors.New("overlay: outbound destination not on the app allowlist")
	// ErrOverlayScopedOutbound means an inbound overlay request tried to
	// drive an outbound connection — the visitor-controls-destination path
	// this package exists to close.
	ErrOverlayScopedOutbound = errors.New("overlay: an inbound overlay request may not choose an outbound destination")
)

// Outbound dial timeouts: Tor circuits are slow, clearnet is not.
const (
	directDialTimeout = 30 * time.Second
	torDialTimeout    = 60 * time.Second
)

// inboundKey types the context value that marks a request as having arrived
// over an overlay network.
type inboundKey struct{}

// WithInboundOverlay marks ctx as belonging to an inbound request that
// arrived over the given overlay kind. Every outbound dial made with a
// context carrying this mark is refused, which is what enforces App-Scoped
// Only in code rather than by convention: the HTTP layer attaches the mark
// once, on the way in, and no handler can fetch a visitor-supplied
// destination afterwards.
func WithInboundOverlay(ctx context.Context, kind string) context.Context {
	return context.WithValue(ctx, inboundKey{}, kind)
}

// InboundOverlay reports the overlay kind ctx was marked with, if any.
func InboundOverlay(ctx context.Context) (string, bool) {
	kind, ok := ctx.Value(inboundKey{}).(string)
	return kind, ok
}

// buildAllowlist normalizes the configured destinations into a lookup set.
// Entries are matched exactly, as "host" or as "host:port"; wildcards are
// deliberately unsupported so a destination can never be widened by accident.
func buildAllowlist(destinations []string) map[string]struct{} {
	allow := make(map[string]struct{}, len(destinations))
	for _, dest := range destinations {
		entry := strings.TrimSpace(dest)
		if entry == "" {
			continue
		}
		if host, port, err := net.SplitHostPort(entry); err == nil {
			allow[NormalizeHost(host)+":"+port] = struct{}{}
			continue
		}
		allow[NormalizeHost(entry)] = struct{}{}
	}
	return allow
}

// destinationAllowed reports whether addr ("host:port") resolves to an entry
// on the app-owned allowlist. An empty allowlist permits nothing: the app has
// declared no outbound destinations, so every dial is refused (fail closed).
func (s *Service) destinationAllowed(addr string) bool {
	if len(s.allow) == 0 {
		return false
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = addr, ""
	}
	host = NormalizeHost(host)
	if host == "" {
		return false
	}

	if _, ok := s.allow[host]; ok {
		return true
	}
	if port != "" {
		_, ok := s.allow[host+":"+port]
		return ok
	}
	return false
}

// DialContext dials addr for app-determined outbound traffic, through the Tor
// SOCKS dialer when one is available and directly otherwise. It enforces
// App-Scoped Only before any packet leaves: a context marked as an inbound
// overlay request is refused outright, and the destination must be on the
// configured allowlist.
func (s *Service) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if kind, ok := InboundOverlay(ctx); ok {
		return nil, fmt.Errorf("%w (inbound kind %q, destination %q)", ErrOverlayScopedOutbound, kind, addr)
	}
	if !s.destinationAllowed(addr) {
		return nil, fmt.Errorf("%w: %s", ErrDestinationNotAllowed, addr)
	}
	return s.dial(ctx, network, addr, true)
}

// DialDirectContext dials addr without the Tor network while still enforcing
// App-Scoped Only. It backs the "user opted out of Tor" branch of the
// outbound preference hierarchy.
func (s *Service) DialDirectContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if kind, ok := InboundOverlay(ctx); ok {
		return nil, fmt.Errorf("%w (inbound kind %q, destination %q)", ErrOverlayScopedOutbound, kind, addr)
	}
	if !s.destinationAllowed(addr) {
		return nil, fmt.Errorf("%w: %s", ErrDestinationNotAllowed, addr)
	}
	return s.dial(ctx, network, addr, false)
}

// dial performs the transport-level dial once the App-Scoped-Only checks have
// passed. useTor falls back to a direct dial when no Tor dialer exists.
func (s *Service) dial(ctx context.Context, network, addr string, useTor bool) (net.Conn, error) {
	s.mu.Lock()
	torRT := s.tor
	s.mu.Unlock()

	if useTor && torRT != nil && torRT.dialer != nil {
		return torRT.dialer.DialContext(ctx, network, addr)
	}
	dialer := &net.Dialer{Timeout: directDialTimeout}
	return dialer.DialContext(ctx, network, addr)
}

// OutboundEnabled reports whether outbound requests can currently be routed
// through the Tor network.
func (s *Service) OutboundEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.tor != nil && s.tor.dialer != nil
}

// HTTPClient returns an HTTP client whose transport always runs through the
// App-Scoped-Only checks. useTor selects the Tor network when it is
// available; false dials directly. Every outbound HTTP call in the app must
// use this client so no code path can bypass the allowlist.
func (s *Service) HTTPClient(useTor bool) *http.Client {
	timeout := directDialTimeout
	dial := s.DialDirectContext
	if useTor {
		timeout = torDialTimeout
		dial = s.DialContext
	}

	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           dial,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          16,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// ShouldUseTor resolves the outbound Tor preference hierarchy from AI.md PART
// 32.1: the server setting decides unless user preferences are allowed, in
// which case a non-nil user preference wins and nil inherits the server.
func ShouldUseTor(serverUseNetwork, allowUserPreference bool, userPref *bool) bool {
	if !allowUserPreference || userPref == nil {
		return serverUseNetwork
	}
	return *userPref
}
