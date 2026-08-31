package service

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/webappsgo/cashp/src/config"
)

// FallbackBinaryPath is used in generated service files when the running
// executable path cannot be resolved. It matches the install location the
// service templates in AI.md PART 25 document.
const FallbackBinaryPath = "/usr/local/bin/" + config.InternalName

// PlistName is the launchd label for the macOS daemon, in reverse-DNS form
// derived from the frozen internal org/name pair.
const PlistName = "com." + config.InternalOrg + "." + config.InternalName

// DocumentationURL is the Documentation= value of the generated systemd
// unit (AI.md PART 25 "systemd (Linux)").
const DocumentationURL = "https://" + config.InternalOrg + ".github.io/" + config.InternalName

// TemplateData carries every value substituted into a generated service
// file. It is passed by value so unit-file rendering stays a pure function
// that tests can exercise without touching the host.
type TemplateData struct {
	// Name is the frozen internal service name ({internal_name}).
	Name string
	// Org is the frozen internal org name ({internal_org}).
	Org string
	// DisplayName is the human-facing service name.
	DisplayName string
	// Description is the one-line service description.
	Description string
	// DocumentationURL is the project documentation link.
	DocumentationURL string
	// BinaryPath is the absolute path of the executable to run.
	BinaryPath string
	// ConfigDir, DataDir, CacheDir, LogDir, BackupDir and RunDir are the
	// resolved directories the service reads and writes.
	ConfigDir string
	DataDir   string
	CacheDir  string
	LogDir    string
	BackupDir string
	RunDir    string
	// PIDFile is the pid file path used by the init systems that need one.
	PIDFile string
	// PlistName is the launchd label (macOS only).
	PlistName string
	// UserMode reports whether this definition is a per-user service, which
	// runs unprivileged and cannot manage host services.
	UserMode bool
}

// DefaultTemplateData builds the template values for this host. userMode
// selects the per-user fallback service described in AI.md PART 24
// "Service Installation Logic".
func DefaultTemplateData(userMode bool) TemplateData {
	d := TemplateData{
		Name:             config.InternalName,
		Org:              config.InternalOrg,
		DisplayName:      config.InternalName,
		Description:      config.InternalName + " service",
		DocumentationURL: DocumentationURL,
		BinaryPath:       executablePath(),
		ConfigDir:        config.ConfigDir(),
		DataDir:          config.DataDir(),
		CacheDir:         config.CacheDir(),
		LogDir:           config.LogDir(),
		BackupDir:        config.BackupDir(),
		PlistName:        PlistName,
		UserMode:         userMode,
	}
	d.RunDir = runDir(userMode, d.DataDir)
	d.PIDFile = filepath.Join(d.RunDir, config.InternalName+".pid")
	return d
}

// StateDirs returns every directory an uninstall must remove, in a safe
// order (deepest state first, config last). AI.md PART 24 "Service
// Uninstall Logic" lists the backup directory among them.
func (d TemplateData) StateDirs() []string {
	dirs := []string{d.CacheDir, d.LogDir, d.DataDir}
	if d.BackupDir != "" {
		dirs = append(dirs, d.BackupDir)
	}
	return append(dirs, d.ConfigDir)
}

// executablePath resolves the absolute path of the running binary, falling
// back to the documented install path when the OS cannot report it.
func executablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return FallbackBinaryPath
	}
	resolved, err := filepath.Abs(exe)
	if err != nil {
		return exe
	}
	return resolved
}

// runDir returns the directory holding the pid file. System services use
// the shared /var/run/{internal_org} location documented in AI.md PART 25;
// user services keep their pid file inside their own data directory.
func runDir(userMode bool, dataDir string) string {
	if userMode || runtime.GOOS == "windows" {
		return dataDir
	}
	return filepath.Join("/var/run", config.InternalOrg)
}
