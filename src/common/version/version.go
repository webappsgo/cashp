// Package version holds the build information injected into the main
// package at link time (AI.md PART 7/8). The main package owns the ldflags
// variables and hands them here with Set so every binary and library shares
// one source of truth for version strings and the User-Agent header.
package version

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProjectName is the frozen internal identifier. It is used for the
// User-Agent header and internal identifiers and never changes, even when
// the executable on disk is renamed (AI.md PART 8 "Binary Rules").
const ProjectName = "cashp"

// ProjectOrg is the frozen internal organization identifier.
const ProjectOrg = "webappsgo"

// Placeholder values used until the main package calls Set.
const (
	unknownValue = "unknown"
	develVersion = "devel"
)

// Info carries the link-time build metadata.
type Info struct {
	// Version is the semantic version string (for example "1.4.0").
	Version string `json:"version"`
	// CommitID is the commit hash the binary was built from.
	CommitID string `json:"commit"`
	// BuildEpoch is the build time as a Unix epoch string.
	BuildEpoch string `json:"build_epoch"`
	// BuildDate is the human-readable build timestamp.
	BuildDate string `json:"build_date"`
}

var (
	mu      sync.RWMutex
	current = Info{
		Version:    develVersion,
		CommitID:   unknownValue,
		BuildEpoch: "0",
		BuildDate:  unknownValue,
	}
)

// Set records the build information supplied by the main package's ldflags
// variables. Empty arguments keep the existing placeholder value so a build
// without ldflags still prints something meaningful.
//
// buildDate is accepted for backward-compatible call signatures but is
// never sourced from an ldflag (AI.md PART 28: BUILD_DATE is Docker OCI
// label-only) — BuildDate is always derived from buildEpoch here instead,
// falling back to the explicit buildDate argument only when buildEpoch
// does not parse.
func Set(version, commitID, buildEpoch, buildDate string) {
	mu.Lock()
	defer mu.Unlock()
	if v := strings.TrimSpace(version); v != "" {
		current.Version = v
	}
	if v := strings.TrimSpace(commitID); v != "" {
		current.CommitID = v
	}
	if v := strings.TrimSpace(buildEpoch); v != "" {
		current.BuildEpoch = v
	}
	if epoch, err := strconv.ParseInt(strings.TrimSpace(current.BuildEpoch), 10, 64); err == nil && epoch > 0 {
		current.BuildDate = time.Unix(epoch, 0).UTC().Format(time.RFC3339)
	} else if v := strings.TrimSpace(buildDate); v != "" {
		current.BuildDate = v
	}
}

// Get returns a copy of the current build information.
func Get() Info {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Number returns just the version string.
func Number() string {
	return Get().Version
}

// Commit returns just the commit identifier.
func Commit() string {
	return Get().CommitID
}

// BuildTime parses BuildEpoch into a time value. The zero time is returned
// when the epoch was never injected or is not a number.
func BuildTime() time.Time {
	epoch, err := strconv.ParseInt(strings.TrimSpace(Get().BuildEpoch), 10, 64)
	if err != nil || epoch <= 0 {
		return time.Time{}
	}
	return time.Unix(epoch, 0).UTC()
}

// BinaryName returns the actual executable filename, which may differ from
// ProjectName because the binary is renamable. This is the name shown in
// --help, --version, and error messages — never the User-Agent.
func BinaryName() string {
	name := filepath.Base(os.Args[0])
	if name == "." || name == string(filepath.Separator) || name == "" {
		return ProjectName
	}
	return strings.TrimSuffix(name, ".exe")
}

// String returns the display line for --version. It uses the actual binary
// name because the user may have renamed the executable.
func String() string {
	info := Get()
	return fmt.Sprintf("%s %s (commit %s, built %s)", BinaryName(), info.Version, info.CommitID, info.BuildDate)
}

// UserAgent returns the outbound HTTP User-Agent. It always uses the
// hardcoded ProjectName so remote services see a stable identifier
// regardless of how the executable was renamed.
func UserAgent() string {
	return ProjectName + "/" + Number()
}

// UserAgentFor returns a User-Agent for a companion binary such as
// "cashp-cli" or "cashp-agent". The suffix is appended to the hardcoded
// project name; an empty suffix yields the plain project User-Agent.
func UserAgentFor(suffix string) string {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return UserAgent()
	}
	return ProjectName + "-" + suffix + "/" + Number()
}
