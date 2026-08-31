package backup

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Verify runs every check AI.md PART 22 requires before a backup may be
// trusted: the file exists and is non-empty, the format parses, an
// encrypted archive decrypts with the supplied password, the manifest is
// readable, the recorded SHA-256 matches the archive's real content, every
// file extracts and matches its own checksum, the required files are
// present, and every bundled database has a valid header. Extraction is
// performed in memory so a decrypted copy never touches disk.
func (s *Service) Verify(ctx context.Context, path, password string) (Manifest, error) {
	m, _, err := s.verify(ctx, path, password)
	if err != nil {
		return Manifest{}, err
	}

	return *m, nil
}

// verify performs the full check sequence and additionally hands back the
// extracted file contents, so a restore never has to read, decrypt, and
// extract the archive a second time.
func (s *Service) verify(ctx context.Context, path, password string) (*Manifest, map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}

	if info.Size() == 0 {
		return nil, nil, ErrEmptyBackup
	}

	m, blocks, err := loadArchive(path, password)
	if err != nil {
		return nil, nil, err
	}

	warning, err := checkVersion(m.Version)
	if err != nil {
		return nil, nil, err
	}

	if err := verifyBlocks(m, blocks); err != nil {
		return nil, nil, err
	}

	sum, err := digest(m.Contents, blocks)
	if err != nil {
		return nil, nil, err
	}

	if sum != m.Checksum {
		return nil, nil, fmt.Errorf("%w: manifest records %s, archive computes %s", ErrChecksumMismatch, m.Checksum, sum)
	}

	files, err := reassemble(m, blocks)
	if err != nil {
		return nil, nil, err
	}

	if err := verifyContents(m, files); err != nil {
		return nil, nil, err
	}

	m.Path = path
	m.FileSize = info.Size()
	m.VersionWarning = warning

	return m, files, nil
}

// loadArchive reads an archive from disk, decrypting it first when it
// carries the encryption envelope. Decryption doubles as the decrypt test:
// AES-256-GCM authentication fails on a wrong password or altered bytes.
func loadArchive(path, password string) (*Manifest, map[string][]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	if len(raw) == 0 {
		return nil, nil, ErrEmptyBackup
	}

	body := raw
	encrypted := IsEncrypted(raw)

	if encrypted {
		if body, err = open(raw, password); err != nil {
			return nil, nil, err
		}
	}

	m, blocks, err := readArchive(body)
	if err != nil {
		return nil, nil, err
	}

	if m.Encrypted != encrypted {
		return nil, nil, fmt.Errorf("%w: manifest encryption flag does not match the archive", ErrInvalidFormat)
	}

	return m, blocks, nil
}

// verifyBlocks checks that every stored block hashes to its own name and
// that the deduplication index describes exactly the blocks present.
func verifyBlocks(m *Manifest, blocks map[string][]byte) error {
	for name, data := range blocks {
		if hashBytes(data) != name {
			return fmt.Errorf("%w: block %s does not match its content hash", ErrChecksumMismatch, name)
		}
	}

	if len(m.BlockIndex) != len(blocks) {
		return fmt.Errorf("%w: block index lists %d blocks, archive holds %d", ErrChecksumMismatch, len(m.BlockIndex), len(blocks))
	}

	for _, b := range m.BlockIndex {
		data, ok := blocks[b.Hash]
		if !ok {
			return fmt.Errorf("%w: block %s listed in the index is missing", ErrChecksumMismatch, b.Hash)
		}
		if int64(len(data)) != b.Size {
			return fmt.Errorf("%w: block %s is %d bytes, index says %d", ErrChecksumMismatch, b.Hash, len(data), b.Size)
		}
	}

	return nil
}

// reassemble rebuilds every archived file from its deduplicated blocks and
// verifies each result against its recorded size and checksum. This is the
// content-extraction test, performed entirely in memory.
func reassemble(m *Manifest, blocks map[string][]byte) (map[string][]byte, error) {
	files := make(map[string][]byte, len(m.Contents))

	for _, e := range m.Contents {
		content := make([]byte, 0, e.Size)

		for _, name := range e.Blocks {
			data, ok := blocks[name]
			if !ok {
				return nil, fmt.Errorf("%w: %s references missing block %s", ErrChecksumMismatch, e.Path, name)
			}
			content = append(content, data...)
		}

		if int64(len(content)) != e.Size {
			return nil, fmt.Errorf("%w: %s extracts to %d bytes, manifest says %d", ErrChecksumMismatch, e.Path, len(content), e.Size)
		}

		if hashBytes(content) != e.Checksum {
			return nil, fmt.Errorf("%w: %s failed its checksum", ErrChecksumMismatch, e.Path)
		}

		files[e.Path] = content
	}

	return files, nil
}

// verifyContents checks that the always-required files are present and
// that every bundled database is a valid SQLite 3 file.
func verifyContents(m *Manifest, files map[string][]byte) error {
	for _, req := range requiredFiles {
		if _, ok := files[req]; !ok {
			return fmt.Errorf("%w: %s", ErrMissingRequiredFile, req)
		}
	}

	for _, e := range m.Contents {
		if !strings.HasSuffix(e.Path, ".db") {
			continue
		}

		content := files[e.Path]
		if len(content) < len(sqliteMagic) || string(content[:len(sqliteMagic)]) != sqliteMagic {
			return fmt.Errorf("%w: %s is not a valid SQLite database", ErrDatabaseIntegrity, e.Path)
		}
	}

	return nil
}
