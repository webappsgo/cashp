package guard

import (
	"path/filepath"
	"strconv"
	"strings"
)

// Backend is a container or virtualization engine cashp drives. All four
// remain first-class and simultaneously usable; the validator applies the
// same isolation posture to every one of them.
type Backend string

// The supported workload backends.
const (
	// BackendDocker is the Docker engine.
	BackendDocker Backend = "docker"
	// BackendPodman is the Podman engine.
	BackendPodman Backend = "podman"
	// BackendIncus is the Incus engine.
	BackendIncus Backend = "incus"
	// BackendLibvirt is libvirt/QEMU-KVM.
	BackendLibvirt Backend = "libvirt"
)

// Valid reports whether b is a supported backend.
func (b Backend) Valid() bool {
	switch b {
	case BackendDocker, BackendPodman, BackendIncus, BackendLibvirt:
		return true
	default:
		return false
	}
}

// deniedCapabilities are Linux capabilities that hand a container a
// practical path to the host or to another tenant's traffic. They are
// refused unconditionally: no billing tier, policy, or operator override
// in this package can add one back.
var deniedCapabilities = map[string]struct{}{
	"AUDIT_CONTROL":   {},
	"BLOCK_SUSPEND":   {},
	"DAC_OVERRIDE":    {},
	"DAC_READ_SEARCH": {},
	"MAC_ADMIN":       {},
	"MAC_OVERRIDE":    {},
	"MKNOD":           {},
	"NET_ADMIN":       {},
	"NET_RAW":         {},
	"SETFCAP":         {},
	"SYS_ADMIN":       {},
	"SYS_BOOT":        {},
	"SYS_MODULE":      {},
	"SYS_PTRACE":      {},
	"SYS_RAWIO":       {},
	"SYS_RESOURCE":    {},
	"SYS_TIME":        {},
	"WAKE_ALARM":      {},
}

// engineSocketNames are the control sockets that, once visible inside a
// workload, are equivalent to root on the host: the tenant can ask the
// engine to start a privileged container for them.
var engineSocketNames = []string{
	"docker.sock",
	"containerd.sock",
	"containerd-shim",
	"podman.sock",
	"crio.sock",
	"libvirt-sock",
	"virtqemud-sock",
	"unix.socket",
	"incus.sock",
	"lxd.sock",
	"buildkitd.sock",
}

// deniedHostPathPrefixes are host directories no tenant workload may ever
// bind-mount, regardless of read-only flags. A read-only mount of /etc or
// /root still discloses credentials, and /proc, /sys, and /dev are direct
// escape surfaces.
var deniedHostPathPrefixes = []string{
	"/boot",
	"/dev",
	"/etc",
	"/home",
	"/lib",
	"/lib64",
	"/proc",
	"/root",
	"/run",
	"/sbin",
	"/sys",
	"/usr",
	"/var/lib/containerd",
	"/var/lib/docker",
	"/var/lib/incus",
	"/var/lib/libvirt",
	"/var/lib/lxd",
	"/var/run",
}

// allowedSysctls is the entire set of sysctls a tenant workload may set.
// Everything outside it, including every kernel.*, vm.*, and fs.* key, is
// refused because those are node-wide and shared with other tenants.
var allowedSysctls = map[string]struct{}{
	"net.ipv4.ip_unprivileged_port_start": {},
	"net.ipv4.tcp_keepalive_intvl":        {},
	"net.ipv4.tcp_keepalive_probes":       {},
	"net.ipv4.tcp_keepalive_time":         {},
}

// unconfinedMarkers are the substrings that identify a security option
// asking for a disabled confinement profile.
var unconfinedMarkers = []string{
	"unconfined",
	"label=disable",
	"label:disable",
	"seccomp=undefined",
	"no-new-privileges=false",
	"no-new-privileges:false",
	"apparmor=",
	"systempaths=unconfined",
}

// noNewPrivileges is the security option every tenant workload must carry,
// so a setuid binary inside the image cannot regain privilege.
const noNewPrivileges = "no-new-privileges:true"

// Mount is a filesystem binding a workload requests.
type Mount struct {
	// Source is the host path.
	Source string
	// Target is the in-workload path.
	Target string
	// ReadOnly marks a read-only binding.
	ReadOnly bool
}

// WorkloadPolicy is the per-installation ceiling the validator enforces
// against. It is data, not behavior: nothing in it can turn a structural
// prohibition off, only lower a numeric ceiling or widen an already
// narrow, explicitly enumerated allowance.
type WorkloadPolicy struct {
	// TenantDataRoot is the absolute directory under which every tenant's own data lives.
	TenantDataRoot string
	// MaxCPUCores is the largest CPU allowance any single workload may request.
	MaxCPUCores float64
	// MaxMemoryBytes is the largest memory allowance any single workload may request.
	MaxMemoryBytes int64
	// MaxPids is the largest process-count allowance any single workload may request.
	MaxPids int64
	// MaxStorageBytes is the largest writable-layer allowance any single workload may request.
	MaxStorageBytes int64
	// MaxVCPUs is the largest vCPU count any single VM may request.
	MaxVCPUs int
	// AllowedDevices is the exact set of host device nodes a workload may be given. Empty means none.
	AllowedDevices []string
	// AllowedPassthrough is the exact set of PCI/USB addresses a VM may be given. Empty means none.
	AllowedPassthrough []string
}

// DefaultWorkloadPolicy returns the conservative ceilings a fresh install
// uses: no device access, no passthrough, and per-workload limits small
// enough to run on the low-power hardware cashp must remain usable on.
func DefaultWorkloadPolicy(tenantDataRoot string) WorkloadPolicy {
	return WorkloadPolicy{
		TenantDataRoot:  tenantDataRoot,
		MaxCPUCores:     4,
		MaxMemoryBytes:  8 << 30,
		MaxPids:         512,
		MaxStorageBytes: 64 << 30,
		MaxVCPUs:        4,
	}
}

// WorkloadSpec is the container or PaaS workload a tenant asked cashp to
// create, as cashp models it before handing anything to an engine. Every
// field is treated as attacker-controlled.
type WorkloadSpec struct {
	// Backend is the engine that will run the workload.
	Backend Backend
	// TenantID is the owning hosting account.
	TenantID string
	// Name is the workload's identifier within the tenant.
	Name string
	// Image is the OCI reference or Incus image alias to run.
	Image string
	// Privileged requests a privileged container.
	Privileged bool
	// HostNetwork requests the host network namespace.
	HostNetwork bool
	// HostPID requests the host PID namespace.
	HostPID bool
	// HostIPC requests the host IPC namespace.
	HostIPC bool
	// HostUTS requests the host UTS namespace.
	HostUTS bool
	// HostUserNS disables user-namespace remapping.
	HostUserNS bool
	// NetworkName is the network the workload joins.
	NetworkName string
	// CapAdd are capabilities the workload asked to keep.
	CapAdd []string
	// CapDrop are capabilities the workload drops; it must contain ALL.
	CapDrop []string
	// SecurityOpt are engine security options.
	SecurityOpt []string
	// Devices are host device nodes the workload asked for.
	Devices []string
	// Mounts are the filesystem bindings the workload asked for.
	Mounts []Mount
	// Sysctls are kernel parameters the workload asked to set.
	Sysctls map[string]string
	// Env is the workload environment.
	Env map[string]string
	// CPUCores is the mandatory CPU limit.
	CPUCores float64
	// MemoryBytes is the mandatory memory limit.
	MemoryBytes int64
	// PidsLimit is the mandatory process-count limit.
	PidsLimit int64
	// StorageBytes is the mandatory writable-layer limit.
	StorageBytes int64
	// ReadOnlyRootFS marks the image layer read-only.
	ReadOnlyRootFS bool
}

// TenantNetworkName returns the per-tenant network a workload must join.
// Every tenant workload is confined to exactly this network, which is what
// prevents one tenant's container from reaching another's over a shared
// bridge.
func TenantNetworkName(tenantID string) string {
	return "cashp-t-" + tenantID
}

// TenantRoot returns the absolute directory that holds a tenant's data
// under the policy's root. It validates the tenant id first, so a
// traversal-shaped id cannot widen the root it derives.
func TenantRoot(policy WorkloadPolicy, tenantID string) (string, error) {
	if !filepath.IsAbs(policy.TenantDataRoot) {
		return "", Deny(ReasonWorkloadUnsafe, "tenant data root is not absolute")
	}
	if err := ValidateIdentifier("tenant id", tenantID); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Clean(policy.TenantDataRoot), tenantID), nil
}

// ValidateWorkload is the mandatory gate before any container workload is
// created on any backend. It returns nil only when the spec satisfies the
// full isolation posture; every other outcome is a *DenyError.
func ValidateWorkload(spec WorkloadSpec, policy WorkloadPolicy) error {
	if !spec.Backend.Valid() {
		return Deny(ReasonWorkloadUnsafe, "unsupported backend "+string(spec.Backend))
	}
	if err := ValidateIdentifier("workload name", spec.Name); err != nil {
		return err
	}
	if err := ValidateImageReference(spec.Image); err != nil {
		return err
	}
	root, err := TenantRoot(policy, spec.TenantID)
	if err != nil {
		return err
	}

	if err := checkNamespaces(spec); err != nil {
		return err
	}
	if err := checkCapabilities(spec); err != nil {
		return err
	}
	if err := checkSecurityOpts(spec.SecurityOpt); err != nil {
		return err
	}
	if err := checkDevices(spec.Devices, policy.AllowedDevices); err != nil {
		return err
	}
	if err := checkMounts(spec.Mounts, root); err != nil {
		return err
	}
	if err := checkSysctls(spec.Sysctls); err != nil {
		return err
	}
	if _, err := ValidateEnvVars(spec.Env); err != nil {
		return err
	}
	if err := checkNetwork(spec); err != nil {
		return err
	}
	// Not "return checkLimits(...)" directly: checkLimits returns a
	// concrete *DenyError, and converting a nil *DenyError straight into
	// the error interface return value produces a non-nil interface with a
	// nil underlying pointer (the classic Go typed-nil footgun) — every
	// caller's `err != nil` check would then wrongly see a denial.
	if err := checkLimits(spec, policy); err != nil {
		return err
	}
	return nil
}

// checkNamespaces refuses every request that would share a host namespace
// or run the workload privileged. Any one of these collapses the isolation
// boundary the whole tenant model rests on.
func checkNamespaces(spec WorkloadSpec) *DenyError {
	switch {
	case spec.Privileged:
		return Deny(ReasonWorkloadUnsafe, "privileged mode is never permitted")
	case spec.HostNetwork:
		return Deny(ReasonWorkloadUnsafe, "host network namespace is never permitted")
	case spec.HostPID:
		return Deny(ReasonWorkloadUnsafe, "host PID namespace is never permitted")
	case spec.HostIPC:
		return Deny(ReasonWorkloadUnsafe, "host IPC namespace is never permitted")
	case spec.HostUTS:
		return Deny(ReasonWorkloadUnsafe, "host UTS namespace is never permitted")
	case spec.HostUserNS:
		return Deny(ReasonWorkloadUnsafe, "host user namespace is never permitted")
	}
	return nil
}

// checkCapabilities requires a full drop and refuses every capability on
// the unconditional denylist, matching case-insensitively and with or
// without the CAP_ prefix so a differently spelled request cannot slip by.
func checkCapabilities(spec WorkloadSpec) *DenyError {
	dropsAll := false
	for _, c := range spec.CapDrop {
		if strings.EqualFold(strings.TrimSpace(c), "ALL") {
			dropsAll = true
			break
		}
	}
	if !dropsAll {
		return Deny(ReasonWorkloadUnsafe, "capabilities must be dropped with ALL")
	}
	for _, c := range spec.CapAdd {
		name := strings.ToUpper(strings.TrimSpace(c))
		name = strings.TrimPrefix(name, "CAP_")
		if name == "" {
			return Deny(ReasonWorkloadUnsafe, "empty capability requested")
		}
		if name == "ALL" {
			return Deny(ReasonWorkloadUnsafe, "capability ALL is never permitted")
		}
		if _, denied := deniedCapabilities[name]; denied {
			return Deny(ReasonWorkloadUnsafe, "capability "+name+" is never permitted")
		}
	}
	return nil
}

// checkSecurityOpts refuses any option that disables a confinement profile
// and requires the no-new-privileges option to be present.
func checkSecurityOpts(opts []string) *DenyError {
	hasNoNewPrivs := false
	for _, opt := range opts {
		normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(opt), " ", ""))
		if normalized == noNewPrivileges || normalized == "no-new-privileges=true" {
			hasNoNewPrivs = true
			continue
		}
		for _, marker := range unconfinedMarkers {
			if strings.Contains(normalized, marker) {
				return Deny(ReasonWorkloadUnsafe, "security option "+opt+" weakens confinement")
			}
		}
	}
	if !hasNoNewPrivs {
		return Deny(ReasonWorkloadUnsafe, "security options must include "+noNewPrivileges)
	}
	return nil
}

// checkDevices permits only device nodes the policy enumerated exactly.
// The default policy enumerates none, so a fresh install grants no device
// access at all.
func checkDevices(requested, allowed []string) *DenyError {
	for _, d := range requested {
		device := strings.TrimSpace(d)
		if device == "" {
			return Deny(ReasonWorkloadUnsafe, "empty device requested")
		}
		permitted := false
		for _, a := range allowed {
			if device == a {
				permitted = true
				break
			}
		}
		if !permitted {
			return Deny(ReasonWorkloadUnsafe, "device "+device+" is not in the allowed set")
		}
	}
	return nil
}

// checkMounts confines every bind mount to the tenant's own directory. The
// source must be an absolute, already-clean path inside that directory,
// which rejects traversal, symlink-shaped relative sources, and the engine
// control sockets in one rule set.
func checkMounts(mounts []Mount, tenantRoot string) *DenyError {
	prefix := tenantRoot + string(filepath.Separator)
	for _, m := range mounts {
		src := m.Source
		if src == "" || m.Target == "" {
			return Deny(ReasonWorkloadUnsafe, "mount is missing a source or target")
		}
		if strings.ContainsRune(src, 0) || strings.ContainsRune(m.Target, 0) {
			return Deny(ReasonWorkloadUnsafe, "mount path contains a null byte")
		}
		if !filepath.IsAbs(src) {
			return Deny(ReasonWorkloadUnsafe, "mount source "+src+" is not absolute")
		}
		if filepath.Clean(src) != src {
			return Deny(ReasonWorkloadUnsafe, "mount source "+src+" is not a clean path")
		}
		base := strings.ToLower(filepath.Base(src))
		for _, sock := range engineSocketNames {
			if strings.Contains(base, sock) {
				return Deny(ReasonWorkloadUnsafe, "mount source "+src+" is an engine control socket")
			}
		}
		for _, denied := range deniedHostPathPrefixes {
			if src == denied || strings.HasPrefix(src, denied+"/") {
				return Deny(ReasonWorkloadUnsafe, "mount source "+src+" is a protected host path")
			}
		}
		if src != tenantRoot && !strings.HasPrefix(src, prefix) {
			return Deny(ReasonWorkloadUnsafe, "mount source "+src+" is outside the tenant directory")
		}
		if !filepath.IsAbs(m.Target) || filepath.Clean(m.Target) != m.Target {
			return Deny(ReasonWorkloadUnsafe, "mount target "+m.Target+" is not a clean absolute path")
		}
	}
	return nil
}

// checkSysctls permits only the small, explicitly enumerated set that is
// safe to set per workload.
func checkSysctls(sysctls map[string]string) *DenyError {
	for key, value := range sysctls {
		if _, ok := allowedSysctls[key]; !ok {
			return Deny(ReasonWorkloadUnsafe, "sysctl "+key+" is not permitted")
		}
		if err := ValidateEnvVarValue(value); err != nil {
			return Deny(ReasonWorkloadUnsafe, "sysctl "+key+" has an unusable value")
		}
	}
	return nil
}

// checkNetwork requires the workload to join exactly its own tenant
// network, and refuses the engine names that mean the host or a shared
// default bridge.
func checkNetwork(spec WorkloadSpec) *DenyError {
	expected := TenantNetworkName(spec.TenantID)
	if spec.NetworkName != expected {
		return Deny(ReasonWorkloadUnsafe, "network "+spec.NetworkName+" is not the tenant network")
	}
	return nil
}

// checkLimits makes every resource limit mandatory and bounded. A missing
// or zero limit is a denial, not an unlimited default, so quota bypass by
// omission is impossible.
func checkLimits(spec WorkloadSpec, policy WorkloadPolicy) *DenyError {
	if spec.CPUCores <= 0 || policy.MaxCPUCores <= 0 || spec.CPUCores > policy.MaxCPUCores {
		return Deny(ReasonWorkloadUnsafe, "cpu limit is missing or above the ceiling")
	}
	if spec.MemoryBytes <= 0 || policy.MaxMemoryBytes <= 0 || spec.MemoryBytes > policy.MaxMemoryBytes {
		return Deny(ReasonWorkloadUnsafe, "memory limit is missing or above the ceiling")
	}
	if spec.PidsLimit <= 0 || policy.MaxPids <= 0 || spec.PidsLimit > policy.MaxPids {
		return Deny(ReasonWorkloadUnsafe, "pids limit is missing or above the ceiling")
	}
	if spec.StorageBytes <= 0 || policy.MaxStorageBytes <= 0 || spec.StorageBytes > policy.MaxStorageBytes {
		return Deny(ReasonWorkloadUnsafe, "storage limit is missing or above the ceiling")
	}
	return nil
}

// VMSpec is the tenant-defined virtual machine cashp models before handing
// anything to libvirt. Like WorkloadSpec, every field is attacker-controlled.
type VMSpec struct {
	// TenantID is the owning hosting account.
	TenantID string
	// Name is the guest's identifier within the tenant.
	Name string
	// Arch is the guest CPU architecture.
	Arch string
	// VCPUs is the mandatory vCPU count.
	VCPUs int
	// MemoryBytes is the mandatory memory limit.
	MemoryBytes int64
	// DiskBytes is the mandatory disk allowance.
	DiskBytes int64
	// DiskPaths are the host-side disk image paths.
	DiskPaths []string
	// PassthroughDevices are PCI/USB addresses the guest asked to be given.
	PassthroughDevices []string
	// NetworkName is the network the guest joins.
	NetworkName string
	// HostBridge requests attachment to a host bridge instead of the tenant network.
	HostBridge bool
	// ConsoleListen is the address the VNC/SPICE console binds to.
	ConsoleListen string
}

// ValidateVM is the mandatory gate before any tenant VM is defined. It
// mirrors ValidateWorkload's posture for the hypervisor surfaces: no host
// bridge, no unenumerated passthrough, disks confined to the tenant
// directory, a loopback-only console, and mandatory bounded limits.
func ValidateVM(spec VMSpec, policy WorkloadPolicy) error {
	if err := ValidateIdentifier("vm name", spec.Name); err != nil {
		return err
	}
	if err := ValidateIdentifier("architecture", spec.Arch); err != nil {
		return err
	}
	root, err := TenantRoot(policy, spec.TenantID)
	if err != nil {
		return err
	}
	if spec.HostBridge {
		return Deny(ReasonWorkloadUnsafe, "host bridge attachment is never permitted")
	}
	if spec.NetworkName != TenantNetworkName(spec.TenantID) {
		return Deny(ReasonWorkloadUnsafe, "network "+spec.NetworkName+" is not the tenant network")
	}
	if err := checkDevices(spec.PassthroughDevices, policy.AllowedPassthrough); err != nil {
		return err
	}

	mounts := make([]Mount, 0, len(spec.DiskPaths))
	for _, p := range spec.DiskPaths {
		mounts = append(mounts, Mount{Source: p, Target: "/dev/null"})
	}
	if err := checkMounts(mounts, root); err != nil {
		return err
	}
	if err := checkConsoleListen(spec.ConsoleListen); err != nil {
		return err
	}

	if spec.VCPUs <= 0 || policy.MaxVCPUs <= 0 || spec.VCPUs > policy.MaxVCPUs {
		return Deny(ReasonWorkloadUnsafe, "vcpu count is missing or above the ceiling")
	}
	if spec.MemoryBytes <= 0 || policy.MaxMemoryBytes <= 0 || spec.MemoryBytes > policy.MaxMemoryBytes {
		return Deny(ReasonWorkloadUnsafe, "memory limit is missing or above the ceiling")
	}
	if spec.DiskBytes <= 0 || policy.MaxStorageBytes <= 0 || spec.DiskBytes > policy.MaxStorageBytes {
		return Deny(ReasonWorkloadUnsafe, "disk limit is missing or above the ceiling")
	}
	return nil
}

// checkConsoleListen confines a VNC or SPICE console to loopback. The
// console protocols carry weak or no authentication, so exposing one on a
// routable address would hand any network peer a guest's screen and
// keyboard.
func checkConsoleListen(addr string) *DenyError {
	if isLoopbackHost(addr) {
		return nil
	}
	return Deny(ReasonWorkloadUnsafe, "console listen address "+addr+" is not loopback")
}

// DescribeLimits renders a workload's limits for an operator-facing audit
// line. It contains no tenant content, only numbers cashp itself set.
func DescribeLimits(spec WorkloadSpec) string {
	return "cpu=" + strconv.FormatFloat(spec.CPUCores, 'f', -1, 64) +
		" mem=" + strconv.FormatInt(spec.MemoryBytes, 10) +
		" pids=" + strconv.FormatInt(spec.PidsLimit, 10) +
		" storage=" + strconv.FormatInt(spec.StorageBytes, 10)
}
