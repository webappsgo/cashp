package overlay

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cretz/bine/tor"
	"github.com/pires/go-proxyproto"
)

// errTorUnavailable marks the "no tor binary on this host" outcome. Tor is
// optional: this is an INFO-level fact, never an error the server fails on.
var errTorUnavailable = errors.New("tor binary not found")

// Tor bootstrap and hidden-service timings.
const (
	// torQuietBootstrap is how long bootstrap stays silent before the
	// "connecting..." notice is printed (AI.md PART 32.1 bootstrap rules).
	torQuietBootstrap = 30 * time.Second
	// torHostnameWait bounds the wait for Tor to write the .onion hostname
	// under HiddenServiceDir after the network came up.
	torHostnameWait = 30 * time.Second
	// torHostnamePoll is the hostname file polling interval.
	torHostnamePoll = 250 * time.Millisecond
	// torProxyHeaderTimeout bounds reading Tor's HAProxy PROXY v1 header on
	// each backend connection.
	torProxyHeaderTimeout = 10 * time.Second
)

// TorConfig holds the Tor tuning knobs from server.yml (AI.md PART 32.1
// "Configuration"). There is deliberately no enable flag: the hidden service
// is always on when a tor binary exists.
type TorConfig struct {
	// Binary is an explicit tor path; empty means auto-detect.
	Binary string `yaml:"binary" json:"binary"`

	// UseNetwork routes the app's own outbound requests through Tor.
	UseNetwork bool `yaml:"use_network" json:"use_network"`
	// AllowUserPreference lets a user override UseNetwork for requests made
	// on their behalf.
	AllowUserPreference bool `yaml:"allow_user_preference" json:"allow_user_preference"`

	// MaxCircuits is the circuit pool ceiling (1-128).
	MaxCircuits int `yaml:"max_circuits" json:"max_circuits"`
	// CircuitTimeout is the per-circuit build timeout in seconds (10-300).
	CircuitTimeout int `yaml:"circuit_timeout" json:"circuit_timeout"`
	// BootstrapTimeout bounds the wait for the Tor network in seconds
	// (30-600).
	BootstrapTimeout int `yaml:"bootstrap_timeout" json:"bootstrap_timeout"`

	// SafeLogging scrubs sensitive information from Tor's own logs.
	SafeLogging bool `yaml:"safe_logging" json:"safe_logging"`
	// MaxStreamsPerCircuit caps concurrent streams per circuit (10-500).
	MaxStreamsPerCircuit int `yaml:"max_streams_per_circuit" json:"max_streams_per_circuit"`
	// CloseCircuitOnStreamLimit closes a circuit that exceeds the stream cap.
	CloseCircuitOnStreamLimit bool `yaml:"close_circuit_on_stream_limit" json:"close_circuit_on_stream_limit"`

	// BandwidthRate is the sustained rate per second, e.g. "1 MB".
	BandwidthRate string `yaml:"bandwidth_rate" json:"bandwidth_rate"`
	// BandwidthBurst is the burst rate per second, e.g. "2 MB".
	BandwidthBurst string `yaml:"bandwidth_burst" json:"bandwidth_burst"`
	// MaxMonthlyBandwidth is the accounting ceiling, or "unlimited".
	MaxMonthlyBandwidth string `yaml:"max_monthly_bandwidth" json:"max_monthly_bandwidth"`

	// NumIntroPoints is the hidden-service introduction point count (3-10).
	NumIntroPoints int `yaml:"num_intro_points" json:"num_intro_points"`
	// VirtualPort is the port visitors connect to on the .onion (1-65535).
	VirtualPort int `yaml:"virtual_port" json:"virtual_port"`
}

// DefaultTorConfig returns the built-in Tor defaults from AI.md PART 32.1.
func DefaultTorConfig() TorConfig {
	return TorConfig{
		Binary:                    "",
		UseNetwork:                false,
		AllowUserPreference:       true,
		MaxCircuits:               32,
		CircuitTimeout:            60,
		BootstrapTimeout:          180,
		SafeLogging:               true,
		MaxStreamsPerCircuit:      100,
		CloseCircuitOnStreamLimit: true,
		BandwidthRate:             "1 MB",
		BandwidthBurst:            "2 MB",
		MaxMonthlyBandwidth:       "100 GB",
		NumIntroPoints:            3,
		VirtualPort:               80,
	}
}

// normalize clamps out-of-range values back to their defaults. Invalid config
// never fails startup — it warns and defaults (AI.md PART 12 config rules).
func (c *TorConfig) normalize() {
	defaults := DefaultTorConfig()
	if c.MaxCircuits < 1 || c.MaxCircuits > 128 {
		c.MaxCircuits = defaults.MaxCircuits
	}
	if c.CircuitTimeout < 10 || c.CircuitTimeout > 300 {
		c.CircuitTimeout = defaults.CircuitTimeout
	}
	if c.BootstrapTimeout < 30 || c.BootstrapTimeout > 600 {
		c.BootstrapTimeout = defaults.BootstrapTimeout
	}
	if c.MaxStreamsPerCircuit < 10 || c.MaxStreamsPerCircuit > 500 {
		c.MaxStreamsPerCircuit = defaults.MaxStreamsPerCircuit
	}
	if c.BandwidthRate == "" {
		c.BandwidthRate = defaults.BandwidthRate
	}
	if c.BandwidthBurst == "" {
		c.BandwidthBurst = defaults.BandwidthBurst
	}
	if c.MaxMonthlyBandwidth == "" {
		c.MaxMonthlyBandwidth = defaults.MaxMonthlyBandwidth
	}
	if c.NumIntroPoints < 3 || c.NumIntroPoints > 10 {
		c.NumIntroPoints = defaults.NumIntroPoints
	}
	if c.VirtualPort < 1 || c.VirtualPort > 65535 {
		c.VirtualPort = defaults.VirtualPort
	}
}

// torPaths collects every filesystem location the managed Tor process uses.
// All of them are derived from the app's own directories — never hardcoded,
// never configurable (AI.md PART 32.1 "Storage Locations").
type torPaths struct {
	torrc        string
	dataDir      string
	hiddenSvcDir string
	hostname     string
	pidFile      string
	logFile      string
}

// torPathsFor derives the Tor paths from the service options.
func torPathsFor(opts Options) torPaths {
	dataDir := filepath.Join(opts.DataDir, "tor")
	hsDir := filepath.Join(dataDir, "site")
	return torPaths{
		torrc:        filepath.Join(opts.ConfigDir, "tor", "torrc"),
		dataDir:      dataDir,
		hiddenSvcDir: hsDir,
		hostname:     filepath.Join(hsDir, "hostname"),
		pidFile:      filepath.Join(dataDir, "tor.pid"),
		logFile:      filepath.Join(opts.LogDir, "tor.log"),
	}
}

// torRuntime is the live state of the managed Tor process.
type torRuntime struct {
	tor         *tor.Tor
	dialer      *tor.Dialer
	onion       string
	backendPort int
	listener    net.Listener
}

// resolveTorBinary locates the tor executable: an explicit cfg.Binary
// override wins, then the common install locations, then $PATH. When nothing
// is found Tor stays disabled and no port is allocated and no file written.
func resolveTorBinary(cfg TorConfig) (string, error) {
	if cfg.Binary != "" {
		if _, err := os.Stat(cfg.Binary); err == nil {
			return cfg.Binary, nil
		}
		return "", fmt.Errorf("%w: configured tor binary missing at %s", errTorUnavailable, cfg.Binary)
	}
	for _, candidate := range []string{
		"/usr/bin/tor",
		"/usr/sbin/tor",
		"/usr/local/bin/tor",
		"/opt/homebrew/bin/tor",
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	if path, err := exec.LookPath("tor"); err == nil {
		return path, nil
	}
	return "", errTorUnavailable
}

// torrcConfig renders the torrc from cfg. The hidden service is declared here
// (HiddenServiceDir block), not via ADD_ONION, so Tor itself generates and
// persists the v3 ed25519 key. The relay/exit surface is nailed shut: exit
// disabled, ORPort/DirPort off, descriptor publication off, SOCKS bound to
// loopback with a loopback-only policy — the app's Tor is a client plus a
// hidden service and nothing else.
func torrcConfig(cfg TorConfig, paths torPaths, backendPort int) string {
	// SOCKS exists only when the app itself may make outbound Tor requests.
	// It is always bound to loopback, never reachable from the hidden
	// service listener.
	socksConfig := "SocksPort 0"
	if cfg.UseNetwork || cfg.AllowUserPreference {
		socksConfig = "SocksPort 127.0.0.1:auto"
	}

	safeLogging := "1"
	if !cfg.SafeLogging {
		safeLogging = "0"
	}

	closeOnLimit := "1"
	if !cfg.CloseCircuitOnStreamLimit {
		closeOnLimit = "0"
	}

	// Monthly accounting is only written when a ceiling is configured.
	accounting := ""
	if cfg.MaxMonthlyBandwidth != "" && !strings.EqualFold(cfg.MaxMonthlyBandwidth, "unlimited") {
		accounting = fmt.Sprintf("\n# Monthly bandwidth limit\nAccountingStart month 1 00:00\nAccountingMax %s\n", cfg.MaxMonthlyBandwidth)
	}

	return fmt.Sprintf(`# ============================================================
# Tor configuration - generated by the cashp server binary
# Derived state: regenerated on every start from server.yml plus the
# current backend port. The .onion identity lives in HiddenServiceDir,
# never here, so overwriting this file is always safe.
# ============================================================

# SOCKS for the app's own outbound requests only (0 = disabled).
# Never a well-known Tor port - loopback runtime detection only.
%s
SocksPolicy accept 127.0.0.1

# Control connection - never a well-known Tor port, always a runtime
# loopback port.
ControlPort 127.0.0.1:auto

# Security hardening
SafeLogging %s
DisableDebuggerAttachment 1

# Circuit limits
MaxCircuitDirtiness 600
MaxClientCircuitsPending %d
CircuitBuildTimeout %d

# Bandwidth limits per second
BandwidthRate %s
BandwidthBurst %s
%s
# Not a relay, not an exit, not a directory, never published.
ExitRelay 0
ExitPolicy reject *:*
DirPort 0
PublishServerDescriptor 0

# Guard-discovery-attack defense (vanguards-lite) - keep enabled.
VanguardsLiteEnabled 1

# Hidden service must stay multi-hop.
HiddenServiceSingleHopMode 0

# Faster startup
FetchDirInfoEarly 1
FetchDirInfoExtraEarly 1

# Runtime files
PidFile %s
Log notice file %s

# ============================================================
# Hidden service (v3) - Tor generates and persists the key and the
# hostname under this directory.
# ============================================================
HiddenServiceDir %s
HiddenServiceVersion 3
HiddenServicePort %d 127.0.0.1:%d
HiddenServiceNumIntroductionPoints %d
HiddenServiceMaxStreams %d
HiddenServiceMaxStreamsCloseCircuit %s
# Per-rendezvous-circuit ID via the HAProxy PROXY protocol (an opaque
# token, never a visitor IP).
HiddenServiceExportCircuitID haproxy
`,
		socksConfig,
		safeLogging,
		cfg.MaxCircuits,
		cfg.CircuitTimeout,
		cfg.BandwidthRate,
		cfg.BandwidthBurst,
		accounting,
		paths.pidFile,
		paths.logFile,
		paths.hiddenSvcDir,
		cfg.VirtualPort,
		backendPort,
		cfg.NumIntroPoints,
		cfg.MaxStreamsPerCircuit,
		closeOnLimit,
	)
}

// ensureTorDirs creates every Tor directory with 0700 and enforces those
// permissions even when the directory already existed. All Tor files are
// owned by the user the server runs as.
func ensureTorDirs(paths torPaths, logDir string) error {
	dirs := []string{
		filepath.Dir(paths.torrc),
		paths.dataDir,
		paths.hiddenSvcDir,
		logDir,
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create tor dir %s: %w", dir, err)
		}
		if err := enforceDirPerms(dir); err != nil {
			return err
		}
	}
	return nil
}

// startTor brings up the app's own dedicated Tor process. It resolves the tor
// binary FIRST: with no binary there is no port allocation, no torrc and no
// process — Tor is simply not enabled. Otherwise it allocates a dedicated
// PROXY-protocol loopback listener (separate from the clearnet port), writes
// the torrc, starts Tor, waits for bootstrap and reads the .onion Tor
// persisted under HiddenServiceDir.
func startTor(ctx context.Context, cfg TorConfig, opts Options) (*torRuntime, error) {
	binary, err := resolveTorBinary(cfg)
	if err != nil {
		return nil, err
	}

	paths := torPathsFor(opts)
	if err := ensureTorDirs(paths, opts.LogDir); err != nil {
		return nil, err
	}

	// The backend port is allocated only now that Tor is confirmed
	// available: no provider, no port.
	rawListener, backendPort, err := listenLoopback()
	if err != nil {
		return nil, fmt.Errorf("allocate tor backend port: %w", err)
	}
	// Tor prepends a HAProxy PROXY v1 header (carrying the circuit ID) to
	// every backend connection, so this listener - and only this listener -
	// parses that header. The clearnet listener never sees one.
	listener := &proxyproto.Listener{
		Listener:          rawListener,
		ReadHeaderTimeout: torProxyHeaderTimeout,
	}

	if err := writeSecretFile(paths.torrc, []byte(torrcConfig(cfg, paths, backendPort))); err != nil {
		listener.Close()
		return nil, fmt.Errorf("write torrc: %w", err)
	}

	log.Printf("Starting Tor hidden service...")
	instance, err := tor.Start(ctx, &tor.StartConf{
		ExePath:   binary,
		TorrcFile: paths.torrc,
		DataDir:   paths.dataDir,
		// The torrc owns SocksPort; bine must not force its own.
		NoAutoSocksPort: true,
	})
	if err != nil {
		listener.Close()
		return nil, fmt.Errorf("start dedicated tor: %w", err)
	}

	bootstrapCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.BootstrapTimeout)*time.Second)
	defer cancel()

	// Bootstrap is silent unless it drags on past the quiet window.
	slowNotice := time.AfterFunc(torQuietBootstrap, func() {
		log.Printf("Tor: connecting...")
	})

	if err := instance.EnableNetwork(bootstrapCtx, true); err != nil {
		slowNotice.Stop()
		instance.Close()
		listener.Close()
		return nil, fmt.Errorf("tor bootstrap failed: %w", err)
	}
	slowNotice.Stop()

	onion, err := waitForOnionHostname(bootstrapCtx, paths.hostname)
	if err != nil {
		instance.Close()
		listener.Close()
		return nil, err
	}

	rt := &torRuntime{
		tor:         instance,
		onion:       onion,
		backendPort: backendPort,
		listener:    listener,
	}

	// The outbound dialer exists only when the app may make outbound Tor
	// requests at all. Its destinations are still gated by the
	// App-Scoped-Only allowlist in DialContext.
	if cfg.UseNetwork || cfg.AllowUserPreference {
		dialer, err := instance.Dialer(ctx, nil)
		if err != nil {
			log.Printf("WARN: Tor outbound dialer unavailable: %v", err)
		} else {
			rt.dialer = dialer
		}
	}

	log.Printf("Tor hidden service started: %s:%d -> 127.0.0.1:%d", onion, cfg.VirtualPort, backendPort)
	return rt, nil
}

// waitForOnionHostname polls the HiddenServiceDir hostname file Tor writes
// once it has generated or loaded the v3 key.
func waitForOnionHostname(ctx context.Context, hostnamePath string) (string, error) {
	deadline := time.Now().Add(torHostnameWait)
	for {
		data, err := os.ReadFile(hostnamePath)
		if err == nil {
			if onion := strings.TrimSpace(string(data)); onion != "" {
				return onion, nil
			}
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("read onion hostname: %w", err)
		}

		if time.Now().After(deadline) {
			return "", fmt.Errorf("onion hostname not published at %s", hostnamePath)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(torHostnamePoll):
		}
	}
}

// healthCheck pings the Tor control connection. A failure is what triggers
// the scheduler's restart of the hidden service.
func (r *torRuntime) healthCheck(ctx context.Context) error {
	if r.tor == nil || r.tor.Control == nil {
		return ErrNotStarted
	}

	done := make(chan error, 1)
	go func() {
		_, err := r.tor.Control.GetInfo("version")
		done <- err
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("control connection lost: %w", err)
		}
		return nil
	}
}

// close terminates the Tor process this server owns and closes the dedicated
// backend listener.
func (r *torRuntime) close() error {
	var errs []error
	if r.tor != nil {
		if err := r.tor.Close(); err != nil {
			errs = append(errs, err)
		}
		r.tor = nil
	}
	if r.listener != nil {
		if err := r.listener.Close(); err != nil {
			errs = append(errs, err)
		}
		r.listener = nil
	}
	return errors.Join(errs...)
}
