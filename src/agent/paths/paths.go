// Package paths resolves the system-scope directories the cashp agent uses.
// The agent is a system daemon: unlike the CLI it always resolves the
// machine-wide locations described in AI.md PART 33 "Execution Context" and
// refuses to load credentials that are readable by anyone but their owner.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/webappsgo/cashp/src/config"
)

// Permission constants for everything the agent writes.
const (
	// DirPerm is owner-only for every directory the agent creates.
	DirPerm = 0o700
	// FilePerm is owner-only for every file that may hold a credential.
	FilePerm = 0o600
)

// File names inside the agent's directories.
const (
	// ConfigFileName is the agent configuration document.
	ConfigFileName = "agent.yml"
	// TokenFileName holds the enrollment token when it is not in agent.yml.
	TokenFileName = "agent.token"
	// StateFileName records the registration result between restarts.
	StateFileName = "agent.state.json"
	// LogFileName is the agent log inside {log_dir}.
	LogFileName = "agent.log"
)

// ErrInsecurePerms is returned when a credential-bearing file is readable
// by the group or by other users.
var ErrInsecurePerms = errors.New("file permissions are too open")

// ErrNotRoot is returned by RequireRoot when the agent is not elevated.
var ErrNotRoot = errors.New("the agent must run as root or Administrator")

// Overrides carries the directory flags accepted on the command line. An
// empty field means "use the platform default".
type Overrides struct {
	Config string
	Data   string
	Log    string
}

// ConfigDir returns the directory holding agent.yml.
func ConfigDir(overrides Overrides) string {
	if overrides.Config != "" {
		return overrides.Config
	}
	return config.ConfigDir()
}

// DataDir returns the directory holding agent state.
func DataDir(overrides Overrides) string {
	if overrides.Data != "" {
		return overrides.Data
	}
	return config.DataDir()
}

// LogDir returns the directory holding agent.log.
func LogDir(overrides Overrides) string {
	if overrides.Log != "" {
		return overrides.Log
	}
	return config.LogDir()
}

// CacheDir returns the agent cache directory.
func CacheDir() string {
	return config.CacheDir()
}

// ConfigFile returns the full path to agent.yml. A --config value naming a
// file rather than a directory is honoured as-is so an operator can point
// the agent at an arbitrary document.
func ConfigFile(overrides Overrides) string {
	if overrides.Config != "" && looksLikeFile(overrides.Config) {
		return overrides.Config
	}
	return filepath.Join(ConfigDir(overrides), ConfigFileName)
}

// TokenFile returns the default enrollment token path.
func TokenFile(overrides Overrides) string {
	return filepath.Join(ConfigDir(overrides), TokenFileName)
}

// StateFile returns the path recording the registration result.
func StateFile(overrides Overrides) string {
	return filepath.Join(DataDir(overrides), StateFileName)
}

// LogFile returns the default agent log path.
func LogFile(overrides Overrides) string {
	return filepath.Join(LogDir(overrides), LogFileName)
}

// EnsureDirs creates every directory the agent writes to, owner-only.
func EnsureDirs(overrides Overrides) error {
	for _, dir := range []string{ConfigDir(overrides), DataDir(overrides), LogDir(overrides), CacheDir()} {
		if err := os.MkdirAll(dir, DirPerm); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		if err := setDirPermissions(dir); err != nil {
			return err
		}
	}
	return nil
}

// WriteSecureFile writes data owner-only, creating the parent directory
// first. It writes to a temporary file and renames so a crash cannot leave
// a half-written credential behind.
func WriteSecureFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, DirPerm); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	temp, err := os.CreateTemp(dir, ".agent-*")
	if err != nil {
		return fmt.Errorf("create temporary file in %s: %w", dir, err)
	}
	tempName := temp.Name()

	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempName)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := setFilePermissions(tempName); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("install %s: %w", path, err)
	}
	return nil
}

// CheckFilePerms refuses a credential-bearing file that other accounts can
// read. A missing file is not an error: the caller decides whether the
// absence matters.
func CheckFilePerms(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory, not a file", path)
	}
	if !permsAreSecure(info.Mode()) {
		return fmt.Errorf("%w: %s is %#o, expected %#o", ErrInsecurePerms, path, info.Mode().Perm(), FilePerm)
	}
	return nil
}

// IsRoot reports whether the process is running with administrative
// privileges.
func IsRoot() bool {
	if runtime.GOOS == "windows" {
		return hasWindowsAdmin()
	}
	return os.Geteuid() == 0
}

// RequireRoot returns ErrNotRoot unless the process is elevated. The agent
// needs system access for metrics, service management and task execution,
// so every privileged entry point calls this first.
func RequireRoot() error {
	if IsRoot() {
		return nil
	}
	return ErrNotRoot
}

// looksLikeFile reports whether a --config value points at a file rather
// than a directory.
func looksLikeFile(path string) bool {
	info, err := os.Stat(path)
	if err == nil {
		return !info.IsDir()
	}
	return filepath.Ext(path) != ""
}
