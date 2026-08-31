package hostpkg

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// osRelease renders an os-release file body from KEY=VALUE pairs.
func osRelease(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

// writeOSRelease writes an os-release file into a temporary directory.
func writeOSRelease(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "os-release")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write os-release: %v", err)
	}

	return path
}

func TestDetectFromFileSupportedDistros(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		family   Family
		manager  ManagerKind
		version  string
		codename string
		major    int
	}{
		{
			name:     "debian 12",
			body:     osRelease(`ID=debian`, `VERSION_ID="12"`, `VERSION_CODENAME=bookworm`),
			family:   FamilyDebian,
			manager:  ManagerAPT,
			version:  "12",
			codename: "bookworm",
			major:    12,
		},
		{
			name:     "debian 11 at the floor",
			body:     osRelease(`ID=debian`, `VERSION_ID="11"`, `VERSION_CODENAME=bullseye`),
			family:   FamilyDebian,
			manager:  ManagerAPT,
			version:  "11",
			codename: "bullseye",
			major:    11,
		},
		{
			name:     "ubuntu 24.04",
			body:     osRelease(`ID=ubuntu`, `ID_LIKE=debian`, `VERSION_ID="24.04"`, `UBUNTU_CODENAME=noble`),
			family:   FamilyUbuntu,
			manager:  ManagerAPT,
			version:  "24.04",
			codename: "noble",
			major:    24,
		},
		{
			name:     "linux mint resolves to its ubuntu base",
			body:     osRelease(`ID=linuxmint`, `ID_LIKE="ubuntu debian"`, `VERSION_ID="21.3"`, `UBUNTU_CODENAME=jammy`),
			family:   FamilyUbuntu,
			manager:  ManagerAPT,
			version:  "22.04",
			codename: "jammy",
			major:    22,
		},
		{
			name:    "alpine 3.20",
			body:    osRelease(`ID=alpine`, `VERSION_ID=3.20.3`),
			family:  FamilyAlpine,
			manager: ManagerAPK,
			version: "3.20.3",
			major:   3,
		},
		{
			name:    "rocky resolves to the rhel family",
			body:    osRelease(`ID="rocky"`, `ID_LIKE="rhel centos fedora"`, `VERSION_ID="9.4"`),
			family:  FamilyRHEL,
			manager: ManagerDNF,
			version: "9.4",
			major:   9,
		},
		{
			name:    "fedora 42",
			body:    osRelease(`ID=fedora`, `VERSION_ID=42`),
			family:  FamilyFedora,
			manager: ManagerDNF,
			version: "42",
			major:   42,
		},
		{
			name:    "arch is rolling",
			body:    osRelease(`ID=arch`),
			family:  FamilyArch,
			manager: ManagerPacman,
			version: RollingVersion,
		},
		{
			name:    "endeavouros resolves to arch",
			body:    osRelease(`ID=endeavouros`, `ID_LIKE=arch`),
			family:  FamilyArch,
			manager: ManagerPacman,
			version: RollingVersion,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := DetectFromFile(writeOSRelease(t, tc.body))
			if err != nil {
				t.Fatalf("DetectFromFile: %v", err)
			}
			if d.Family != tc.family {
				t.Errorf("family = %q, want %q", d.Family, tc.family)
			}
			if d.Manager != tc.manager {
				t.Errorf("manager = %q, want %q", d.Manager, tc.manager)
			}
			if d.Version != tc.version {
				t.Errorf("version = %q, want %q", d.Version, tc.version)
			}
			if tc.codename != "" && d.Codename != tc.codename {
				t.Errorf("codename = %q, want %q", d.Codename, tc.codename)
			}
			if tc.major != 0 && d.Major != tc.major {
				t.Errorf("major = %d, want %d", d.Major, tc.major)
			}
		})
	}
}

func TestDetectFromFileRejections(t *testing.T) {
	cases := []struct {
		name string
		body string
		want error
	}{
		{"debian below the floor", osRelease(`ID=debian`, `VERSION_ID="10"`), ErrVersionTooOld},
		{"ubuntu below the floor", osRelease(`ID=ubuntu`, `VERSION_ID="20.04"`), ErrVersionTooOld},
		{"alpine below the floor", osRelease(`ID=alpine`, `VERSION_ID=3.18.6`), ErrVersionTooOld},
		{"rhel below the floor", osRelease(`ID=rhel`, `VERSION_ID="7.9"`), ErrVersionTooOld},
		{"fedora below the floor", osRelease(`ID=fedora`, `VERSION_ID=39`), ErrVersionTooOld},
		{"debian without a version", osRelease(`ID=debian`), ErrVersionTooOld},
		{"unsupported distribution", osRelease(`ID=gentoo`, `VERSION_ID=2.17`), ErrUnsupportedDistro},
		{"unsupported derivative", osRelease(`ID=slackware`, `ID_LIKE=slack`), ErrUnsupportedDistro},
		{"missing id", osRelease(`NAME="Mystery Linux"`), ErrUnsupportedDistro},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DetectFromFile(writeOSRelease(t, tc.body)); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDetectFromFileMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent")
	if _, err := DetectFromFile(path); !errors.Is(err, ErrOSReleaseMissing) {
		t.Fatalf("error = %v, want ErrOSReleaseMissing", err)
	}
}

func TestParseOSReleaseEmptyIsMalformed(t *testing.T) {
	if _, err := ParseOSRelease(strings.NewReader("# only a comment\n\n")); !errors.Is(err, ErrOSReleaseMalformed) {
		t.Fatalf("error = %v, want ErrOSReleaseMalformed", err)
	}
}

func TestParseOSReleaseQuoting(t *testing.T) {
	body := osRelease(
		`# comment`,
		`ID="debian"`,
		`PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"`,
		`SINGLE='value'`,
		`ESCAPED="a\"b"`,
		`NOT A PAIR`,
	)

	fields, err := ParseOSRelease(strings.NewReader(body))
	if err != nil {
		t.Fatalf("ParseOSRelease: %v", err)
	}
	if fields["ID"] != "debian" {
		t.Errorf("ID = %q, want debian", fields["ID"])
	}
	if fields["PRETTY_NAME"] != "Debian GNU/Linux 12 (bookworm)" {
		t.Errorf("PRETTY_NAME = %q", fields["PRETTY_NAME"])
	}
	if fields["SINGLE"] != "value" {
		t.Errorf("SINGLE = %q, want value", fields["SINGLE"])
	}
	if fields["ESCAPED"] != `a"b` {
		t.Errorf("ESCAPED = %q, want a\"b", fields["ESCAPED"])
	}
	if _, ok := fields["NOT A PAIR"]; ok {
		t.Error("a line without an equals sign became a field")
	}
}

func TestDistroAtLeast(t *testing.T) {
	d := mustDistro(t, `ID=ubuntu`, `VERSION_ID="24.04"`, `UBUNTU_CODENAME=noble`)

	if !d.AtLeast("22.04") {
		t.Error("24.04 should be at least 22.04")
	}
	if d.AtLeast("26.04") {
		t.Error("24.04 should not be at least 26.04")
	}

	arch := mustDistro(t, `ID=arch`)
	if !arch.AtLeast("99") {
		t.Error("a rolling release should satisfy every floor")
	}
	if arch.String() != "arch" {
		t.Errorf("String() = %q, want arch", arch.String())
	}
}

// mustDistro builds a distribution from os-release lines or fails the test.
func mustDistro(t *testing.T, lines ...string) *Distro {
	t.Helper()

	fields, err := ParseOSRelease(strings.NewReader(osRelease(lines...)))
	if err != nil {
		t.Fatalf("ParseOSRelease: %v", err)
	}
	d, err := DistroFromFields(fields)
	if err != nil {
		t.Fatalf("DistroFromFields: %v", err)
	}

	return d
}
