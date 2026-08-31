// Package orchestrator drives containers and virtual machines through a
// single backend-agnostic interface so the rest of cashp never encodes a
// dependency on one engine.
//
// Four backends implement the interface: Docker and Podman over their
// respective Engine REST APIs on a unix socket, Incus over its REST API for
// both container and virtual-machine instances, and libvirt through virsh
// with generated domain XML. Operations an engine cannot express return a
// typed unsupported-operation error; nothing is ever a silent no-op.
//
// The package distinguishes the three workload categories the product
// defines. Native host services are not orchestrated here at all and are
// refused with an isolation error. App-managed service containers run under
// a documented, slightly wider profile. Tenant-defined workloads are treated
// as hostile input: no privileged mode, no host networking, no arbitrary
// host bind mounts, no engine socket, capabilities dropped by default, and
// every identifier checked against an allowlist before it reaches an argv
// slice or a request path.
//
// Nothing in the package builds a command from a formatted string. External
// programs run through exec.CommandContext with an argv slice, and libvirt
// domain XML is produced by marshaling structs so escaping is the encoder's
// job rather than a caller's.
package orchestrator

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Timeout budgets for the work this package performs. Every engine call and
// every external command runs under one of these; an orchestration request
// without a deadline can pin a worker on a shared node indefinitely.
const (
	// DefaultRequestTimeout bounds an ordinary engine API call.
	DefaultRequestTimeout = 30 * time.Second
	// DefaultProbeTimeout bounds an availability check, which must fail fast
	// so a missing engine does not stall startup.
	DefaultProbeTimeout = 5 * time.Second
	// DefaultCreateTimeout bounds a create, which may allocate disk.
	DefaultCreateTimeout = 2 * time.Minute
	// DefaultStopGrace is how long a workload gets to shut down cleanly
	// before it is killed.
	DefaultStopGrace = 10 * time.Second
	// DefaultPullTimeout bounds an image or template download.
	DefaultPullTimeout = 15 * time.Minute
	// DefaultExecTimeout bounds one exec inside a workload.
	DefaultExecTimeout = 2 * time.Minute
	// DefaultLogTimeout bounds a bounded, tailed log read.
	DefaultLogTimeout = 30 * time.Second
	// DefaultSnapshotTimeout bounds a snapshot or restore.
	DefaultSnapshotTimeout = 10 * time.Minute
)

// Default engine socket and binary locations. An operator may override any
// of them; the defaults match a stock installation on the platforms the
// product supports.
const (
	// DefaultDockerSocket is the Docker Engine API socket.
	DefaultDockerSocket = "/var/run/docker.sock"
	// DefaultPodmanSocket is the rootful Podman API socket.
	DefaultPodmanSocket = "/run/podman/podman.sock"
	// DefaultIncusSocket is the Incus API socket.
	DefaultIncusSocket = "/var/run/incus/unix.socket"
	// DefaultVirshBinary is the libvirt command-line client.
	DefaultVirshBinary = "virsh"
	// DefaultPodmanBinary is the Podman command-line client, used only for
	// the few operations the API socket cannot express.
	DefaultPodmanBinary = "podman"
	// DefaultLibvirtURI is the hypervisor connection virsh is pointed at.
	DefaultLibvirtURI = "qemu:///system"
)

// Config describes where the engines live on this host and where tenant
// data is allowed to sit. A zero Config is usable for validation-only work
// but has no volume root, so any workload that asks for a volume is
// refused rather than defaulting to somewhere on the host filesystem.
type Config struct {
	// TenantVolumeRoot is the directory beneath which every tenant's
	// container volumes and VM disks are confined. Relative mount sources
	// resolve under TenantVolumeRoot/<tenant>/ via security.SafeJoin.
	TenantVolumeRoot string
	// AppDataRoots are the directories an app-managed service container may
	// bind-mount from. Tenant workloads may never use them.
	AppDataRoots []string

	// DockerSocket is the Docker Engine API socket path.
	DockerSocket string
	// PodmanSocket is the Podman API socket path.
	PodmanSocket string
	// IncusSocket is the Incus API socket path.
	IncusSocket string

	// VirshBinary is the libvirt client program name or absolute path.
	VirshBinary string
	// PodmanBinary is the Podman client program name or absolute path.
	PodmanBinary string
	// LibvirtURI is the hypervisor connection URI passed to virsh.
	LibvirtURI string
	// LibvirtNetwork is the libvirt network guests are attached to when a
	// spec does not name one.
	LibvirtNetwork string
	// LibvirtUEFILoader is the OVMF firmware image used for a UEFI guest.
	// Zero uses domainDefaultLoader.
	LibvirtUEFILoader string

	// RequestTimeout bounds an ordinary engine API call. Zero uses
	// DefaultRequestTimeout.
	RequestTimeout time.Duration
	// ProbeTimeout bounds an availability check. Zero uses
	// DefaultProbeTimeout.
	ProbeTimeout time.Duration
	// CreateTimeout bounds a create. Zero uses DefaultCreateTimeout.
	CreateTimeout time.Duration
	// PullTimeout bounds an image pull. Zero uses DefaultPullTimeout.
	PullTimeout time.Duration
	// ExecTimeout bounds one exec. Zero uses DefaultExecTimeout.
	ExecTimeout time.Duration
	// LogTimeout bounds a log read. Zero uses DefaultLogTimeout.
	LogTimeout time.Duration
	// SnapshotTimeout bounds a snapshot or restore. Zero uses
	// DefaultSnapshotTimeout.
	SnapshotTimeout time.Duration
}

// withDefaults returns a copy of c with every unset field filled in.
func (c Config) withDefaults() Config {
	if c.DockerSocket == "" {
		c.DockerSocket = DefaultDockerSocket
	}
	if c.PodmanSocket == "" {
		c.PodmanSocket = DefaultPodmanSocket
	}
	if c.IncusSocket == "" {
		c.IncusSocket = DefaultIncusSocket
	}
	if c.VirshBinary == "" {
		c.VirshBinary = DefaultVirshBinary
	}
	if c.PodmanBinary == "" {
		c.PodmanBinary = DefaultPodmanBinary
	}
	if c.LibvirtURI == "" {
		c.LibvirtURI = DefaultLibvirtURI
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = DefaultRequestTimeout
	}
	if c.ProbeTimeout <= 0 {
		c.ProbeTimeout = DefaultProbeTimeout
	}
	if c.CreateTimeout <= 0 {
		c.CreateTimeout = DefaultCreateTimeout
	}
	if c.PullTimeout <= 0 {
		c.PullTimeout = DefaultPullTimeout
	}
	if c.ExecTimeout <= 0 {
		c.ExecTimeout = DefaultExecTimeout
	}
	if c.LogTimeout <= 0 {
		c.LogTimeout = DefaultLogTimeout
	}
	if c.SnapshotTimeout <= 0 {
		c.SnapshotTimeout = DefaultSnapshotTimeout
	}
	return c
}

// Validate checks the operator-supplied paths before anything is built from
// them, so a typo in configuration fails at startup rather than midway
// through a tenant request.
func (c Config) Validate() error {
	full := c.withDefaults()
	if full.TenantVolumeRoot != "" {
		if err := ValidateHostPath("tenant_volume_root", full.TenantVolumeRoot); err != nil {
			return err
		}
	}
	for _, root := range c.AppDataRoots {
		if err := ValidateHostPath("app_data_root", root); err != nil {
			return err
		}
	}
	if err := ValidateSocketPath(full.DockerSocket); err != nil {
		return err
	}
	if err := ValidateSocketPath(full.PodmanSocket); err != nil {
		return err
	}
	if err := ValidateSocketPath(full.IncusSocket); err != nil {
		return err
	}
	if hasUnsafeChars(full.VirshBinary) || hasUnsafeChars(full.PodmanBinary) {
		return validationErr("binary", "charset")
	}
	if hasUnsafeChars(full.LibvirtURI) {
		return validationErr("libvirt_uri", "charset")
	}
	if full.LibvirtNetwork != "" {
		if err := ValidateNetworkName(full.LibvirtNetwork); err != nil {
			return err
		}
	}
	if full.LibvirtUEFILoader != "" {
		if err := ValidateHostPath("uefi_loader", full.LibvirtUEFILoader); err != nil {
			return err
		}
	}
	return nil
}

// Manager holds the backends this host can actually drive and picks one for
// a request. Registration is explicit: a backend whose engine did not
// answer a probe is never registered, so the admin panel offers only what
// the node can really do.
type Manager struct {
	cfg Config

	mu       sync.RWMutex
	backends map[BackendName]Backend
	order    []BackendName
}

// NewManager builds an empty manager. Backends are added with Register or
// by Discover.
func NewManager(cfg Config) (*Manager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Manager{
		cfg:      cfg.withDefaults(),
		backends: make(map[BackendName]Backend, 4),
	}, nil
}

// Config returns the effective configuration, with defaults applied.
func (m *Manager) Config() Config { return m.cfg }

// Register adds a backend. Registering the same name twice replaces the
// earlier entry, which lets an operator swap a live engine for a scripted
// one during a maintenance window without restarting the server.
func (m *Manager) Register(b Backend) error {
	if b == nil {
		return validationErr("backend", "required")
	}
	name := b.Name()
	switch name {
	case BackendDocker, BackendPodman, BackendIncus, BackendLibvirt:
	default:
		return validationErr("backend", "unknown")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.backends[name]; !exists {
		m.order = append(m.order, name)
	}
	m.backends[name] = b
	return nil
}

// Backend returns a registered backend by name.
func (m *Manager) Backend(name BackendName) (Backend, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.backends[name]
	if !ok {
		return nil, unavailableErr(name, "not_registered", nil)
	}
	return b, nil
}

// Names lists the registered backends in registration order.
func (m *Manager) Names() []BackendName {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]BackendName, len(m.order))
	copy(out, m.order)
	return out
}

// Select picks a backend able to manage a workload kind. An explicit
// preference wins when it is registered and supports the kind; otherwise the
// package's own preference order decides, so a node with both Docker and
// Podman behaves predictably.
func (m *Manager) Select(kind Kind, preferred BackendName) (Backend, error) {
	if preferred != "" {
		b, err := m.Backend(preferred)
		if err != nil {
			return nil, err
		}
		if !supportsKind(b, kind) {
			return nil, unsupportedErr(preferred, string(kind))
		}
		return b, nil
	}

	for _, name := range preferenceOrder(kind) {
		b, err := m.Backend(name)
		if err != nil {
			continue
		}
		if supportsKind(b, kind) {
			return b, nil
		}
	}
	return nil, unavailableErr("", "no_backend_for_kind", nil)
}

// Status probes every registered backend and reports what each one can do.
// Probes run sequentially under a per-backend deadline; a node with a wedged
// engine still returns for the rest.
func (m *Manager) Status(ctx context.Context) []BackendStatus {
	names := m.Names()
	out := make([]BackendStatus, 0, len(names))
	for _, name := range names {
		b, err := m.Backend(name)
		if err != nil {
			continue
		}
		probeCtx, cancel := withTimeout(ctx, m.cfg.ProbeTimeout)
		status, probeErr := b.Probe(probeCtx)
		cancel()
		if probeErr != nil {
			status = BackendStatus{Name: name, Kinds: b.Kinds(), Available: false, Reason: "unreachable"}
		}
		status.Name = name
		if len(status.Kinds) == 0 {
			status.Kinds = b.Kinds()
		}
		out = append(out, status)
	}
	return out
}

// Available lists the registered backends that answered a probe.
func (m *Manager) Available(ctx context.Context) []BackendName {
	var out []BackendName
	for _, status := range m.Status(ctx) {
		if status.Available {
			out = append(out, status.Name)
		}
	}
	return out
}

// CapabilityMatrix reports, per registered backend, the sorted capability
// names it implements. The admin panel renders this directly rather than
// hardcoding what each engine can do.
func (m *Manager) CapabilityMatrix() map[BackendName][]string {
	names := m.Names()
	out := make(map[BackendName][]string, len(names))
	for _, name := range names {
		b, err := m.Backend(name)
		if err != nil {
			continue
		}
		out[name] = b.Capabilities().Sorted()
	}
	return out
}

// Discover probes each engine this host might have and registers only the
// ones that answered. It never returns an error for a missing engine: a node
// with Docker but no hypervisor is a normal, fully supported deployment.
func (m *Manager) Discover(ctx context.Context) []BackendStatus {
	candidates := []func() (Backend, error){
		func() (Backend, error) { return NewDockerBackend(m.cfg, nil) },
		func() (Backend, error) { return NewPodmanBackend(m.cfg, nil, nil) },
		func() (Backend, error) { return NewIncusBackend(m.cfg, nil) },
		func() (Backend, error) { return NewLibvirtBackend(m.cfg, nil) },
	}

	var out []BackendStatus
	for _, build := range candidates {
		b, err := build()
		if err != nil {
			continue
		}
		probeCtx, cancel := withTimeout(ctx, m.cfg.ProbeTimeout)
		status, probeErr := b.Probe(probeCtx)
		cancel()
		if probeErr != nil || !status.Available {
			continue
		}
		if err := m.Register(b); err != nil {
			continue
		}
		out = append(out, status)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// preferenceOrder is the fixed order backends are tried in for a kind when
// the caller expressed no preference. Containers prefer Docker, then Podman,
// then Incus's container instances. Virtual machines prefer libvirt, which
// gives the most control, then Incus.
func preferenceOrder(kind Kind) []BackendName {
	if kind == KindVM {
		return []BackendName{BackendLibvirt, BackendIncus}
	}
	return []BackendName{BackendDocker, BackendPodman, BackendIncus}
}

// supportsKind reports whether a backend manages a workload kind.
func supportsKind(b Backend, kind Kind) bool {
	for _, k := range b.Kinds() {
		if k == kind {
			return true
		}
	}
	return false
}
