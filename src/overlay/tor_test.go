package overlay

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// testTorPaths returns Tor paths rooted in a temporary directory so torrc
// generation can be asserted without touching a real install.
func testTorPaths(t *testing.T) torPaths {
	t.Helper()

	root := t.TempDir()
	return torPathsFor(Options{
		DataDir:   root,
		ConfigDir: filepath.Join(root, "etc"),
		LogDir:    filepath.Join(root, "log"),
	})
}

// The generated torrc must never turn this Tor instance into a relay, an exit
// or a directory server (AI.md PART 32.1 "App-Scoped Only"). ORPort is absent
// entirely rather than set to 0, so no configuration path can enable it.
func TestTorrcConfigNeverRelaysOrExits(t *testing.T) {
	torrc := torrcConfig(DefaultTorConfig(), testTorPaths(t), 64123)

	required := []string{
		"ExitRelay 0",
		"ExitPolicy reject *:*",
		"PublishServerDescriptor 0",
		"DirPort 0",
		"HiddenServiceSingleHopMode 0",
		"VanguardsLiteEnabled 1",
		"SocksPolicy accept 127.0.0.1",
		"ControlPort 127.0.0.1:auto",
		"HiddenServiceVersion 3",
		"HiddenServiceExportCircuitID haproxy",
		"SafeLogging 1",
	}
	for _, directive := range required {
		if !strings.Contains(torrc, directive) {
			t.Errorf("torrc is missing %q", directive)
		}
	}

	forbidden := []string{
		"ORPort",
		"PublishServerDescriptor 1",
		"ExitRelay 1",
		"9050",
		"9051",
		"0.0.0.0",
	}
	for _, directive := range forbidden {
		if strings.Contains(torrc, directive) {
			t.Errorf("torrc contains forbidden %q:\n%s", directive, torrc)
		}
	}
}

func TestTorrcConfigHiddenServiceMapping(t *testing.T) {
	paths := testTorPaths(t)
	cfg := DefaultTorConfig()
	cfg.VirtualPort = 80

	torrc := torrcConfig(cfg, paths, 64321)

	want := []string{
		"HiddenServiceDir " + paths.hiddenSvcDir,
		"HiddenServicePort 80 127.0.0.1:64321",
		"PidFile " + paths.pidFile,
		"Log notice file " + paths.logFile,
	}
	for _, line := range want {
		if !strings.Contains(torrc, line) {
			t.Errorf("torrc is missing %q", line)
		}
	}
}

func TestTorrcConfigSocksPort(t *testing.T) {
	paths := testTorPaths(t)

	offline := DefaultTorConfig()
	offline.UseNetwork = false
	offline.AllowUserPreference = false
	if torrc := torrcConfig(offline, paths, 64123); !strings.Contains(torrc, "SocksPort 0") {
		t.Error("torrc should disable SocksPort when no outbound Tor use is possible")
	}

	outbound := DefaultTorConfig()
	outbound.UseNetwork = true
	torrc := torrcConfig(outbound, paths, 64123)
	if !strings.Contains(torrc, "SocksPort 127.0.0.1:auto") {
		t.Error("torrc should bind SocksPort to a loopback runtime port when outbound is possible")
	}
}

func TestTorrcConfigAccounting(t *testing.T) {
	paths := testTorPaths(t)

	limited := DefaultTorConfig()
	torrc := torrcConfig(limited, paths, 64123)
	if !strings.Contains(torrc, "AccountingMax 100 GB") || !strings.Contains(torrc, "AccountingStart month 1 00:00") {
		t.Error("torrc should carry the monthly accounting limit")
	}

	unlimited := DefaultTorConfig()
	unlimited.MaxMonthlyBandwidth = "unlimited"
	if torrc := torrcConfig(unlimited, paths, 64123); strings.Contains(torrc, "AccountingMax") {
		t.Error("torrc should omit accounting when bandwidth is unlimited")
	}
}

func TestResolveTorBinaryMissing(t *testing.T) {
	cfg := DefaultTorConfig()
	cfg.Binary = filepath.Join(t.TempDir(), "no-such-tor")

	if _, err := resolveTorBinary(cfg); !errors.Is(err, errTorUnavailable) {
		t.Fatalf("resolveTorBinary error = %v, want errTorUnavailable", err)
	}
}

func TestTorConfigNormalizeClampsInvalidValues(t *testing.T) {
	cfg := TorConfig{
		MaxCircuits:          0,
		CircuitTimeout:       9,
		BootstrapTimeout:     5,
		MaxStreamsPerCircuit: 9000,
		NumIntroPoints:       99,
		VirtualPort:          70000,
	}
	cfg.normalize()

	defaults := DefaultTorConfig()
	if cfg.MaxCircuits != defaults.MaxCircuits ||
		cfg.CircuitTimeout != defaults.CircuitTimeout ||
		cfg.BootstrapTimeout != defaults.BootstrapTimeout ||
		cfg.MaxStreamsPerCircuit != defaults.MaxStreamsPerCircuit ||
		cfg.NumIntroPoints != defaults.NumIntroPoints ||
		cfg.VirtualPort != defaults.VirtualPort {
		t.Fatalf("normalize did not clamp invalid values: %+v", cfg)
	}
	if cfg.BandwidthRate != defaults.BandwidthRate || cfg.BandwidthBurst != defaults.BandwidthBurst {
		t.Fatalf("normalize did not default bandwidth settings: %+v", cfg)
	}
}
