package overlay

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/notify"
)

// newTestNotifier builds a real, SQLite-backed Notifier so dispatch can be
// asserted through its own dedup store, matching the pattern used across the
// packages that wire notify events.
func newTestNotifier(t *testing.T) *notify.Notifier {
	t.Helper()

	db, err := database.Open(database.Config{Driver: database.DriverSQLite, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	n, err := notify.New(notify.Options{DB: db, ConfigDir: t.TempDir(), AppName: "cashp"})
	if err != nil {
		t.Fatalf("new notifier: %v", err)
	}
	return n
}

// TestNotifyDispatchesTorReady exercises the private notify helper directly:
// no tor binary is available in the test/CI sandbox, so Start can never
// reach a real Tor-ready state, but the dispatch call itself is unit-tested
// exactly as Start invokes it.
func TestNotifyDispatchesTorReady(t *testing.T) {
	svc := New(Options{DataDir: t.TempDir()})
	svc.opts.Notifier = newTestNotifier(t)
	ctx := context.Background()

	svc.notify(ctx, notify.EventTorReady, map[string]string{"onion_address": "abc123.onion"})

	held, err := svc.opts.Notifier.Store().DedupHeld(ctx, notify.EventTorReady+":")
	if err != nil {
		t.Fatalf("dedup held: %v", err)
	}
	if !held {
		t.Fatal("expected tor_ready to have been dispatched")
	}
}

// TestNotifyWithoutNotifierSkipsSilently confirms the nil-safe no-op path
// never panics.
func TestNotifyWithoutNotifierSkipsSilently(t *testing.T) {
	svc := New(Options{DataDir: t.TempDir()})
	svc.notify(context.Background(), notify.EventTorReady, map[string]string{"onion_address": "abc123.onion"})
}

func TestNewFillsDefaults(t *testing.T) {
	root := t.TempDir()
	svc := New(Options{DataDir: root, Port: 64080, I2PSAMAddr: "127.0.0.1:7777"})

	if svc.opts.ConfigDir != root {
		t.Errorf("ConfigDir = %q, want %q", svc.opts.ConfigDir, root)
	}
	if want := filepath.Join(root, "log"); svc.opts.LogDir != want {
		t.Errorf("LogDir = %q, want %q", svc.opts.LogDir, want)
	}
	if svc.torCfg.VirtualPort != DefaultTorConfig().VirtualPort {
		t.Errorf("tor config was not defaulted: %+v", svc.torCfg)
	}
	if svc.i2pCfg.Enabled {
		t.Error("I2P must stay opted out when Options.I2PEnabled is false")
	}
	if svc.i2pCfg.SAMAddress != "127.0.0.1:7777" {
		t.Errorf("SAMAddress = %q, want the Options override", svc.i2pCfg.SAMAddress)
	}
}

// With no tor binary and I2P opted out, the service must start silently, do
// nothing, own no listener and still report healthy — the server never fails
// to start because of an overlay network.
func TestStartWithoutProvidersIsGracefulNoOp(t *testing.T) {
	root := t.TempDir()
	torCfg := DefaultTorConfig()
	torCfg.Binary = filepath.Join(t.TempDir(), "no-such-tor")

	svc := New(Options{DataDir: root, Port: 64080, Tor: &torCfg})

	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start = %v, want nil", err)
	}
	t.Cleanup(func() {
		if err := svc.Stop(); err != nil {
			t.Errorf("Stop = %v, want nil", err)
		}
	})

	if onion, ok := svc.OnionAddress(); ok || onion != "" {
		t.Errorf("OnionAddress = (%q, %v), want (\"\", false)", onion, ok)
	}
	if eepsite, ok := svc.EepsiteAddress(); ok || eepsite != "" {
		t.Errorf("EepsiteAddress = (%q, %v), want (\"\", false)", eepsite, ok)
	}
	if listeners := svc.Listeners(); len(listeners) != 0 {
		t.Errorf("Listeners = %d, want 0", len(listeners))
	}
	if _, ok := svc.TorBackendPort(); ok {
		t.Error("TorBackendPort reported a port with no Tor running")
	}
	if _, ok := svc.I2PBackendPort(); ok {
		t.Error("I2PBackendPort reported a port with no I2P running")
	}
	if provider := svc.I2PProvider(); provider != providerNone {
		t.Errorf("I2PProvider = %q, want %q", provider, providerNone)
	}
	if svc.OutboundEnabled() {
		t.Error("OutboundEnabled is true with no Tor dialer")
	}
	if err := svc.HealthCheck(context.Background()); err != nil {
		t.Errorf("HealthCheck = %v, want nil when no provider is running", err)
	}

	// No provider means no port, no directory and no generated config.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read data dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("a provider-less start created %d entries under the data dir", len(entries))
	}
}

func TestStartIsIdempotentAndStopResets(t *testing.T) {
	torCfg := DefaultTorConfig()
	torCfg.Binary = filepath.Join(t.TempDir(), "no-such-tor")

	svc := New(Options{DataDir: t.TempDir(), Tor: &torCfg})

	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("first Start = %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("second Start = %v", err)
	}
	if err := svc.Stop(); err != nil {
		t.Fatalf("Stop = %v", err)
	}
	if svc.started {
		t.Error("Stop left the service marked as started")
	}
	if err := svc.Stop(); err != nil {
		t.Fatalf("second Stop = %v, want nil", err)
	}
}

func TestStartHonoursCancelledContext(t *testing.T) {
	svc := New(Options{DataDir: t.TempDir()})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := svc.Start(ctx); err == nil {
		t.Fatal("Start with a cancelled context returned nil, want the context error")
	}
}

func TestTorPathsAreDerived(t *testing.T) {
	paths := torPathsFor(Options{
		DataDir:   "/data/cashp",
		ConfigDir: "/config/cashp",
		LogDir:    "/log/cashp",
	})

	cases := map[string]string{
		paths.torrc:        "/config/cashp/tor/torrc",
		paths.dataDir:      "/data/cashp/tor",
		paths.hiddenSvcDir: "/data/cashp/tor/site",
		paths.hostname:     "/data/cashp/tor/site/hostname",
		paths.pidFile:      "/data/cashp/tor/tor.pid",
		paths.logFile:      "/log/cashp/tor.log",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
	}
}

func TestWriteSecretFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "torrc")

	if err := writeSecretFile(path, []byte("ExitRelay 0\n")); err != nil {
		t.Fatalf("writeSecretFile = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != filePerm {
		t.Errorf("file mode = %v, want %v", perm, filePerm)
	}

	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != dirPerm {
		t.Errorf("dir mode = %v, want %v", perm, dirPerm)
	}
}
