package overlay

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultI2PConfigIsOptIn(t *testing.T) {
	if DefaultI2PConfig().Enabled {
		t.Fatal("I2P must default to disabled (opt-in only)")
	}
}

// A disabled I2P config must contact no provider, allocate no port and write
// no file at all.
func TestStartI2PDisabledIsInert(t *testing.T) {
	root := t.TempDir()
	opts := Options{DataDir: root, ConfigDir: filepath.Join(root, "etc"), LogDir: filepath.Join(root, "log")}

	rt, err := startI2P(context.Background(), DefaultI2PConfig(), opts)
	if rt != nil {
		t.Fatal("startI2P returned a runtime while disabled")
	}
	if !errors.Is(err, errI2PDisabled) {
		t.Fatalf("startI2P error = %v, want errI2PDisabled", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read data dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("disabled I2P created %d entries under the data dir", len(entries))
	}
}

func TestTunnelsConfig(t *testing.T) {
	cfg := DefaultI2PConfig()
	cfg.InboundLength = 2
	cfg.OutboundQuantity = 4

	conf := tunnelsConfig(cfg, "/data/i2p/site/site-keys.dat", 64500)

	want := []string{
		"[site]",
		"type = server",
		"host = 127.0.0.1",
		"port = 64500",
		"keys = /data/i2p/site/site-keys.dat",
		"inbound.length = 2",
		"outbound.quantity = 4",
		"signaturetype = 7",
	}
	for _, line := range want {
		if !strings.Contains(conf, line) {
			t.Errorf("tunnels.conf is missing %q:\n%s", line, conf)
		}
	}
}

func TestB32Address(t *testing.T) {
	got := B32Address([]byte("cashp-overlay-test"))
	const want = "yhjvnyh4g35f3e4tsryfgb7zxosegmnslvxsfuby5hroxb25rmxa.b32.i2p"

	if got != want {
		t.Fatalf("B32Address = %q, want %q", got, want)
	}
	if Kind(got) != KindI2P {
		t.Fatalf("Kind(%q) = %q, want %q", got, Kind(got), KindI2P)
	}
}

func TestDestinationFromKeyFile(t *testing.T) {
	// A DSA-era destination ends after the zero certificate; nothing that
	// follows in the key file belongs to it.
	legacy := make([]byte, 387+256)
	dest, err := destinationFromKeyFile(legacy)
	if err != nil {
		t.Fatalf("legacy key file: %v", err)
	}
	if len(dest) != 387 {
		t.Fatalf("legacy destination length = %d, want 387", len(dest))
	}

	// An Ed25519 destination carries a 4-byte key certificate.
	modern := make([]byte, 391+256)
	modern[384] = 5
	binary.BigEndian.PutUint16(modern[385:387], 4)
	dest, err = destinationFromKeyFile(modern)
	if err != nil {
		t.Fatalf("modern key file: %v", err)
	}
	if len(dest) != 391 {
		t.Fatalf("modern destination length = %d, want 391", len(dest))
	}

	if _, err := destinationFromKeyFile(make([]byte, 100)); err == nil {
		t.Fatal("short key file accepted, want error")
	}

	truncated := make([]byte, 388)
	truncated[384] = 5
	binary.BigEndian.PutUint16(truncated[385:387], 64)
	if _, err := destinationFromKeyFile(truncated); err == nil {
		t.Fatal("truncated certificate accepted, want error")
	}
}

func TestI2PConfigNormalizeClampsInvalidValues(t *testing.T) {
	cfg := I2PConfig{
		VirtualPort:      0,
		InboundLength:    9,
		OutboundLength:   -1,
		InboundQuantity:  0,
		OutboundQuantity: 99,
		BootstrapTimeout: 1,
	}
	cfg.normalize()

	defaults := DefaultI2PConfig()
	if cfg.SAMAddress != defaults.SAMAddress ||
		cfg.VirtualPort != defaults.VirtualPort ||
		cfg.InboundLength != defaults.InboundLength ||
		cfg.OutboundLength != defaults.OutboundLength ||
		cfg.InboundQuantity != defaults.InboundQuantity ||
		cfg.OutboundQuantity != defaults.OutboundQuantity ||
		cfg.BootstrapTimeout != defaults.BootstrapTimeout {
		t.Fatalf("normalize did not clamp invalid values: %+v", cfg)
	}
}

func TestParseSAMFields(t *testing.T) {
	fields := parseSAMFields(`RESULT=I2P_ERROR MESSAGE="tunnel build failed" ID=site`)

	if fields["RESULT"] != "I2P_ERROR" {
		t.Errorf("RESULT = %q, want I2P_ERROR", fields["RESULT"])
	}
	if fields["MESSAGE"] != "tunnel build failed" {
		t.Errorf("MESSAGE = %q, want the unquoted message", fields["MESSAGE"])
	}
	if fields["ID"] != "site" {
		t.Errorf("ID = %q, want site", fields["ID"])
	}
}

func TestI2PPathsAreDerived(t *testing.T) {
	paths := i2pPathsFor(Options{
		DataDir:   "/data/cashp",
		ConfigDir: "/config/cashp",
		LogDir:    "/log/cashp",
	})

	cases := map[string]string{
		paths.tunnelsConf: "/config/cashp/i2p/tunnels.conf",
		paths.dataDir:     "/data/cashp/i2p",
		paths.siteDir:     "/data/cashp/i2p/site",
		paths.keys:        "/data/cashp/i2p/site/site-keys.dat",
		paths.pidFile:     "/data/cashp/i2p/i2pd.pid",
		paths.logFile:     "/log/cashp/i2pd.log",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
	}
}
