package hostpkg

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

// requireLines fails the test when a rendered definition is missing a line.
func requireLines(t *testing.T, definition string, want ...string) {
	t.Helper()

	lines := map[string]bool{}
	for _, line := range strings.Split(definition, "\n") {
		lines[line] = true
	}
	for _, line := range want {
		if !lines[line] {
			t.Errorf("definition is missing %q; got:\n%s", line, definition)
		}
	}
}

func TestPlanRepoAPTDefinitions(t *testing.T) {
	cases := []struct {
		name        string
		id          RepoID
		family      Family
		path        string
		keyURL      string
		fingerprint string
		keyPath     string
		lines       []string
	}{
		{
			name:        "docker on debian",
			id:          RepoDocker,
			family:      FamilyDebian,
			path:        "/etc/apt/sources.list.d/docker.sources",
			keyURL:      "https://download.docker.com/linux/debian/gpg",
			fingerprint: fingerprintDockerDeb,
			keyPath:     "/etc/apt/keyrings/docker.gpg",
			lines: []string{
				"Types: deb",
				"URIs: https://download.docker.com/linux/debian",
				"Suites: bookworm",
				"Components: stable",
				"Signed-By: /etc/apt/keyrings/docker.gpg",
			},
		},
		{
			name:        "docker on ubuntu",
			id:          RepoDocker,
			family:      FamilyUbuntu,
			path:        "/etc/apt/sources.list.d/docker.sources",
			keyURL:      "https://download.docker.com/linux/ubuntu/gpg",
			fingerprint: fingerprintDockerDeb,
			keyPath:     "/etc/apt/keyrings/docker.gpg",
			lines: []string{
				"URIs: https://download.docker.com/linux/ubuntu",
				"Suites: noble",
				"Components: stable",
			},
		},
		{
			name:        "sury on debian",
			id:          RepoSury,
			family:      FamilyDebian,
			path:        "/etc/apt/sources.list.d/sury-php.sources",
			keyURL:      "https://packages.sury.org/php/apt.gpg",
			fingerprint: fingerprintSury,
			keyPath:     "/etc/apt/keyrings/sury-php.gpg",
			lines: []string{
				"URIs: https://packages.sury.org/php/",
				"Suites: bookworm",
				"Components: main",
				"Signed-By: /etc/apt/keyrings/sury-php.gpg",
			},
		},
		{
			name:        "ondrej on ubuntu",
			id:          RepoOndrejPHP,
			family:      FamilyUbuntu,
			path:        "/etc/apt/sources.list.d/ondrej-php.sources",
			keyURL:      ondrejKeyURL,
			fingerprint: fingerprintOndrejPHP,
			keyPath:     "/etc/apt/keyrings/ondrej-php.gpg",
			lines: []string{
				"URIs: https://ppa.launchpadcontent.net/ondrej/php/ubuntu",
				"Suites: noble",
				"Components: main",
			},
		},
		{
			name:        "zabbly incus on debian 12",
			id:          RepoZabblyIncus,
			family:      FamilyDebian,
			path:        "/etc/apt/sources.list.d/zabbly-incus.sources",
			keyURL:      "https://pkgs.zabbly.com/key.asc",
			fingerprint: fingerprintZabbly,
			keyPath:     "/etc/apt/keyrings/zabbly-incus.gpg",
			lines: []string{
				"URIs: https://pkgs.zabbly.com/incus/stable",
				"Suites: bookworm",
				"Components: main",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := PlanRepo(tc.id, distroFor(t, tc.family))
			if err != nil {
				t.Fatalf("PlanRepo: %v", err)
			}
			if plan.ID != tc.id || plan.Manager != ManagerAPT {
				t.Fatalf("plan = %s/%s, want %s/apt", plan.ID, plan.Manager, tc.id)
			}
			if plan.DefinitionPath != tc.path {
				t.Errorf("DefinitionPath = %q, want %q", plan.DefinitionPath, tc.path)
			}
			if plan.ArmoredKeys {
				t.Error("apt keyrings must be binary, not armored")
			}
			if len(plan.Keys) != 1 {
				t.Fatalf("keys = %d, want 1", len(plan.Keys))
			}
			key := plan.Keys[0]
			if key.URL != tc.keyURL || key.Fingerprint != tc.fingerprint || key.Path != tc.keyPath {
				t.Errorf("key = %+v, want %s / %s / %s", key, tc.keyURL, tc.fingerprint, tc.keyPath)
			}
			requireLines(t, plan.Definition, tc.lines...)
			if !strings.HasSuffix(plan.Definition, "\n") {
				t.Error("definition does not end with a newline")
			}
			if arch := DebArch(); arch != "" {
				requireLines(t, plan.Definition, "Architectures: "+arch)
			} else if strings.Contains(plan.Definition, "Architectures:") {
				t.Error("an unmapped architecture still emitted an Architectures field")
			}
		})
	}
}

func TestPlanRepoDNFDefinitions(t *testing.T) {
	cases := []struct {
		name    string
		id      RepoID
		family  Family
		path    string
		section string
		baseURL string
		keys    []string
	}{
		{
			name:    "docker on rhel",
			id:      RepoDocker,
			family:  FamilyRHEL,
			path:    "/etc/yum.repos.d/docker-ce.repo",
			section: "[docker-ce-stable]",
			baseURL: "baseurl=https://download.docker.com/linux/centos/9/$basearch/stable",
			keys:    []string{"file:///etc/pki/rpm-gpg/RPM-GPG-KEY-docker-ce"},
		},
		{
			name:    "docker on fedora",
			id:      RepoDocker,
			family:  FamilyFedora,
			path:    "/etc/yum.repos.d/docker-ce.repo",
			section: "[docker-ce-stable]",
			baseURL: "baseurl=https://download.docker.com/linux/fedora/43/$basearch/stable",
			keys:    []string{"file:///etc/pki/rpm-gpg/RPM-GPG-KEY-docker-ce"},
		},
		{
			name:    "remi on rhel",
			id:      RepoRemi,
			family:  FamilyRHEL,
			path:    "/etc/yum.repos.d/remi-safe.repo",
			section: "[remi-safe]",
			baseURL: "baseurl=https://rpms.remirepo.net/enterprise/9/safe/$basearch/",
			keys: []string{
				"file:///etc/pki/rpm-gpg/RPM-GPG-KEY-remi2021",
				"file:///etc/pki/rpm-gpg/RPM-GPG-KEY-remi2022",
				"file:///etc/pki/rpm-gpg/RPM-GPG-KEY-remi2023",
				"file:///etc/pki/rpm-gpg/RPM-GPG-KEY-remi2024",
				"file:///etc/pki/rpm-gpg/RPM-GPG-KEY-remi2025",
				"file:///etc/pki/rpm-gpg/RPM-GPG-KEY-remi2026",
			},
		},
		{
			name:    "remi on fedora",
			id:      RepoRemi,
			family:  FamilyFedora,
			path:    "/etc/yum.repos.d/remi-safe.repo",
			section: "[remi-safe]",
			baseURL: "baseurl=https://rpms.remirepo.net/fedora/43/remi/$basearch/",
			keys: []string{
				"file:///etc/pki/rpm-gpg/RPM-GPG-KEY-remi2021",
				"file:///etc/pki/rpm-gpg/RPM-GPG-KEY-remi2022",
				"file:///etc/pki/rpm-gpg/RPM-GPG-KEY-remi2023",
				"file:///etc/pki/rpm-gpg/RPM-GPG-KEY-remi2024",
				"file:///etc/pki/rpm-gpg/RPM-GPG-KEY-remi2025",
				"file:///etc/pki/rpm-gpg/RPM-GPG-KEY-remi2026",
			},
		},
		{
			name:    "incus copr on rhel",
			id:      RepoIncusCOPR,
			family:  FamilyRHEL,
			path:    "/etc/yum.repos.d/incus-copr.repo",
			section: "[incus-copr]",
			baseURL: "baseurl=https://download.copr.fedorainfracloud.org/results/neil/incus/epel-9-$basearch/",
			keys:    []string{"file:///etc/pki/rpm-gpg/RPM-GPG-KEY-incus-copr"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := PlanRepo(tc.id, distroFor(t, tc.family))
			if err != nil {
				t.Fatalf("PlanRepo: %v", err)
			}
			if plan.Manager != ManagerDNF {
				t.Fatalf("manager = %s, want dnf", plan.Manager)
			}
			if plan.DefinitionPath != tc.path {
				t.Errorf("DefinitionPath = %q, want %q", plan.DefinitionPath, tc.path)
			}
			if !plan.ArmoredKeys {
				t.Error("rpm keys must be written as ASCII armor")
			}
			if len(plan.Keys) != len(tc.keys) {
				t.Fatalf("keys = %d, want %d", len(plan.Keys), len(tc.keys))
			}
			requireLines(t, plan.Definition,
				tc.section,
				tc.baseURL,
				"enabled=1",
				"gpgcheck=1",
				"gpgkey="+strings.Join(tc.keys, " "),
				"skip_if_unavailable=False",
			)
			if strings.Contains(plan.Definition, "gpgcheck=0") {
				t.Error("signature verification was disabled")
			}
		})
	}
}

func TestPlanRepoNotApplicable(t *testing.T) {
	cases := []struct {
		name   string
		id     RepoID
		family Family
	}{
		{"docker on arch", RepoDocker, FamilyArch},
		{"docker on alpine", RepoDocker, FamilyAlpine},
		{"sury on ubuntu", RepoSury, FamilyUbuntu},
		{"sury on fedora", RepoSury, FamilyFedora},
		{"ondrej on debian", RepoOndrejPHP, FamilyDebian},
		{"remi on debian", RepoRemi, FamilyDebian},
		{"zabbly on ubuntu 24.04", RepoZabblyIncus, FamilyUbuntu},
		{"zabbly on fedora", RepoZabblyIncus, FamilyFedora},
		{"incus copr on fedora", RepoIncusCOPR, FamilyFedora},
		{"incus copr on debian", RepoIncusCOPR, FamilyDebian},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := PlanRepo(tc.id, distroFor(t, tc.family)); !errors.Is(err, ErrRepoNotApplicable) {
				t.Fatalf("error = %v, want ErrRepoNotApplicable", err)
			}
		})
	}
}

func TestPlanRepoRejectsUnknownAndNil(t *testing.T) {
	if _, err := PlanRepo("definitely-not-a-repo", distroFor(t, FamilyDebian)); !errors.Is(err, ErrRepoUnknown) {
		t.Fatalf("error = %v, want ErrRepoUnknown", err)
	}
	if _, err := PlanRepo(RepoDocker, nil); !errors.Is(err, ErrUnsupportedDistro) {
		t.Fatalf("error = %v, want ErrUnsupportedDistro", err)
	}
}

func TestPlanRepoRequiresACodename(t *testing.T) {
	// A Debian release newer than the built-in codename table, whose
	// os-release carries no codename, cannot produce a suite name; the plan
	// must fail rather than guess one.
	d := mustDistro(t, `ID=debian`, `VERSION_ID="99"`)
	if _, err := PlanRepo(RepoDocker, d); !errors.Is(err, ErrUnsupportedDistro) {
		t.Fatalf("error = %v, want ErrUnsupportedDistro", err)
	}
}

func TestPlanIncusCOPRRejectsEL8(t *testing.T) {
	d := mustDistro(t, `ID=rhel`, `ID_LIKE="fedora"`, `VERSION_ID="8.10"`)
	if _, err := PlanRepo(RepoIncusCOPR, d); !errors.Is(err, ErrServiceNotAvailable) {
		t.Fatalf("error = %v, want ErrServiceNotAvailable", err)
	}
}

func TestReposIsTheCompleteSanctionedSet(t *testing.T) {
	want := []RepoID{RepoDocker, RepoSury, RepoOndrejPHP, RepoRemi, RepoZabblyIncus, RepoIncusCOPR}

	got := Repos()
	if len(got) != len(want) {
		t.Fatalf("Repos() = %v, want %v", got, want)
	}
	for i, id := range want {
		if got[i] != id {
			t.Errorf("Repos()[%d] = %q, want %q", i, got[i], id)
		}
	}
}

func TestRepoForService(t *testing.T) {
	cases := []struct {
		name   string
		svc    Service
		family Family
		want   RepoID
		ok     bool
	}{
		{"docker on debian", ServiceDocker, FamilyDebian, RepoDocker, true},
		{"docker on ubuntu", ServiceDocker, FamilyUbuntu, RepoDocker, true},
		{"docker on rhel", ServiceDocker, FamilyRHEL, RepoDocker, true},
		{"docker on fedora", ServiceDocker, FamilyFedora, RepoDocker, true},
		{"docker on alpine is native", ServiceDocker, FamilyAlpine, "", false},
		{"docker on arch is native", ServiceDocker, FamilyArch, "", false},
		{"incus on debian 12", ServiceIncus, FamilyDebian, RepoZabblyIncus, true},
		{"incus on ubuntu 24.04 is native", ServiceIncus, FamilyUbuntu, "", false},
		{"incus on rhel", ServiceIncus, FamilyRHEL, RepoIncusCOPR, true},
		{"incus on fedora is native", ServiceIncus, FamilyFedora, "", false},
		{"php on debian", ServicePHPFPM, FamilyDebian, RepoSury, true},
		{"php on ubuntu", ServicePHPFPM, FamilyUbuntu, RepoOndrejPHP, true},
		{"php on rhel", ServicePHPFPM, FamilyRHEL, RepoRemi, true},
		{"php on fedora", ServicePHPFPM, FamilyFedora, RepoRemi, true},
		{"php on alpine is native", ServicePHPFPM, FamilyAlpine, "", false},
		{"php on arch is native", ServicePHPFPM, FamilyArch, "", false},
		{"web server never needs a repository", ServiceWebServer, FamilyDebian, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := RepoForService(tc.svc, distroFor(t, tc.family))
			if id != tc.want || ok != tc.ok {
				t.Fatalf("RepoForService = %q, %v, want %q, %v", id, ok, tc.want, tc.ok)
			}
		})
	}

	if _, ok := RepoForService(ServiceDocker, nil); ok {
		t.Error("a nil distribution reported a repository")
	}
}

func TestIncusNeedsZabbly(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  bool
	}{
		{"debian 12", []string{`ID=debian`, `VERSION_ID="12"`, `VERSION_CODENAME=bookworm`}, true},
		{"debian 13", []string{`ID=debian`, `VERSION_ID="13"`, `VERSION_CODENAME=trixie`}, false},
		{"ubuntu 22.04", []string{`ID=ubuntu`, `VERSION_ID="22.04"`, `UBUNTU_CODENAME=jammy`}, true},
		{"ubuntu 24.04", []string{`ID=ubuntu`, `VERSION_ID="24.04"`, `UBUNTU_CODENAME=noble`}, false},
		{"fedora", []string{`ID=fedora`, `VERSION_ID=43`}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := incusNeedsZabbly(mustDistro(t, tc.lines...)); got != tc.want {
				t.Fatalf("incusNeedsZabbly = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAlpineCommunityRepository(t *testing.T) {
	path, entry, err := AlpineCommunityRepository(distroFor(t, FamilyAlpine))
	if err != nil {
		t.Fatalf("AlpineCommunityRepository: %v", err)
	}
	if path != "/etc/apk/repositories" {
		t.Errorf("path = %q, want /etc/apk/repositories", path)
	}
	if entry != "https://dl-cdn.alpinelinux.org/alpine/v3.21/community" {
		t.Errorf("entry = %q", entry)
	}

	for _, family := range []Family{FamilyDebian, FamilyArch, FamilyRHEL} {
		if _, _, err := AlpineCommunityRepository(distroFor(t, family)); !errors.Is(err, ErrRepoNotApplicable) {
			t.Errorf("%s error = %v, want ErrRepoNotApplicable", family, err)
		}
	}
	if _, _, err := AlpineCommunityRepository(nil); !errors.Is(err, ErrRepoNotApplicable) {
		t.Errorf("nil error = %v, want ErrRepoNotApplicable", err)
	}
}

func TestRenderPacmanRepoSection(t *testing.T) {
	// A public IP literal keeps the outbound check off DNS.
	const server = "https://93.184.216.34/archlinux/$repo/os/$arch"

	path, section, err := RenderPacmanRepoSection("custom-repo", server)
	if err != nil {
		t.Fatalf("RenderPacmanRepoSection: %v", err)
	}
	if path != "/etc/pacman.conf" {
		t.Errorf("path = %q, want /etc/pacman.conf", path)
	}
	requireLines(t, section,
		"[custom-repo]",
		"SigLevel = Required DatabaseOptional",
		"Server = "+server,
	)

	if _, _, err := RenderPacmanRepoSection("Bad Name", server); !errors.Is(err, ErrInvalidRepoName) {
		t.Errorf("invalid name error = %v, want ErrInvalidRepoName", err)
	}
	if _, _, err := RenderPacmanRepoSection("custom-repo", "http://93.184.216.34/archlinux"); !errors.Is(err, ErrInsecureRepoURL) {
		t.Errorf("plain http error = %v, want ErrInsecureRepoURL", err)
	}
}

func TestDebArchMapping(t *testing.T) {
	want := map[string]string{
		"amd64":   "amd64",
		"arm64":   "arm64",
		"arm":     "armhf",
		"386":     "i386",
		"ppc64le": "ppc64el",
		"s390x":   "s390x",
		"riscv64": "riscv64",
	}

	got := DebArch()
	if expected, ok := want[runtime.GOARCH]; ok {
		if got != expected {
			t.Fatalf("DebArch() = %q, want %q on %s", got, expected, runtime.GOARCH)
		}
		return
	}
	if got != "" {
		t.Fatalf("DebArch() = %q on unmapped %s, want an empty string", got, runtime.GOARCH)
	}
}
