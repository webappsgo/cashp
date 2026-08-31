package dbservice

import (
	"context"
	"io"
	"time"
)

// This file declares the narrow contract dbservice needs from the container
// orchestration layer (src/orchestrator, backends Docker/Podman/Incus/
// libvirt). dbservice depends on the interface, never on a concrete backend,
// so the test suite runs against a fake and never starts a container, opens a
// socket or needs root. The orchestrator package's concrete type is expected
// to satisfy Orchestrator at wiring time.

// Orchestrator is the container runtime dbservice drives. Every method is
// context-bound; implementations must honour cancellation.
type Orchestrator interface {
	// CreateVolume creates (or adopts, when it already exists) the persistent
	// volume backing an instance and returns its backend name.
	CreateVolume(ctx context.Context, spec VolumeSpec) (string, error)
	// RemoveVolume deletes a volume and everything in it.
	RemoveVolume(ctx context.Context, name string) error
	// VolumeUsage reports the bytes currently consumed by a volume.
	VolumeUsage(ctx context.Context, name string) (int64, error)
	// Create defines a container from spec and returns its backend id. It
	// does not start the container.
	Create(ctx context.Context, spec ContainerSpec) (string, error)
	// Start starts a created or stopped container.
	Start(ctx context.Context, id string) error
	// Stop requests a graceful stop, killing the container after timeout.
	Stop(ctx context.Context, id string, timeout time.Duration) error
	// Remove deletes a container. Volumes are never removed as a side effect;
	// RemoveVolume is always an explicit second call.
	Remove(ctx context.Context, id string, force bool) error
	// Inspect reports the current runtime state of a container.
	Inspect(ctx context.Context, id string) (ContainerState, error)
	// Exec runs an argv slice inside a running container. Implementations
	// must exec the argv directly and must never route it through a shell.
	Exec(ctx context.Context, id string, req ExecRequest) (ExecResult, error)
	// Logs returns up to the last n lines of container output.
	Logs(ctx context.Context, id string, lines int) ([]string, error)
	// UpdateLimits applies new cpu/memory/pids limits to an existing
	// container without recreating it.
	UpdateLimits(ctx context.Context, id string, limits ResourceLimits) error
	// WriteFile writes a file inside a container with the given path, mode
	// and ownership. It is used for credential files that must not appear on
	// a command line and for restoring engine snapshot files. It must work on
	// a created-but-not-yet-started container so bootstrap configuration can
	// be placed before the engine's first start.
	WriteFile(ctx context.Context, id string, file FileSpec, r io.Reader) error
	// ReadFile reads a file out of a container. The caller closes the reader.
	ReadFile(ctx context.Context, id, path string) (io.ReadCloser, error)
	// RemoveFile deletes a file inside a container. Removing a file that does
	// not exist is not an error.
	RemoveFile(ctx context.Context, id, path string) error
}

// VolumeSpec describes the persistent volume of one managed instance.
type VolumeSpec struct {
	// Name is the backend volume name, unique per instance.
	Name string
	// Labels carry the tenant and instance identifiers so an operator can
	// attribute a volume without consulting the database.
	Labels map[string]string
	// SizeBytes is the requested volume ceiling. Backends without a native
	// quota report it back through ContainerState and enforce nothing.
	SizeBytes int64
}

// FileSpec describes a file dbservice writes into a container. Ownership is
// explicit because several engines refuse to start when a credential file is
// not owned by the account the engine runs as.
type FileSpec struct {
	// Path is the absolute path inside the container.
	Path string
	// Mode is the file permission bits, always the narrowest the engine
	// tolerates.
	Mode uint32
	// UID is the owning user id inside the container.
	UID int
	// GID is the owning group id inside the container.
	GID int
}

// Mount attaches a volume to a path inside the container.
type Mount struct {
	// Volume is the backend volume name returned by CreateVolume.
	Volume string
	// Target is the absolute path inside the container.
	Target string
	// ReadOnly mounts the volume without write access.
	ReadOnly bool
}

// PortMap publishes a container port on the host.
type PortMap struct {
	// HostIP is the address the port binds to. dbservice always sets a
	// loopback or private address; a managed database is never published on
	// 0.0.0.0 by this package.
	HostIP string
	// HostPort is the host-side port.
	HostPort int
	// ContainerPort is the port the engine listens on inside the container.
	ContainerPort int
	// Protocol is "tcp" for every engine this package manages.
	Protocol string
}

// ResourceLimits is the cpu/memory/disk/pids envelope of an instance. Zero in
// any field means "no explicit limit" and lets the backend default apply.
type ResourceLimits struct {
	// CPUCores is a fractional core count, for example 0.5 or 2.
	CPUCores float64
	// MemoryBytes caps resident memory.
	MemoryBytes int64
	// DiskBytes caps the instance volume.
	DiskBytes int64
	// PidsLimit caps the process count inside the container.
	PidsLimit int
}

// IsZero reports whether no limit at all is set.
func (l ResourceLimits) IsZero() bool {
	return l.CPUCores == 0 && l.MemoryBytes == 0 && l.DiskBytes == 0 && l.PidsLimit == 0
}

// ContainerSpec is a full container definition. dbservice builds one per
// managed instance and hands it to the orchestrator unchanged.
type ContainerSpec struct {
	// Name is the container name, derived from the tenant and instance ids.
	Name string
	// Image is the fully qualified engine image including its tag.
	Image string
	// Env is the container environment. Engine bootstrap passwords are
	// delivered here and never on the command line.
	Env map[string]string
	// Cmd overrides the image entrypoint arguments. It is always an argv
	// slice and never a shell string.
	Cmd []string
	// Mounts attaches the instance volume and any credential mounts.
	Mounts []Mount
	// Ports publishes the engine port on the host loopback address.
	Ports []PortMap
	// Network is the per-tenant isolated network the container joins.
	Network string
	// Labels carry tenant/instance/engine attribution.
	Labels map[string]string
	// Limits is the resource envelope applied at creation.
	Limits ResourceLimits
	// RestartPolicy is the backend restart policy, "unless-stopped" for every
	// managed instance.
	RestartPolicy string
	// ReadOnlyRootFS mounts the image read-only; only the data volume and
	// declared tmpfs paths stay writable.
	ReadOnlyRootFS bool
	// TmpfsPaths are in-memory writable paths for engines that need a
	// scratch directory under a read-only root filesystem.
	TmpfsPaths []string
	// CapDrop lists Linux capabilities dropped from the container.
	CapDrop []string
	// NoNewPrivileges sets the no_new_privs bit so a setuid binary inside the
	// image cannot raise privileges.
	NoNewPrivileges bool
	// User is the uid:gid the engine runs as inside the container.
	User string
}

// ContainerState is the orchestrator's view of a container.
type ContainerState struct {
	// ID is the backend container id.
	ID string
	// Name is the container name.
	Name string
	// Image is the image the container was created from.
	Image string
	// Running is true while the main process is alive.
	Running bool
	// Status is the backend status word, for example "running" or "exited".
	Status string
	// ExitCode is the last exit status of the main process.
	ExitCode int
	// StartedAt is when the container last started.
	StartedAt time.Time
	// Limits is the resource envelope currently in force.
	Limits ResourceLimits
	// MemoryUsedBytes is current resident memory, when the backend reports it.
	MemoryUsedBytes int64
	// CPUPercent is current cpu utilisation, when the backend reports it.
	CPUPercent float64
}

// ExecRequest runs one command inside a container. Argv is executed
// directly: there is no shell, no string splitting and no interpolation
// anywhere in this package.
type ExecRequest struct {
	// Argv is the command and its arguments. Argv[0] is the binary.
	Argv []string
	// Env supplies per-exec environment, used to pass engine passwords out
	// of the process argument list.
	Env map[string]string
	// User is the uid or username the command runs as inside the container.
	User string
	// Stdin, when set, is streamed to the command's standard input.
	Stdin io.Reader
	// Stdout, when set, receives the command's standard output as a stream;
	// otherwise output is captured into ExecResult.Stdout.
	Stdout io.Writer
	// Stderr, when set, receives standard error as a stream; otherwise it is
	// captured into ExecResult.Stderr.
	Stderr io.Writer
	// Timeout bounds the command. Zero lets the caller's context deadline
	// apply on its own.
	Timeout time.Duration
}

// ExecResult is the outcome of an ExecRequest. Captured output is only
// populated for the streams the request did not redirect.
type ExecResult struct {
	// ExitCode is the command's exit status.
	ExitCode int
	// Stdout is captured standard output when Stdout was not redirected.
	Stdout string
	// Stderr is captured standard error when Stderr was not redirected.
	Stderr string
}
