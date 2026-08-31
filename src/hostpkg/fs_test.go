package hostpkg

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSystemWriteAndRead(t *testing.T) {
	root := t.TempDir()
	fsys := NewFileSystem(root)

	if err := fsys.WriteFile("/etc/yum.repos.d/docker-ce.repo", []byte("[docker-ce-stable]\n")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// The write must land under the root, never at the real host path.
	onDisk := filepath.Join(root, "etc", "yum.repos.d", "docker-ce.repo")
	info, err := os.Stat(onDisk)
	if err != nil {
		t.Fatalf("stat written file: %v", err)
	}
	if info.Mode().Perm() != defaultFilePerm {
		t.Errorf("mode = %v, want %v", info.Mode().Perm(), defaultFilePerm)
	}

	data, err := fsys.ReadFile("/etc/yum.repos.d/docker-ce.repo")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "[docker-ce-stable]\n" {
		t.Fatalf("ReadFile = %q", data)
	}

	// No temporary file may survive the atomic rename.
	entries, err := os.ReadDir(filepath.Dir(onDisk))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory holds %d entries, want only the renamed file", len(entries))
	}
}

func TestFileSystemExists(t *testing.T) {
	fsys := NewFileSystem(t.TempDir())

	exists, err := fsys.Exists("/etc/apk/repositories")
	if err != nil || exists {
		t.Fatalf("Exists on missing file = %v, %v", exists, err)
	}

	if err := fsys.WriteFile("/etc/apk/repositories", []byte("")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	exists, err = fsys.Exists("/etc/apk/repositories")
	if err != nil || !exists {
		t.Fatalf("Exists on present file = %v, %v", exists, err)
	}
}

func TestFileSystemResolveRejectsEscape(t *testing.T) {
	fsys := NewFileSystem(t.TempDir())

	cases := []string{
		"etc/passwd",
		"",
		"../etc/passwd",
		"/etc/../../etc/shadow",
		"/etc/apt/../../../../root/.ssh/authorized_keys",
	}

	for _, path := range cases {
		if _, err := fsys.Resolve(path); !errors.Is(err, ErrPathEscape) {
			t.Errorf("Resolve(%q) = %v, want ErrPathEscape", path, err)
		}
		if err := fsys.WriteFile(path, []byte("x")); !errors.Is(err, ErrPathEscape) {
			t.Errorf("WriteFile(%q) = %v, want ErrPathEscape", path, err)
		}
		if _, err := fsys.ReadFile(path); !errors.Is(err, ErrPathEscape) {
			t.Errorf("ReadFile(%q) = %v, want ErrPathEscape", path, err)
		}
		if _, err := fsys.Exists(path); !errors.Is(err, ErrPathEscape) {
			t.Errorf("Exists(%q) = %v, want ErrPathEscape", path, err)
		}
	}
}

func TestFileSystemReadMissingFile(t *testing.T) {
	fsys := NewFileSystem(t.TempDir())

	if _, err := fsys.ReadFile("/etc/absent"); !errors.Is(err, ErrCommandFailed) {
		t.Fatalf("error = %v, want ErrCommandFailed", err)
	}
}

func TestEnsureLineIsIdempotent(t *testing.T) {
	fsys := NewFileSystem(t.TempDir())
	const path = "/etc/apk/repositories"
	const line = "https://dl-cdn.alpinelinux.org/alpine/v3.21/community"

	if err := fsys.WriteFile(path, []byte("https://dl-cdn.alpinelinux.org/alpine/v3.21/main")); err != nil {
		t.Fatalf("seed repositories: %v", err)
	}

	changed, err := fsys.EnsureLine(path, line)
	if err != nil || !changed {
		t.Fatalf("first EnsureLine = %v, %v, want changed", changed, err)
	}

	body, err := fsys.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := "https://dl-cdn.alpinelinux.org/alpine/v3.21/main\n" + line + "\n"
	if string(body) != want {
		t.Fatalf("repositories = %q, want %q", body, want)
	}

	changed, err = fsys.EnsureLine(path, line)
	if err != nil || changed {
		t.Fatalf("second EnsureLine = %v, %v, want unchanged", changed, err)
	}

	after, err := fsys.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(after) != want {
		t.Fatalf("repositories changed on the second call: %q", after)
	}
}

func TestEnsureLineCreatesMissingFile(t *testing.T) {
	fsys := NewFileSystem(t.TempDir())

	changed, err := fsys.EnsureLine("/etc/apk/repositories", "https://example.invalid/alpine/v3.21/community")
	if err != nil || !changed {
		t.Fatalf("EnsureLine = %v, %v, want changed", changed, err)
	}

	exists, err := fsys.Exists("/etc/apk/repositories")
	if err != nil || !exists {
		t.Fatalf("Exists = %v, %v, want true", exists, err)
	}
}

func TestNewFileSystemDefaults(t *testing.T) {
	fsys := NewFileSystem("")
	if fsys.Root != string(os.PathSeparator) {
		t.Fatalf("Root = %q, want %q", fsys.Root, string(os.PathSeparator))
	}

	bare := &FileSystem{Root: t.TempDir()}
	if bare.dirPerm() != defaultDirPerm || bare.filePerm() != defaultFilePerm {
		t.Fatalf("zero modes = %v/%v, want defaults", bare.dirPerm(), bare.filePerm())
	}
}
