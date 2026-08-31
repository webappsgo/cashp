package hostpkg

import (
	"errors"
	"strings"
	"testing"
)

// distroFixtures is one representative host per supported family.
var distroFixtures = map[Family][]string{
	FamilyDebian: {`ID=debian`, `VERSION_ID="12"`, `VERSION_CODENAME=bookworm`},
	FamilyUbuntu: {`ID=ubuntu`, `VERSION_ID="24.04"`, `UBUNTU_CODENAME=noble`},
	FamilyAlpine: {`ID=alpine`, `VERSION_ID=3.21.0`},
	FamilyRHEL:   {`ID=rocky`, `ID_LIKE="rhel centos fedora"`, `VERSION_ID="9.4"`},
	FamilyFedora: {`ID=fedora`, `VERSION_ID=43`},
	FamilyArch:   {`ID=arch`},
}

// distroFor returns the fixture distribution for a family.
func distroFor(t *testing.T, family Family) *Distro {
	t.Helper()

	lines, ok := distroFixtures[family]
	if !ok {
		t.Fatalf("no fixture for family %q", family)
	}

	return mustDistro(t, lines...)
}

func TestServiceMappingTable(t *testing.T) {
	// Every cell of IDEA.md's "Managed services & OS package mapping" table.
	want := map[Service]map[Family]string{
		ServiceWebServer: {
			FamilyDebian: "nginx", FamilyUbuntu: "nginx", FamilyAlpine: "nginx",
			FamilyRHEL: "nginx", FamilyFedora: "nginx", FamilyArch: "nginx",
		},
		ServiceMailTransport: {
			FamilyDebian: "postfix", FamilyUbuntu: "postfix", FamilyAlpine: "postfix",
			FamilyRHEL: "postfix", FamilyFedora: "postfix", FamilyArch: "postfix",
		},
		ServiceMailDelivery: {
			FamilyDebian: "dovecot-imapd dovecot-pop3d dovecot-lmtpd",
			FamilyUbuntu: "dovecot-imapd dovecot-pop3d dovecot-lmtpd",
			FamilyAlpine: "dovecot dovecot-pop3d dovecot-lmtpd",
			FamilyRHEL:   "dovecot", FamilyFedora: "dovecot", FamilyArch: "dovecot",
		},
		ServiceMailFilter: {
			FamilyDebian: "amavisd-new", FamilyUbuntu: "amavisd-new", FamilyAlpine: "amavis",
			FamilyRHEL: "amavis", FamilyFedora: "amavis", FamilyArch: "amavisd-new",
		},
		ServiceAntiSpam: {
			FamilyDebian: "spamassassin", FamilyUbuntu: "spamassassin", FamilyAlpine: "spamassassin",
			FamilyRHEL: "spamassassin", FamilyFedora: "spamassassin", FamilyArch: "spamassassin",
		},
		ServiceAntiVirus: {
			FamilyDebian: "clamav-daemon", FamilyUbuntu: "clamav-daemon", FamilyAlpine: "clamav-daemon",
			FamilyRHEL: "clamd", FamilyFedora: "clamd", FamilyArch: "clamav",
		},
		ServiceDKIM: {
			FamilyDebian: "opendkim", FamilyUbuntu: "opendkim", FamilyAlpine: "opendkim",
			FamilyRHEL: "opendkim", FamilyFedora: "opendkim", FamilyArch: "opendkim",
		},
		ServiceDMARC: {
			FamilyDebian: "opendmarc", FamilyUbuntu: "opendmarc", FamilyAlpine: "opendmarc",
			FamilyRHEL: "opendmarc", FamilyFedora: "opendmarc", FamilyArch: "opendmarc",
		},
		ServiceDNSServer: {
			FamilyDebian: "bind9", FamilyUbuntu: "bind9", FamilyAlpine: "bind",
			FamilyRHEL: "bind", FamilyFedora: "bind bind-utils", FamilyArch: "bind",
		},
		ServiceIntrusion: {
			FamilyDebian: "fail2ban", FamilyUbuntu: "fail2ban", FamilyAlpine: "fail2ban",
			FamilyRHEL: "fail2ban", FamilyFedora: "fail2ban", FamilyArch: "fail2ban",
		},
		ServiceFirewall: {
			FamilyDebian: "nftables", FamilyUbuntu: "nftables", FamilyAlpine: "nftables",
			FamilyRHEL: "nftables", FamilyFedora: "nftables", FamilyArch: "nftables",
		},
		ServiceDocker: {
			FamilyDebian: "docker-ce docker-ce-cli containerd.io",
			FamilyUbuntu: "docker-ce docker-ce-cli containerd.io",
			FamilyAlpine: "docker docker-cli",
			FamilyRHEL:   "docker-ce docker-ce-cli containerd.io",
			FamilyFedora: "docker-ce docker-ce-cli containerd.io",
			FamilyArch:   "docker",
		},
		ServiceIncus: {
			FamilyDebian: "incus", FamilyUbuntu: "incus", FamilyAlpine: "incus",
			FamilyRHEL: "incus", FamilyFedora: "incus", FamilyArch: "incus",
		},
		ServicePodman: {
			FamilyDebian: "podman", FamilyUbuntu: "podman", FamilyAlpine: "podman",
			FamilyRHEL: "podman", FamilyFedora: "podman", FamilyArch: "podman",
		},
		ServiceLibvirt: {
			FamilyDebian: "libvirt-daemon-system",
			FamilyUbuntu: "libvirt-daemon-system",
			FamilyAlpine: "libvirt libvirt-daemon libvirt-qemu libvirt-client",
			FamilyRHEL:   "libvirt-daemon-config-network libvirt-daemon-kvm",
			FamilyFedora: "libvirt-daemon-config-network libvirt-daemon-kvm",
			FamilyArch:   "libvirt",
		},
		ServiceQEMU: {
			FamilyDebian: "qemu-system-x86 qemu-system-arm",
			FamilyUbuntu: "qemu-system-x86 qemu-system-arm",
			FamilyAlpine: "qemu-system-x86_64 qemu-system-aarch64",
			FamilyRHEL:   "qemu-kvm", FamilyFedora: "qemu-kvm", FamilyArch: "qemu-full",
		},
		ServiceVirtInstall: {
			FamilyDebian: "virtinst", FamilyUbuntu: "virtinst", FamilyAlpine: "virt-install",
			FamilyRHEL: "virt-install", FamilyFedora: "virt-install", FamilyArch: "virt-install",
		},
		ServiceOVMF: {
			FamilyDebian: "ovmf", FamilyUbuntu: "ovmf", FamilyAlpine: "ovmf",
			FamilyRHEL: "edk2-ovmf", FamilyFedora: "edk2-ovmf", FamilyArch: "edk2-ovmf",
		},
	}

	for svc, perFamily := range want {
		for family, expected := range perFamily {
			d := distroFor(t, family)
			got, err := PackagesFor(svc, d)
			if err != nil {
				t.Errorf("PackagesFor(%s, %s): %v", svc, family, err)
				continue
			}
			if strings.Join(got, " ") != expected {
				t.Errorf("PackagesFor(%s, %s) = %q, want %q", svc, family, strings.Join(got, " "), expected)
			}
			for _, name := range got {
				if err := ValidatePackageName(name); err != nil {
					t.Errorf("mapped package %q fails validation: %v", name, err)
				}
			}
		}
	}

	// Every service in the table is covered by the expectations above, except
	// PHP-FPM, whose packages depend on a requested version.
	for _, svc := range Services() {
		if svc == ServicePHPFPM {
			continue
		}
		if _, ok := want[svc]; !ok {
			t.Errorf("service %q is in the table but not covered by this test", svc)
		}
	}
}

func TestServicesIsSortedAndComplete(t *testing.T) {
	services := Services()
	if len(services) != len(serviceTable) {
		t.Fatalf("Services() returned %d entries, want %d", len(services), len(serviceTable))
	}
	for i := 1; i < len(services); i++ {
		if services[i-1] >= services[i] {
			t.Fatalf("Services() is not sorted: %q before %q", services[i-1], services[i])
		}
	}
}

func TestLookupServiceUnknown(t *testing.T) {
	if _, err := LookupService(Service("nope")); !errors.Is(err, ErrServiceUnknown) {
		t.Fatalf("error = %v, want ErrServiceUnknown", err)
	}
	if _, err := PackagesFor(Service("nope"), distroFor(t, FamilyDebian)); !errors.Is(err, ErrServiceUnknown) {
		t.Fatalf("error = %v, want ErrServiceUnknown", err)
	}
}

func TestPackagesForRejectsTemplatedAndNilDistro(t *testing.T) {
	if _, err := PackagesFor(ServicePHPFPM, distroFor(t, FamilyDebian)); !errors.Is(err, ErrInvalidVersion) {
		t.Fatalf("error = %v, want ErrInvalidVersion", err)
	}
	if _, err := PackagesFor(ServiceWebServer, nil); !errors.Is(err, ErrUnsupportedDistro) {
		t.Fatalf("error = %v, want ErrUnsupportedDistro", err)
	}
}

func TestServiceNotAvailableIsTyped(t *testing.T) {
	if err := serviceNotAvailable(ServiceWebServer, distroFor(t, FamilyArch)); !errors.Is(err, ErrServiceNotAvailable) {
		t.Fatalf("error = %v, want ErrServiceNotAvailable", err)
	}
}

func TestPHPFPMPlanPerDistro(t *testing.T) {
	cases := []struct {
		family   Family
		version  string
		packages string
		repo     RepoID
	}{
		{FamilyDebian, "8.3", "php8.3-fpm", RepoSury},
		{FamilyUbuntu, "8.4", "php8.4-fpm", RepoOndrejPHP},
		{FamilyAlpine, "8.3", "php83-fpm", ""},
		{FamilyRHEL, "8.3", "php83-php-fpm", RepoRemi},
		{FamilyFedora, "8.3", "php83-php-fpm", RepoRemi},
	}

	for _, tc := range cases {
		plan, err := PHPFPMPlan(tc.version, distroFor(t, tc.family))
		if err != nil {
			t.Errorf("PHPFPMPlan(%s, %s): %v", tc.version, tc.family, err)
			continue
		}
		if strings.Join(plan.Packages, " ") != tc.packages {
			t.Errorf("%s packages = %v, want %q", tc.family, plan.Packages, tc.packages)
		}
		if plan.Repo != tc.repo {
			t.Errorf("%s repo = %q, want %q", tc.family, plan.Repo, tc.repo)
		}
		for _, name := range plan.Packages {
			if err := ValidatePackageName(name); err != nil {
				t.Errorf("php package %q fails validation: %v", name, err)
			}
		}
	}
}

func TestPHPFPMPlanArch(t *testing.T) {
	arch := distroFor(t, FamilyArch)

	plan, err := PHPFPMPlan(PHPNative, arch)
	if err != nil {
		t.Fatalf("PHPFPMPlan(native): %v", err)
	}
	if strings.Join(plan.Packages, " ") != "php" || plan.Repo != "" {
		t.Fatalf("arch plan = %+v", plan)
	}

	if _, err := PHPFPMPlan("8.1", arch); !errors.Is(err, ErrServiceNotAvailable) {
		t.Fatalf("error = %v, want ErrServiceNotAvailable", err)
	}
}

func TestPHPFPMPlanAlpineGaps(t *testing.T) {
	alpine320 := mustDistro(t, `ID=alpine`, `VERSION_ID=3.20.3`)

	// PHP 5.6 and 7.x were never packaged by Alpine at all.
	if _, err := PHPFPMPlan("7.4", alpine320); !errors.Is(err, ErrServiceNotAvailable) {
		t.Fatalf("error = %v, want ErrServiceNotAvailable", err)
	}
	// PHP 8.4 needs Alpine 3.21 or newer.
	if _, err := PHPFPMPlan("8.4", alpine320); !errors.Is(err, ErrServiceNotAvailable) {
		t.Fatalf("error = %v, want ErrServiceNotAvailable", err)
	}
	if _, err := PHPFPMPlan("8.4", mustDistro(t, `ID=alpine`, `VERSION_ID=3.21.0`)); err != nil {
		t.Fatalf("PHPFPMPlan(8.4, alpine 3.21): %v", err)
	}
}

func TestPHPFPMPlanFedoraDegraded(t *testing.T) {
	plan, err := PHPFPMPlan("8.3", mustDistro(t, `ID=fedora`, `VERSION_ID=40`))
	if err != nil {
		t.Fatalf("PHPFPMPlan: %v", err)
	}
	if !plan.Degraded || plan.DegradedReason == "" {
		t.Fatalf("fedora 40 should be a degraded Remi path: %+v", plan)
	}

	plan, err = PHPFPMPlan("8.3", mustDistro(t, `ID=fedora`, `VERSION_ID=44`))
	if err != nil {
		t.Fatalf("PHPFPMPlan: %v", err)
	}
	if plan.Degraded {
		t.Fatalf("fedora 44 should not be degraded: %+v", plan)
	}
}

func TestPHPFPMPlanRejectsBadInput(t *testing.T) {
	if _, err := PHPFPMPlan("8.3", nil); !errors.Is(err, ErrUnsupportedDistro) {
		t.Fatalf("error = %v, want ErrUnsupportedDistro", err)
	}
	for _, version := range []string{"8.3; rm -rf /", "../8.3", "8", ""} {
		if _, err := PHPFPMPlan(version, distroFor(t, FamilyDebian)); !errors.Is(err, ErrInvalidVersion) {
			t.Errorf("PHPFPMPlan(%q) = %v, want ErrInvalidVersion", version, err)
		}
	}
}
