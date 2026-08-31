package config

import (
	"path/filepath"

	"github.com/webappsgo/cashp/src/common/paths"
)

// InternalOrg and InternalName are frozen at first-time setup per IDEA.md
// "Project variables" and must never change afterward — every {config_dir}
// / {data_dir} / {log_dir} / {cache_dir} / systemd unit path is derived
// from them.
const (
	InternalOrg  = "webappsgo"
	InternalName = "cashp"
)

// DefaultConfigFileName is the only accepted config file name. A legacy
// server.yaml is auto-migrated to this name on startup (AI.md PART 5).
const DefaultConfigFileName = "server.yml"

// DefaultPortRangeMin and DefaultPortRangeMax bound the random port chosen
// on first run when no port is configured (AI.md PART 5 "Port Rules").
const (
	DefaultPortRangeMin = 64000
	DefaultPortRangeMax = 64999
)

// isRoot reports whether the process started with root or Administrator
// privileges, which selects the system-wide path set instead of the
// per-user one. The decision is locked at process start by paths.
func isRoot() bool {
	return paths.IsSystemMode()
}

// ConfigDir returns the OS- and privilege-appropriate directory containing
// server.yml, per AI.md PART 4. Path resolution lives in one place —
// src/common/paths — so the server, client and agent binaries agree.
func ConfigDir() string {
	return paths.ConfigDir("")
}

// DataDir returns the OS- and privilege-appropriate directory for
// persistent application data, per AI.md PART 4.
func DataDir() string {
	return paths.DataDir("")
}

// CacheDir returns the OS- and privilege-appropriate cache directory, per
// AI.md PART 4.
func CacheDir() string {
	return paths.CacheDir("")
}

// LogDir returns the OS- and privilege-appropriate log directory, per
// AI.md PART 4.
func LogDir() string {
	return paths.LogDir("")
}

// BackupDir returns the backup destination, preferring the system backup
// location when it is writable and falling back mode-aware, per AI.md
// PART 8 "Directory Flags".
func BackupDir() string {
	return paths.BackupDir("", DataDir())
}

// ConfigFilePath returns the full path to server.yml.
func ConfigFilePath() string {
	return filepath.Join(ConfigDir(), DefaultConfigFileName)
}
