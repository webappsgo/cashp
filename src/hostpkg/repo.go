package hostpkg

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// This file encodes IDEA.md's third-party repository contract: cashp writes
// the repository definition itself from values it owns, pins every signing
// key by fingerprint, and never runs a vendor install script. Arch is
// excluded from the mechanism entirely, per the same contract.

// RepoID names a third-party repository cashp is allowed to add.
type RepoID string

// The complete set of third-party repositories IDEA.md sanctions. Nothing
// outside this list is ever added to a host.
const (
	// RepoDocker is Docker's official apt/dnf repository, used instead of the
	// distribution's own docker.io packaging.
	RepoDocker RepoID = "docker"
	// RepoSury is Sury's Debian repository for concurrent multi-version PHP.
	RepoSury RepoID = "sury-php"
	// RepoOndrejPHP is the ondrej/php PPA, the Ubuntu equivalent of Sury.
	RepoOndrejPHP RepoID = "ondrej-php"
	// RepoRemi is Remi's repository in "safe" (non-module) mode for the RHEL
	// family and Fedora.
	RepoRemi RepoID = "remi-safe"
	// RepoZabblyIncus is Zabbly's Incus repository for the Debian and Ubuntu
	// releases that predate native Incus packaging.
	RepoZabblyIncus RepoID = "zabbly-incus"
	// RepoIncusCOPR is the COPR Incus build for the RHEL family, which has no
	// official RHEL or EPEL package.
	RepoIncusCOPR RepoID = "incus-copr"
)

// Pinned signing key fingerprints, compiled into cashp. A downloaded key is
// trusted only when it contains the primary key named here; trust-on-first-use
// of whatever the network returns is never acceptable.
const (
	fingerprintDockerDeb = "9DC858229FC7DD38854AE2D88D81803C0EBFCD88"
	fingerprintDockerRPM = "060A61C51B558A7F742B77AAC52FEB6B621E9F35"
	fingerprintSury      = "15058500A0235D97F5D10063B188E2B695BD4743"
	fingerprintOndrejPHP = "14AA40EC0831756756D7F66C4F4EA0AAE5267A6C"
	fingerprintZabbly    = "4EFC590696CB15B87C73A3AD82CC8797C838DCFD"
	fingerprintIncusCOPR = "F9C1299505472F50658A5540AC582BA43B8A7C62"
	fingerprintRemi2021  = "B1ABF71E14C9D74897E198A8B19527F1478F8947"
	fingerprintRemi2022  = "845160D23149DAD504F0A32D83C0639E1FEF0014"
	fingerprintRemi2023  = "50A5E157DFE548EC7C05E9D8D5933DAB6DEFD35E"
	fingerprintRemi2024  = "CF1DF0057CE85DFF5B2F2A37C2FD3B2C2A0948E4"
	fingerprintRemi2025  = "83833E4687A4AA03B6AC94F2061566968F1F4B2D"
	fingerprintRemi2026  = "2E375FE24EDFF0F2E0D8E165ED5B58C5BEFA00E2"
)

// Base locations of the repositories and their signing keys.
const (
	dockerBaseURL      = "https://download.docker.com/linux"
	suryBaseURL        = "https://packages.sury.org/php"
	ondrejBaseURL      = "https://ppa.launchpadcontent.net/ondrej/php/ubuntu"
	ondrejKeyURL       = "https://keyserver.ubuntu.com/pks/lookup?op=get&options=mr&search=0x" + fingerprintOndrejPHP
	zabblyBaseURL      = "https://pkgs.zabbly.com/incus/stable"
	zabblyKeyURL       = "https://pkgs.zabbly.com/key.asc"
	remiEnterpriseURL  = "https://rpms.remirepo.net/enterprise"
	remiFedoraURL      = "https://rpms.remirepo.net/fedora"
	remiKeyBaseURL     = "https://rpms.remirepo.net/RPM-GPG-KEY-"
	incusCOPRResultURL = "https://download.copr.fedorainfracloud.org/results/neil/incus"
	alpineMirrorURL    = "https://dl-cdn.alpinelinux.org/alpine"
)

// Host locations cashp writes.
const (
	aptSourcesDir = "/etc/apt/sources.list.d"
	aptKeyringDir = "/etc/apt/keyrings"
	dnfRepoDir    = "/etc/yum.repos.d"
	rpmKeyDir     = "/etc/pki/rpm-gpg"
	apkRepoFile   = "/etc/apk/repositories"
	pacmanConf    = "/etc/pacman.conf"
)

// KeyPin is one signing key with the fingerprint cashp requires it to have
// and the host path the verified key is written to.
type KeyPin struct {
	// URL is the HTTPS location the key is fetched from.
	URL string
	// Fingerprint is the pinned primary-key fingerprint.
	Fingerprint string
	// Path is the absolute destination path on the host.
	Path string
}

// RepoPlan is a fully resolved repository definition: the file to write, its
// exact content, and the keys that must be verified and installed first.
// Planning is pure, so a plan can be rendered and asserted on in tests
// without touching the host.
type RepoPlan struct {
	// ID identifies the repository.
	ID RepoID
	// Manager is the package manager the definition is written for.
	Manager ManagerKind
	// DefinitionPath is the absolute path of the repository definition file.
	DefinitionPath string
	// Definition is the exact file content cashp writes.
	Definition string
	// Keys are the pinned signing keys, in installation order.
	Keys []KeyPin
	// ArmoredKeys is true when the keys are written as ASCII armor, which is
	// what dnf and rpm expect; apt keyrings are binary.
	ArmoredKeys bool
}

// Repos returns every third-party repository cashp knows how to add.
func Repos() []RepoID {
	return []RepoID{RepoDocker, RepoSury, RepoOndrejPHP, RepoRemi, RepoZabblyIncus, RepoIncusCOPR}
}

// PlanRepo resolves a repository definition for a distribution. It returns a
// typed not-applicable error when the distribution needs no such repository,
// which is a decision the caller must handle rather than a silent no-op.
func PlanRepo(id RepoID, d *Distro) (*RepoPlan, error) {
	if d == nil {
		return nil, failUnavailable(ErrUnsupportedDistro, "host operating system is not supported")
	}
	if d.Family == FamilyArch || d.Family == FamilyAlpine {
		return nil, repoNotApplicable(id, d)
	}

	switch id {
	case RepoDocker:
		return planDocker(d)
	case RepoSury:
		return planSury(d)
	case RepoOndrejPHP:
		return planOndrej(d)
	case RepoRemi:
		return planRemi(d)
	case RepoZabblyIncus:
		return planZabbly(d)
	case RepoIncusCOPR:
		return planIncusCOPR(d)
	default:
		return nil, fail(ErrRepoUnknown, apperr.CodeNotFound, http.StatusNotFound, "unknown package repository").
			WithDetails(map[string]any{"repository": string(id)})
	}
}

// RepoForService returns the third-party repository a service needs on a
// distribution. The second result is false when the distribution packages the
// service natively and no repository is added.
func RepoForService(svc Service, d *Distro) (RepoID, bool) {
	if d == nil {
		return "", false
	}

	switch svc {
	case ServiceDocker:
		if d.IsAPT() || d.IsDNF() {
			return RepoDocker, true
		}
	case ServiceIncus:
		if incusNeedsZabbly(d) {
			return RepoZabblyIncus, true
		}
		if d.Family == FamilyRHEL {
			return RepoIncusCOPR, true
		}
	case ServicePHPFPM:
		switch d.Family {
		case FamilyDebian:
			return RepoSury, true
		case FamilyUbuntu:
			return RepoOndrejPHP, true
		case FamilyRHEL, FamilyFedora:
			return RepoRemi, true
		}
	}

	return "", false
}

// incusNeedsZabbly reports whether a Debian or Ubuntu release predates native
// Incus packaging: Debian ships it from 13 and Ubuntu from 24.04.
func incusNeedsZabbly(d *Distro) bool {
	switch d.Family {
	case FamilyDebian:
		return !d.AtLeast("13")
	case FamilyUbuntu:
		return !d.AtLeast("24.04")
	default:
		return false
	}
}

// planDocker builds Docker's official repository definition for the apt and
// dnf families; Alpine and Arch are handled by their native packages.
func planDocker(d *Distro) (*RepoPlan, error) {
	switch {
	case d.IsAPT():
		codename, err := aptCodename(d)
		if err != nil {
			return nil, err
		}
		vendor := "debian"
		if d.Family == FamilyUbuntu {
			vendor = "ubuntu"
		}
		key := KeyPin{
			URL:         dockerBaseURL + "/" + vendor + "/gpg",
			Fingerprint: fingerprintDockerDeb,
			Path:        aptKeyringDir + "/docker.gpg",
		}

		return &RepoPlan{
			ID:             RepoDocker,
			Manager:        ManagerAPT,
			DefinitionPath: aptSourcesDir + "/docker.sources",
			Definition: renderDeb822(deb822Source{
				Name:       "Docker CE",
				URI:        dockerBaseURL + "/" + vendor,
				Suite:      codename,
				Components: "stable",
				SignedBy:   key.Path,
			}),
			Keys: []KeyPin{key},
		}, nil
	case d.IsDNF():
		vendor := "centos"
		release := fmt.Sprintf("%d", d.Major)
		if d.Family == FamilyFedora {
			vendor = "fedora"
		}
		key := KeyPin{
			URL:         dockerBaseURL + "/" + vendor + "/gpg",
			Fingerprint: fingerprintDockerRPM,
			Path:        rpmKeyDir + "/RPM-GPG-KEY-docker-ce",
		}

		return &RepoPlan{
			ID:             RepoDocker,
			Manager:        ManagerDNF,
			DefinitionPath: dnfRepoDir + "/docker-ce.repo",
			Definition: renderDNFRepo(dnfSection{
				ID:      "docker-ce-stable",
				Name:    "Docker CE Stable",
				BaseURL: dockerBaseURL + "/" + vendor + "/" + release + "/$basearch/stable",
				Keys:    []KeyPin{key},
			}),
			Keys:        []KeyPin{key},
			ArmoredKeys: true,
		}, nil
	default:
		return nil, repoNotApplicable(RepoDocker, d)
	}
}

// planSury builds the Sury repository definition, which is Debian-only.
func planSury(d *Distro) (*RepoPlan, error) {
	if d.Family != FamilyDebian {
		return nil, repoNotApplicable(RepoSury, d)
	}

	codename, err := aptCodename(d)
	if err != nil {
		return nil, err
	}
	key := KeyPin{
		URL:         suryBaseURL + "/apt.gpg",
		Fingerprint: fingerprintSury,
		Path:        aptKeyringDir + "/sury-php.gpg",
	}

	return &RepoPlan{
		ID:             RepoSury,
		Manager:        ManagerAPT,
		DefinitionPath: aptSourcesDir + "/sury-php.sources",
		Definition: renderDeb822(deb822Source{
			Name:       "Sury PHP",
			URI:        suryBaseURL + "/",
			Suite:      codename,
			Components: "main",
			SignedBy:   key.Path,
		}),
		Keys: []KeyPin{key},
	}, nil
}

// planOndrej builds the ondrej/php PPA definition, which is Ubuntu-only.
func planOndrej(d *Distro) (*RepoPlan, error) {
	if d.Family != FamilyUbuntu {
		return nil, repoNotApplicable(RepoOndrejPHP, d)
	}

	codename, err := aptCodename(d)
	if err != nil {
		return nil, err
	}
	key := KeyPin{
		URL:         ondrejKeyURL,
		Fingerprint: fingerprintOndrejPHP,
		Path:        aptKeyringDir + "/ondrej-php.gpg",
	}

	return &RepoPlan{
		ID:             RepoOndrejPHP,
		Manager:        ManagerAPT,
		DefinitionPath: aptSourcesDir + "/ondrej-php.sources",
		Definition: renderDeb822(deb822Source{
			Name:       "ondrej PHP PPA",
			URI:        ondrejBaseURL,
			Suite:      codename,
			Components: "main",
			SignedBy:   key.Path,
		}),
		Keys: []KeyPin{key},
	}, nil
}

// planRemi builds Remi's "safe" repository definition. Every yearly signing
// key Remi still uses is pinned, because Remi rotates keys annually and a
// single pin would break the repository at the turn of a year.
func planRemi(d *Distro) (*RepoPlan, error) {
	var (
		baseURL string
		title   string
	)

	release := fmt.Sprintf("%d", d.Major)
	switch d.Family {
	case FamilyRHEL:
		baseURL = remiEnterpriseURL + "/" + release + "/safe/$basearch/"
		title = "Remi's RPM repository (safe) for Enterprise Linux " + release
	case FamilyFedora:
		baseURL = remiFedoraURL + "/" + release + "/remi/$basearch/"
		title = "Remi's RPM repository for Fedora " + release
	default:
		return nil, repoNotApplicable(RepoRemi, d)
	}

	keys := remiKeys()

	return &RepoPlan{
		ID:             RepoRemi,
		Manager:        ManagerDNF,
		DefinitionPath: dnfRepoDir + "/remi-safe.repo",
		Definition: renderDNFRepo(dnfSection{
			ID:      "remi-safe",
			Name:    title,
			BaseURL: baseURL,
			Keys:    keys,
		}),
		Keys:        keys,
		ArmoredKeys: true,
	}, nil
}

// remiKeys returns the pinned Remi signing keys in year order.
func remiKeys() []KeyPin {
	years := []struct {
		year        string
		fingerprint string
	}{
		{"2021", fingerprintRemi2021},
		{"2022", fingerprintRemi2022},
		{"2023", fingerprintRemi2023},
		{"2024", fingerprintRemi2024},
		{"2025", fingerprintRemi2025},
		{"2026", fingerprintRemi2026},
	}

	keys := make([]KeyPin, 0, len(years))
	for _, y := range years {
		keys = append(keys, KeyPin{
			URL:         remiKeyBaseURL + "remi" + y.year,
			Fingerprint: y.fingerprint,
			Path:        rpmKeyDir + "/RPM-GPG-KEY-remi" + y.year,
		})
	}

	return keys
}

// planZabbly builds the Zabbly Incus definition for the Debian and Ubuntu
// releases that predate native Incus packaging.
func planZabbly(d *Distro) (*RepoPlan, error) {
	if !incusNeedsZabbly(d) {
		return nil, repoNotApplicable(RepoZabblyIncus, d)
	}

	codename, err := aptCodename(d)
	if err != nil {
		return nil, err
	}
	key := KeyPin{
		URL:         zabblyKeyURL,
		Fingerprint: fingerprintZabbly,
		Path:        aptKeyringDir + "/zabbly-incus.gpg",
	}

	return &RepoPlan{
		ID:             RepoZabblyIncus,
		Manager:        ManagerAPT,
		DefinitionPath: aptSourcesDir + "/zabbly-incus.sources",
		Definition: renderDeb822(deb822Source{
			Name:       "Zabbly Incus (stable)",
			URI:        zabblyBaseURL,
			Suite:      codename,
			Components: "main",
			SignedBy:   key.Path,
		}),
		Keys: []KeyPin{key},
	}, nil
}

// planIncusCOPR builds the COPR Incus definition for the RHEL family. Fedora
// packages Incus natively and never gets this repository, and the COPR builds
// exist only for the EPEL releases the project still targets.
func planIncusCOPR(d *Distro) (*RepoPlan, error) {
	if d.Family != FamilyRHEL {
		return nil, repoNotApplicable(RepoIncusCOPR, d)
	}
	if d.Major < 9 {
		return nil, fail(ErrServiceNotAvailable, apperr.CodeUnavailable, http.StatusServiceUnavailable,
			"this service has no supported package on this operating system release").
			WithDetails(map[string]any{"service": string(ServiceIncus), "distribution": d.ID})
	}

	release := fmt.Sprintf("%d", d.Major)
	key := KeyPin{
		URL:         incusCOPRResultURL + "/pubkey.gpg",
		Fingerprint: fingerprintIncusCOPR,
		Path:        rpmKeyDir + "/RPM-GPG-KEY-incus-copr",
	}

	return &RepoPlan{
		ID:             RepoIncusCOPR,
		Manager:        ManagerDNF,
		DefinitionPath: dnfRepoDir + "/incus-copr.repo",
		Definition: renderDNFRepo(dnfSection{
			ID:      "incus-copr",
			Name:    "Incus builds for Enterprise Linux " + release,
			BaseURL: incusCOPRResultURL + "/epel-" + release + "-$basearch/",
			Keys:    []KeyPin{key},
		}),
		Keys:        []KeyPin{key},
		ArmoredKeys: true,
	}, nil
}

// deb822Source is the field set of a deb822 apt source stanza.
type deb822Source struct {
	Name       string
	URI        string
	Suite      string
	Components string
	SignedBy   string
}

// renderDeb822 renders an apt source entry in the deb822 format current
// Debian and Ubuntu releases use, pinned to the cashp-managed keyring.
func renderDeb822(src deb822Source) string {
	var b strings.Builder
	b.WriteString("# " + src.Name + " repository managed by cashp. Manual edits are overwritten.\n")
	b.WriteString("Types: deb\n")
	b.WriteString("URIs: " + src.URI + "\n")
	b.WriteString("Suites: " + src.Suite + "\n")
	b.WriteString("Components: " + src.Components + "\n")
	if arch := DebArch(); arch != "" {
		b.WriteString("Architectures: " + arch + "\n")
	}
	b.WriteString("Signed-By: " + src.SignedBy + "\n")

	return b.String()
}

// dnfSection is the field set of a dnf repository section.
type dnfSection struct {
	ID      string
	Name    string
	BaseURL string
	Keys    []KeyPin
}

// renderDNFRepo renders a dnf .repo file. gpgcheck stays enabled and every
// pinned key is referenced, so signature verification is never weakened to
// make an install succeed.
func renderDNFRepo(section dnfSection) string {
	refs := make([]string, 0, len(section.Keys))
	for _, key := range section.Keys {
		refs = append(refs, "file://"+key.Path)
	}

	var b strings.Builder
	b.WriteString("# Repository managed by cashp. Manual edits are overwritten.\n")
	b.WriteString("[" + section.ID + "]\n")
	b.WriteString("name=" + section.Name + "\n")
	b.WriteString("baseurl=" + section.BaseURL + "\n")
	b.WriteString("enabled=1\n")
	b.WriteString("gpgcheck=1\n")
	b.WriteString("gpgkey=" + strings.Join(refs, " ") + "\n")
	b.WriteString("skip_if_unavailable=False\n")

	return b.String()
}

// RenderPacmanRepoSection returns the pacman configuration path and a
// repository section with signature checking required. Arch is excluded from
// the automatic third-party repository mechanism, so cashp never adds one on
// its own; the renderer exists so an operator-requested section is produced in
// exactly one place, with SigLevel never relaxed.
func RenderPacmanRepoSection(name, server string) (string, string, error) {
	if err := ValidateRepoName(name); err != nil {
		return "", "", err
	}
	if err := ValidateKeyURL(server); err != nil {
		return "", "", err
	}

	var b strings.Builder
	b.WriteString("# Repository section managed by cashp. Manual edits are overwritten.\n")
	b.WriteString("[" + name + "]\n")
	b.WriteString("SigLevel = Required DatabaseOptional\n")
	b.WriteString("Server = " + server + "\n")

	return pacmanConf, b.String(), nil
}

// AlpineCommunityRepository returns the repositories file and the entry that
// enables Alpine's community repository, where most managed services live.
func AlpineCommunityRepository(d *Distro) (string, string, error) {
	if d == nil || d.Family != FamilyAlpine {
		return "", "", repoNotApplicable("alpine-community", d)
	}
	if d.Major <= 0 {
		return "", "", failUnavailable(ErrUnsupportedDistro, "host operating system is not supported")
	}

	entry := fmt.Sprintf("%s/v%d.%d/community", alpineMirrorURL, d.Major, d.Minor)

	return apkRepoFile, entry, nil
}

// DebArch maps the running architecture to its Debian name, returning an
// empty string for an architecture with no mapping so the source entry simply
// omits the Architectures field.
func DebArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	case "arm":
		return "armhf"
	case "386":
		return "i386"
	case "ppc64le":
		return "ppc64el"
	case "s390x":
		return "s390x"
	case "riscv64":
		return "riscv64"
	default:
		return ""
	}
}

// aptCodename returns the validated suite name for an apt source entry.
func aptCodename(d *Distro) (string, error) {
	if err := ValidateCodename(d.Codename); err != nil {
		return "", failValidation(ErrUnsupportedDistro, "host operating system release could not be identified")
	}

	return d.Codename, nil
}

// repoNotApplicable builds the typed "this host needs no such repository"
// failure, which callers treat as "use the native package".
func repoNotApplicable(id RepoID, d *Distro) error {
	details := map[string]any{"repository": string(id)}
	if d != nil {
		details["distribution"] = d.ID
	}

	return fail(ErrRepoNotApplicable, apperr.CodeConflict, http.StatusConflict,
		"this operating system does not use that package repository").WithDetails(details)
}
