package backup

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// source is one file selected for inclusion, with the absolute path it is
// read from and the manifest path it is stored under.
type source struct {
	abs string
	rel string
}

// Create builds a verified backup archive and returns its path and
// manifest. An empty password means an unencrypted archive, which is
// refused outright when compliance mode is enabled. The plaintext archive
// is assembled in memory and, when a password is supplied, only the
// encrypted form is ever written to disk. The finished file is verified
// immediately; a backup that fails any check is deleted and no existing
// backup is touched.
func (s *Service) Create(ctx context.Context, password string) (archivePath string, result Manifest, err error) {
	defer func() {
		s.notifyCreate(ctx, archivePath, result, err)
	}()

	if err := ctx.Err(); err != nil {
		return "", Manifest{}, err
	}

	if s.opts.Compliance && password == "" {
		return "", Manifest{}, ErrEncryptionRequired
	}

	sources, err := s.collectSources()
	if err != nil {
		return "", Manifest{}, err
	}

	created := s.now().UTC()

	m := &Manifest{
		Version:    FormatVersion,
		CreatedAt:  created,
		CreatedBy:  s.opts.CreatedBy,
		AppVersion: s.opts.AppVersion,
		Encrypted:  password != "",
	}

	if m.Encrypted {
		m.EncryptionMethod = EncryptionMethod
		m.KeyDerivation = KeyDerivation
	}

	blocks := make(map[string][]byte)

	for _, src := range sources {
		if err := ctx.Err(); err != nil {
			return "", Manifest{}, err
		}

		entry, err := addFile(src, blocks)
		if err != nil {
			return "", Manifest{}, err
		}

		m.Contents = append(m.Contents, entry)
		m.OriginalSize += entry.Size
	}

	m.BlockIndex = blockIndex(blocks)
	for _, b := range m.BlockIndex {
		m.StoredSize += b.Size
	}

	sum, err := digest(m.Contents, blocks)
	if err != nil {
		return "", Manifest{}, err
	}
	m.Checksum = sum

	body, err := buildArchive(m, blocks)
	if err != nil {
		return "", Manifest{}, err
	}

	if m.Encrypted {
		if body, err = seal(body, password); err != nil {
			return "", Manifest{}, err
		}
	}

	target := filepath.Join(s.opts.BackupDir, archiveName(created, m.Encrypted))
	if err := writeFileAtomic(target, body, 0o600); err != nil {
		return "", Manifest{}, err
	}

	verified, err := s.Verify(ctx, target, password)
	if err != nil {
		os.Remove(target)

		return "", Manifest{}, fmt.Errorf("backup verification failed: %w", err)
	}

	return target, verified, nil
}

// addFile chunks one source file into the shared block map and returns its
// manifest entry. Blocks already present are referenced, not stored again.
func addFile(src source, blocks map[string][]byte) (Entry, error) {
	info, err := os.Stat(src.abs)
	if err != nil {
		return Entry{}, err
	}

	data, err := os.ReadFile(src.abs)
	if err != nil {
		return Entry{}, err
	}

	entry := Entry{
		Path:     src.rel,
		Mode:     uint32(info.Mode().Perm()),
		Size:     int64(len(data)),
		ModTime:  info.ModTime().UTC(),
		Checksum: hashBytes(data),
	}

	for _, block := range splitBlocks(data) {
		name := hashBytes(block)
		if _, ok := blocks[name]; !ok {
			stored := make([]byte, len(block))
			copy(stored, block)
			blocks[name] = stored
		}
		entry.Blocks = append(entry.Blocks, name)
	}

	return entry, nil
}

// collectSources resolves every file that belongs in the archive: the
// always-included server.yml, server.db, and users.db, plus the optional
// template, theme, ssl, and data trees described in AI.md PART 22.
func (s *Service) collectSources() ([]source, error) {
	var sources []source
	seen := make(map[string]bool)

	add := func(abs, rel string) {
		if seen[rel] {
			return
		}
		seen[rel] = true
		sources = append(sources, source{abs: abs, rel: rel})
	}

	required := []source{
		{abs: filepath.Join(s.opts.ConfigDir, "server.yml"), rel: configRoot + "/server.yml"},
		{abs: filepath.Join(s.opts.DataDir, "server.db"), rel: dataRoot + "/server.db"},
		{abs: filepath.Join(s.opts.DataDir, "users.db"), rel: dataRoot + "/users.db"},
	}

	for _, req := range required {
		if _, err := os.Stat(req.abs); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrMissingRequiredFile, req.rel)
		}
		add(req.abs, req.rel)
	}

	optional := []string{"template", "theme"}
	if s.opts.IncludeSSL {
		optional = append(optional, "ssl")
	}

	for _, dir := range optional {
		found, err := walkTree(filepath.Join(s.opts.ConfigDir, dir), configRoot+"/"+dir)
		if err != nil {
			return nil, err
		}
		for _, f := range found {
			add(f.abs, f.rel)
		}
	}

	if s.opts.IncludeData {
		found, err := walkTree(s.opts.DataDir, dataRoot)
		if err != nil {
			return nil, err
		}
		for _, f := range found {
			add(f.abs, f.rel)
		}
	}

	sort.Slice(sources, func(i, j int) bool { return sources[i].rel < sources[j].rel })

	return sources, nil
}

// walkTree lists every regular file under root, mapping it to prefix in the
// manifest namespace. A missing root is not an error: optional trees are
// simply skipped. Symlinks and special files are never archived.
func walkTree(root, prefix string) ([]source, error) {
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}

	var out []source

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}

		out = append(out, source{abs: p, rel: path.Join(prefix, filepath.ToSlash(rel))})

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// writeFileAtomic writes data to a temporary file in the destination
// directory and renames it into place, so a partial write can never be
// mistaken for a complete backup.
func writeFileAtomic(target string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return err
	}

	name := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)

		return err
	}

	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)

		return err
	}

	if err := tmp.Close(); err != nil {
		os.Remove(name)

		return err
	}

	if err := os.Chmod(name, mode); err != nil {
		os.Remove(name)

		return err
	}

	if err := os.Rename(name, target); err != nil {
		os.Remove(name)

		return err
	}

	return nil
}

// safeJoin resolves a manifest path under root, refusing any path that
// escapes it via traversal, absolute paths, or an unknown namespace.
func safeJoin(root, rel string) (string, error) {
	clean := path.Clean("/" + rel)
	if clean == "/" || strings.Contains(rel, "..") {
		return "", fmt.Errorf("%w: %s", ErrUnsafePath, rel)
	}

	target := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))

	rooted := filepath.Clean(root) + string(os.PathSeparator)
	if !strings.HasPrefix(target, rooted) {
		return "", fmt.Errorf("%w: %s", ErrUnsafePath, rel)
	}

	return target, nil
}
