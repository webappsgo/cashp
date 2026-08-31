package overlay

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
)

// Overlay files are private to the user the server runs as: 0700 for
// directories, 0600 for generated config and key material.
const (
	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600
)

// enforceDirPerms applies 0700 and the current ownership to dir, even when
// the directory already existed with looser permissions.
func enforceDirPerms(dir string) error {
	if err := os.Chmod(dir, dirPerm); err != nil {
		return fmt.Errorf("chmod overlay dir %s: %w", dir, err)
	}
	enforceOwnership(dir)
	return nil
}

// writeSecretFile creates the parent directory and (over)writes path with
// 0600 permissions. It is used for derived state such as torrc and
// tunnels.conf as well as for persisted key material.
func writeSecretFile(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create parent dir %s: %w", dir, err)
	}
	if err := os.WriteFile(path, content, filePerm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(path, filePerm); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	enforceOwnership(path)
	return nil
}

// enforceOwnership best-effort chowns path to the user the server runs as.
// Windows has no chown and inherits ACLs from the user profile instead; a
// failure elsewhere is a warning, never a startup failure.
func enforceOwnership(path string) {
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Chown(path, os.Getuid(), os.Getgid()); err != nil {
		log.Printf("WARN: could not set ownership on %s: %v", path, err)
	}
}
