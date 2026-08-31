package orchestrator

import (
	"context"
	"time"
)

// Podman API path prefixes. Podman serves a Docker-compatible surface and
// its own libpod surface on the same socket; this backend uses the
// compatibility surface for the operations both engines share and libpod
// only where it reports something the compatibility surface does not.
const (
	// podmanCompatPrefix is the Docker-compatible API prefix.
	podmanCompatPrefix = "/v1.41"
	// podmanLibpodPrefix is the native libpod API prefix.
	podmanLibpodPrefix = "/v4.0.0/libpod"
	// podmanHost is the placeholder authority in the request URL.
	podmanHost = "podman"
)

// PodmanBackend drives Podman over its REST API on a local unix socket.
//
// Almost every operation goes over the socket. The one exception is an exec
// that has to feed standard input: the API expresses that only through a
// hijacked bidirectional stream, which this HTTP transport deliberately does
// not implement, so that single case falls back to the podman command-line
// client. The fallback still passes the program and each argument as a
// separate element of an argv slice — no shell is involved there either.
type PodmanBackend struct {
	cfg    Config
	compat *DockerBackend
	libpod *apiClient
	runner Runner
	binary string
}

// NewPodmanBackend builds a Podman backend. A nil doer opens the configured
// socket and a nil runner uses the real process runner; the test suite
// supplies both so nothing touches a socket or spawns a process.
func NewPodmanBackend(cfg Config, doer Doer, runner Runner) (*PodmanBackend, error) {
	full := cfg.withDefaults()
	if doer == nil {
		transport, err := NewUnixTransport(full.PodmanSocket, full.RequestTimeout)
		if err != nil {
			return nil, err
		}
		doer = transport
	}
	if runner == nil {
		runner = &ExecRunner{}
	}

	backend := &PodmanBackend{
		cfg:    full,
		compat: newCompatBackend(full, doer, BackendPodman, podmanHost, podmanCompatPrefix),
		libpod: newAPIClient(doer, BackendPodman, podmanHost, podmanLibpodPrefix),
		runner: runner,
	}
	// A missing client binary is not fatal: everything except stdin-fed exec
	// works over the socket alone, and that one operation reports itself as
	// unsupported rather than the whole backend refusing to register.
	if path, err := LookupBinary(full.PodmanBinary); err == nil {
		backend.binary = path
	}
	return backend, nil
}

// Name identifies the backend.
func (b *PodmanBackend) Name() BackendName { return BackendPodman }

// Kinds reports that Podman manages containers only.
func (b *PodmanBackend) Kinds() []Kind { return []Kind{KindContainer} }

// Capabilities reports what Podman can do. Checkpointing exists but is a
// CRIU freeze rather than a restorable snapshot, so it is not presented as
// one.
func (b *PodmanBackend) Capabilities() CapabilitySet {
	return newCapabilitySet(
		CapCreate, CapStart, CapStop, CapRestart, CapRemove, CapInspect, CapList,
		CapLogs, CapExec, CapCPULimit, CapMemoryLimit, CapPidsLimit,
		CapPortMapping, CapVolumeMount, CapImagePull, CapDigestPin, CapRegistryAuth,
	)
}

// podmanInfo is the subset of the libpod info document this backend reads.
type podmanInfo struct {
	Version struct {
		Version string `json:"Version"`
	} `json:"version"`
}

// Probe checks that the Podman service answers on its socket. It queries the
// libpod surface specifically, so a Docker daemon listening on a
// misconfigured path cannot be mistaken for Podman.
func (b *PodmanBackend) Probe(ctx context.Context) (BackendStatus, error) {
	status := BackendStatus{Name: BackendPodman, Kinds: b.Kinds()}

	probeCtx, cancel := withTimeout(ctx, b.cfg.ProbeTimeout)
	defer cancel()

	var info podmanInfo
	if err := b.libpod.do(probeCtx, "GET", "/info", nil, nil, &info); err != nil {
		status.Reason = "unreachable"
		return status, err
	}
	status.Available = true
	status.Version = info.Version.Version
	return status, nil
}

// Create builds a container from a resolved spec.
func (b *PodmanBackend) Create(ctx context.Context, spec resolvedSpec) (Instance, error) {
	return b.compat.Create(ctx, spec)
}

// Start starts an existing container.
func (b *PodmanBackend) Start(ctx context.Context, ref Ref) error {
	return b.compat.Start(ctx, ref)
}

// Stop stops a running container.
func (b *PodmanBackend) Stop(ctx context.Context, ref Ref, grace time.Duration) error {
	return b.compat.Stop(ctx, ref, grace)
}

// Restart restarts a container.
func (b *PodmanBackend) Restart(ctx context.Context, ref Ref, grace time.Duration) error {
	return b.compat.Restart(ctx, ref, grace)
}

// Remove destroys a container.
func (b *PodmanBackend) Remove(ctx context.Context, ref Ref, opts RemoveOptions) error {
	return b.compat.Remove(ctx, ref, opts)
}

// Inspect reports one container's current status.
func (b *PodmanBackend) Inspect(ctx context.Context, ref Ref) (Instance, error) {
	return b.compat.Inspect(ctx, ref)
}

// List enumerates containers matching a tenant-scoped filter.
func (b *PodmanBackend) List(ctx context.Context, filter Filter) ([]Instance, error) {
	return b.compat.List(ctx, filter)
}

// Logs reads a bounded, tailed slice of a container's output.
func (b *PodmanBackend) Logs(ctx context.Context, ref Ref, opts LogOptions) ([]LogLine, error) {
	return b.compat.Logs(ctx, ref, opts)
}

// PullImage fetches an image, pinned to a digest when one was supplied.
func (b *PodmanBackend) PullImage(ctx context.Context, req ImageRequest) (ImageRef, error) {
	return b.compat.PullImage(ctx, req)
}

// Exec runs an argv slice inside a container. Without stdin the request goes
// over the socket exactly as it does for Docker; with stdin it goes through
// the client binary, which is the only interface that can deliver it.
func (b *PodmanBackend) Exec(ctx context.Context, ref Ref, req ExecRequest) (ExecResult, error) {
	if len(req.Stdin) == 0 {
		return b.compat.Exec(ctx, ref, req)
	}
	return b.execViaCLI(ctx, ref, req)
}

// execViaCLI runs an exec that carries standard input.
//
// This is the one operation the Podman REST API cannot express for this
// transport: feeding stdin requires the hijacked bidirectional stream that
// the socket client here does not implement, so the client binary is used
// instead. The command is assembled as an argv slice and handed to
// exec.CommandContext; nothing is formatted into a command line.
func (b *PodmanBackend) execViaCLI(ctx context.Context, ref Ref, req ExecRequest) (ExecResult, error) {
	var out ExecResult
	if b.binary == "" {
		return out, unsupportedErr(BackendPodman, "exec_stdin")
	}
	if err := ValidateArgv(req.Argv); err != nil {
		return out, err
	}
	if err := ValidateEnv(req.Env); err != nil {
		return out, err
	}
	if req.WorkingDir != "" {
		if err := ValidateGuestPath("working_dir", req.WorkingDir); err != nil {
			return out, err
		}
	}
	if req.User != "" && hasUnsafeChars(req.User) {
		return out, validationErr("user", "charset")
	}
	qualified, err := ref.Qualified()
	if err != nil {
		return out, err
	}

	args := []string{"exec", "--interactive"}
	if req.WorkingDir != "" {
		args = append(args, "--workdir", req.WorkingDir)
	}
	if req.User != "" {
		args = append(args, "--user", req.User)
	}
	for _, pair := range envPairs(req.Env) {
		args = append(args, "--env", pair)
	}
	// The double dash stops option parsing, so a workload command that begins
	// with a hyphen is treated as the command and never as a podman flag.
	args = append(args, qualified, "--")
	args = append(args, req.Argv...)

	execCtx, cancel := withTimeout(ctx, b.cfg.ExecTimeout)
	defer cancel()

	result, err := b.runner.Run(execCtx, b.binary, args, req.Stdin)
	if err != nil {
		return out, err
	}
	limit := clampExecBytes(req.MaxOutputBytes)
	stdout, stdoutCut := capBytes(result.Stdout, limit)
	stderr, stderrCut := capBytes(result.Stderr, limit)
	return ExecResult{
		ExitCode:  result.ExitCode,
		Stdout:    stdout,
		Stderr:    stderr,
		Truncated: result.Truncated || stdoutCut || stderrCut,
	}, nil
}

// Snapshot is not offered: Podman's checkpoint is a CRIU freeze of a running
// process tree, not a restorable point-in-time image of the workload.
func (b *PodmanBackend) Snapshot(ctx context.Context, ref Ref, name string) (Snapshot, error) {
	return Snapshot{}, unsupportedErr(BackendPodman, string(CapSnapshot))
}

// Restore is not offered for the same reason as Snapshot.
func (b *PodmanBackend) Restore(ctx context.Context, ref Ref, name string) error {
	return unsupportedErr(BackendPodman, string(CapRestore))
}

// ListSnapshots is not offered for the same reason as Snapshot.
func (b *PodmanBackend) ListSnapshots(ctx context.Context, ref Ref) ([]Snapshot, error) {
	return nil, unsupportedErr(BackendPodman, string(CapListSnapshots))
}

// capBytes truncates a captured stream to a ceiling, reporting whether it
// had to.
func capBytes(data []byte, limit int64) ([]byte, bool) {
	if limit <= 0 || int64(len(data)) <= limit {
		return data, false
	}
	return data[:limit], true
}
