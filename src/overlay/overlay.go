// Package overlay implements cashp's overlay-network integrations per AI.md
// PART 32: a Tor hidden service (PART 32.1, auto-enabled whenever the tor
// binary is found, no toggle) and an I2P eepsite (PART 32.2, opt-in only).
// Both serve the app over plain http:// at an address that is itself the
// cryptographic identity — never TLS, never HSTS, never an upgrade redirect.
package overlay

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"sync"

	"github.com/webappsgo/cashp/src/notify"
)

// Options configures the overlay service. DataDir, Port, I2PEnabled and
// I2PSAMAddr are the fields the HTTP server layer sets; the remaining fields
// are optional and fall back to spec defaults when left zero.
type Options struct {
	// DataDir is {data_dir}: Tor lives under DataDir/tor, I2P under
	// DataDir/i2p (AI.md PART 32 "Storage Locations").
	DataDir string
	// Port is the app's clearnet HTTP port. It is recorded for status
	// reporting only — the hidden service and the eepsite NEVER forward to
	// the clearnet port, they each get a dedicated loopback backend port.
	Port int
	// I2PEnabled is the opt-in switch for the eepsite
	// (features.i2p.enabled). Tor has no equivalent switch by design.
	I2PEnabled bool
	// I2PSAMAddr is the SAMv3 bridge address used by Model B when no i2pd
	// binary is available. Empty means the default 127.0.0.1:7656.
	I2PSAMAddr string
	// ConfigDir is {config_dir}, holding tor/torrc and i2p/tunnels.conf.
	// Empty falls back to DataDir.
	ConfigDir string
	// LogDir is {log_dir}, holding i2pd.log for Model A. Empty falls back to
	// DataDir/log.
	LogDir string
	// AllowedDestinations is the App-Scoped-Only outbound allowlist: the
	// exact "host" or "host:port" values this app may reach through the
	// outbound Tor client. Empty means no outbound destination is permitted.
	AllowedDestinations []string
	// Tor overrides the Tor tuning defaults. Nil uses DefaultTorConfig().
	Tor *TorConfig
	// I2P overrides the I2P tuning defaults. Nil uses DefaultI2PConfig().
	I2P *I2PConfig
	// Notifier delivers tor_ready notifications per AI.md PART 18's decision
	// matrix; nil disables notification entirely.
	Notifier *notify.Notifier
}

// ErrNotStarted is returned by operations that need a running provider.
var ErrNotStarted = errors.New("overlay: service not started")

// Service owns the Tor process and the I2P provider for this app instance.
// Every provider is optional: a missing tor binary or a disabled/unavailable
// I2P provider leaves the service running as a no-op, never an error.
type Service struct {
	opts   Options
	torCfg TorConfig
	i2pCfg I2PConfig
	allow  map[string]struct{}

	mu      sync.Mutex
	started bool
	tor     *torRuntime
	i2p     *i2pRuntime
}

// New builds a Service from opts, filling in every default the caller left
// unset. It performs no I/O and starts no process — call Start for that.
func New(opts Options) *Service {
	if opts.ConfigDir == "" {
		opts.ConfigDir = opts.DataDir
	}
	if opts.LogDir == "" {
		opts.LogDir = filepath.Join(opts.DataDir, "log")
	}

	torCfg := DefaultTorConfig()
	if opts.Tor != nil {
		torCfg = *opts.Tor
	}
	torCfg.normalize()

	i2pCfg := DefaultI2PConfig()
	if opts.I2P != nil {
		i2pCfg = *opts.I2P
	}
	// Options.I2PEnabled and Options.I2PSAMAddr are the authoritative
	// server-level switches and win over the tuning struct.
	i2pCfg.Enabled = opts.I2PEnabled
	if opts.I2PSAMAddr != "" {
		i2pCfg.SAMAddress = opts.I2PSAMAddr
	}
	i2pCfg.normalize()

	return &Service{
		opts:   opts,
		torCfg: torCfg,
		i2pCfg: i2pCfg,
		allow:  buildAllowlist(opts.AllowedDestinations),
	}
}

// Start brings up every available provider. Tor auto-starts whenever the tor
// binary is found; I2P starts only when it was opted in AND a provider is
// available. A missing binary, an unreachable SAM bridge or a failed
// bootstrap is logged and skipped — Start never fails for those reasons, so
// the server never fails to start because of an overlay network.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.started = true

	if rt, err := startTor(ctx, s.torCfg, s.opts); err != nil {
		if errors.Is(err, errTorUnavailable) {
			log.Printf("Tor hidden service: disabled (%v)", err)
		} else {
			log.Printf("WARN: Tor hidden service unavailable: %v", err)
		}
	} else {
		s.tor = rt
		log.Printf("Tor: %s", rt.onion)
		s.notify(ctx, notify.EventTorReady, map[string]string{"onion_address": rt.onion})
	}

	if rt, err := startI2P(ctx, s.i2pCfg, s.opts); err != nil {
		if errors.Is(err, errI2PDisabled) {
			log.Printf("I2P eepsite: disabled (opt-in)")
		} else {
			log.Printf("WARN: I2P eepsite unavailable: %v", err)
		}
	} else {
		s.i2p = rt
		log.Printf("I2P: %s (%s)", rt.eepsite, rt.provider)
	}

	return nil
}

// notify dispatches one overlay event, tolerating both an absent notifier
// and a delivery failure - a notification is never allowed to fail overlay
// startup.
func (s *Service) notify(ctx context.Context, event string, vars map[string]string) {
	if s.opts.Notifier == nil {
		return
	}
	if err := s.opts.Notifier.Notify(ctx, notify.Message{Event: event, Vars: vars}); err != nil {
		log.Printf("WARN: overlay notification %s failed: %v", event, err)
	}
}

// Stop shuts down every provider this service owns and closes the dedicated
// backend listeners. It is safe to call when nothing was ever started.
func (s *Service) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var errs []error
	if s.tor != nil {
		if err := s.tor.close(); err != nil {
			errs = append(errs, fmt.Errorf("tor: %w", err))
		}
		s.tor = nil
	}
	if s.i2p != nil {
		if err := s.i2p.close(); err != nil {
			errs = append(errs, fmt.Errorf("i2p: %w", err))
		}
		s.i2p = nil
	}
	s.started = false

	return errors.Join(errs...)
}

// Restart stops every provider and starts them again with the current
// configuration, regenerating torrc and tunnels.conf in the process.
func (s *Service) Restart(ctx context.Context) error {
	if err := s.Stop(); err != nil {
		log.Printf("WARN: overlay restart: stop reported %v", err)
	}
	return s.Start(ctx)
}

// OnionAddress returns the full v3 .onion address and whether the hidden
// service is running.
func (s *Service) OnionAddress() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.tor == nil || s.tor.onion == "" {
		return "", false
	}
	return s.tor.onion, true
}

// EepsiteAddress returns the full .b32.i2p address and whether the eepsite is
// running.
func (s *Service) EepsiteAddress() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.i2p == nil || s.i2p.eepsite == "" {
		return "", false
	}
	return s.i2p.eepsite, true
}

// Listeners returns the dedicated loopback listeners the HTTP server must
// Serve() on: the Tor backend (PROXY-protocol aware, because Tor prepends a
// HAProxy v1 header carrying the circuit ID) and the I2P backend (plain, I2P
// prepends nothing). The slice is empty when no provider is running.
func (s *Service) Listeners() []net.Listener {
	s.mu.Lock()
	defer s.mu.Unlock()

	listeners := make([]net.Listener, 0, 2)
	if s.tor != nil && s.tor.listener != nil {
		listeners = append(listeners, s.tor.listener)
	}
	if s.i2p != nil && s.i2p.listener != nil {
		listeners = append(listeners, s.i2p.listener)
	}
	return listeners
}

// HealthCheck reports whether every running provider is still responsive. It
// is the probe the scheduler's tor/i2p health task calls; a service with no
// running provider is healthy by definition (both are optional).
func (s *Service) HealthCheck(ctx context.Context) error {
	s.mu.Lock()
	torRT, i2pRT := s.tor, s.i2p
	s.mu.Unlock()

	var errs []error
	if torRT != nil {
		if err := torRT.healthCheck(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tor: %w", err))
		}
	}
	if i2pRT != nil {
		if err := i2pRT.healthCheck(ctx); err != nil {
			errs = append(errs, fmt.Errorf("i2p: %w", err))
		}
	}
	return errors.Join(errs...)
}

// TorBackendPort returns the dedicated loopback port the hidden service
// forwards to, and whether Tor is running.
func (s *Service) TorBackendPort() (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.tor == nil {
		return 0, false
	}
	return s.tor.backendPort, true
}

// I2PBackendPort returns the dedicated loopback port the eepsite forwards to,
// and whether I2P is running.
func (s *Service) I2PBackendPort() (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.i2p == nil {
		return 0, false
	}
	return s.i2p.backendPort, true
}

// I2PProvider returns the name of the active I2P provider: "i2pd" for the
// managed Model A process, "sam" for an external Model B bridge, or "none".
func (s *Service) I2PProvider() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.i2p == nil {
		return providerNone
	}
	return s.i2p.provider
}
