package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Build carries the build-time identity injected into package main by the
// linker. It is passed in through options so this package never imports
// main and never reads build metadata from a global.
type Build struct {
	// Version is the release version: a SemVer MAJOR.MINOR.PATCH with no "v"
	// prefix, a "YYYYMMDDHHMMSS-beta" pre-release stamp, or a short commit
	// hash for daily builds.
	Version string
	// CommitID is the short commit hash the binary was built from.
	CommitID string
	// BuildEpoch is the build time as a Unix epoch string.
	BuildEpoch string
}

// DevVersion is reported when no release version was injected at build time.
const DevVersion = "dev"

// semverPattern matches a release version: no "v" prefix, three numeric
// components, an optional pre-release such as "-rc1", and optional build
// metadata.
var semverPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// betaPattern matches a beta version stamp: YYYYMMDDHHMMSS-beta.
var betaPattern = regexp.MustCompile(`^[0-9]{14}-beta$`)

// dailyPattern matches a daily build version, which is a short commit hash.
var dailyPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// VersionKind classifies a version string.
type VersionKind string

const (
	// KindStable is a SemVer release.
	KindStable VersionKind = "stable"
	// KindBeta is a timestamped beta build.
	KindBeta VersionKind = "beta"
	// KindDaily is a commit-hash daily build.
	KindDaily VersionKind = "daily"
	// KindDev is an uninjected development build.
	KindDev VersionKind = "dev"
)

// ClassifyVersion reports which release channel a version string belongs to.
func ClassifyVersion(version string) VersionKind {
	switch {
	case betaPattern.MatchString(version):
		return KindBeta
	case semverPattern.MatchString(version):
		return KindStable
	case dailyPattern.MatchString(version):
		return KindDaily
	default:
		return KindDev
	}
}

// Normalize fills in safe defaults for a build that was compiled without
// linker-injected values, and strips a stray "v" prefix so the reported
// version always follows the no-prefix rule.
func (b Build) Normalize() Build {
	b.Version = strings.TrimSpace(b.Version)
	if len(b.Version) > 1 && b.Version[0] == 'v' && semverPattern.MatchString(b.Version[1:]) {
		b.Version = b.Version[1:]
	}
	if b.Version == "" {
		b.Version = DevVersion
	}
	if b.CommitID == "" {
		b.CommitID = "unknown"
	}
	return b
}

// BuildTime parses the injected epoch into a time value. The zero time is
// returned when no usable epoch was injected.
func (b Build) BuildTime() time.Time {
	epoch, err := strconv.ParseInt(strings.TrimSpace(b.BuildEpoch), 10, 64)
	if err != nil || epoch <= 0 {
		return time.Time{}
	}
	return time.Unix(epoch, 0).UTC()
}

// DateString renders the build time as an RFC 3339 timestamp, or "unknown"
// when no build epoch was injected.
func (b Build) DateString() string {
	t := b.BuildTime()
	if t.IsZero() {
		return "unknown"
	}
	return t.Format(time.RFC3339)
}

// VersionResponse is the payload of the version endpoint. Like health it is
// a bare object so scripts can read it without unwrapping an envelope.
type VersionResponse struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	Channel    string `json:"channel"`
	Commit     string `json:"commit"`
	BuildEpoch string `json:"build_epoch"`
	BuildDate  string `json:"build_date"`
	GoVersion  string `json:"go_version"`
}

// VersionHandler serves the version endpoint. A single instance is mounted
// at each of its aliases so no alias is ever a redirect.
type VersionHandler struct {
	name  string
	build Build
	// goVersion is captured once so every response is identical.
	goVersion string
}

// NewVersionHandler builds the version handler for a project name and build.
func NewVersionHandler(name string, build Build, goVersion string) *VersionHandler {
	return &VersionHandler{name: name, build: build.Normalize(), goVersion: goVersion}
}

// Response returns the version payload.
func (h *VersionHandler) Response() VersionResponse {
	return VersionResponse{
		Name:       h.name,
		Version:    h.build.Version,
		Channel:    string(ClassifyVersion(h.build.Version)),
		Commit:     h.build.CommitID,
		BuildEpoch: h.build.BuildEpoch,
		BuildDate:  h.build.DateString(),
		GoVersion:  h.goVersion,
	}
}

// ServeHTTP renders the version payload in the negotiated format.
func (h *VersionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resp := h.Response()
	w.Header().Set("Cache-Control", "no-store")
	Write(w, r, http.StatusOK, Body{
		JSON:  resp,
		Text:  resp.RenderText(),
		HTML:  resp.RenderHTML(),
		Title: resp.Name + " - Version",
	})
}

// RenderText renders the version payload as dot-notation plain text.
func (v VersionResponse) RenderText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "name: %s\n", v.Name)
	fmt.Fprintf(&b, "version: %s\n", v.Version)
	fmt.Fprintf(&b, "channel: %s\n", v.Channel)
	fmt.Fprintf(&b, "commit: %s\n", v.Commit)
	fmt.Fprintf(&b, "build_epoch: %s\n", v.BuildEpoch)
	fmt.Fprintf(&b, "build_date: %s\n", v.BuildDate)
	fmt.Fprintf(&b, "go_version: %s\n", v.GoVersion)
	return b.String()
}
