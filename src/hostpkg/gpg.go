package hostpkg

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// This file implements just enough of OpenPGP (RFC 4880 and RFC 9580) to
// answer one question without a third-party dependency: does this downloaded
// key material contain exactly the primary key whose fingerprint cashp has
// pinned? Nothing here verifies signatures or decrypts anything; the key is
// handed to apt, dnf or rpm only after its fingerprint matches the pin.

// OpenPGP packet tags used for public key material.
const (
	tagPublicKey    = 6
	tagPublicSubkey = 14
)

// Fingerprint hash prefixes defined by the OpenPGP specifications.
const (
	fingerprintPrefixV4 = 0x99
	fingerprintPrefixV5 = 0x9a
	fingerprintPrefixV6 = 0x9b
)

// armorHeader and armorFooter delimit an ASCII-armored public key block.
const (
	armorHeader = "-----BEGIN PGP PUBLIC KEY BLOCK-----"
	armorFooter = "-----END PGP PUBLIC KEY BLOCK-----"
)

// armorLineLength is the base64 line width used by every OpenPGP producer.
const armorLineLength = 64

// packet is one parsed OpenPGP packet plus its byte range in the input, which
// is what lets a pinned key be sliced back out of a multi-key file.
type packet struct {
	tag   int
	body  []byte
	start int
	end   int
}

// NormalizeFingerprint uppercases a fingerprint and strips the spaces GPG
// prints, then checks that what remains is a v4 (40 hex) or v6 (64 hex)
// fingerprint.
func NormalizeFingerprint(raw string) (string, error) {
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ':':
			continue
		case r >= '0' && r <= '9', r >= 'A' && r <= 'F':
			b.WriteRune(r)
		case r >= 'a' && r <= 'f':
			b.WriteRune(r - 32)
		default:
			return "", failValidation(ErrKeyUnparsable, "repository signing key fingerprint is not valid")
		}
	}

	normalized := b.String()
	if len(normalized) != 40 && len(normalized) != 64 {
		return "", failValidation(ErrKeyUnparsable, "repository signing key fingerprint is not valid")
	}

	return normalized, nil
}

// DearmorKey converts ASCII-armored key material to its binary form and
// returns binary input unchanged, so a caller can accept either encoding.
func DearmorKey(data []byte) ([]byte, error) {
	text := string(data)
	if !strings.Contains(text, armorHeader) {
		if len(data) == 0 {
			return nil, failValidation(ErrKeyUnparsable, "repository signing key could not be read")
		}
		return data, nil
	}

	_, rest, ok := strings.Cut(text, armorHeader)
	if !ok {
		return nil, failValidation(ErrKeyUnparsable, "repository signing key could not be read")
	}

	var (
		payload  strings.Builder
		checksum string
		inBody   bool
		closed   bool
	)

	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, armorFooter) {
			closed = true
			break
		}
		if !inBody {
			// Armor headers run until the first blank line; a body that
			// starts immediately is also valid.
			if strings.TrimSpace(line) == "" {
				inBody = true
				continue
			}
			if strings.Contains(line, ": ") {
				continue
			}
			inBody = true
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "=") {
			checksum = strings.TrimPrefix(trimmed, "=")
			continue
		}
		payload.WriteString(trimmed)
	}

	if !closed {
		return nil, failValidation(ErrKeyUnparsable, "repository signing key could not be read")
	}

	decoded, err := base64.StdEncoding.DecodeString(payload.String())
	if err != nil {
		return nil, failValidation(ErrKeyUnparsable, "repository signing key could not be read")
	}
	if len(decoded) == 0 {
		return nil, failValidation(ErrKeyUnparsable, "repository signing key could not be read")
	}

	if checksum != "" {
		want, err := base64.StdEncoding.DecodeString(checksum)
		if err != nil || len(want) != 3 {
			return nil, failValidation(ErrKeyUnparsable, "repository signing key could not be read")
		}
		got := crc24(decoded)
		if want[0] != byte(got>>16) || want[1] != byte(got>>8) || want[2] != byte(got) {
			return nil, failValidation(ErrKeyUnparsable, "repository signing key failed its integrity check")
		}
	}

	return decoded, nil
}

// ArmorKey renders binary key material as an ASCII-armored block, which is
// the encoding dnf and rpm expect in a gpgkey file.
func ArmorKey(data []byte) []byte {
	encoded := base64.StdEncoding.EncodeToString(data)

	var b strings.Builder
	b.WriteString(armorHeader)
	b.WriteString("\n\n")
	for len(encoded) > armorLineLength {
		b.WriteString(encoded[:armorLineLength])
		b.WriteString("\n")
		encoded = encoded[armorLineLength:]
	}
	if encoded != "" {
		b.WriteString(encoded)
		b.WriteString("\n")
	}

	sum := crc24(data)
	b.WriteString("=")
	b.WriteString(base64.StdEncoding.EncodeToString([]byte{byte(sum >> 16), byte(sum >> 8), byte(sum)}))
	b.WriteString("\n")
	b.WriteString(armorFooter)
	b.WriteString("\n")

	return []byte(b.String())
}

// KeyFingerprints returns the fingerprints of the primary public keys in key
// material, which may be armored or binary. Subkeys are ignored: a pin always
// names a primary key.
func KeyFingerprints(data []byte) ([]string, error) {
	binaryKey, err := DearmorKey(data)
	if err != nil {
		return nil, err
	}

	packets, err := parsePackets(binaryKey)
	if err != nil {
		return nil, err
	}

	var fingerprints []string
	for _, p := range packets {
		if p.tag != tagPublicKey {
			continue
		}
		fp, err := keyFingerprint(p.body)
		if err != nil {
			return nil, err
		}
		fingerprints = append(fingerprints, fp)
	}

	if len(fingerprints) == 0 {
		return nil, failValidation(ErrKeyUnparsable, "repository signing key contains no public key")
	}

	return fingerprints, nil
}

// ExtractPinnedKey returns the binary packets belonging to the primary key
// whose fingerprint equals the pin, discarding any other key bundled in the
// same file. A file that does not contain the pinned key is a hard failure,
// so a substituted or extended key never reaches the host keyring.
func ExtractPinnedKey(data []byte, pinned string) ([]byte, error) {
	want, err := NormalizeFingerprint(pinned)
	if err != nil {
		return nil, err
	}

	binaryKey, err := DearmorKey(data)
	if err != nil {
		return nil, err
	}

	packets, err := parsePackets(binaryKey)
	if err != nil {
		return nil, err
	}

	start := -1
	end := len(binaryKey)
	for _, p := range packets {
		if p.tag != tagPublicKey {
			continue
		}
		if start >= 0 {
			end = p.start
			break
		}
		fp, err := keyFingerprint(p.body)
		if err != nil {
			return nil, err
		}
		if subtle.ConstantTimeCompare([]byte(fp), []byte(want)) == 1 {
			start = p.start
		}
	}

	if start < 0 {
		return nil, failValidation(ErrKeyFingerprintMismatch, "repository signing key does not match its pinned fingerprint")
	}

	out := make([]byte, end-start)
	copy(out, binaryKey[start:end])

	return out, nil
}

// keyFingerprint computes the fingerprint of a public key packet body.
func keyFingerprint(body []byte) (string, error) {
	if len(body) < 6 {
		return "", failValidation(ErrKeyUnparsable, "repository signing key could not be read")
	}

	switch body[0] {
	case 4:
		header := []byte{fingerprintPrefixV4, byte(len(body) >> 8), byte(len(body))}
		sum := sha1.Sum(append(header, body...))
		return strings.ToUpper(hex.EncodeToString(sum[:])), nil
	case 5, 6:
		prefix := byte(fingerprintPrefixV6)
		if body[0] == 5 {
			prefix = fingerprintPrefixV5
		}
		header := make([]byte, 5)
		header[0] = prefix
		binary.BigEndian.PutUint32(header[1:], uint32(len(body)))
		sum := sha256.Sum256(append(header, body...))
		return strings.ToUpper(hex.EncodeToString(sum[:])), nil
	default:
		return "", failValidation(ErrKeyUnparsable, fmt.Sprintf("repository signing key uses unsupported version %d", body[0]))
	}
}

// parsePackets walks the OpenPGP packet stream, supporting both the legacy
// and the current packet header formats. Partial-length bodies are rejected
// because no public key packet uses them.
func parsePackets(data []byte) ([]packet, error) {
	var packets []packet

	for i := 0; i < len(data); {
		start := i
		header := data[i]
		if header&0x80 == 0 {
			return nil, failValidation(ErrKeyUnparsable, "repository signing key could not be read")
		}

		var (
			tag    int
			length int
		)

		if header&0x40 != 0 {
			tag = int(header & 0x3f)
			i++
			if i >= len(data) {
				return nil, failValidation(ErrKeyUnparsable, "repository signing key could not be read")
			}
			first := int(data[i])
			switch {
			case first < 192:
				length = first
				i++
			case first < 224:
				if i+1 >= len(data) {
					return nil, failValidation(ErrKeyUnparsable, "repository signing key could not be read")
				}
				length = (first-192)<<8 + int(data[i+1]) + 192
				i += 2
			case first == 255:
				if i+4 >= len(data) {
					return nil, failValidation(ErrKeyUnparsable, "repository signing key could not be read")
				}
				length = int(binary.BigEndian.Uint32(data[i+1 : i+5]))
				i += 5
			default:
				return nil, failValidation(ErrKeyUnparsable, "repository signing key uses an unsupported packet length")
			}
		} else {
			tag = int(header&0x3c) >> 2
			lengthType := int(header & 0x03)
			i++
			switch lengthType {
			case 0:
				if i >= len(data) {
					return nil, failValidation(ErrKeyUnparsable, "repository signing key could not be read")
				}
				length = int(data[i])
				i++
			case 1:
				if i+1 >= len(data) {
					return nil, failValidation(ErrKeyUnparsable, "repository signing key could not be read")
				}
				length = int(binary.BigEndian.Uint16(data[i : i+2]))
				i += 2
			case 2:
				if i+3 >= len(data) {
					return nil, failValidation(ErrKeyUnparsable, "repository signing key could not be read")
				}
				length = int(binary.BigEndian.Uint32(data[i : i+4]))
				i += 4
			default:
				return nil, failValidation(ErrKeyUnparsable, "repository signing key uses an unsupported packet length")
			}
		}

		if length < 0 || i+length > len(data) {
			return nil, failValidation(ErrKeyUnparsable, "repository signing key is truncated")
		}

		packets = append(packets, packet{tag: tag, body: data[i : i+length], start: start, end: i + length})
		i += length
	}

	if len(packets) == 0 {
		return nil, failValidation(ErrKeyUnparsable, "repository signing key could not be read")
	}

	return packets, nil
}

// crc24 is the OpenPGP armor checksum from RFC 4880 section 6.1.
func crc24(data []byte) uint32 {
	const (
		initial = 0x00b704ce
		poly    = 0x01864cfb
	)

	crc := uint32(initial)
	for _, b := range data {
		crc ^= uint32(b) << 16
		for i := 0; i < 8; i++ {
			crc <<= 1
			if crc&0x01000000 != 0 {
				crc ^= poly
			}
		}
	}

	return crc & 0x00ffffff
}
