package orchestrator

import (
	"math"
	"path/filepath"
	"sort"
	"strings"

	"github.com/webappsgo/cashp/src/security"
)

// Profile is the isolation contract for one service-hosting class. Every
// field is set explicitly by the constructor for its class: nothing about a
// workload's privilege level is ever inherited by accident or defaulted
// into existence by a zero value that happens to mean "allowed".
type Profile struct {
	// Class is the service-hosting class this profile governs.
	Class Class
	// AllowPrivileged permits a privileged workload.
	AllowPrivileged bool
	// AllowHostNetwork permits sharing the host network namespace.
	AllowHostNetwork bool
	// AllowHostPaths permits an absolute host path as a mount source.
	AllowHostPaths bool
	// DropCapabilities is the capability set removed before start.
	DropCapabilities []string
	// AllowedAddCapabilities is the exhaustive set a spec may add back.
	AllowedAddCapabilities []string
	// NoNewPrivileges sets the no_new_privs bit on the workload.
	NoNewPrivileges bool
	// DefaultPidsLimit is applied when the spec sets no explicit limit.
	DefaultPidsLimit int64
}

// baseAddCapabilities is the only set of Linux capabilities any workload
// may ask for. Every capability that grants host visibility or kernel
// control — SYS_ADMIN, SYS_MODULE, SYS_RAWIO, SYS_PTRACE, SYS_BOOT,
// SYS_TIME, SYS_CHROOT, NET_ADMIN, NET_RAW, MKNOD, DAC_READ_SEARCH — is
// absent by construction and cannot be granted through any code path.
var baseAddCapabilities = []string{
	"CHOWN",
	"DAC_OVERRIDE",
	"FOWNER",
	"FSETID",
	"KILL",
	"NET_BIND_SERVICE",
	"SETGID",
	"SETUID",
}

// TenantProfile is the isolation profile for tenant-defined workloads.
// IDEA.md treats tenant PaaS source, container images, and VM images as
// untrusted arbitrary code, contained by isolation rather than inspection,
// so this profile permits nothing beyond an unprivileged, capability-shorn
// workload on a per-tenant network with mounts confined to the tenant's own
// storage root.
func TenantProfile() Profile {
	return Profile{
		Class:                  ClassTenant,
		AllowPrivileged:        false,
		AllowHostNetwork:       false,
		AllowHostPaths:         false,
		DropCapabilities:       []string{"ALL"},
		AllowedAddCapabilities: append([]string(nil), baseAddCapabilities...),
		NoNewPrivileges:        true,
		DefaultPidsLimit:       512,
	}
}

// AppManagedProfile is the isolation profile for cashp's own service
// containers (PostgreSQL, MariaDB, MongoDB, Valkey). It differs from the
// tenant profile in exactly one respect and that difference is deliberate:
// an app-managed container may mount an absolute host path, because
// IDEA.md requires its volumes to map to the standard OS data directories.
// Those paths are still confined to the operator-configured data roots and
// still pass the unconditional engine-socket deny list. Privileged mode,
// host networking, and every escalating capability stay forbidden here
// exactly as they are for a tenant.
func AppManagedProfile() Profile {
	return Profile{
		Class:            ClassAppManaged,
		AllowPrivileged:  false,
		AllowHostNetwork: false,
		AllowHostPaths:   true,
		DropCapabilities: []string{"ALL"},
		// Database images chown their data directory and drop to an
		// unprivileged uid at startup, which needs SETPCAP and SETFCAP on
		// top of the base set.
		AllowedAddCapabilities: append(append([]string(nil), baseAddCapabilities...), "SETFCAP", "SETPCAP"),
		NoNewPrivileges:        true,
		DefaultPidsLimit:       2048,
	}
}

// ProfileFor returns the profile governing a class. A native host service
// has no orchestration profile at all: it is a distro package under
// systemd or OpenRC and is refused here rather than being quietly turned
// into a container.
func ProfileFor(class Class) (Profile, error) {
	switch class {
	case ClassTenant:
		return TenantProfile(), nil
	case ClassAppManaged:
		return AppManagedProfile(), nil
	case ClassNative:
		return Profile{}, isolationErr(ClassNative, "class",
			"native host services are managed by the service supervisor, not the orchestrator")
	default:
		return Profile{}, validationErr("class", "unknown")
	}
}

// allowsCapability reports whether the profile permits adding cap.
func (p Profile) allowsCapability(capName string) bool {
	for _, allowed := range p.AllowedAddCapabilities {
		if allowed == capName {
			return true
		}
	}
	return false
}

// resolvedMount is a mount whose source has been resolved to a concrete,
// deny-list-checked absolute host path.
type resolvedMount struct {
	// HostPath is the resolved absolute source on the host.
	HostPath string
	// Target is the absolute destination inside the workload.
	Target string
	// ReadOnly mounts without write access.
	ReadOnly bool
}

// resolvedDisk is a VM disk whose image path has been resolved.
type resolvedDisk struct {
	// HostPath is the resolved absolute image path on the host.
	HostPath string
	// Format is the validated image format.
	Format string
	// Target is the guest device name.
	Target string
	// Bus is the guest bus.
	Bus string
	// ReadOnly attaches without write access.
	ReadOnly bool
	// Boot marks the boot device.
	Boot bool
}

// resolvedSpec is a Spec that has passed validation and profile
// enforcement. Backends consume this type and never the raw Spec, so no
// backend can accidentally skip a check by reading a caller-supplied field
// directly.
type resolvedSpec struct {
	// Spec is the original request, retained for the fields that need no
	// transformation.
	Spec Spec
	// Profile is the isolation profile that was enforced.
	Profile Profile
	// Qualified is the derived backend-visible object name.
	Qualified string
	// Mounts are the resolved container mounts.
	Mounts []resolvedMount
	// Disks are the resolved VM disks.
	Disks []resolvedDisk
	// Labels are the cashp labels merged with the caller's own.
	Labels map[string]string
	// AddCapabilities is the profile-approved capability add list.
	AddCapabilities []string
	// PidsLimit is the effective process limit.
	PidsLimit int64
	// VCPUs is the whole-core count derived for VM backends.
	VCPUs int
	// NetworkName is the validated per-tenant network, empty when the
	// workload has no network.
	NetworkName string
}

// tenantRoot returns the storage root belonging to one tenant. It is built
// with security.SafeJoin so a crafted tenant identifier can never walk out
// of the configured root, even though the identifier has already passed the
// allowlist.
func (c Config) tenantRoot(class Class, tenantID string) (string, error) {
	if c.TenantVolumeRoot == "" {
		return "", validationErr("volumes", "no_volume_root_configured")
	}
	segment := tenantID
	if class == ClassAppManaged {
		segment = SystemTenantID
	}
	root, err := security.SafeJoin(c.TenantVolumeRoot, segment)
	if err != nil {
		return "", validationErr("volumes", "tenant_root_escape")
	}
	return root, nil
}

// resolveSource turns a spec-supplied mount or disk source into a concrete
// host path. A relative source always resolves under the owning tenant's
// storage root. An absolute source is only considered when the profile
// allows host paths, and then only when it sits inside one of the
// operator-configured application data roots.
func (c Config) resolveSource(p Profile, ref Ref, field, source string) (string, error) {
	if source == "" {
		return "", validationErr(field, "required")
	}
	if hasUnsafeChars(source) {
		return "", validationErr(field, "charset")
	}

	if filepath.IsAbs(source) {
		if !p.AllowHostPaths {
			return "", isolationErr(p.Class, field,
				"tenant volumes resolve under the tenant storage root; absolute host paths are refused")
		}
		clean := filepath.Clean(source)
		if err := ValidateHostPath(field, clean); err != nil {
			return "", err
		}
		if !c.underAppDataRoot(clean) {
			return "", isolationErr(p.Class, field,
				"app-managed volumes must resolve under a configured application data root")
		}
		return clean, nil
	}

	root, err := c.tenantRoot(ref.Class, ref.TenantID)
	if err != nil {
		return "", err
	}
	joined, err := security.SafeJoin(root, source)
	if err != nil {
		return "", validationErr(field, "path_escape")
	}
	if err := ValidateHostPath(field, joined); err != nil {
		return "", err
	}
	return joined, nil
}

// underAppDataRoot reports whether an absolute path sits inside one of the
// operator-configured application data roots.
func (c Config) underAppDataRoot(p string) bool {
	for _, root := range c.AppDataRoots {
		if root == "" {
			continue
		}
		clean := filepath.Clean(root)
		if p == clean || strings.HasPrefix(p, clean+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// resolveSpec validates a create request end to end and enforces the class
// isolation profile. It is the single gate every backend's Create call goes
// through; there is no path into a backend that bypasses it.
func (c Config) resolveSpec(spec Spec) (resolvedSpec, error) {
	var out resolvedSpec

	profile, err := ProfileFor(spec.Ref.Class)
	if err != nil {
		return out, err
	}

	qualified, err := spec.Ref.Qualified()
	if err != nil {
		return out, err
	}

	switch spec.Kind {
	case KindContainer, KindVM:
	default:
		return out, validationErr("kind", "unknown")
	}

	if err := ValidateImageRef(spec.Image); err != nil {
		return out, err
	}
	if err := ValidateDigest(spec.ImageDigest); err != nil {
		return out, err
	}
	if len(spec.Command) > 0 {
		if err := ValidateArgv(spec.Command); err != nil {
			return out, err
		}
	}
	if err := ValidateEnv(spec.Env); err != nil {
		return out, err
	}
	if err := ValidateArchitecture(spec.Architecture); err != nil {
		return out, err
	}
	if spec.WorkingDir != "" {
		if err := ValidateGuestPath("working_dir", spec.WorkingDir); err != nil {
			return out, err
		}
	}
	if spec.User != "" && hasUnsafeChars(spec.User) {
		return out, validationErr("user", "charset")
	}
	switch spec.Restart {
	case RestartNever, RestartOnFailure, RestartAlways, "":
	default:
		return out, validationErr("restart", "unsupported")
	}
	switch spec.Firmware {
	case FirmwareBIOS, FirmwareUEFI, "":
	default:
		return out, validationErr("firmware", "unsupported")
	}

	if spec.Privileged && !profile.AllowPrivileged {
		return out, isolationErr(profile.Class, "privileged",
			"privileged workloads are never permitted for this class")
	}

	networkName, err := c.resolveNetwork(profile, spec)
	if err != nil {
		return out, err
	}

	adds, err := resolveCapabilities(profile, spec.AddCapabilities)
	if err != nil {
		return out, err
	}

	mounts, err := c.resolveMounts(profile, spec)
	if err != nil {
		return out, err
	}

	disks, err := c.resolveDisks(profile, spec)
	if err != nil {
		return out, err
	}

	if err := validateResources(spec.Resources); err != nil {
		return out, err
	}

	pids := spec.Resources.PidsLimit
	if pids <= 0 {
		pids = profile.DefaultPidsLimit
	}

	vcpus := 1
	if spec.Resources.CPUCores > 0 {
		vcpus = int(math.Ceil(spec.Resources.CPUCores))
	}

	out = resolvedSpec{
		Spec:            spec,
		Profile:         profile,
		Qualified:       qualified,
		Mounts:          mounts,
		Disks:           disks,
		Labels:          buildLabels(spec),
		AddCapabilities: adds,
		PidsLimit:       pids,
		VCPUs:           vcpus,
		NetworkName:     networkName,
	}
	return out, nil
}

// resolveNetwork enforces the networking half of the isolation profile and
// returns the validated per-tenant network name.
func (c Config) resolveNetwork(p Profile, spec Spec) (string, error) {
	switch spec.Network.Mode {
	case NetworkHost:
		if !p.AllowHostNetwork {
			return "", isolationErr(p.Class, "network.mode",
				"host networking would expose every other workload on this node")
		}
		return "", nil
	case NetworkNone:
		if len(spec.Network.Ports) > 0 {
			return "", validationErr("network.ports", "no_network")
		}
		return "", nil
	case NetworkBridge, "":
	default:
		return "", validationErr("network.mode", "unsupported")
	}

	name := spec.Network.Name
	if name == "" {
		derived, err := c.defaultNetworkName(spec.Ref)
		if err != nil {
			return "", err
		}
		name = derived
	}
	if err := ValidateNetworkName(name); err != nil {
		return "", err
	}

	if len(spec.Network.Ports) > MaxPorts {
		return "", validationErr("network.ports", "too_many")
	}
	for _, port := range spec.Network.Ports {
		if err := ValidatePort(port); err != nil {
			return "", err
		}
	}
	return name, nil
}

// defaultNetworkName derives the per-tenant network a workload joins when
// the caller named none, keeping tenant traffic off any shared bridge.
func (c Config) defaultNetworkName(ref Ref) (string, error) {
	if ref.Class == ClassAppManaged {
		return namePrefix + "-" + SystemTenantID, nil
	}
	if err := ValidateTenantID(ref.TenantID); err != nil {
		return "", err
	}
	return namePrefix + "-t-" + ref.TenantID, nil
}

// resolveCapabilities checks every requested capability against the class
// allowlist and returns the deduplicated, sorted add list.
func resolveCapabilities(p Profile, requested []string) ([]string, error) {
	if len(requested) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(requested))
	out := make([]string, 0, len(requested))
	for _, raw := range requested {
		capName := strings.ToUpper(strings.TrimSpace(raw))
		capName = strings.TrimPrefix(capName, "CAP_")
		if capName == "" || hasUnsafeChars(capName) {
			return nil, validationErr("add_capabilities", "charset")
		}
		if !p.allowsCapability(capName) {
			return nil, isolationErr(p.Class, "add_capabilities",
				"the requested capability is outside the class allowlist")
		}
		if !seen[capName] {
			seen[capName] = true
			out = append(out, capName)
		}
	}
	sort.Strings(out)
	return out, nil
}

// resolveMounts validates and resolves every container mount.
func (c Config) resolveMounts(p Profile, spec Spec) ([]resolvedMount, error) {
	if len(spec.Volumes) == 0 {
		return nil, nil
	}
	if spec.Kind != KindContainer {
		return nil, validationErr("volumes", "containers_only")
	}
	if len(spec.Volumes) > MaxVolumes {
		return nil, validationErr("volumes", "too_many")
	}
	out := make([]resolvedMount, 0, len(spec.Volumes))
	targets := make(map[string]bool, len(spec.Volumes))
	for _, v := range spec.Volumes {
		host, err := c.resolveSource(p, spec.Ref, "volumes.source", v.Source)
		if err != nil {
			return nil, err
		}
		if err := ValidateGuestPath("volumes.target", v.Target); err != nil {
			return nil, err
		}
		if targets[v.Target] {
			return nil, validationErr("volumes.target", "duplicate")
		}
		targets[v.Target] = true
		out = append(out, resolvedMount{HostPath: host, Target: v.Target, ReadOnly: v.ReadOnly})
	}
	return out, nil
}

// resolveDisks validates and resolves every VM disk.
func (c Config) resolveDisks(p Profile, spec Spec) ([]resolvedDisk, error) {
	if len(spec.Disks) == 0 {
		return nil, nil
	}
	if spec.Kind != KindVM {
		return nil, validationErr("disks", "vms_only")
	}
	if len(spec.Disks) > MaxVolumes {
		return nil, validationErr("disks", "too_many")
	}
	out := make([]resolvedDisk, 0, len(spec.Disks))
	targets := make(map[string]bool, len(spec.Disks))
	boots := 0
	for _, d := range spec.Disks {
		host, err := c.resolveSource(p, spec.Ref, "disks.source", d.Source)
		if err != nil {
			return nil, err
		}
		if err := ValidateDiskFormat(d.Format); err != nil {
			return nil, err
		}
		if err := ValidateDiskTarget(d.Target); err != nil {
			return nil, err
		}
		if err := ValidateDiskBus(d.Bus); err != nil {
			return nil, err
		}
		if targets[d.Target] {
			return nil, validationErr("disks.target", "duplicate")
		}
		targets[d.Target] = true
		bus := d.Bus
		if bus == "" {
			bus = "virtio"
		}
		if d.Boot {
			boots++
		}
		out = append(out, resolvedDisk{
			HostPath: host,
			Format:   d.Format,
			Target:   d.Target,
			Bus:      bus,
			ReadOnly: d.ReadOnly,
			Boot:     d.Boot,
		})
	}
	if boots > 1 {
		return nil, validationErr("disks.boot", "multiple_boot_disks")
	}
	return out, nil
}

// validateResources rejects negative or absurd resource requests before
// they reach a backend.
func validateResources(r Resources) error {
	if r.CPUCores < 0 || r.CPUCores > 1024 {
		return validationErr("resources.cpu_cores", "range")
	}
	if r.MemoryBytes < 0 {
		return validationErr("resources.memory_bytes", "range")
	}
	if r.DiskBytes < 0 {
		return validationErr("resources.disk_bytes", "range")
	}
	if r.PidsLimit < 0 {
		return validationErr("resources.pids_limit", "range")
	}
	return nil
}

// buildLabels merges the caller's labels under the cashp label namespace.
// The cashp keys are written last so a caller can never forge ownership by
// supplying a label of their own.
func buildLabels(spec Spec) map[string]string {
	labels := make(map[string]string, len(spec.Labels)+4)
	for k, v := range spec.Labels {
		if k == "" || strings.HasPrefix(k, namePrefix+".") {
			continue
		}
		if hasUnsafeChars(k) || strings.ContainsAny(v, "\x00\n\r") {
			continue
		}
		labels[k] = v
	}
	labels[LabelManaged] = "true"
	labels[LabelTenant] = spec.Ref.TenantID
	labels[LabelClass] = string(spec.Ref.Class)
	labels[LabelWorkload] = spec.Ref.Name
	return labels
}
