package orchestrator

import (
	"context"
	"strings"
	"time"
)

// libvirt client details. The hypervisor is driven through the virsh
// command-line client rather than a Go binding: every libvirt binding needs
// cgo, and this product ships a single statically linked binary.
const (
	// DefaultLibvirtNetwork is the libvirt network a guest joins when neither
	// the spec nor the operator configuration names one.
	DefaultLibvirtNetwork = "default"
	// libvirtStdinFile is the path virsh reads a domain definition from, so
	// the XML is piped in rather than written to a file on the host.
	libvirtStdinFile = "/dev/stdin"
)

// LibvirtBackend manages virtual machines through virsh.
//
// Every invocation goes through the Runner interface as a program plus an
// argv slice. No command string is ever assembled, and the domain definition
// is produced by the XML encoder in domainxml.go rather than by formatting,
// so a tenant-supplied disk path or network name cannot break out of the
// element it was placed in.
type LibvirtBackend struct {
	cfg    Config
	runner Runner
	binary string
}

// NewLibvirtBackend builds a libvirt backend. A nil runner uses the real
// process runner; the test suite supplies FakeRunner so nothing is spawned.
// A missing virsh binary is reported here so the backend is never registered
// on a node that cannot run it.
func NewLibvirtBackend(cfg Config, runner Runner) (*LibvirtBackend, error) {
	full := cfg.withDefaults()
	if runner != nil {
		// A substituted runner never spawns a process, so there is nothing to
		// resolve on this host; the configured name is carried through as-is
		// for the recorded argv.
		if hasUnsafeChars(full.VirshBinary) {
			return nil, validationErr("binary", "charset")
		}
		return &LibvirtBackend{cfg: full, runner: runner, binary: full.VirshBinary}, nil
	}
	binary, err := LookupBinary(full.VirshBinary)
	if err != nil {
		return nil, err
	}
	return &LibvirtBackend{cfg: full, runner: &ExecRunner{}, binary: binary}, nil
}

// Name identifies the backend.
func (b *LibvirtBackend) Name() BackendName { return BackendLibvirt }

// Kinds reports that libvirt manages virtual machines only.
func (b *LibvirtBackend) Kinds() []Kind { return []Kind{KindVM} }

// Capabilities reports what libvirt can do through virsh.
//
// Port mapping, volume mounts, image pulls and pids limits are absent on
// purpose: a domain has no port-publishing concept, filesystem passthrough
// needs host-side virtiofs daemons this package does not manage, libvirt has
// no image registry, and process counts live inside the guest kernel.
func (b *LibvirtBackend) Capabilities() CapabilitySet {
	return newCapabilitySet(
		CapCreate, CapStart, CapStop, CapRestart, CapRemove, CapInspect, CapList,
		CapCPULimit, CapMemoryLimit, CapDiskAttach,
		CapSnapshot, CapRestore, CapListSnapshots,
	)
}

// virsh runs one virsh subcommand against the configured hypervisor.
func (b *LibvirtBackend) virsh(ctx context.Context, timeout time.Duration, stdin []byte, args ...string) (RunResult, error) {
	full := append([]string{"--connect", b.cfg.LibvirtURI}, args...)

	runCtx, cancel := withTimeout(ctx, timeout)
	defer cancel()

	result, err := b.runner.Run(runCtx, b.binary, full, stdin)
	if err != nil {
		return result, err
	}
	if result.ExitCode != 0 {
		return result, b.commandError(args, result)
	}
	return result, nil
}

// commandError turns a non-zero virsh exit into a typed error. The raw
// stderr is deliberately not carried into the message: it contains the
// connection URI and host paths.
func (b *LibvirtBackend) commandError(args []string, result RunResult) error {
	op := "virsh"
	if len(args) > 0 {
		op = args[0]
	}
	text := strings.ToLower(string(result.Stderr))
	switch {
	case strings.Contains(text, "domain not found"),
		strings.Contains(text, "no domain with matching"),
		strings.Contains(text, "no such domain"),
		strings.Contains(text, "snapshot not found"),
		strings.Contains(text, "no domain snapshot with matching"):
		return notFoundErr()
	case strings.Contains(text, "failed to connect to the hypervisor"),
		strings.Contains(text, "cannot connect to libvirt"):
		return unavailableErr(BackendLibvirt, "unreachable", nil)
	default:
		return backendErr(BackendLibvirt, op, nil)
	}
}

// Probe checks that the hypervisor answers.
func (b *LibvirtBackend) Probe(ctx context.Context) (BackendStatus, error) {
	status := BackendStatus{Name: BackendLibvirt, Kinds: b.Kinds()}

	result, err := b.virsh(ctx, b.cfg.ProbeTimeout, nil, "version")
	if err != nil {
		status.Reason = "unreachable"
		return status, err
	}
	status.Available = true
	status.Version = parseVirshVersion(string(result.Stdout))
	return status, nil
}

// parseVirshVersion pulls the running hypervisor version out of the version
// report, falling back to the library version when the hypervisor line is
// absent.
func parseVirshVersion(out string) string {
	var library string
	for _, line := range strings.Split(out, "\n") {
		label, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(label) {
		case "Running hypervisor":
			return value
		case "Using library":
			library = value
		}
	}
	return library
}

// Create defines a domain from a resolved spec. The domain is defined but
// not started, matching the behaviour of the container backends.
func (b *LibvirtBackend) Create(ctx context.Context, spec resolvedSpec) (Instance, error) {
	var out Instance

	if err := checkSpecSupport(BackendLibvirt, b.Capabilities(), spec); err != nil {
		return out, err
	}
	if len(spec.Spec.Command) > 0 {
		// A domain boots the operating system on its disk; there is no
		// equivalent of overriding the entrypoint from outside the guest.
		return out, unsupportedErr(BackendLibvirt, "command_override")
	}

	spec.NetworkName = b.networkFor(spec)

	definition, err := buildDomainXML(b.cfg, spec)
	if err != nil {
		return out, err
	}

	// virsh reads the definition from standard input, so a tenant-influenced
	// document never lands on the host filesystem.
	if _, err := b.virsh(ctx, b.cfg.CreateTimeout, definition, "define", libvirtStdinFile); err != nil {
		return out, err
	}
	return b.Inspect(ctx, spec.Spec.Ref)
}

// networkFor picks the libvirt network a guest attaches to. A per-tenant
// bridge name derived by the profile layer is only meaningful to the
// container engines, so libvirt falls back to the operator-configured
// network unless the caller named one explicitly.
func (b *LibvirtBackend) networkFor(spec resolvedSpec) string {
	if spec.NetworkName == "" {
		return ""
	}
	if spec.Spec.Network.Name != "" {
		return spec.Spec.Network.Name
	}
	if b.cfg.LibvirtNetwork != "" {
		return b.cfg.LibvirtNetwork
	}
	return DefaultLibvirtNetwork
}

// Start boots a defined domain.
func (b *LibvirtBackend) Start(ctx context.Context, ref Ref) error {
	qualified, err := b.owned(ctx, ref)
	if err != nil {
		return err
	}
	_, err = b.virsh(ctx, b.cfg.RequestTimeout, nil, "start", qualified)
	return err
}

// Stop shuts a domain down. A zero grace period asks for an immediate power
// off; otherwise the guest is asked to shut down cleanly.
func (b *LibvirtBackend) Stop(ctx context.Context, ref Ref, grace time.Duration) error {
	qualified, err := b.owned(ctx, ref)
	if err != nil {
		return err
	}
	command := "shutdown"
	if grace <= 0 {
		command = "destroy"
	}
	_, err = b.virsh(ctx, b.cfg.RequestTimeout, nil, command, qualified)
	return err
}

// Restart reboots a running domain.
func (b *LibvirtBackend) Restart(ctx context.Context, ref Ref, grace time.Duration) error {
	qualified, err := b.owned(ctx, ref)
	if err != nil {
		return err
	}
	_, err = b.virsh(ctx, b.cfg.RequestTimeout, nil, "reboot", qualified)
	return err
}

// Remove undefines a domain. A forced removal powers it off first, because
// libvirt refuses to undefine a running domain without also destroying it.
func (b *LibvirtBackend) Remove(ctx context.Context, ref Ref, opts RemoveOptions) error {
	qualified, err := b.owned(ctx, ref)
	if err != nil {
		return err
	}
	if opts.Force {
		// A domain that is already powered off makes this call fail, which is
		// not a reason to abandon the removal; a genuine problem resurfaces
		// from the undefine below.
		if _, destroyErr := b.virsh(ctx, b.cfg.RequestTimeout, nil, "destroy", qualified); IsNotFound(destroyErr) {
			return destroyErr
		}
	}

	args := []string{"undefine", qualified, "--nvram"}
	if opts.RemoveVolumes {
		args = append(args, "--remove-all-storage")
	}
	_, err = b.virsh(ctx, b.cfg.RequestTimeout, nil, args...)
	return err
}

// Inspect reports one domain's current status.
func (b *LibvirtBackend) Inspect(ctx context.Context, ref Ref) (Instance, error) {
	var out Instance

	qualified, err := ref.Qualified()
	if err != nil {
		return out, err
	}
	parsed, err := b.dumpXML(ctx, qualified)
	if err != nil {
		return out, err
	}
	owner, ok := parseDomainOwner(parsed)
	if !ok {
		return out, notFoundErr()
	}
	// The qualified name already carries the account, but the definition is
	// checked independently so a domain renamed outside the panel cannot be
	// reached by another account.
	if owner.TenantID != ref.TenantID || owner.Class != ref.Class {
		return out, tenantErr()
	}

	state, err := b.domainState(ctx, qualified)
	if err != nil {
		return out, err
	}
	return Instance{
		Ref:           owner,
		Backend:       BackendLibvirt,
		Kind:          KindVM,
		ID:            parsed.UUID,
		QualifiedName: qualified,
		State:         state,
		Resources:     parseDomainResources(parsed),
	}, nil
}

// dumpXML reads a domain definition back from the hypervisor.
func (b *LibvirtBackend) dumpXML(ctx context.Context, qualified string) (domainXML, error) {
	result, err := b.virsh(ctx, b.cfg.RequestTimeout, nil, "dumpxml", qualified)
	if err != nil {
		return domainXML{}, err
	}
	return decodeDomain(result.Stdout)
}

// domainState reads a domain's run state.
func (b *LibvirtBackend) domainState(ctx context.Context, qualified string) (State, error) {
	result, err := b.virsh(ctx, b.cfg.RequestTimeout, nil, "domstate", qualified)
	if err != nil {
		return StateUnknown, err
	}
	return libvirtState(strings.TrimSpace(string(result.Stdout))), nil
}

// owned resolves a reference to its qualified name after confirming the
// domain belongs to the requesting account.
func (b *LibvirtBackend) owned(ctx context.Context, ref Ref) (string, error) {
	instance, err := b.Inspect(ctx, ref)
	if err != nil {
		return "", err
	}
	return instance.QualifiedName, nil
}

// libvirtState maps a virsh domain state onto the package vocabulary.
func libvirtState(raw string) State {
	switch raw {
	case "running", "idle":
		return StateRunning
	case "paused", "pmsuspended":
		return StatePaused
	case "shut off", "in shutdown":
		return StateStopped
	case "crashed":
		return StateError
	default:
		return StateUnknown
	}
}

// List enumerates the domains this package manages, filtered to one account.
//
// libvirt has no server-side label filter, so the domain list is narrowed by
// the qualified-name scheme and then each candidate is confirmed against its
// own ownership metadata.
func (b *LibvirtBackend) List(ctx context.Context, filter Filter) ([]Instance, error) {
	if filter.Kind != "" && filter.Kind != KindVM {
		return nil, nil
	}
	result, err := b.virsh(ctx, b.cfg.RequestTimeout, nil, "list", "--all", "--name")
	if err != nil {
		return nil, err
	}

	var out []Instance
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		ref, ok := parseQualified(name)
		if !ok {
			continue
		}
		if filter.TenantID != "" && ref.TenantID != filter.TenantID {
			continue
		}
		if filter.Class != "" && ref.Class != filter.Class {
			continue
		}
		instance, err := b.Inspect(ctx, ref)
		if err != nil {
			if IsNotFound(err) || IsTenantMismatch(err) {
				continue
			}
			return nil, err
		}
		if filter.State != "" && instance.State != filter.State {
			continue
		}
		out = append(out, instance)
	}
	return out, nil
}

// Snapshot captures a point-in-time snapshot of a domain.
func (b *LibvirtBackend) Snapshot(ctx context.Context, ref Ref, name string) (Snapshot, error) {
	var out Snapshot

	if err := ValidateSnapshotName(name); err != nil {
		return out, err
	}
	qualified, err := b.owned(ctx, ref)
	if err != nil {
		return out, err
	}
	if _, err := b.virsh(ctx, b.cfg.SnapshotTimeout, nil,
		"snapshot-create-as", "--domain", qualified, "--name", name); err != nil {
		return out, err
	}
	return Snapshot{Name: name, CreatedAt: time.Now().UTC()}, nil
}

// Restore reverts a domain to a previously captured snapshot.
func (b *LibvirtBackend) Restore(ctx context.Context, ref Ref, name string) error {
	if err := ValidateSnapshotName(name); err != nil {
		return err
	}
	qualified, err := b.owned(ctx, ref)
	if err != nil {
		return err
	}
	_, err = b.virsh(ctx, b.cfg.SnapshotTimeout, nil,
		"snapshot-revert", "--domain", qualified, "--snapshotname", name)
	return err
}

// ListSnapshots enumerates a domain's snapshots.
func (b *LibvirtBackend) ListSnapshots(ctx context.Context, ref Ref) ([]Snapshot, error) {
	qualified, err := b.owned(ctx, ref)
	if err != nil {
		return nil, err
	}
	result, err := b.virsh(ctx, b.cfg.RequestTimeout, nil, "snapshot-list", "--domain", qualified, "--name")
	if err != nil {
		return nil, err
	}

	var out []Snapshot
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		out = append(out, Snapshot{Name: name})
	}
	return out, nil
}

// Logs is not offered. A domain's only output channel is its serial console,
// which virsh exposes as an interactive session rather than as a bounded,
// tailed read, and attaching to it would take the console away from whoever
// else is using it.
func (b *LibvirtBackend) Logs(ctx context.Context, ref Ref, opts LogOptions) ([]LogLine, error) {
	return nil, unsupportedErr(BackendLibvirt, string(CapLogs))
}

// Exec is not offered. Running a command inside a domain requires the guest
// agent to be installed and trusted inside a tenant-controlled operating
// system, which is not something this node can rely on.
func (b *LibvirtBackend) Exec(ctx context.Context, ref Ref, req ExecRequest) (ExecResult, error) {
	return ExecResult{}, unsupportedErr(BackendLibvirt, string(CapExec))
}

// PullImage is not offered: libvirt has no image registry, and a domain
// boots from a disk image that already exists on the node.
func (b *LibvirtBackend) PullImage(ctx context.Context, req ImageRequest) (ImageRef, error) {
	return ImageRef{}, unsupportedErr(BackendLibvirt, string(CapImagePull))
}
