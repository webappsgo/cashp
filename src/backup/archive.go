package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/webappsgo/cashp/src/security"
)

// Encryption envelope constants. The envelope stores only the KDF salt:
// the password itself is never written to disk, to the manifest, or to any
// configuration file.
const (
	// envelopeMagic identifies an encrypted cashp archive.
	envelopeMagic = "CASHPBK1"
	// envelopeVersion is the envelope layout version.
	envelopeVersion = 1
	// saltLen is the Argon2id salt length in bytes.
	saltLen = 16
	// keyLen is the AES-256 key length in bytes.
	keyLen = 32
	// kdfTime is the Argon2id iteration count.
	kdfTime = 3
	// kdfMemory is the Argon2id memory cost in KiB (64 MB).
	kdfMemory = 64 * 1024
	// kdfThreads is the Argon2id parallelism factor.
	kdfThreads = 4
)

// buildArchive serializes a manifest and its deduplicated blocks into a
// gzip-compressed tar stream held entirely in memory. Blocks are written
// in hash order so the same inputs always produce the same archive body.
func buildArchive(m *Manifest, blocks map[string][]byte) ([]byte, error) {
	manifestJSON, err := marshalManifest(m)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer

	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}

	tw := tar.NewWriter(gz)

	if err := writeTarFile(tw, ManifestName, manifestJSON, m.CreatedAt); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(blocks))
	for name := range blocks {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if err := writeTarFile(tw, blockPrefix+name, blocks[name], m.CreatedAt); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	if err := gz.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// writeTarFile appends one regular file to the tar stream.
func writeTarFile(tw *tar.Writer, name string, data []byte, modTime time.Time) error {
	hdr := &tar.Header{
		Name:     name,
		Mode:     0o600,
		Size:     int64(len(data)),
		ModTime:  modTime,
		Typeflag: tar.TypeReg,
		Format:   tar.FormatPAX,
	}

	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}

	_, err := tw.Write(data)

	return err
}

// readArchive parses a gzip-compressed tar stream back into its manifest
// and block map. maxEntrySize bounds a single entry so a malformed archive
// cannot exhaust memory.
func readArchive(data []byte) (*Manifest, map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrInvalidFormat, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	blocks := make(map[string][]byte)

	var manifestJSON []byte

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %s", ErrInvalidFormat, err)
		}

		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		body, err := io.ReadAll(io.LimitReader(tr, maxEntrySize))
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %s", ErrInvalidFormat, err)
		}

		switch {
		case hdr.Name == ManifestName:
			manifestJSON = body
		case strings.HasPrefix(hdr.Name, blockPrefix):
			blocks[strings.TrimPrefix(hdr.Name, blockPrefix)] = body
		}
	}

	if manifestJSON == nil {
		return nil, nil, ErrManifestMissing
	}

	m, err := unmarshalManifest(manifestJSON)
	if err != nil {
		return nil, nil, err
	}

	return m, blocks, nil
}

// maxEntrySize caps a single archive entry at 2 GiB.
const maxEntrySize = 2 << 30

// deriveKey turns a backup password into a 256-bit AES key with Argon2id.
// The password is used only here and is never persisted.
func deriveKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, kdfTime, kdfMemory, kdfThreads, keyLen)
}

// seal encrypts an archive body with AES-256-GCM under a freshly salted
// key derived from password, producing magic | version | salt | ciphertext.
func seal(plaintext []byte, password string) ([]byte, error) {
	if password == "" {
		return nil, ErrPasswordRequired
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}

	ciphertext, err := security.Encrypt(deriveKey(password, salt), plaintext)
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, len(envelopeMagic)+1+saltLen+len(ciphertext))
	out = append(out, envelopeMagic...)
	out = append(out, envelopeVersion)
	out = append(out, salt...)
	out = append(out, ciphertext...)

	return out, nil
}

// open reverses seal. A wrong password fails the GCM authentication tag and
// is reported as ErrInvalidPassword, which doubles as the decrypt test.
func open(data []byte, password string) ([]byte, error) {
	if !IsEncrypted(data) {
		return nil, ErrInvalidFormat
	}

	if password == "" {
		return nil, ErrPasswordRequired
	}

	header := len(envelopeMagic) + 1
	if data[len(envelopeMagic)] != envelopeVersion {
		return nil, fmt.Errorf("%w: unsupported envelope version %d", ErrInvalidFormat, data[len(envelopeMagic)])
	}

	if len(data) < header+saltLen {
		return nil, ErrInvalidFormat
	}

	salt := data[header : header+saltLen]

	plaintext, err := security.Decrypt(deriveKey(password, salt), data[header+saltLen:])
	if err != nil {
		return nil, ErrInvalidPassword
	}

	return plaintext, nil
}

// IsEncrypted reports whether data starts with the encrypted-archive
// envelope. The file extension is a hint; this is the authority.
func IsEncrypted(data []byte) bool {
	return len(data) > len(envelopeMagic)+1 && string(data[:len(envelopeMagic)]) == envelopeMagic
}
