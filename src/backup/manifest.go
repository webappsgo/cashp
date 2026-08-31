package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Entry describes one file stored in a backup. Content is not stored in the
// entry: Blocks lists the deduplicated block hashes that, concatenated in
// order, reproduce the file exactly.
type Entry struct {
	Path     string    `json:"path"`
	Mode     uint32    `json:"mode"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
	Checksum string    `json:"checksum"`
	Blocks   []string  `json:"blocks"`
}

// Block is one entry of the deduplication index: a content block stored
// once in the archive no matter how many files or offsets reference it.
type Block struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

// Manifest is manifest.json: the description of an archive's format
// version, provenance, contents, deduplication index, content checksum,
// and encryption state.
type Manifest struct {
	Version          string    `json:"version"`
	CreatedAt        time.Time `json:"created_at"`
	CreatedBy        string    `json:"created_by,omitempty"`
	AppVersion       string    `json:"app_version,omitempty"`
	Contents         []Entry   `json:"contents"`
	BlockIndex       []Block   `json:"block_index"`
	Checksum         string    `json:"checksum"`
	Encrypted        bool      `json:"encrypted"`
	EncryptionMethod string    `json:"encryption_method,omitempty"`
	KeyDerivation    string    `json:"key_derivation,omitempty"`
	// OriginalSize is the total size of the files before deduplication.
	OriginalSize int64 `json:"original_size"`
	// StoredSize is the total size of the unique blocks actually stored.
	StoredSize int64 `json:"stored_size"`
	// Path is the archive's location on disk; it is never part of the archive.
	Path string `json:"-"`
	// FileSize is the on-disk size of the archive; it is never part of the archive.
	FileSize int64 `json:"-"`
	// VersionWarning is set by verification when the archive format is older than this build.
	VersionWarning string `json:"-"`
	// Readable is true when the manifest was parsed from the archive rather than inferred from disk.
	Readable bool `json:"-"`
}

// PathList returns the manifest paths of every file in the archive, in
// archive order, matching the "contents" listing shown in AI.md PART 22.
func (m *Manifest) PathList() []string {
	out := make([]string, 0, len(m.Contents))
	for _, e := range m.Contents {
		out = append(out, e.Path)
	}

	return out
}

// Entry returns the manifest entry for path.
func (m *Manifest) Entry(path string) (Entry, bool) {
	for _, e := range m.Contents {
		if e.Path == path {
			return e, true
		}
	}

	return Entry{}, false
}

// DedupRatio reports stored bytes divided by original bytes; a value below
// 1 means deduplication removed repeated content before compression.
func (m *Manifest) DedupRatio() float64 {
	if m.OriginalSize <= 0 {
		return 0
	}

	return float64(m.StoredSize) / float64(m.OriginalSize)
}

// marshalManifest serializes a manifest for storage inside the archive.
func marshalManifest(m *Manifest) ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

// unmarshalManifest parses manifest.json from an archive.
func unmarshalManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrManifestMissing, err)
	}

	return &m, nil
}

// digest computes the canonical SHA-256 of an archive's logical content:
// every entry's identity plus every stored block's identity and bytes. It
// is independent of tar framing, gzip settings, and JSON key order, so a
// verifier can recompute it from an extracted archive and compare it with
// the value recorded in the manifest.
func digest(entries []Entry, blocks map[string][]byte) (string, error) {
	h := sha256.New()

	for _, e := range entries {
		fmt.Fprintf(h, "F\x00%s\x00%d\x00%d\x00%s\n", e.Path, e.Mode, e.Size, e.Checksum)
		for _, b := range e.Blocks {
			fmt.Fprintf(h, "B\x00%s\n", b)
		}
	}

	names := make([]string, 0, len(blocks))
	for name := range blocks {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		data := blocks[name]
		fmt.Fprintf(h, "D\x00%s\x00%d\n", name, len(data))
		if _, err := h.Write(data); err != nil {
			return "", err
		}
	}

	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// blockIndex builds the sorted deduplication index for blocks.
func blockIndex(blocks map[string][]byte) []Block {
	names := make([]string, 0, len(blocks))
	for name := range blocks {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]Block, 0, len(names))
	for _, name := range names {
		out = append(out, Block{Hash: name, Size: int64(len(blocks[name]))})
	}

	return out
}

// hashBytes returns the lowercase hex SHA-256 of data.
func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}
