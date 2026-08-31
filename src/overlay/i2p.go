package overlay

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// I2P provider names reported by Service.I2PProvider.
const (
	// providerNone means no provider is running (I2P off or unavailable).
	providerNone = "none"
	// providerI2PD is Model A: a dedicated i2pd process this binary owns.
	providerI2PD = "i2pd"
	// providerSAM is Model B: an external router reached over SAMv3.
	providerSAM = "sam"
)

// I2P start-up outcomes that are facts rather than failures.
var (
	// errI2PDisabled marks the default state: I2P is opt-in and was not
	// opted into, so nothing is contacted, allocated or written.
	errI2PDisabled = errors.New("i2p disabled (opt-in)")
	// errI2PNoProvider marks "enabled, but neither an i2pd binary nor a
	// reachable SAM bridge exists" — a warning, never a startup failure.
	errI2PNoProvider = errors.New("no i2p provider available")
)

// I2P timings and the SAM session identifier.
const (
	// samDialTimeout bounds the initial SAM control connection.
	samDialTimeout = 5 * time.Second
	// samProbeTimeout bounds the reachability probe of the SAM bridge.
	samProbeTimeout = 3 * time.Second
	// samSessionID is the SAM session/tunnel name used for the eepsite.
	samSessionID = "site"
	// i2pdAddressPoll is the interval at which the persisted destination
	// key is polled while i2pd starts up.
	i2pdAddressPoll = 500 * time.Millisecond
	// i2pdStopGrace is how long a stopping i2pd may take before it is
	// killed outright.
	i2pdStopGrace = 10 * time.Second
)

// I2PConfig holds the I2P tuning knobs from server.yml (AI.md PART 32.2
// "Configuration"). Unlike Tor, Enabled defaults to false: the eepsite is
// created only on an explicit opt-in.
type I2PConfig struct {
	// Enabled is the opt-in switch. Nothing happens while it is false.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Binary is an explicit i2pd path; empty means auto-detect. A resolved
	// binary selects Model A.
	Binary string `yaml:"binary" json:"binary"`
	// SAMAddress is the SAMv3 bridge used by Model B when no i2pd binary is
	// available.
	SAMAddress string `yaml:"sam_address" json:"sam_address"`

	// VirtualPort is the port visitors connect to on the eepsite (1-65535).
	VirtualPort int `yaml:"virtual_port" json:"virtual_port"`

	// InboundLength and OutboundLength are tunnel hop counts (0-7).
	InboundLength  int `yaml:"inbound_length" json:"inbound_length"`
	OutboundLength int `yaml:"outbound_length" json:"outbound_length"`
	// InboundQuantity and OutboundQuantity are parallel tunnel counts
	// (1-16).
	InboundQuantity  int `yaml:"inbound_quantity" json:"inbound_quantity"`
	OutboundQuantity int `yaml:"outbound_quantity" json:"outbound_quantity"`

	// SignatureType is the destination signature type (7 =
	// EdDSA-SHA512-Ed25519).
	SignatureType int `yaml:"signature_type" json:"signature_type"`
	// BootstrapTimeout bounds the wait for the destination and tunnels in
	// seconds (30-600).
	BootstrapTimeout int `yaml:"bootstrap_timeout" json:"bootstrap_timeout"`
}

// DefaultI2PConfig returns the built-in, disabled-by-default I2P settings
// from AI.md PART 32.2.
func DefaultI2PConfig() I2PConfig {
	return I2PConfig{
		Enabled:          false,
		Binary:           "",
		SAMAddress:       "127.0.0.1:7656",
		VirtualPort:      80,
		InboundLength:    3,
		OutboundLength:   3,
		InboundQuantity:  5,
		OutboundQuantity: 5,
		SignatureType:    7,
		BootstrapTimeout: 300,
	}
}

// normalize clamps out-of-range values back to their defaults; invalid config
// warns and defaults instead of failing startup.
func (c *I2PConfig) normalize() {
	defaults := DefaultI2PConfig()
	if c.SAMAddress == "" {
		c.SAMAddress = defaults.SAMAddress
	}
	if c.VirtualPort < 1 || c.VirtualPort > 65535 {
		c.VirtualPort = defaults.VirtualPort
	}
	if c.InboundLength < 0 || c.InboundLength > 7 {
		c.InboundLength = defaults.InboundLength
	}
	if c.OutboundLength < 0 || c.OutboundLength > 7 {
		c.OutboundLength = defaults.OutboundLength
	}
	if c.InboundQuantity < 1 || c.InboundQuantity > 16 {
		c.InboundQuantity = defaults.InboundQuantity
	}
	if c.OutboundQuantity < 1 || c.OutboundQuantity > 16 {
		c.OutboundQuantity = defaults.OutboundQuantity
	}
	if c.SignatureType < 0 {
		c.SignatureType = defaults.SignatureType
	}
	if c.BootstrapTimeout < 30 || c.BootstrapTimeout > 600 {
		c.BootstrapTimeout = defaults.BootstrapTimeout
	}
}

// i2pPaths collects every filesystem location the I2P integration uses, all
// derived from the app's own directories (AI.md PART 32.2 "Storage
// Locations").
type i2pPaths struct {
	tunnelsConf string
	dataDir     string
	siteDir     string
	keys        string
	pidFile     string
	logFile     string
}

// i2pPathsFor derives the I2P paths from the service options.
func i2pPathsFor(opts Options) i2pPaths {
	dataDir := filepath.Join(opts.DataDir, "i2p")
	siteDir := filepath.Join(dataDir, "site")
	return i2pPaths{
		tunnelsConf: filepath.Join(opts.ConfigDir, "i2p", "tunnels.conf"),
		dataDir:     dataDir,
		siteDir:     siteDir,
		keys:        filepath.Join(siteDir, "site-keys.dat"),
		pidFile:     filepath.Join(dataDir, "i2pd.pid"),
		logFile:     filepath.Join(opts.LogDir, "i2pd.log"),
	}
}

// i2pRuntime is the live state of whichever provider created the eepsite.
type i2pRuntime struct {
	provider    string
	eepsite     string
	backendPort int
	listener    net.Listener

	// Model A: the managed i2pd process and a channel closed when it exits.
	cmd    *exec.Cmd
	exited chan struct{}

	// Model B: the SAM control connection and a channel closed when the
	// session is lost.
	sam *samSession
}

// resolveI2PDBinary locates the i2pd executable: an explicit cfg.Binary
// override wins, then the common install locations, then $PATH. A failure
// means the caller falls back to the SAM bridge (Model B).
func resolveI2PDBinary(cfg I2PConfig) (string, error) {
	if cfg.Binary != "" {
		if _, err := os.Stat(cfg.Binary); err == nil {
			return cfg.Binary, nil
		}
		return "", fmt.Errorf("configured i2pd binary missing at %s", cfg.Binary)
	}
	for _, candidate := range []string{
		"/usr/bin/i2pd",
		"/usr/sbin/i2pd",
		"/usr/local/bin/i2pd",
		"/opt/homebrew/bin/i2pd",
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	if path, err := exec.LookPath("i2pd"); err == nil {
		return path, nil
	}
	return "", errors.New("i2pd binary not found")
}

// samReachable reports whether a SAMv3 bridge accepts connections at addr.
func samReachable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, samProbeTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// ensureI2PDirs creates every I2P directory with 0700 and enforces those
// permissions even when the directory already existed.
func ensureI2PDirs(paths i2pPaths, logDir string) error {
	dirs := []string{
		filepath.Dir(paths.tunnelsConf),
		paths.dataDir,
		paths.siteDir,
		logDir,
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return fmt.Errorf("create i2p dir %s: %w", dir, err)
		}
		if err := enforceDirPerms(dir); err != nil {
			return err
		}
	}
	return nil
}

// startI2P creates the eepsite when I2P was opted into AND a provider is
// available. The provider is resolved FIRST: with no provider there is no
// port allocation, no tunnels.conf and no SAM session. Only afterwards does
// it allocate the dedicated plain loopback listener — I2P prepends no
// PROXY-protocol header, so this backend, unlike Tor's, is unwrapped.
func startI2P(ctx context.Context, cfg I2PConfig, opts Options) (*i2pRuntime, error) {
	if !cfg.Enabled {
		return nil, errI2PDisabled
	}

	provider := providerNone
	binary := ""
	if resolved, err := resolveI2PDBinary(cfg); err == nil {
		provider, binary = providerI2PD, resolved
	} else if samReachable(cfg.SAMAddress) {
		provider = providerSAM
	} else {
		return nil, fmt.Errorf("%w: no i2pd binary and SAM %s unreachable", errI2PNoProvider, cfg.SAMAddress)
	}

	paths := i2pPathsFor(opts)
	if err := ensureI2PDirs(paths, opts.LogDir); err != nil {
		return nil, err
	}

	listener, backendPort, err := listenLoopback()
	if err != nil {
		return nil, fmt.Errorf("allocate i2p backend port: %w", err)
	}

	rt := &i2pRuntime{
		provider:    provider,
		backendPort: backendPort,
		listener:    listener,
	}

	switch provider {
	case providerI2PD:
		err = startI2PD(ctx, cfg, binary, paths, backendPort, rt)
	case providerSAM:
		err = startSAMEepsite(ctx, cfg, paths, backendPort, rt)
	}
	if err != nil {
		listener.Close()
		return nil, err
	}

	log.Printf("I2P eepsite started (%s): %s:%d -> 127.0.0.1:%d", provider, rt.eepsite, cfg.VirtualPort, backendPort)
	return rt, nil
}

// tunnelsConfig renders the i2pd server-tunnel definition (Model A). It is
// derived state, regenerated on every start; the destination identity lives
// in the keys file, so overwriting this file is always safe.
func tunnelsConfig(cfg I2PConfig, keysPath string, backendPort int) string {
	return fmt.Sprintf(`# Generated by the cashp server binary - derived state, rewritten on
# every start. The eepsite identity lives in the keys file below.
[%s]
type = server
host = 127.0.0.1
port = %d
keys = %s
inbound.length = %d
outbound.length = %d
inbound.quantity = %d
outbound.quantity = %d
signaturetype = %d
`, samSessionID, backendPort, keysPath,
		cfg.InboundLength, cfg.OutboundLength,
		cfg.InboundQuantity, cfg.OutboundQuantity, cfg.SignatureType)
}

// startI2PD writes tunnels.conf and starts the dedicated i2pd child process
// this binary owns, then waits for i2pd to persist the destination and
// derives the .b32.i2p address from it.
func startI2PD(ctx context.Context, cfg I2PConfig, binary string, paths i2pPaths, backendPort int, rt *i2pRuntime) error {
	if err := writeSecretFile(paths.tunnelsConf, []byte(tunnelsConfig(cfg, paths.keys, backendPort))); err != nil {
		return fmt.Errorf("write tunnels.conf: %w", err)
	}

	cmd := exec.CommandContext(ctx, binary,
		"--datadir", paths.dataDir,
		"--tunconf", paths.tunnelsConf,
		"--pidfile", paths.pidFile,
		"--log", "file",
		"--logfile", paths.logFile,
		"--loglevel", "warn",
		"--daemon", "false",
	)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start i2pd: %w", err)
	}

	rt.cmd = cmd
	rt.exited = make(chan struct{})
	// Reaping the child in the background is what makes the liveness check
	// in healthCheck meaningful.
	go func(exited chan struct{}) {
		_ = cmd.Wait()
		close(exited)
	}(rt.exited)

	address, err := waitForI2PDAddress(ctx, paths.keys, time.Duration(cfg.BootstrapTimeout)*time.Second, rt.exited)
	if err != nil {
		stopProcess(cmd, rt.exited)
		return err
	}
	rt.eepsite = address
	return nil
}

// waitForI2PDAddress polls the destination key file i2pd persists and derives
// the .b32.i2p address as soon as it is readable.
func waitForI2PDAddress(ctx context.Context, keysPath string, timeout time.Duration, exited <-chan struct{}) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		if data, err := os.ReadFile(keysPath); err == nil {
			if dest, err := destinationFromKeyFile(data); err == nil {
				return B32Address(dest), nil
			}
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("read i2p keys %s: %w", keysPath, err)
		}

		if time.Now().After(deadline) {
			return "", fmt.Errorf("i2pd did not publish a destination within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-exited:
			return "", errors.New("i2pd exited before publishing a destination")
		case <-time.After(i2pdAddressPoll):
		}
	}
}

// destinationFromKeyFile extracts the public Destination from an I2P private
// key file. The Destination is the leading record: a 256-byte public key, a
// 128-byte signing key, then a one-byte certificate type and a two-byte
// certificate length whose payload completes it.
func destinationFromKeyFile(data []byte) ([]byte, error) {
	const baseLen = 387
	if len(data) < baseLen {
		return nil, fmt.Errorf("i2p key file too short: %d bytes", len(data))
	}

	certType := data[384]
	certLen := int(binary.BigEndian.Uint16(data[385:387]))
	destLen := baseLen
	if certType != 0 {
		destLen += certLen
	}
	if len(data) < destLen {
		return nil, fmt.Errorf("i2p key file truncated: need %d bytes, have %d", destLen, len(data))
	}
	return data[:destLen], nil
}

// B32Address derives the .b32.i2p address of a binary Destination:
// lowercase, unpadded base32 of its SHA-256 digest.
func B32Address(destination []byte) string {
	sum := sha256.Sum256(destination)
	encoding := base32.StdEncoding.WithPadding(base32.NoPadding)
	return toLowerASCII(encoding.EncodeToString(sum[:])) + ".b32.i2p"
}

// toLowerASCII lowercases an ASCII base32 digest without pulling in locale
// aware casing.
func toLowerASCII(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + ('a' - 'A')
		}
	}
	return string(out)
}

// stopProcess ends a managed child process, escalating to a kill if it does
// not exit within the grace period.
func stopProcess(cmd *exec.Cmd, exited <-chan struct{}) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		_ = cmd.Process.Kill()
		return
	}
	select {
	case <-exited:
	case <-time.After(i2pdStopGrace):
		_ = cmd.Process.Kill()
	}
}

// healthCheck reports whether the active provider is still alive: the i2pd
// child for Model A, the SAM control session for Model B.
func (r *i2pRuntime) healthCheck(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	switch r.provider {
	case providerI2PD:
		if r.exited == nil {
			return ErrNotStarted
		}
		select {
		case <-r.exited:
			return errors.New("i2pd process exited")
		default:
			return nil
		}
	case providerSAM:
		if r.sam == nil {
			return ErrNotStarted
		}
		return r.sam.health()
	default:
		return ErrNotStarted
	}
}

// close shuts down the provider and the dedicated backend listener.
func (r *i2pRuntime) close() error {
	var errs []error
	if r.sam != nil {
		if err := r.sam.close(); err != nil {
			errs = append(errs, err)
		}
		r.sam = nil
	}
	if r.cmd != nil {
		stopProcess(r.cmd, r.exited)
		r.cmd = nil
	}
	if r.listener != nil {
		if err := r.listener.Close(); err != nil {
			errs = append(errs, err)
		}
		r.listener = nil
	}
	return errors.Join(errs...)
}
