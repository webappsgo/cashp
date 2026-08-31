package orchestrator

import (
	"bytes"
	"encoding/xml"
)

// domainNamespace is the XML namespace cashp stamps its ownership metadata
// with inside a libvirt domain. libvirt preserves foreign-namespace metadata
// verbatim, which makes it a durable place to record which account a domain
// belongs to.
const domainNamespace = "https://cashp.dev/xmlns/orchestrator/1.0"

// Domain defaults. They describe an ordinary modern KVM guest; anything a
// caller can influence arrives through the resolved spec, already validated.
const (
	// domainType is the libvirt domain type this package emits.
	domainType = "kvm"
	// domainMachine is the guest machine model.
	domainMachine = "q35"
	// domainDefaultArch is the guest architecture used when a spec names none.
	domainDefaultArch = "x86_64"
	// domainOSType marks a fully virtualized guest.
	domainOSType = "hvm"
	// domainDefaultLoader is the UEFI firmware image used for a UEFI guest.
	domainDefaultLoader = "/usr/share/OVMF/OVMF_CODE.fd"
	// bytesPerKiB converts a byte count to the unit libvirt wants.
	bytesPerKiB = 1024
)

// domainXML is a libvirt domain definition.
//
// The definition is produced by marshaling this structure, never by
// formatting a string. That is deliberate: a domain definition embeds
// tenant-influenced values such as disk paths and a network name, and
// encoding/xml is what guarantees each of them is escaped and stays inside
// the element it was assigned to.
type domainXML struct {
	XMLName       xml.Name       `xml:"domain"`
	Type          string         `xml:"type,attr"`
	Name          string         `xml:"name"`
	UUID          string         `xml:"uuid,omitempty"`
	Metadata      domainMetadata `xml:"metadata"`
	Memory        domainMemory   `xml:"memory"`
	CurrentMemory domainMemory   `xml:"currentMemory"`
	VCPU          domainVCPU     `xml:"vcpu"`
	OS            domainOS       `xml:"os"`
	Features      domainFeatures `xml:"features"`
	Clock         domainClock    `xml:"clock"`
	OnPoweroff    string         `xml:"on_poweroff"`
	OnReboot      string         `xml:"on_reboot"`
	OnCrash       string         `xml:"on_crash"`
	Devices       domainDevices  `xml:"devices"`
}

// domainMetadata carries the cashp ownership block.
type domainMetadata struct {
	Workload *workloadMetadata
}

// workloadMetadata records which account and which workload class a domain
// belongs to, so ownership survives a daemon restart and can be re-checked
// on every read.
type workloadMetadata struct {
	XMLName xml.Name `xml:"https://cashp.dev/xmlns/orchestrator/1.0 workload"`
	Managed string   `xml:"managed"`
	Tenant  string   `xml:"tenant"`
	Class   string   `xml:"class"`
	Name    string   `xml:"name"`
}

// domainMemory is a memory quantity with its unit.
type domainMemory struct {
	Unit  string `xml:"unit,attr"`
	Value int64  `xml:",chardata"`
}

// domainVCPU is the virtual CPU allocation.
type domainVCPU struct {
	Placement string `xml:"placement,attr"`
	Value     int    `xml:",chardata"`
}

// domainOS is the guest boot configuration.
type domainOS struct {
	Type   domainOSTypeElement `xml:"type"`
	Loader *domainLoader       `xml:"loader,omitempty"`
	Boot   []domainBoot        `xml:"boot"`
}

// domainOSTypeElement names the guest architecture and machine model.
type domainOSTypeElement struct {
	Arch    string `xml:"arch,attr"`
	Machine string `xml:"machine,attr"`
	Value   string `xml:",chardata"`
}

// domainLoader points at the guest firmware image.
type domainLoader struct {
	ReadOnly string `xml:"readonly,attr"`
	Type     string `xml:"type,attr"`
	Secure   string `xml:"secure,attr,omitempty"`
	Path     string `xml:",chardata"`
}

// domainBoot selects a boot device.
type domainBoot struct {
	Dev string `xml:"dev,attr"`
}

// domainFeatures enables the standard guest feature set.
type domainFeatures struct {
	ACPI *struct{} `xml:"acpi"`
	APIC *struct{} `xml:"apic"`
}

// domainClock sets the guest clock policy.
type domainClock struct {
	Offset string `xml:"offset,attr"`
}

// domainDevices is the guest device set.
type domainDevices struct {
	Disks      []domainDisk      `xml:"disk"`
	Interfaces []domainInterface `xml:"interface"`
	Serials    []domainSerial    `xml:"serial"`
	Consoles   []domainConsole   `xml:"console"`
	MemBalloon domainMemBalloon  `xml:"memballoon"`
	RNG        *domainRNG        `xml:"rng,omitempty"`
}

// domainDisk is one attached block device.
type domainDisk struct {
	Type     string           `xml:"type,attr"`
	Device   string           `xml:"device,attr"`
	Driver   domainDiskDriver `xml:"driver"`
	Source   domainDiskSource `xml:"source"`
	Target   domainDiskTarget `xml:"target"`
	ReadOnly *struct{}        `xml:"readonly,omitempty"`
	Boot     *domainDiskBoot  `xml:"boot,omitempty"`
}

// domainDiskDriver selects the image format.
type domainDiskDriver struct {
	Name string `xml:"name,attr"`
	Type string `xml:"type,attr"`
}

// domainDiskSource points at the backing image file.
type domainDiskSource struct {
	File string `xml:"file,attr"`
}

// domainDiskTarget names the guest device and bus.
type domainDiskTarget struct {
	Dev string `xml:"dev,attr"`
	Bus string `xml:"bus,attr"`
}

// domainDiskBoot orders a disk in the boot sequence.
type domainDiskBoot struct {
	Order int `xml:"order,attr"`
}

// domainInterface is one guest network interface.
type domainInterface struct {
	Type   string                `xml:"type,attr"`
	Source domainInterfaceSource `xml:"source"`
	Model  domainInterfaceModel  `xml:"model"`
}

// domainInterfaceSource names the libvirt network to attach to.
type domainInterfaceSource struct {
	Network string `xml:"network,attr"`
}

// domainInterfaceModel selects the emulated NIC model.
type domainInterfaceModel struct {
	Type string `xml:"type,attr"`
}

// domainSerial is the guest serial port.
type domainSerial struct {
	Type   string             `xml:"type,attr"`
	Target domainSerialTarget `xml:"target"`
}

// domainSerialTarget indexes a serial port.
type domainSerialTarget struct {
	Port int `xml:"port,attr"`
}

// domainConsole is the guest console.
type domainConsole struct {
	Type   string              `xml:"type,attr"`
	Target domainConsoleTarget `xml:"target"`
}

// domainConsoleTarget types and indexes a console.
type domainConsoleTarget struct {
	Type string `xml:"type,attr"`
	Port int    `xml:"port,attr"`
}

// domainMemBalloon selects the balloon device model.
type domainMemBalloon struct {
	Model string `xml:"model,attr"`
}

// domainRNG gives the guest an entropy source.
type domainRNG struct {
	Model   string          `xml:"model,attr"`
	Backend domainRNGSource `xml:"backend"`
}

// domainRNGSource names the host entropy backend.
type domainRNGSource struct {
	Model string `xml:"model,attr"`
	Value string `xml:",chardata"`
}

// buildDomainXML renders a resolved spec as a libvirt domain definition and
// verifies the result round-trips back to the same values. The round trip is
// the validation step: it proves every tenant-influenced value landed in the
// element it was assigned to and came back unchanged.
func buildDomainXML(cfg Config, spec resolvedSpec) ([]byte, error) {
	if spec.Spec.Kind != KindVM {
		return nil, unsupportedErr(BackendLibvirt, string(KindContainer))
	}
	if len(spec.Disks) == 0 {
		return nil, validationErr("disks", "required")
	}
	if spec.Spec.Resources.MemoryBytes <= 0 {
		return nil, validationErr("memory_bytes", "required")
	}

	arch := spec.Spec.Architecture
	if arch == "" {
		arch = domainDefaultArch
	}
	vcpus := spec.VCPUs
	if vcpus <= 0 {
		vcpus = 1
	}
	kib := spec.Spec.Resources.MemoryBytes / bytesPerKiB
	if kib <= 0 {
		return nil, validationErr("memory_bytes", "too_small")
	}

	domain := domainXML{
		Type: domainType,
		Name: spec.Qualified,
		Metadata: domainMetadata{Workload: &workloadMetadata{
			Managed: "true",
			Tenant:  spec.Spec.Ref.TenantID,
			Class:   string(spec.Spec.Ref.Class),
			Name:    spec.Spec.Ref.Name,
		}},
		Memory:        domainMemory{Unit: "KiB", Value: kib},
		CurrentMemory: domainMemory{Unit: "KiB", Value: kib},
		VCPU:          domainVCPU{Placement: "static", Value: vcpus},
		OS: domainOS{
			Type: domainOSTypeElement{Arch: arch, Machine: domainMachine, Value: domainOSType},
			Boot: []domainBoot{{Dev: "hd"}},
		},
		Features:   domainFeatures{ACPI: &struct{}{}, APIC: &struct{}{}},
		Clock:      domainClock{Offset: "utc"},
		OnPoweroff: "destroy",
		OnReboot:   "restart",
		OnCrash:    "destroy",
		Devices: domainDevices{
			Serials:    []domainSerial{{Type: "pty", Target: domainSerialTarget{Port: 0}}},
			Consoles:   []domainConsole{{Type: "pty", Target: domainConsoleTarget{Type: "serial", Port: 0}}},
			MemBalloon: domainMemBalloon{Model: "virtio"},
			RNG: &domainRNG{
				Model:   "virtio",
				Backend: domainRNGSource{Model: "random", Value: "/dev/urandom"},
			},
		},
	}

	if spec.Spec.Firmware == FirmwareUEFI {
		loader := cfg.LibvirtUEFILoader
		if loader == "" {
			loader = domainDefaultLoader
		}
		if err := ValidateHostPath("uefi_loader", loader); err != nil {
			return nil, err
		}
		domain.OS.Loader = &domainLoader{ReadOnly: "yes", Type: "pflash", Secure: "no", Path: loader}
	}

	bootOrder := 0
	for _, disk := range spec.Disks {
		entry := domainDisk{
			Type:   "file",
			Device: "disk",
			Driver: domainDiskDriver{Name: "qemu", Type: disk.Format},
			Source: domainDiskSource{File: disk.HostPath},
			Target: domainDiskTarget{Dev: disk.Target, Bus: disk.Bus},
		}
		if disk.ReadOnly {
			entry.ReadOnly = &struct{}{}
		}
		if disk.Boot {
			bootOrder++
			entry.Boot = &domainDiskBoot{Order: bootOrder}
		}
		domain.Devices.Disks = append(domain.Devices.Disks, entry)
	}

	if spec.Spec.Network.Mode != NetworkNone && spec.NetworkName != "" {
		domain.Devices.Interfaces = append(domain.Devices.Interfaces, domainInterface{
			Type:   "network",
			Source: domainInterfaceSource{Network: spec.NetworkName},
			Model:  domainInterfaceModel{Type: "virtio"},
		})
	}

	encoded, err := xml.MarshalIndent(domain, "", "  ")
	if err != nil {
		return nil, backendErr(BackendLibvirt, "encode_domain", err)
	}
	encoded = append(encoded, '\n')

	if err := validateDomainXML(encoded, spec); err != nil {
		return nil, err
	}
	return encoded, nil
}

// validateDomainXML re-parses generated XML and checks that the values that
// matter for isolation survived the encoder unchanged. A mismatch means the
// document does not describe the workload that was asked for, so it is never
// handed to the hypervisor.
func validateDomainXML(encoded []byte, spec resolvedSpec) error {
	if bytes.Contains(encoded, []byte("]]>")) {
		return validationErr("domain_xml", "unsafe_marker")
	}

	var parsed domainXML
	if err := xml.Unmarshal(encoded, &parsed); err != nil {
		return backendErr(BackendLibvirt, "verify_domain", err)
	}
	if parsed.Name != spec.Qualified {
		return validationErr("domain_xml", "name_mismatch")
	}
	if parsed.Metadata.Workload == nil {
		return validationErr("domain_xml", "metadata_missing")
	}
	if parsed.Metadata.Workload.Tenant != spec.Spec.Ref.TenantID {
		return validationErr("domain_xml", "tenant_mismatch")
	}
	if parsed.Metadata.Workload.Class != string(spec.Spec.Ref.Class) {
		return validationErr("domain_xml", "class_mismatch")
	}
	if len(parsed.Devices.Disks) != len(spec.Disks) {
		return validationErr("domain_xml", "disk_count_mismatch")
	}
	for i, disk := range spec.Disks {
		if parsed.Devices.Disks[i].Source.File != disk.HostPath {
			return validationErr("domain_xml", "disk_source_mismatch")
		}
		if parsed.Devices.Disks[i].Target.Dev != disk.Target {
			return validationErr("domain_xml", "disk_target_mismatch")
		}
	}
	if spec.Spec.Network.Mode != NetworkNone && spec.NetworkName != "" {
		if len(parsed.Devices.Interfaces) != 1 {
			return validationErr("domain_xml", "interface_count_mismatch")
		}
		if parsed.Devices.Interfaces[0].Source.Network != spec.NetworkName {
			return validationErr("domain_xml", "network_mismatch")
		}
	} else if len(parsed.Devices.Interfaces) != 0 {
		return validationErr("domain_xml", "unexpected_interface")
	}
	if parsed.VCPU.Value != spec.VCPUs && spec.VCPUs > 0 {
		return validationErr("domain_xml", "vcpu_mismatch")
	}
	expectedKiB := spec.Spec.Resources.MemoryBytes / bytesPerKiB
	if parsed.Memory.Value != expectedKiB {
		return validationErr("domain_xml", "memory_mismatch")
	}
	return nil
}

// decodeDomain parses a domain definition read back from the hypervisor.
func decodeDomain(encoded []byte) (domainXML, error) {
	var parsed domainXML
	if err := xml.Unmarshal(encoded, &parsed); err != nil {
		return domainXML{}, backendErr(BackendLibvirt, "decode_domain", err)
	}
	return parsed, nil
}

// parseDomainOwner reads the cashp ownership block out of an existing domain
// definition, which is how a domain read back from the hypervisor is matched
// to an account.
func parseDomainOwner(parsed domainXML) (Ref, bool) {
	if parsed.Metadata.Workload == nil || parsed.Metadata.Workload.Managed != "true" {
		return Ref{}, false
	}
	ref := Ref{
		Class:    Class(parsed.Metadata.Workload.Class),
		TenantID: parsed.Metadata.Workload.Tenant,
		Name:     parsed.Metadata.Workload.Name,
	}
	if err := ref.Validate(); err != nil {
		return Ref{}, false
	}
	return ref, true
}

// parseDomainResources reads the effective limits back out of a domain
// definition.
func parseDomainResources(parsed domainXML) Resources {
	return Resources{
		CPUCores:    float64(parsed.VCPU.Value),
		MemoryBytes: parsed.Memory.Value * bytesPerKiB,
	}
}
