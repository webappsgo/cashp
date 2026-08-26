package config

import (
	"os"
	"path/filepath"
	"runtime"
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

// isRoot reports whether the current process is running as the root/
// Administrator identity, which selects the system-wide path set instead
// of the per-user one.
func isRoot() bool {
	return os.Geteuid() == 0
}

// ConfigDir returns the OS- and privilege-appropriate directory containing
// server.yml, per AI.md PART 4.
func ConfigDir() string {
	switch runtime.GOOS {
	case "darwin":
		if isRoot() {
			return filepath.Join("/Library/Application Support", InternalOrg, InternalName)
		}
		return filepath.Join(homeDir(), "Library/Application Support", InternalOrg, InternalName)
	case "windows":
		if isRoot() {
			return filepath.Join(os.Getenv("ProgramData"), InternalOrg, InternalName)
		}
		return filepath.Join(os.Getenv("AppData"), InternalOrg, InternalName)
	default:
		if isRoot() {
			return filepath.Join("/etc", InternalOrg, InternalName)
		}
		return filepath.Join(homeDir(), ".config", InternalOrg, InternalName)
	}
}

// DataDir returns the OS- and privilege-appropriate directory for
// persistent application data, per AI.md PART 4.
func DataDir() string {
	switch runtime.GOOS {
	case "darwin":
		if isRoot() {
			return filepath.Join("/Library/Application Support", InternalOrg, InternalName)
		}
		return filepath.Join(homeDir(), "Library/Application Support", InternalOrg, InternalName)
	case "windows":
		if isRoot() {
			return filepath.Join(os.Getenv("ProgramData"), InternalOrg, InternalName)
		}
		return filepath.Join(os.Getenv("LocalAppData"), InternalOrg, InternalName)
	default:
		if isRoot() {
			return filepath.Join("/var/lib", InternalOrg, InternalName)
		}
		return filepath.Join(homeDir(), ".local/share", InternalOrg, InternalName)
	}
}

// CacheDir returns the OS- and privilege-appropriate cache directory, per
// AI.md PART 4.
func CacheDir() string {
	switch runtime.GOOS {
	case "darwin":
		if isRoot() {
			return filepath.Join("/Library/Caches", InternalOrg, InternalName)
		}
		return filepath.Join(homeDir(), "Library/Caches", InternalOrg, InternalName)
	case "windows":
		if isRoot() {
			return filepath.Join(os.Getenv("ProgramData"), InternalOrg, InternalName, "cache")
		}
		return filepath.Join(os.Getenv("LocalAppData"), InternalOrg, InternalName, "cache")
	default:
		if isRoot() {
			return filepath.Join("/var/cache", InternalOrg, InternalName)
		}
		return filepath.Join(homeDir(), ".cache", InternalOrg, InternalName)
	}
}

// LogDir returns the OS- and privilege-appropriate log directory, per
// AI.md PART 4.
func LogDir() string {
	switch runtime.GOOS {
	case "darwin":
		if isRoot() {
			return "/Library/Logs/" + InternalOrg + "/" + InternalName
		}
		return filepath.Join(homeDir(), "Library/Logs", InternalOrg, InternalName)
	case "windows":
		return filepath.Join(DataDir(), "logs")
	default:
		if isRoot() {
			return filepath.Join("/var/log", InternalOrg, InternalName)
		}
		return filepath.Join(homeDir(), ".local/log", InternalOrg, InternalName)
	}
}

// ConfigFilePath returns the full path to server.yml.
func ConfigFilePath() string {
	return filepath.Join(ConfigDir(), DefaultConfigFileName)
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}
