package hostpkg

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// Distribution support follows IDEA.md -> "Supported operating systems":
// Debian 11+, Ubuntu 22.04+, Alpine 3.20+, RHEL family 8+, Fedora 40+ and
// rolling Arch. Anything else — including a non-Linux host — is a hard,
// typed error rather than a degraded fallback.

// Family is the packaging family a distribution belongs to. Debian and
// Ubuntu are separate families even though they share apt, because their
// third-party PHP repositories differ (Sury vs the ondrej PPA).
type Family string

// The supported families.
const (
	FamilyDebian Family = "debian"
	FamilyUbuntu Family = "ubuntu"
	FamilyAlpine Family = "alpine"
	FamilyRHEL   Family = "rhel"
	FamilyFedora Family = "fedora"
	FamilyArch   Family = "arch"
)

// ManagerKind identifies the package manager a family uses.
type ManagerKind string

// The supported package managers.
const (
	ManagerAPT    ManagerKind = "apt"
	ManagerAPK    ManagerKind = "apk"
	ManagerDNF    ManagerKind = "dnf"
	ManagerPacman ManagerKind = "pacman"
)

// RollingVersion is the Version value used for rolling releases (Arch),
// which have no version floor to track.
const RollingVersion = "rolling"

// osReleasePaths are the standard locations of the os-release file, in the
// order systemd documents them.
var osReleasePaths = []string{"/etc/os-release", "/usr/lib/os-release"}

// Distro is a resolved, supported host distribution.
type Distro struct {
	// ID is the raw os-release ID, e.g. "debian", "rocky", "linuxmint".
	ID string
	// Like holds the raw ID_LIKE tokens in order.
	Like []string
	// Family is the resolved packaging family.
	Family Family
	// Manager is the package manager for the family.
	Manager ManagerKind
	// Version is the upstream-normalized release, e.g. "12", "22.04",
	// "3.20", "9", "43", or RollingVersion for Arch.
	Version string
	// VersionID is the raw os-release VERSION_ID.
	VersionID string
	// Codename is the apt suite name, e.g. "bookworm" or "jammy". It is
	// empty on families that have no codename.
	Codename string
	// Major is the leading numeric component of Version.
	Major int
	// Minor is the second numeric component of Version.
	Minor int
}

// familyByID maps a known os-release ID directly to its family.
var familyByID = map[string]Family{
	"debian":       FamilyDebian,
	"raspbian":     FamilyDebian,
	"devuan":       FamilyDebian,
	"ubuntu":       FamilyUbuntu,
	"linuxmint":    FamilyUbuntu,
	"pop":          FamilyUbuntu,
	"elementary":   FamilyUbuntu,
	"zorin":        FamilyUbuntu,
	"neon":         FamilyUbuntu,
	"alpine":       FamilyAlpine,
	"postmarketos": FamilyAlpine,
	"rhel":         FamilyRHEL,
	"centos":       FamilyRHEL,
	"rocky":        FamilyRHEL,
	"almalinux":    FamilyRHEL,
	"ol":           FamilyRHEL,
	"oracle":       FamilyRHEL,
	"scientific":   FamilyRHEL,
	"circle":       FamilyRHEL,
	"virtuozzo":    FamilyRHEL,
	"fedora":       FamilyFedora,
	"arch":         FamilyArch,
	"archarm":      FamilyArch,
	"artix":        FamilyArch,
	"manjaro":      FamilyArch,
	"manjaro-arm":  FamilyArch,
	"endeavouros":  FamilyArch,
	"garuda":       FamilyArch,
	"cachyos":      FamilyArch,
}

// likePriority is the order in which ID_LIKE tokens are consulted. Ubuntu
// wins over Debian and RHEL wins over Fedora, because derivatives list both
// (Mint: "ubuntu debian"; Rocky: "rhel centos fedora") and the more specific
// token is the correct family.
var likePriority = []string{"ubuntu", "debian", "rhel", "centos", "fedora", "arch", "alpine"}

// managerByFamily maps a family to its package manager.
var managerByFamily = map[Family]ManagerKind{
	FamilyDebian: ManagerAPT,
	FamilyUbuntu: ManagerAPT,
	FamilyAlpine: ManagerAPK,
	FamilyRHEL:   ManagerDNF,
	FamilyFedora: ManagerDNF,
	FamilyArch:   ManagerPacman,
}

// minimumVersion is the documented floor per family. Arch is absent because
// it is a rolling release.
var minimumVersion = map[Family]string{
	FamilyDebian: "11",
	FamilyUbuntu: "22.04",
	FamilyAlpine: "3.20",
	FamilyRHEL:   "8",
	FamilyFedora: "40",
}

// debianCodenames maps Debian major releases to their apt suite names, used
// both for version recovery on derivatives and for repository rendering.
var debianCodenames = map[string]string{
	"9":  "stretch",
	"10": "buster",
	"11": "bullseye",
	"12": "bookworm",
	"13": "trixie",
	"14": "forky",
}

// ubuntuCodenames maps Ubuntu releases to their apt suite names.
var ubuntuCodenames = map[string]string{
	"20.04": "focal",
	"22.04": "jammy",
	"22.10": "kinetic",
	"23.04": "lunar",
	"23.10": "mantic",
	"24.04": "noble",
	"24.10": "oracular",
	"25.04": "plucky",
	"25.10": "questing",
	"26.04": "resolute",
}

// Detect resolves the running host distribution. It refuses to run anywhere
// other than Linux and returns a typed error for every unsupported host.
func Detect() (*Distro, error) {
	if runtime.GOOS != "linux" {
		return nil, fail(ErrNotLinux, apperr.CodeUnavailable, http.StatusServiceUnavailable,
			"host operating system is not supported")
	}

	for _, path := range osReleasePaths {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		return DetectFromFile(path)
	}

	return nil, failUnavailable(ErrOSReleaseMissing, "host operating system could not be identified")
}

// DetectFromFile resolves a distribution from a specific os-release file.
// Tests use it with a temporary directory so detection never depends on the
// machine running the suite.
func DetectFromFile(path string) (*Distro, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, failUnavailable(ErrOSReleaseMissing, "host operating system could not be identified")
	}
	defer f.Close()

	fields, err := ParseOSRelease(f)
	if err != nil {
		return nil, err
	}

	return DistroFromFields(fields)
}

// ParseOSRelease parses the shell-like KEY=VALUE syntax of os-release,
// honouring single and double quotes and ignoring comments and blank lines.
func ParseOSRelease(r io.Reader) (map[string]string, error) {
	fields := make(map[string]string, 16)
	scanner := bufio.NewScanner(io.LimitReader(r, 1<<20))
	scanner.Buffer(make([]byte, 0, 4096), 1<<16)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		fields[key] = unquoteOSRelease(strings.TrimSpace(value))
	}
	if err := scanner.Err(); err != nil {
		return nil, failUnavailable(ErrOSReleaseMalformed, "host operating system could not be identified")
	}
	if len(fields) == 0 {
		return nil, failUnavailable(ErrOSReleaseMalformed, "host operating system could not be identified")
	}

	return fields, nil
}

// unquoteOSRelease strips one layer of matching quotes and the backslash
// escapes systemd allows inside a double-quoted os-release value.
func unquoteOSRelease(v string) string {
	if len(v) >= 2 {
		first, last := v[0], v[len(v)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			v = v[1 : len(v)-1]
		}
	}
	replacer := strings.NewReplacer(`\"`, `"`, `\'`, `'`, `\\`, `\`, "\\$", "$", "\\`", "`")

	return replacer.Replace(v)
}

// DistroFromFields resolves parsed os-release fields into a supported
// distribution, rejecting unknown distributions and below-floor releases.
func DistroFromFields(fields map[string]string) (*Distro, error) {
	id := strings.ToLower(strings.TrimSpace(fields["ID"]))
	if id == "" {
		return nil, failUnavailable(ErrUnsupportedDistro, "host operating system is not supported")
	}

	like := splitLike(fields["ID_LIKE"])
	family, ok := resolveFamily(id, like)
	if !ok {
		return nil, failUnavailable(ErrUnsupportedDistro, "host operating system is not supported")
	}

	d := &Distro{
		ID:        id,
		Like:      like,
		Family:    family,
		Manager:   managerByFamily[family],
		VersionID: strings.TrimSpace(fields["VERSION_ID"]),
	}
	d.Version = resolveVersion(family, id, fields)
	d.Codename = resolveCodename(family, d.Version, fields)
	d.Major, d.Minor = parseMajorMinor(d.Version)

	if floor, has := minimumVersion[family]; has {
		if d.Version == "" || compareVersions(d.Version, floor) < 0 {
			return nil, failUnavailable(ErrVersionTooOld, "host operating system release is not supported")
		}
	}

	return d, nil
}

// splitLike normalizes the whitespace-separated ID_LIKE list.
func splitLike(raw string) []string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(raw)))
	if len(fields) == 0 {
		return nil
	}

	return fields
}

// resolveFamily maps an ID, then an ID_LIKE chain, onto a supported family.
func resolveFamily(id string, like []string) (Family, bool) {
	if family, ok := familyByID[id]; ok {
		return family, true
	}

	for _, candidate := range likePriority {
		for _, token := range like {
			if token != candidate {
				continue
			}
			if family, ok := familyByID[token]; ok {
				return family, true
			}
		}
	}

	return "", false
}

// resolveVersion normalizes the release to the upstream numbering the
// support floor is expressed in. Derivatives are mapped back onto their
// upstream release through the codename they inherit, because a derivative's
// own VERSION_ID (Mint 21) is unrelated to its base (Ubuntu 22.04).
func resolveVersion(family Family, id string, fields map[string]string) string {
	versionID := strings.TrimSpace(fields["VERSION_ID"])

	switch family {
	case FamilyArch:
		return RollingVersion
	case FamilyUbuntu:
		if id == "ubuntu" {
			return versionID
		}
		if v := versionForCodename(ubuntuCodenames, fields["UBUNTU_CODENAME"]); v != "" {
			return v
		}
		return versionID
	case FamilyDebian:
		if id == "debian" {
			return versionID
		}
		if v := versionForCodename(debianCodenames, fields["DEBIAN_CODENAME"]); v != "" {
			return v
		}
		if v := versionForCodename(debianCodenames, fields["VERSION_CODENAME"]); v != "" {
			return v
		}
		return versionID
	default:
		return versionID
	}
}

// versionForCodename reverses a codename table, returning "" when the
// codename is unknown or empty.
func versionForCodename(table map[string]string, codename string) string {
	name := strings.ToLower(strings.TrimSpace(codename))
	if name == "" {
		return ""
	}
	for version, known := range table {
		if known == name {
			return version
		}
	}

	return ""
}

// resolveCodename picks the apt suite name for the distribution, preferring
// the explicit os-release fields and falling back to the version table.
func resolveCodename(family Family, version string, fields map[string]string) string {
	switch family {
	case FamilyUbuntu:
		for _, key := range []string{"UBUNTU_CODENAME", "VERSION_CODENAME"} {
			if v := strings.ToLower(strings.TrimSpace(fields[key])); v != "" {
				return v
			}
		}
		return ubuntuCodenames[version]
	case FamilyDebian:
		for _, key := range []string{"DEBIAN_CODENAME", "VERSION_CODENAME"} {
			if v := strings.ToLower(strings.TrimSpace(fields[key])); v != "" {
				return v
			}
		}
		return debianCodenames[version]
	default:
		return ""
	}
}

// parseMajorMinor extracts the leading numeric components of a version.
func parseMajorMinor(version string) (int, int) {
	if version == "" || version == RollingVersion {
		return 0, 0
	}
	parts := strings.Split(version, ".")
	major, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
	minor := 0
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
	}

	return major, minor
}

// compareVersions compares two dotted numeric versions, returning -1, 0 or 1.
// Non-numeric components compare as zero, which is what a suffixed release
// such as "9.4" needs for a floor check.
func compareVersions(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	length := len(as)
	if len(bs) > length {
		length = len(bs)
	}

	for i := 0; i < length; i++ {
		av, bv := 0, 0
		if i < len(as) {
			av, _ = strconv.Atoi(strings.TrimSpace(as[i]))
		}
		if i < len(bs) {
			bv, _ = strconv.Atoi(strings.TrimSpace(bs[i]))
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}

	return 0
}

// AtLeast reports whether the distribution's release is at or above version.
// A rolling release is always at least any version.
func (d *Distro) AtLeast(version string) bool {
	if d == nil {
		return false
	}
	if d.Version == RollingVersion {
		return true
	}

	return compareVersions(d.Version, version) >= 0
}

// IsAPT reports whether the distribution uses apt.
func (d *Distro) IsAPT() bool { return d != nil && d.Manager == ManagerAPT }

// IsDNF reports whether the distribution uses dnf.
func (d *Distro) IsDNF() bool { return d != nil && d.Manager == ManagerDNF }

// String renders the distribution for logs and audit records.
func (d *Distro) String() string {
	if d == nil {
		return "unknown"
	}
	if d.Version == "" || d.Version == RollingVersion {
		return d.ID
	}

	return d.ID + " " + d.Version
}
