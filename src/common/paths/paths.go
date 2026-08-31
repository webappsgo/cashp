// Package paths resolves every directory and file location the binaries
// use, following the AI.md PART 8 "Directory Flags" table: CLI flag first,
// then the environment variable, then the OS- and privilege-appropriate
// default.
//
// The system-versus-user decision is locked once at process start from the
// effective UID, before any privilege drop, and never re-evaluated. Service
// accounts have HOME pointing at {data_dir}, so a late $HOME lookup would
// nest user-style directories inside /var/lib/webappsgo/cashp.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

// Org and Name are the frozen internal identifiers every path is derived
// from. They mirror config.InternalOrg and config.InternalName, which are
// frozen at first-time setup and never change; they are duplicated here so
// this package stays a dependency-free leaf shared by all three binaries.
const (
	Org  = "webappsgo"
	Name = "cashp"
)

// startedElevated is captured ONCE at process start, BEFORE any privilege
// drop, and never re-evaluated.
var startedElevated = isElevated()

// startHome is the home directory as it was at process start, captured for
// the same reason as startedElevated.
var startHome = resolveHome()

// IsSystemMode reports whether the process started with root or
// Administrator privileges, which selects the system-wide path set.
func IsSystemMode() bool {
	return startedElevated
}

// DirPerm returns the mode new directories are created with: 0755 in
// system mode, 0700 in user mode.
func DirPerm() os.FileMode {
	if startedElevated {
		return 0755
	}
	return 0700
}

// FilePerm returns the mode new files are created with: 0644 in system
// mode, 0600 in user mode.
func FilePerm() os.FileMode {
	if startedElevated {
		return 0644
	}
	return 0600
}

// PIDPerm returns the mode the PID file is created with, which follows the
// same system/user split as FilePerm.
func PIDPerm() os.FileMode {
	return FilePerm()
}

// ConfigDir returns the configuration directory.
func ConfigDir(flagValue string) string {
	return resolve(flagValue, "CONFIG_DIR", defaultConfigDir)
}

// DataDir returns the persistent data directory.
func DataDir(flagValue string) string {
	return resolve(flagValue, "DATA_DIR", defaultDataDir)
}

// CacheDir returns the cache directory.
func CacheDir(flagValue string) string {
	return resolve(flagValue, "CACHE_DIR", defaultCacheDir)
}

// LogDir returns the log directory.
func LogDir(flagValue string) string {
	return resolve(flagValue, "LOG_DIR", defaultLogDir)
}

// PIDFile returns the PID file path. Containers never use a PID file, but
// the path is still resolved so callers can report it.
func PIDFile(flagValue string) string {
	return resolve(flagValue, "PID_FILE", defaultPIDFile)
}

// DatabaseDir returns the SQLite database directory. Containers keep the
// database on its own volume at /data/db/sqlite; native installs put it
// under the data directory.
func DatabaseDir(dataDir string) string {
	if envValue := os.Getenv("DATABASE_DIR"); envValue != "" {
		return envValue
	}
	if inContainer() {
		return "/data/db/sqlite"
	}
	return filepath.Join(dataDir, "db")
}

// BackupDir returns the backup directory. The system backup location wins
// when it is writable. In system mode the fallback stays inside the data
// directory and is NEVER derived from $HOME, because a service account's
// HOME points at the data directory itself.
func BackupDir(flagValue, dataDir string) string {
	if flagValue != "" {
		return flagValue
	}
	if envValue := os.Getenv("BACKUP_DIR"); envValue != "" {
		return envValue
	}
	sysBackup := SystemBackupDir()
	if IsWritable(sysBackup) {
		return sysBackup
	}
	if startedElevated {
		return filepath.Join(dataDir, "backup")
	}
	return UserBackupDir()
}

// SystemBackupDir returns the system-level backup directory for this OS.
func SystemBackupDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join("/Library/Backups", Org, Name)
	case "windows":
		return filepath.Join(os.Getenv("ProgramData"), "Backups", Org, Name)
	case "freebsd", "openbsd", "netbsd":
		return filepath.Join("/var/backups", Org, Name)
	default:
		return filepath.Join("/mnt/Backups", Org, Name)
	}
}

// UserBackupDir returns the user-level backup directory. It is only valid
// in user mode; system mode must never fall back to a $HOME-derived path.
func UserBackupDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(startHome, "Library/Backups", Org, Name)
	case "windows":
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "Backups", Org, Name)
	default:
		return filepath.Join(startHome, ".local/share/Backups", Org, Name)
	}
}

// EnsureDir creates a directory (and its parents) with the privilege-
// appropriate permissions and verifies that it is writable.
func EnsureDir(path string, isRoot bool) error {
	perm := os.FileMode(0700)
	filePerm := os.FileMode(0600)
	if isRoot {
		perm = 0755
		filePerm = 0644
	}

	if err := os.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}

	testFile := filepath.Join(path, ".write-test")
	if err := os.WriteFile(testFile, []byte{}, filePerm); err != nil {
		return fmt.Errorf("directory %s is not writable: %w", path, err)
	}
	os.Remove(testFile)

	return nil
}

// EnsurePIDFile creates the directory holding the PID file and validates
// that it is writable. It is a no-op in containers, which never write a
// PID file at all.
func EnsurePIDFile(path string, isRoot bool) error {
	if inContainer() {
		return nil
	}
	return EnsureDir(filepath.Dir(path), isRoot)
}

// EnsureAll creates every directory in the list with the current
// privilege-appropriate permissions, stopping at the first failure.
func EnsureAll(dirs ...string) error {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if err := EnsureDir(dir, startedElevated); err != nil {
			return err
		}
	}
	return nil
}

// IsWritable reports whether a directory can be written to, or created and
// then written to, by testing its parent.
func IsWritable(path string) bool {
	if path == "" {
		return false
	}
	target := path
	if _, err := os.Stat(target); err != nil {
		target = filepath.Dir(path)
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		return false
	}
	testFile := filepath.Join(target, ".write_test_"+strconv.FormatInt(time.Now().UnixNano(), 36))
	f, err := os.Create(testFile)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(testFile)
	return true
}

// resolve applies the flag > environment variable > default precedence.
func resolve(flagValue, envKey string, fallback func() string) string {
	if flagValue != "" {
		return flagValue
	}
	if envValue := os.Getenv(envKey); envValue != "" {
		return envValue
	}
	return fallback()
}

// defaultConfigDir returns the OS- and privilege-appropriate config dir.
func defaultConfigDir() string {
	switch runtime.GOOS {
	case "darwin":
		if startedElevated {
			return filepath.Join("/Library/Application Support", Org, Name)
		}
		return filepath.Join(startHome, "Library/Application Support", Org, Name)
	case "windows":
		if startedElevated {
			return filepath.Join(os.Getenv("ProgramData"), Org, Name)
		}
		return filepath.Join(os.Getenv("AppData"), Org, Name)
	default:
		if startedElevated {
			return filepath.Join("/etc", Org, Name)
		}
		return filepath.Join(startHome, ".config", Org, Name)
	}
}

// defaultDataDir returns the OS- and privilege-appropriate data dir.
func defaultDataDir() string {
	switch runtime.GOOS {
	case "darwin":
		if startedElevated {
			return filepath.Join("/Library/Application Support", Org, Name)
		}
		return filepath.Join(startHome, "Library/Application Support", Org, Name)
	case "windows":
		if startedElevated {
			return filepath.Join(os.Getenv("ProgramData"), Org, Name)
		}
		return filepath.Join(os.Getenv("LocalAppData"), Org, Name)
	default:
		if startedElevated {
			return filepath.Join("/var/lib", Org, Name)
		}
		return filepath.Join(startHome, ".local/share", Org, Name)
	}
}

// defaultCacheDir returns the OS- and privilege-appropriate cache dir.
func defaultCacheDir() string {
	switch runtime.GOOS {
	case "darwin":
		if startedElevated {
			return filepath.Join("/Library/Caches", Org, Name)
		}
		return filepath.Join(startHome, "Library/Caches", Org, Name)
	case "windows":
		if startedElevated {
			return filepath.Join(os.Getenv("ProgramData"), Org, Name, "cache")
		}
		return filepath.Join(os.Getenv("LocalAppData"), Org, Name, "cache")
	default:
		if startedElevated {
			return filepath.Join("/var/cache", Org, Name)
		}
		return filepath.Join(startHome, ".cache", Org, Name)
	}
}

// defaultLogDir returns the OS- and privilege-appropriate log dir.
func defaultLogDir() string {
	switch runtime.GOOS {
	case "darwin":
		if startedElevated {
			return filepath.Join("/Library/Logs", Org, Name)
		}
		return filepath.Join(startHome, "Library/Logs", Org, Name)
	case "windows":
		return filepath.Join(defaultDataDir(), "logs")
	default:
		if startedElevated {
			return filepath.Join("/var/log", Org, Name)
		}
		return filepath.Join(startHome, ".local/log", Org, Name)
	}
}

// defaultPIDFile returns the OS- and privilege-appropriate PID file path.
func defaultPIDFile() string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(defaultDataDir(), Name+".pid")
	default:
		if startedElevated {
			return filepath.Join("/var/run", Org, Name+".pid")
		}
		return filepath.Join(defaultDataDir(), Name+".pid")
	}
}

// resolveHome returns the home directory at process start. It falls back
// to the current directory so path building never produces an empty root.
func resolveHome() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	return home
}
