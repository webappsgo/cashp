package orchestrator

import (
	"time"
)

// Kind is the form a workload takes. A backend declares which kinds it can
// manage; Docker and Podman are container-only, libvirt is VM-only, and
// Incus manages both.
type Kind string

// Workload kinds.
const (
	// KindContainer is an OCI-style or system container.
	KindContainer Kind = "container"
	// KindVM is a full virtual machine guest.
	KindVM Kind = "vm"
)

// BackendName identifies one concrete orchestration backend.
type BackendName string

// Registered backend identifiers.
const (
	// BackendDocker talks to the Docker Engine API over its unix socket.
	BackendDocker BackendName = "docker"
	// BackendPodman talks to the Podman REST API over its unix socket.
	BackendPodman BackendName = "podman"
	// BackendIncus talks to the Incus REST API over its unix socket.
	BackendIncus BackendName = "incus"
	// BackendLibvirt drives libvirt through the virsh command line.
	BackendLibvirt BackendName = "libvirt"
)

// Class is the service-hosting category from IDEA.md "Service hosting
// model". The three categories have different lifecycles and different
// trust levels, and this package keeps them distinct rather than treating
// every workload as equivalent.
type Class string

// Workload classes.
const (
	// ClassNative marks a real distro package supervised by systemd or
	// OpenRC (web server, PHP-FPM, mail stack, BIND, fail2ban, nftables,
	// and the container/VM engines themselves). This orchestrator never
	// manages a native host service; the class exists so a caller that
	// hands one to a backend is rejected with a typed error instead of
	// silently getting a container.
	ClassNative Class = "native"
	// ClassAppManaged marks a cashp-orchestrated service container
	// (PostgreSQL, MariaDB, MongoDB, Valkey). It is never tenant-defined,
	// runs under the app-managed isolation profile, and may mount host
	// data directories that cashp itself owns.
	ClassAppManaged Class = "app-managed"
	// ClassTenant marks a tenant-defined workload. Tenant content is
	// untrusted by design and always runs under the tenant isolation
	// profile.
	ClassTenant Class = "tenant"
)

// State is the normalized lifecycle state reported by every backend.
type State string

// Normalized workload states.
const (
	// StateUnknown means the backend reported a state this package does
	// not model.
	StateUnknown State = "unknown"
	// StateCreated means the workload exists but has never run.
	StateCreated State = "created"
	// StateRunning means the workload is executing.
	StateRunning State = "running"
	// StateStopped means the workload exists and is not executing.
	StateStopped State = "stopped"
	// StatePaused means execution is suspended but state is resident.
	StatePaused State = "paused"
	// StateError means the backend reported a failure state.
	StateError State = "error"
)

// SystemTenantID is the reserved tenant identifier that owns every
// app-managed service container. It is never assignable to a real tenant.
const SystemTenantID = "sys"

// Label keys stamped onto every workload this package creates. They are the
// only reliable way to tell a cashp-managed object from one an operator
// created by hand on the same host.
const (
	// LabelManaged marks the object as created by cashp.
	LabelManaged = "cashp.managed"
	// LabelTenant carries the owning tenant identifier.
	LabelTenant = "cashp.tenant"
	// LabelClass carries the service-hosting class.
	LabelClass = "cashp.class"
	// LabelWorkload carries the tenant-visible workload name.
	LabelWorkload = "cashp.workload"
)

// Ref identifies one workload by owner and tenant-visible name. The
// backend-visible object name is always derived from this pair, never taken
// verbatim from a caller, so two tenants can never collide and a tenant can
// never address another tenant's object by guessing a name.
type Ref struct {
	// Class is the service-hosting category of the workload.
	Class Class
	// TenantID is the owning tenant, or SystemTenantID for app-managed.
	TenantID string
	// Name is the tenant-visible workload name.
	Name string
}

// Resources is the resource envelope applied to a workload. A zero field
// means "no explicit limit"; a backend that cannot express a set field
// returns an unsupported-operation error rather than dropping it.
type Resources struct {
	// CPUCores is the fractional core allowance, e.g. 1.5.
	CPUCores float64
	// MemoryBytes caps resident memory.
	MemoryBytes int64
	// DiskBytes caps the root filesystem or root disk size.
	DiskBytes int64
	// PidsLimit caps the number of processes or threads.
	PidsLimit int64
}

// NetworkMode selects how a workload is attached to the network.
type NetworkMode string

// Supported network modes.
const (
	// NetworkBridge attaches the workload to a named per-tenant bridge.
	NetworkBridge NetworkMode = "bridge"
	// NetworkNone gives the workload no network interface at all.
	NetworkNone NetworkMode = "none"
	// NetworkHost shares the host network namespace. It is only ever
	// permitted for a class whose profile explicitly allows it, and no
	// profile in this package allows it for a tenant workload.
	NetworkHost NetworkMode = "host"
)

// PortMapping publishes one workload port on the host.
type PortMapping struct {
	// HostIP is the address to bind on. Empty means every address.
	HostIP string
	// HostPort is the published host port.
	HostPort int
	// TargetPort is the port inside the workload.
	TargetPort int
	// Protocol is "tcp" or "udp".
	Protocol string
}

// Network is the networking section of a workload spec.
type Network struct {
	// Mode selects bridge, none, or host attachment.
	Mode NetworkMode
	// Name is the per-tenant network to attach to in bridge mode.
	Name string
	// Ports lists the host port publications.
	Ports []PortMapping
}

// VolumeMount attaches host storage into a container. Source is always
// interpreted relative to the owning tenant's volume root and resolved with
// security.SafeJoin; an absolute source is only accepted for a class whose
// profile allows host paths, and then only under a configured root.
type VolumeMount struct {
	// Source is the host-side path, tenant-relative unless the profile
	// allows host paths.
	Source string
	// Target is the absolute path inside the workload.
	Target string
	// ReadOnly mounts the volume without write access.
	ReadOnly bool
}

// Disk attaches a block image to a virtual machine.
type Disk struct {
	// Source is the host-side disk image path, resolved the same way as a
	// VolumeMount source.
	Source string
	// Format is the image format, e.g. "qcow2" or "raw".
	Format string
	// Target is the guest device name, e.g. "vda".
	Target string
	// Bus is the guest bus, e.g. "virtio" or "sata".
	Bus string
	// ReadOnly attaches the disk without write access.
	ReadOnly bool
	// Boot marks the disk as the boot device.
	Boot bool
}

// RestartPolicy describes how the backend should react to an exit.
type RestartPolicy string

// Supported restart policies.
const (
	// RestartNever leaves an exited workload stopped.
	RestartNever RestartPolicy = "no"
	// RestartOnFailure restarts only after a non-zero exit.
	RestartOnFailure RestartPolicy = "on-failure"
	// RestartAlways always restarts, including after a host reboot.
	RestartAlways RestartPolicy = "always"
)

// Firmware selects the guest boot firmware for a virtual machine.
type Firmware string

// Supported guest firmware.
const (
	// FirmwareBIOS boots the guest with SeaBIOS.
	FirmwareBIOS Firmware = "bios"
	// FirmwareUEFI boots the guest with an OVMF/edk2 pflash loader.
	FirmwareUEFI Firmware = "uefi"
)

// RegistryAuth carries credentials for a tenant-configured OCI registry.
// The password is a highest-sensitivity value: it is never logged, never
// persisted by this package, and never placed in an error message.
type RegistryAuth struct {
	// Username is the registry account name.
	Username string
	// Password is the registry secret or token.
	Password string
	// ServerAddress is the registry host.
	ServerAddress string
}

// ImageRequest asks a backend to make an image locally available.
type ImageRequest struct {
	// Reference is the image reference, optionally carrying a tag.
	Reference string
	// Digest pins the exact content, e.g. "sha256:<64 hex>". When the
	// backend supports digest pinning the pull is performed by digest.
	Digest string
	// Auth is optional registry authentication.
	Auth *RegistryAuth
}

// ImageRef is the result of a successful image pull.
type ImageRef struct {
	// Reference is the normalized reference the backend resolved.
	Reference string
	// Digest is the content digest, empty when the backend cannot report
	// one.
	Digest string
	// ID is the backend-local image identifier.
	ID string
	// SizeBytes is the on-disk size, zero when unknown.
	SizeBytes int64
}

// Spec is the full description of a workload to create.
type Spec struct {
	// Ref identifies the workload and its owner.
	Ref Ref
	// Kind selects container or VM.
	Kind Kind
	// Image is the container image reference or the VM template/disk
	// reference.
	Image string
	// ImageDigest pins the image content where the backend supports it.
	ImageDigest string
	// Command overrides the image entrypoint arguments. It is always an
	// argv slice and is never joined into a shell string.
	Command []string
	// Env is the environment handed to the workload. Values are treated as
	// tenant secrets: they are validated, never logged, and never
	// persisted by this package.
	Env map[string]string
	// Labels are extra metadata merged under the cashp label namespace.
	Labels map[string]string
	// Resources is the resource envelope.
	Resources Resources
	// Network is the networking section.
	Network Network
	// Volumes attaches host directories into a container.
	Volumes []VolumeMount
	// Disks attaches block images to a virtual machine.
	Disks []Disk
	// Restart is the restart policy.
	Restart RestartPolicy
	// Architecture is the guest CPU architecture for a VM, e.g. "x86_64"
	// or "aarch64". Empty means the host architecture.
	Architecture string
	// Firmware selects BIOS or UEFI boot for a VM.
	Firmware Firmware
	// Privileged requests a privileged workload. It is rejected unless the
	// class profile allows it, and no profile allows it for a tenant.
	Privileged bool
	// AddCapabilities requests extra Linux capabilities. Every entry must
	// appear in the class profile's allowlist.
	AddCapabilities []string
	// ReadOnlyRoot mounts the root filesystem read-only.
	ReadOnlyRoot bool
	// WorkingDir is the initial working directory inside the workload.
	WorkingDir string
	// User is the uid/gid or user name the workload runs as.
	User string
}

// Instance is the normalized view of one workload as a backend reports it.
type Instance struct {
	// Ref identifies the workload and its owner.
	Ref Ref
	// Backend is the backend that owns the object.
	Backend BackendName
	// Kind is container or VM.
	Kind Kind
	// ID is the backend-assigned identifier.
	ID string
	// QualifiedName is the backend-visible object name.
	QualifiedName string
	// State is the normalized lifecycle state.
	State State
	// Image is the image or template the workload was created from.
	Image string
	// ImageDigest is the pinned content digest when known.
	ImageDigest string
	// CreatedAt is when the object was created.
	CreatedAt time.Time
	// StartedAt is when the object last started.
	StartedAt time.Time
	// ExitCode is the last exit status, valid only when stopped.
	ExitCode int
	// Addresses lists the workload's IP addresses.
	Addresses []string
	// Ports lists the active host port publications.
	Ports []PortMapping
	// Resources is the effective resource envelope.
	Resources Resources
}

// Filter narrows a List call. TenantID is mandatory: there is no listing
// surface in this package that can return another tenant's workloads.
type Filter struct {
	// TenantID is the owning tenant whose workloads are listed.
	TenantID string
	// Class narrows to one service-hosting class. Empty lists every class
	// owned by the tenant.
	Class Class
	// Kind narrows to containers or VMs. Empty lists both.
	Kind Kind
	// State narrows to one lifecycle state. Empty lists every state.
	State State
}

// RemoveOptions tunes a destroy operation.
type RemoveOptions struct {
	// Force removes a running workload instead of failing.
	Force bool
	// RemoveVolumes also deletes anonymous storage owned by the workload.
	RemoveVolumes bool
}

// Log stream identifiers used in a LogLine.
const (
	// StreamStdout marks a line from the workload's standard output.
	StreamStdout = "stdout"
	// StreamStderr marks a line from the workload's standard error.
	StreamStderr = "stderr"
)

// LogOptions bounds a log read. Log retrieval in this package is always
// bounded: there is no unbounded follow mode, because an unbounded stream
// on a shared node is a denial-of-service surface.
type LogOptions struct {
	// Tail is the maximum number of trailing lines to return.
	Tail int
	// Since drops lines older than this instant when the backend can
	// filter by time.
	Since time.Time
	// Stdout includes the standard output stream.
	Stdout bool
	// Stderr includes the standard error stream.
	Stderr bool
	// MaxBytes caps the total decoded payload.
	MaxBytes int64
}

// LogLine is one decoded log record.
type LogLine struct {
	// Stream is StreamStdout or StreamStderr.
	Stream string
	// Text is the line content with its trailing newline removed.
	Text string
	// Time is the record timestamp when the backend supplies one.
	Time time.Time
}

// ExecRequest runs a command inside an existing workload. Argv is always a
// slice; this package never accepts, builds, or interprets a shell string.
type ExecRequest struct {
	// Argv is the command and its arguments.
	Argv []string
	// WorkingDir is the directory to run in.
	WorkingDir string
	// Env is extra environment for the command only.
	Env map[string]string
	// User is the uid/gid or user name to run as.
	User string
	// Stdin is the input handed to the command.
	Stdin []byte
	// MaxOutputBytes caps captured output per stream.
	MaxOutputBytes int64
}

// ExecResult is the captured outcome of an exec.
type ExecResult struct {
	// ExitCode is the command's exit status.
	ExitCode int
	// Stdout is the captured standard output.
	Stdout []byte
	// Stderr is the captured standard error.
	Stderr []byte
	// Truncated reports that output hit MaxOutputBytes.
	Truncated bool
}

// Snapshot is one point-in-time capture of a workload.
type Snapshot struct {
	// Name is the snapshot identifier.
	Name string
	// CreatedAt is when the snapshot was taken.
	CreatedAt time.Time
	// SizeBytes is the snapshot size, zero when unknown.
	SizeBytes int64
	// Stateful reports whether guest memory was captured too.
	Stateful bool
}

// BackendStatus is one backend's availability as reported by a probe.
type BackendStatus struct {
	// Name is the backend identifier.
	Name BackendName
	// Kinds lists the workload kinds the backend manages.
	Kinds []Kind
	// Available reports whether the probe succeeded.
	Available bool
	// Version is the backend version string when the probe reported one.
	Version string
	// Reason is a safe, non-leaking explanation when Available is false.
	Reason string
}

// Actor is the authenticated principal on whose behalf an operation runs.
// Every audited entry carries it, so an action can always be attributed.
type Actor struct {
	// UserID is the acting account identifier.
	UserID string
	// Username is the acting account name.
	Username string
	// Role is global_admin, account_admin, or end_user.
	Role string
	// TenantID is the tenant the actor belongs to.
	TenantID string
	// RequestID correlates the action with the request log.
	RequestID string
}

// Roles recognized for the tenant-scope check, matching the RBAC table in
// IDEA.md "Roles & permissions".
const (
	// RoleGlobalAdmin administers the whole installation or cluster.
	RoleGlobalAdmin = "global_admin"
	// RoleAccountAdmin administers a single tenant.
	RoleAccountAdmin = "account_admin"
	// RoleEndUser holds only the grants an account admin issued.
	RoleEndUser = "end_user"
)
