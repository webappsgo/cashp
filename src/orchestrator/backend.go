package orchestrator

import (
	"context"
	"sort"
	"time"
)

// Capability names one operation a backend may or may not implement. A
// backend that lacks a capability returns an unsupported-operation error
// from the corresponding method; it never returns success for work it did
// not do.
type Capability string

// The capability vocabulary. Every backend declares its own set and the
// admin panel renders exactly what a node can do rather than offering
// controls that fail on use.
const (
	// CapCreate creates a workload from a spec.
	CapCreate Capability = "create"
	// CapStart starts an existing workload.
	CapStart Capability = "start"
	// CapStop stops a running workload.
	CapStop Capability = "stop"
	// CapRestart restarts a workload.
	CapRestart Capability = "restart"
	// CapRemove destroys a workload.
	CapRemove Capability = "remove"
	// CapInspect reports one workload's status.
	CapInspect Capability = "inspect"
	// CapList enumerates a tenant's workloads.
	CapList Capability = "list"
	// CapLogs reads bounded, tailed workload logs.
	CapLogs Capability = "logs"
	// CapExec runs an argv slice inside a workload.
	CapExec Capability = "exec"
	// CapCPULimit applies a CPU allowance.
	CapCPULimit Capability = "cpu_limit"
	// CapMemoryLimit applies a memory ceiling.
	CapMemoryLimit Capability = "memory_limit"
	// CapDiskLimit applies a root disk size ceiling.
	CapDiskLimit Capability = "disk_limit"
	// CapPidsLimit applies a process count ceiling.
	CapPidsLimit Capability = "pids_limit"
	// CapPortMapping publishes workload ports on the host.
	CapPortMapping Capability = "port_mapping"
	// CapVolumeMount attaches a host directory into a container.
	CapVolumeMount Capability = "volume_mount"
	// CapDiskAttach attaches a block image to a virtual machine.
	CapDiskAttach Capability = "disk_attach"
	// CapImagePull fetches an image or template.
	CapImagePull Capability = "image_pull"
	// CapDigestPin pulls by immutable content digest.
	CapDigestPin Capability = "digest_pin"
	// CapRegistryAuth authenticates to a private registry.
	CapRegistryAuth Capability = "registry_auth"
	// CapSnapshot captures a point-in-time snapshot.
	CapSnapshot Capability = "snapshot"
	// CapRestore reverts to a snapshot.
	CapRestore Capability = "restore"
	// CapListSnapshots enumerates snapshots.
	CapListSnapshots Capability = "list_snapshots"
)

// CapabilitySet is the set of operations one backend implements.
type CapabilitySet map[Capability]bool

// newCapabilitySet builds a set from a capability list.
func newCapabilitySet(caps ...Capability) CapabilitySet {
	set := make(CapabilitySet, len(caps))
	for _, c := range caps {
		set[c] = true
	}
	return set
}

// Has reports whether the backend implements c.
func (s CapabilitySet) Has(c Capability) bool { return s[c] }

// Sorted returns the capability names in a stable order, for the admin
// panel's per-node capability table.
func (s CapabilitySet) Sorted() []string {
	out := make([]string, 0, len(s))
	for c, ok := range s {
		if ok {
			out = append(out, string(c))
		}
	}
	sort.Strings(out)
	return out
}

// Backend is the backend-agnostic orchestration contract. Docker, Podman,
// Incus, and libvirt each implement it in full; the operations a given
// engine cannot express return an unsupported-operation error rather than
// succeeding silently.
//
// Every method takes a context and every implementation applies a deadline
// to the work it performs.
type Backend interface {
	// Name identifies the backend.
	Name() BackendName
	// Kinds lists the workload kinds this backend manages.
	Kinds() []Kind
	// Capabilities reports the operations this backend implements.
	Capabilities() CapabilitySet
	// Probe checks that the engine is reachable and usable on this host.
	Probe(ctx context.Context) (BackendStatus, error)

	// Create builds a workload from an already-resolved spec.
	Create(ctx context.Context, spec resolvedSpec) (Instance, error)
	// Start starts an existing workload.
	Start(ctx context.Context, ref Ref) error
	// Stop stops a running workload, allowing grace for shutdown.
	Stop(ctx context.Context, ref Ref, grace time.Duration) error
	// Restart restarts a workload.
	Restart(ctx context.Context, ref Ref, grace time.Duration) error
	// Remove destroys a workload.
	Remove(ctx context.Context, ref Ref, opts RemoveOptions) error
	// Inspect reports one workload's current status.
	Inspect(ctx context.Context, ref Ref) (Instance, error)
	// List enumerates the workloads matching a tenant-scoped filter.
	List(ctx context.Context, filter Filter) ([]Instance, error)
	// Logs reads bounded, tailed workload logs.
	Logs(ctx context.Context, ref Ref, opts LogOptions) ([]LogLine, error)
	// Exec runs an argv slice inside a workload.
	Exec(ctx context.Context, ref Ref, req ExecRequest) (ExecResult, error)
	// PullImage makes an image or template locally available.
	PullImage(ctx context.Context, req ImageRequest) (ImageRef, error)
	// Snapshot captures a point-in-time snapshot.
	Snapshot(ctx context.Context, ref Ref, name string) (Snapshot, error)
	// Restore reverts a workload to a snapshot.
	Restore(ctx context.Context, ref Ref, name string) error
	// ListSnapshots enumerates a workload's snapshots.
	ListSnapshots(ctx context.Context, ref Ref) ([]Snapshot, error)
}

// checkResourceSupport rejects a spec that asks for a limit the backend
// cannot enforce. Silently dropping a memory or disk ceiling would let a
// tenant exceed the quota their billing tier bought, which IDEA.md lists as
// an explicit abuse case.
func checkResourceSupport(backend BackendName, caps CapabilitySet, r Resources) error {
	if r.CPUCores > 0 && !caps.Has(CapCPULimit) {
		return unsupportedErr(backend, string(CapCPULimit))
	}
	if r.MemoryBytes > 0 && !caps.Has(CapMemoryLimit) {
		return unsupportedErr(backend, string(CapMemoryLimit))
	}
	if r.DiskBytes > 0 && !caps.Has(CapDiskLimit) {
		return unsupportedErr(backend, string(CapDiskLimit))
	}
	if r.PidsLimit > 0 && !caps.Has(CapPidsLimit) {
		return unsupportedErr(backend, string(CapPidsLimit))
	}
	return nil
}

// checkSpecSupport rejects a spec whose networking or storage requirements
// the backend cannot express.
func checkSpecSupport(backend BackendName, caps CapabilitySet, spec resolvedSpec) error {
	if err := checkResourceSupport(backend, caps, spec.Spec.Resources); err != nil {
		return err
	}
	if len(spec.Spec.Network.Ports) > 0 && !caps.Has(CapPortMapping) {
		return unsupportedErr(backend, string(CapPortMapping))
	}
	if len(spec.Mounts) > 0 && !caps.Has(CapVolumeMount) {
		return unsupportedErr(backend, string(CapVolumeMount))
	}
	if len(spec.Disks) > 0 && !caps.Has(CapDiskAttach) {
		return unsupportedErr(backend, string(CapDiskAttach))
	}
	if spec.Spec.ImageDigest != "" && !caps.Has(CapDigestPin) {
		return unsupportedErr(backend, string(CapDigestPin))
	}
	return nil
}

// normalizeLogOptions clamps a caller's log request to the package bounds
// and defaults both streams on when the caller selected neither.
func normalizeLogOptions(opts LogOptions) LogOptions {
	opts.Tail = clampTail(opts.Tail)
	opts.MaxBytes = clampLogBytes(opts.MaxBytes)
	if !opts.Stdout && !opts.Stderr {
		opts.Stdout = true
		opts.Stderr = true
	}
	return opts
}
