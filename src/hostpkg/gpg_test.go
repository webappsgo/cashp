package hostpkg

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dockerRPMKeyFingerprint is the pinned fingerprint of the fixture key.
const dockerRPMKeyFingerprint = "060A61C51B558A7F742B77AAC52FEB6B621E9F35"

// readKeyFixture loads the armored signing key used across the key tests.
func readKeyFixture(t *testing.T) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", "docker-rpm-key.asc"))
	if err != nil {
		t.Fatalf("read key fixture: %v", err)
	}

	return data
}

func TestNormalizeFingerprint(t *testing.T) {
	got, err := NormalizeFingerprint("060a 61c5 1b55 8a7f 742b 77aa c52f eb6b 621e 9f35")
	if err != nil {
		t.Fatalf("NormalizeFingerprint: %v", err)
	}
	if got != dockerRPMKeyFingerprint {
		t.Fatalf("NormalizeFingerprint = %q, want %q", got, dockerRPMKeyFingerprint)
	}

	for _, raw := range []string{"", "060A61C5", "zz" + dockerRPMKeyFingerprint[2:], strings.Repeat("A", 41)} {
		if _, err := NormalizeFingerprint(raw); !errors.Is(err, ErrKeyUnparsable) {
			t.Errorf("NormalizeFingerprint(%q) = %v, want ErrKeyUnparsable", raw, err)
		}
	}
}

func TestKeyFingerprintsFromArmoredFixture(t *testing.T) {
	fingerprints, err := KeyFingerprints(readKeyFixture(t))
	if err != nil {
		t.Fatalf("KeyFingerprints: %v", err)
	}
	if len(fingerprints) == 0 || fingerprints[0] != dockerRPMKeyFingerprint {
		t.Fatalf("KeyFingerprints = %v, want first %q", fingerprints, dockerRPMKeyFingerprint)
	}
}

func TestArmorRoundTrip(t *testing.T) {
	binaryKey, err := DearmorKey(readKeyFixture(t))
	if err != nil {
		t.Fatalf("DearmorKey: %v", err)
	}

	armored := ArmorKey(binaryKey)
	if !bytes.HasPrefix(armored, []byte(armorHeader)) || !bytes.Contains(armored, []byte(armorFooter)) {
		t.Fatal("ArmorKey did not produce a complete armored block")
	}

	roundTripped, err := DearmorKey(armored)
	if err != nil {
		t.Fatalf("DearmorKey(ArmorKey(...)): %v", err)
	}
	if !bytes.Equal(binaryKey, roundTripped) {
		t.Fatal("armor round trip changed the key bytes")
	}

	// Binary input passes through unchanged.
	passthrough, err := DearmorKey(binaryKey)
	if err != nil || !bytes.Equal(passthrough, binaryKey) {
		t.Fatalf("DearmorKey(binary) = %d bytes, %v", len(passthrough), err)
	}
}

func TestDearmorKeyRejectsCorruption(t *testing.T) {
	armored := string(readKeyFixture(t))

	// A truncated block never reaches the footer.
	truncated := armored[:len(armored)/2]
	if _, err := DearmorKey([]byte(truncated)); !errors.Is(err, ErrKeyUnparsable) {
		t.Errorf("truncated block error = %v, want ErrKeyUnparsable", err)
	}

	// A flipped checksum must fail the CRC24 integrity check.
	lines := strings.Split(strings.TrimRight(armored, "\n"), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "=") {
			if line[1] == 'A' {
				lines[i] = "=B" + line[2:]
			} else {
				lines[i] = "=A" + line[2:]
			}
		}
	}
	if _, err := DearmorKey([]byte(strings.Join(lines, "\n") + "\n")); !errors.Is(err, ErrKeyUnparsable) {
		t.Errorf("bad checksum error = %v, want ErrKeyUnparsable", err)
	}

	if _, err := DearmorKey(nil); !errors.Is(err, ErrKeyUnparsable) {
		t.Errorf("empty input error = %v, want ErrKeyUnparsable", err)
	}
	if _, err := KeyFingerprints([]byte("this is not a key at all")); !errors.Is(err, ErrKeyUnparsable) {
		t.Errorf("garbage input error = %v, want ErrKeyUnparsable", err)
	}
}

func TestExtractPinnedKeyMatches(t *testing.T) {
	extracted, err := ExtractPinnedKey(readKeyFixture(t), dockerRPMKeyFingerprint)
	if err != nil {
		t.Fatalf("ExtractPinnedKey: %v", err)
	}
	if len(extracted) == 0 {
		t.Fatal("ExtractPinnedKey returned no key material")
	}

	fingerprints, err := KeyFingerprints(extracted)
	if err != nil {
		t.Fatalf("KeyFingerprints(extracted): %v", err)
	}
	if len(fingerprints) != 1 || fingerprints[0] != dockerRPMKeyFingerprint {
		t.Fatalf("extracted key fingerprints = %v, want exactly the pinned key", fingerprints)
	}
}

func TestExtractPinnedKeyMismatchIsHardFailure(t *testing.T) {
	// A well-formed key whose fingerprint is not the pinned one is refused.
	other := "9DC858229FC7DD38854AE2D88D81803C0EBFCD88"
	if _, err := ExtractPinnedKey(readKeyFixture(t), other); !errors.Is(err, ErrKeyFingerprintMismatch) {
		t.Fatalf("error = %v, want ErrKeyFingerprintMismatch", err)
	}

	// An unparsable pin is rejected before the key material is even read.
	if _, err := ExtractPinnedKey(readKeyFixture(t), "not-a-fingerprint"); !errors.Is(err, ErrKeyUnparsable) {
		t.Fatalf("error = %v, want ErrKeyUnparsable", err)
	}
}

func TestParsePacketsRejectsMalformedStreams(t *testing.T) {
	cases := [][]byte{
		{0x00, 0x01},
		{0x99, 0x00},
		{0x99, 0x00, 0x10, 0x04},
		{0xc6, 0xff, 0x00, 0x00, 0x10},
	}

	for i, data := range cases {
		if _, err := parsePackets(data); !errors.Is(err, ErrKeyUnparsable) {
			t.Errorf("case %d error = %v, want ErrKeyUnparsable", i, err)
		}
	}
}

func TestKeyFingerprintRejectsUnsupportedVersion(t *testing.T) {
	body := append([]byte{3}, bytes.Repeat([]byte{0x00}, 16)...)
	if _, err := keyFingerprint(body); !errors.Is(err, ErrKeyUnparsable) {
		t.Fatalf("error = %v, want ErrKeyUnparsable", err)
	}
	if _, err := keyFingerprint([]byte{4}); !errors.Is(err, ErrKeyUnparsable) {
		t.Fatalf("short body error = %v, want ErrKeyUnparsable", err)
	}
}

func TestCRC24MatchesKnownValue(t *testing.T) {
	// RFC 4880 section 6.1 worked example: the CRC-24 of an empty message is
	// the initial value itself.
	if got := crc24(nil); got != 0x00b704ce {
		t.Fatalf("crc24(nil) = %#x, want 0xb704ce", got)
	}
	if crc24([]byte("cashp")) == crc24([]byte("cashq")) {
		t.Fatal("crc24 did not distinguish two different inputs")
	}
}
