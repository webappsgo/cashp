// Package paths resolves the user-scope directories used by the cashp-cli
// binary. CLI runtime state is ALWAYS user-scope, even when the invoking
// user is root/Administrator (AI.md PART 33 "Configuration").
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/webappsgo/cashp/src/config"
)

// DirPerm is the permission applied to every CLI directory (user-only).
const DirPerm os.FileMode = 0o700

// FilePerm is the permission applied to every CLI file that may hold a
// bearer credential (cli.yml, token, cli.log).
const FilePerm os.FileMode = 0o600

// ConfigFileName is the default CLI config file name.
const ConfigFileName = "cli.yml"

// TokenFileName is the standalone token file name.
const TokenFileName = "token"

// LogFileName is the CLI log file name.
const LogFileName = "cli.log"

// ErrInsecurePerms is returned when a credential-bearing file is readable
// by group or other and must not be used until the user tightens it.
var ErrInsecurePerms = errors.New("insecure file permissions")

// ConfigDir returns the CLI config directory.
func ConfigDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("APPDATA"), config.InternalOrg, config.InternalName)
	}
	return filepath.Join(homeDir(), ".config", config.InternalOrg, config.InternalName)
}

// DataDir returns the CLI persistent data directory.
func DataDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), config.InternalOrg, config.InternalName, "data")
	}
	return filepath.Join(homeDir(), ".local", "share", config.InternalOrg, config.InternalName)
}

// CacheDir returns the CLI cache directory.
func CacheDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), config.InternalOrg, config.InternalName, "cache")
	}
	return filepath.Join(homeDir(), ".cache", config.InternalOrg, config.InternalName)
}

// LogDir returns the CLI log directory.
func LogDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), config.InternalOrg, config.InternalName, "log")
	}
	return filepath.Join(homeDir(), ".local", "log", config.InternalOrg, config.InternalName)
}

// ConfigFile returns the default CLI config file path.
func ConfigFile() string {
	return filepath.Join(ConfigDir(), ConfigFileName)
}

// TokenFile returns the standalone token file path.
func TokenFile() string {
	return filepath.Join(ConfigDir(), TokenFileName)
}

// LogFile returns the CLI log file path.
func LogFile() string {
	return filepath.Join(LogDir(), LogFileName)
}

// DraftDir returns the directory used to preserve unsaved TUI drafts when a
// session is interrupted by a revoked token (AI.md PART 33).
func DraftDir() string {
	return filepath.Join(ConfigDir(), "draft")
}

// EnsureDirs creates every CLI directory with user-only permissions. It is
// called on every startup before any file operation.
func EnsureDirs() error {
	for _, dir := range []string{ConfigDir(), DataDir(), CacheDir(), LogDir()} {
		if err := os.MkdirAll(dir, DirPerm); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
		if err := setDirPermissions(dir); err != nil {
			return fmt.Errorf("set permissions %s: %w", dir, err)
		}
	}
	return nil
}

// WriteSecureFile creates parent directories and writes data with owner-only
// permissions, re-applying the mode afterwards as defence in depth.
func WriteSecureFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), DirPerm); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}
	if err := os.WriteFile(path, data, FilePerm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return setFilePermissions(path)
}

// CheckFilePerms reports whether a credential-bearing file is safe to read.
// A missing file is not an error: callers treat it as "no credential".
func CheckFilePerms(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !permsAreSecure(info.Mode().Perm()) {
		return fmt.Errorf("%w: %s is accessible to group or other (want %04o)", ErrInsecurePerms, path, uint32(FilePerm))
	}
	return nil
}

// ResolveConfigPath resolves the --config flag value to an absolute config
// file path following the AI.md PART 33 "--config Flag" resolution rules.
func ResolveConfigPath(configFlag string) string {
	if configFlag == "" {
		return ConfigFile()
	}

	expanded := configFlag
	if strings.HasPrefix(expanded, "~/") {
		expanded = filepath.Join(homeDir(), expanded[2:])
	}

	if filepath.IsAbs(expanded) {
		return resolveYamlExtension(expanded)
	}

	return resolveYamlExtension(filepath.Join(ConfigDir(), expanded))
}

// resolveYamlExtension appends .yml or .yaml when the caller omitted an
// extension, preferring an existing file and defaulting to .yml.
func resolveYamlExtension(path string) string {
	switch ext := filepath.Ext(path); ext {
	case ".yml", ".yaml":
		return path
	case "":
		if fileExists(path + ".yml") {
			return path + ".yml"
		}
		if fileExists(path + ".yaml") {
			return path + ".yaml"
		}
		return path + ".yml"
	default:
		return path
	}
}

// fileExists reports whether path exists and is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// homeDir returns the invoking user's home directory, falling back to the
// working directory when the platform cannot report one.
func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}
