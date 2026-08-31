package hostpkg

import (
	"net/http"
	"sort"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// This table encodes IDEA.md -> "Managed services & OS package mapping"
// verbatim. Package names come from that table and are never approximated:
// where the table lists several packages for one cell, every one of them is
// listed here in the same order.

// Service identifies one managed native host service.
type Service string

// The managed services, one per row of the mapping table.
const (
	ServiceWebServer     Service = "web-server"
	ServicePHPFPM        Service = "php-fpm"
	ServiceMailTransport Service = "mail-transport"
	ServiceMailDelivery  Service = "mail-delivery"
	ServiceMailFilter    Service = "mail-filter"
	ServiceAntiSpam      Service = "anti-spam"
	ServiceAntiVirus     Service = "anti-virus"
	ServiceDKIM          Service = "dkim"
	ServiceDMARC         Service = "dmarc"
	ServiceDNSServer     Service = "dns-server"
	ServiceIntrusion     Service = "intrusion-prevention"
	ServiceFirewall      Service = "firewall"
	ServiceDocker        Service = "container-docker"
	ServiceIncus         Service = "container-incus"
	ServicePodman        Service = "container-podman"
	ServiceLibvirt       Service = "vm-hypervisor"
	ServiceQEMU          Service = "vm-emulator"
	ServiceVirtInstall   Service = "vm-install-helper"
	ServiceOVMF          Service = "vm-uefi-firmware"
)

// ServiceDef is one row of the mapping table.
type ServiceDef struct {
	// Service is the row identifier.
	Service Service
	// Title is the human-readable row name from IDEA.md.
	Title string
	// Packages holds the package set per family. A family missing from the
	// map has no package for this service on that distribution.
	Packages map[Family][]string
	// Templated marks a row whose packages depend on a version argument and
	// therefore cannot be looked up through Packages.
	Templated bool
}

// serviceTable is the mapping table itself. The apt column of IDEA.md is
// shared by the Debian and Ubuntu families; the dnf column is split because
// IDEA.md lists RHEL and Fedora separately.
var serviceTable = map[Service]ServiceDef{
	ServiceWebServer: {
		Service: ServiceWebServer,
		Title:   "Web server",
		Packages: map[Family][]string{
			FamilyDebian: {"nginx"},
			FamilyUbuntu: {"nginx"},
			FamilyAlpine: {"nginx"},
			FamilyRHEL:   {"nginx"},
			FamilyFedora: {"nginx"},
			FamilyArch:   {"nginx"},
		},
	},
	ServicePHPFPM: {
		Service:   ServicePHPFPM,
		Title:     "PHP-FPM (per version)",
		Templated: true,
	},
	ServiceMailTransport: {
		Service: ServiceMailTransport,
		Title:   "Mail transport",
		Packages: map[Family][]string{
			FamilyDebian: {"postfix"},
			FamilyUbuntu: {"postfix"},
			FamilyAlpine: {"postfix"},
			FamilyRHEL:   {"postfix"},
			FamilyFedora: {"postfix"},
			FamilyArch:   {"postfix"},
		},
	},
	ServiceMailDelivery: {
		Service: ServiceMailDelivery,
		Title:   "IMAP/POP3/LMTP",
		Packages: map[Family][]string{
			FamilyDebian: {"dovecot-imapd", "dovecot-pop3d", "dovecot-lmtpd"},
			FamilyUbuntu: {"dovecot-imapd", "dovecot-pop3d", "dovecot-lmtpd"},
			FamilyAlpine: {"dovecot", "dovecot-pop3d", "dovecot-lmtpd"},
			FamilyRHEL:   {"dovecot"},
			FamilyFedora: {"dovecot"},
			FamilyArch:   {"dovecot"},
		},
	},
	ServiceMailFilter: {
		Service: ServiceMailFilter,
		Title:   "Mail filtering",
		Packages: map[Family][]string{
			FamilyDebian: {"amavisd-new"},
			FamilyUbuntu: {"amavisd-new"},
			FamilyAlpine: {"amavis"},
			FamilyRHEL:   {"amavis"},
			FamilyFedora: {"amavis"},
			FamilyArch:   {"amavisd-new"},
		},
	},
	ServiceAntiSpam: {
		Service: ServiceAntiSpam,
		Title:   "Anti-spam",
		Packages: map[Family][]string{
			FamilyDebian: {"spamassassin"},
			FamilyUbuntu: {"spamassassin"},
			FamilyAlpine: {"spamassassin"},
			FamilyRHEL:   {"spamassassin"},
			FamilyFedora: {"spamassassin"},
			FamilyArch:   {"spamassassin"},
		},
	},
	ServiceAntiVirus: {
		Service: ServiceAntiVirus,
		Title:   "Anti-virus daemon",
		Packages: map[Family][]string{
			FamilyDebian: {"clamav-daemon"},
			FamilyUbuntu: {"clamav-daemon"},
			FamilyAlpine: {"clamav-daemon"},
			FamilyRHEL:   {"clamd"},
			FamilyFedora: {"clamd"},
			FamilyArch:   {"clamav"},
		},
	},
	ServiceDKIM: {
		Service: ServiceDKIM,
		Title:   "DKIM signing",
		Packages: map[Family][]string{
			FamilyDebian: {"opendkim"},
			FamilyUbuntu: {"opendkim"},
			FamilyAlpine: {"opendkim"},
			FamilyRHEL:   {"opendkim"},
			FamilyFedora: {"opendkim"},
			FamilyArch:   {"opendkim"},
		},
	},
	ServiceDMARC: {
		Service: ServiceDMARC,
		Title:   "DMARC",
		Packages: map[Family][]string{
			FamilyDebian: {"opendmarc"},
			FamilyUbuntu: {"opendmarc"},
			FamilyAlpine: {"opendmarc"},
			FamilyRHEL:   {"opendmarc"},
			FamilyFedora: {"opendmarc"},
			FamilyArch:   {"opendmarc"},
		},
	},
	ServiceDNSServer: {
		Service: ServiceDNSServer,
		Title:   "DNS server",
		Packages: map[Family][]string{
			FamilyDebian: {"bind9"},
			FamilyUbuntu: {"bind9"},
			FamilyAlpine: {"bind"},
			FamilyRHEL:   {"bind"},
			FamilyFedora: {"bind", "bind-utils"},
			FamilyArch:   {"bind"},
		},
	},
	ServiceIntrusion: {
		Service: ServiceIntrusion,
		Title:   "Intrusion prevention",
		Packages: map[Family][]string{
			FamilyDebian: {"fail2ban"},
			FamilyUbuntu: {"fail2ban"},
			FamilyAlpine: {"fail2ban"},
			FamilyRHEL:   {"fail2ban"},
			FamilyFedora: {"fail2ban"},
			FamilyArch:   {"fail2ban"},
		},
	},
	ServiceFirewall: {
		Service: ServiceFirewall,
		Title:   "Firewall",
		Packages: map[Family][]string{
			FamilyDebian: {"nftables"},
			FamilyUbuntu: {"nftables"},
			FamilyAlpine: {"nftables"},
			FamilyRHEL:   {"nftables"},
			FamilyFedora: {"nftables"},
			FamilyArch:   {"nftables"},
		},
	},
	ServiceDocker: {
		Service: ServiceDocker,
		Title:   "Container engine",
		Packages: map[Family][]string{
			FamilyDebian: {"docker-ce", "docker-ce-cli", "containerd.io"},
			FamilyUbuntu: {"docker-ce", "docker-ce-cli", "containerd.io"},
			FamilyAlpine: {"docker", "docker-cli"},
			FamilyRHEL:   {"docker-ce", "docker-ce-cli", "containerd.io"},
			FamilyFedora: {"docker-ce", "docker-ce-cli", "containerd.io"},
			FamilyArch:   {"docker"},
		},
	},
	ServiceIncus: {
		Service: ServiceIncus,
		Title:   "Container engine (Incus)",
		Packages: map[Family][]string{
			FamilyDebian: {"incus"},
			FamilyUbuntu: {"incus"},
			FamilyAlpine: {"incus"},
			FamilyRHEL:   {"incus"},
			FamilyFedora: {"incus"},
			FamilyArch:   {"incus"},
		},
	},
	ServicePodman: {
		Service: ServicePodman,
		Title:   "Container engine (Podman)",
		Packages: map[Family][]string{
			FamilyDebian: {"podman"},
			FamilyUbuntu: {"podman"},
			FamilyAlpine: {"podman"},
			FamilyRHEL:   {"podman"},
			FamilyFedora: {"podman"},
			FamilyArch:   {"podman"},
		},
	},
	ServiceLibvirt: {
		Service: ServiceLibvirt,
		Title:   "VM hypervisor",
		Packages: map[Family][]string{
			FamilyDebian: {"libvirt-daemon-system"},
			FamilyUbuntu: {"libvirt-daemon-system"},
			FamilyAlpine: {"libvirt", "libvirt-daemon", "libvirt-qemu", "libvirt-client"},
			FamilyRHEL:   {"libvirt-daemon-config-network", "libvirt-daemon-kvm"},
			FamilyFedora: {"libvirt-daemon-config-network", "libvirt-daemon-kvm"},
			FamilyArch:   {"libvirt"},
		},
	},
	ServiceQEMU: {
		Service: ServiceQEMU,
		Title:   "VM emulator",
		Packages: map[Family][]string{
			FamilyDebian: {"qemu-system-x86", "qemu-system-arm"},
			FamilyUbuntu: {"qemu-system-x86", "qemu-system-arm"},
			FamilyAlpine: {"qemu-system-x86_64", "qemu-system-aarch64"},
			FamilyRHEL:   {"qemu-kvm"},
			FamilyFedora: {"qemu-kvm"},
			FamilyArch:   {"qemu-full"},
		},
	},
	ServiceVirtInstall: {
		Service: ServiceVirtInstall,
		Title:   "VM install helper",
		Packages: map[Family][]string{
			FamilyDebian: {"virtinst"},
			FamilyUbuntu: {"virtinst"},
			FamilyAlpine: {"virt-install"},
			FamilyRHEL:   {"virt-install"},
			FamilyFedora: {"virt-install"},
			FamilyArch:   {"virt-install"},
		},
	},
	ServiceOVMF: {
		Service: ServiceOVMF,
		Title:   "VM UEFI firmware",
		Packages: map[Family][]string{
			FamilyDebian: {"ovmf"},
			FamilyUbuntu: {"ovmf"},
			FamilyAlpine: {"ovmf"},
			FamilyRHEL:   {"edk2-ovmf"},
			FamilyFedora: {"edk2-ovmf"},
			FamilyArch:   {"edk2-ovmf"},
		},
	},
}

// Services lists every managed service identifier in a stable order.
func Services() []Service {
	names := make([]string, 0, len(serviceTable))
	for svc := range serviceTable {
		names = append(names, string(svc))
	}
	sort.Strings(names)

	out := make([]Service, 0, len(names))
	for _, name := range names {
		out = append(out, Service(name))
	}

	return out
}

// LookupService returns the mapping table row for a service.
func LookupService(svc Service) (ServiceDef, error) {
	def, ok := serviceTable[svc]
	if !ok {
		return ServiceDef{}, fail(ErrServiceUnknown, apperr.CodeNotFound, http.StatusNotFound,
			"unknown managed service")
	}

	return def, nil
}

// PackagesFor returns the package set a service needs on a distribution. A
// service with no package on that distribution is a typed error, never an
// empty success.
func PackagesFor(svc Service, d *Distro) ([]string, error) {
	if d == nil {
		return nil, failUnavailable(ErrUnsupportedDistro, "host operating system is not supported")
	}

	def, err := LookupService(svc)
	if err != nil {
		return nil, err
	}
	if def.Templated {
		return nil, failValidation(ErrInvalidVersion, "this service requires an explicit version")
	}

	pkgs, ok := def.Packages[d.Family]
	if !ok || len(pkgs) == 0 {
		return nil, serviceNotAvailable(svc, d)
	}

	return append([]string(nil), pkgs...), nil
}

// serviceNotAvailable builds the typed "not available on this distribution"
// error, carrying only non-sensitive identifiers as details.
func serviceNotAvailable(svc Service, d *Distro) error {
	return fail(ErrServiceNotAvailable, apperr.CodeUnavailable, http.StatusServiceUnavailable,
		"this service is not available on the host operating system").
		WithDetails(map[string]any{"service": string(svc), "distribution": d.ID})
}
