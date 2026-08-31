package hostpkg

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/webappsgo/cashp/src/security"
)

// FileSystem is a rooted view of the host filesystem. Production code roots
// it at "/" and the test suite roots it at t.TempDir(), so the suite can
// exercise the real write paths without ever touching /etc or needing root.
type FileSystem struct {
	// Root is the prefix every absolute path is resolved against.
	Root string
	// DirPerm is the mode new directories are created with.
	DirPerm os.FileMode
	// FilePerm is the mode new files are created with.
	FilePerm os.FileMode
}

// Default modes for repository definitions and keyrings, which must be
// world-readable for apt, dnf and rpm to use them.
const (
	defaultDirPerm  = os.FileMode(0o755)
	defaultFilePerm = os.FileMode(0o644)
)

// NewFileSystem returns a filesystem rooted at root; an empty root means "/".
func NewFileSystem(root string) *FileSystem {
	if root == "" {
		root = string(os.PathSeparator)
	}

	return &FileSystem{Root: root, DirPerm: defaultDirPerm, FilePerm: defaultFilePerm}
}

// Resolve maps an absolute host path into the rooted filesystem, refusing any
// path that would escape the root.
func (f *FileSystem) Resolve(path string) (string, error) {
	if !strings.HasPrefix(path, "/") {
		return "", failValidation(ErrPathEscape, "invalid destination path")
	}

	resolved, err := security.SafeJoin(f.Root, strings.TrimPrefix(path, "/"))
	if err != nil {
		return "", failValidation(ErrPathEscape, "invalid destination path")
	}

	return resolved, nil
}

// WriteFile writes data atomically: the content lands in a sibling temporary
// file that is renamed into place, so a crash never leaves a half-written
// repository definition or keyring behind.
func (f *FileSystem) WriteFile(path string, data []byte) error {
	resolved, err := f.Resolve(path)
	if err != nil {
		return err
	}

	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, f.dirPerm()); err != nil {
		return failUnavailable(ErrCommandFailed, "host configuration could not be written")
	}

	tmp, err := os.CreateTemp(dir, ".cashp-*")
	if err != nil {
		return failUnavailable(ErrCommandFailed, "host configuration could not be written")
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return failUnavailable(ErrCommandFailed, "host configuration could not be written")
	}
	if err := tmp.Chmod(f.filePerm()); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return failUnavailable(ErrCommandFailed, "host configuration could not be written")
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return failUnavailable(ErrCommandFailed, "host configuration could not be written")
	}
	if err := os.Rename(tmpName, resolved); err != nil {
		os.Remove(tmpName)
		return failUnavailable(ErrCommandFailed, "host configuration could not be written")
	}

	return nil
}

// ReadFile reads a file through the rooted filesystem.
func (f *FileSystem) ReadFile(path string) ([]byte, error) {
	resolved, err := f.Resolve(path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, failUnavailable(ErrCommandFailed, "host configuration could not be read")
	}

	return data, nil
}

// Exists reports whether a path exists inside the rooted filesystem.
func (f *FileSystem) Exists(path string) (bool, error) {
	resolved, err := f.Resolve(path)
	if err != nil {
		return false, err
	}

	if _, err := os.Stat(resolved); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, failUnavailable(ErrCommandFailed, "host configuration could not be read")
	}

	return true, nil
}

// EnsureLine appends line to a newline-separated file when it is not already
// present, and reports whether the file changed. It is the idempotent editor
// used for Alpine's repositories file.
func (f *FileSystem) EnsureLine(path, line string) (bool, error) {
	exists, err := f.Exists(path)
	if err != nil {
		return false, err
	}

	var current []byte
	if exists {
		current, err = f.ReadFile(path)
		if err != nil {
			return false, err
		}
	}

	for _, existing := range strings.Split(string(current), "\n") {
		if strings.TrimSpace(existing) == line {
			return false, nil
		}
	}

	updated := string(current)
	if updated != "" && !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += line + "\n"

	if err := f.WriteFile(path, []byte(updated)); err != nil {
		return false, err
	}

	return true, nil
}

// dirPerm returns the configured directory mode or the default.
func (f *FileSystem) dirPerm() os.FileMode {
	if f.DirPerm == 0 {
		return defaultDirPerm
	}

	return f.DirPerm
}

// filePerm returns the configured file mode or the default.
func (f *FileSystem) filePerm() os.FileMode {
	if f.FilePerm == 0 {
		return defaultFilePerm
	}

	return f.FilePerm
}
