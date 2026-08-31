package orchestrator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// dockerAPIVersion pins the Engine API version this backend speaks. Pinning
// rather than negotiating keeps request and response shapes stable across
// daemon upgrades on a host nobody is watching.
const dockerAPIVersion = "/v1.43"

// dockerHost is the placeholder authority in the request URL. The unix
// transport ignores it entirely; it exists only because http.NewRequest
// requires a host.
const dockerHost = "docker"

// DockerBackend drives the Docker Engine over its REST API on a local unix
// socket. It speaks plain HTTP through the shared Doer, so no vendored
// Docker SDK is involved and the whole backend is exercisable in-process.
// The Podman compatibility API answers the same request shapes on its own
// socket, so PodmanBackend reuses this type with a different name, host, and
// version prefix rather than duplicating every request builder.
type DockerBackend struct {
	cfg  Config
	name BackendName
	api  *apiClient
}

// NewDockerBackend builds a Docker backend. A nil doer opens the configured
// engine socket; a non-nil doer is used verbatim, which is how the test
// suite drives the backend without a daemon.
func NewDockerBackend(cfg Config, doer Doer) (*DockerBackend, error) {
	full := cfg.withDefaults()
	if doer == nil {
		transport, err := NewUnixTransport(full.DockerSocket, full.RequestTimeout)
		if err != nil {
			return nil, err
		}
		doer = transport
	}
	return newCompatBackend(full, doer, BackendDocker, dockerHost, dockerAPIVersion), nil
}

// newCompatBackend builds a Docker-compatible API backend under a given
// engine identity. Docker and the Podman compatibility API share every
// request shape this package uses; only the name, authority, and version
// prefix differ.
func newCompatBackend(cfg Config, doer Doer, name BackendName, host, prefix string) *DockerBackend {
	return &DockerBackend{cfg: cfg, name: name, api: newAPIClient(doer, name, host, prefix)}
}

// Name identifies the backend.
func (b *DockerBackend) Name() BackendName { return b.name }

// Kinds reports that Docker manages containers only.
func (b *DockerBackend) Kinds() []Kind { return []Kind{KindContainer} }

// Capabilities reports what the Engine API can actually do. Root filesystem
// quotas depend on the storage driver and snapshots have no restore
// primitive, so neither is claimed.
func (b *DockerBackend) Capabilities() CapabilitySet {
	return newCapabilitySet(
		CapCreate, CapStart, CapStop, CapRestart, CapRemove, CapInspect, CapList,
		CapLogs, CapExec, CapCPULimit, CapMemoryLimit, CapPidsLimit,
		CapPortMapping, CapVolumeMount, CapImagePull, CapDigestPin, CapRegistryAuth,
	)
}

// dockerVersion is the subset of the version document this backend reads.
type dockerVersion struct {
	Version    string `json:"Version"`
	APIVersion string `json:"ApiVersion"`
}

// Probe checks that the daemon answers on its socket.
func (b *DockerBackend) Probe(ctx context.Context) (BackendStatus, error) {
	status := BackendStatus{Name: b.name, Kinds: b.Kinds()}

	probeCtx, cancel := withTimeout(ctx, b.cfg.ProbeTimeout)
	defer cancel()

	var version dockerVersion
	if err := b.api.do(probeCtx, "GET", "/version", nil, nil, &version); err != nil {
		status.Reason = "unreachable"
		return status, err
	}
	status.Available = true
	status.Version = version.Version
	return status, nil
}

// dockerPortBinding is one published host endpoint.
type dockerPortBinding struct {
	HostIP   string `json:"HostIp,omitempty"`
	HostPort string `json:"HostPort,omitempty"`
}

// dockerRestartPolicy mirrors the daemon's restart policy document.
type dockerRestartPolicy struct {
	Name string `json:"Name"`
}

// dockerHostConfig is the host-side half of a create request.
type dockerHostConfig struct {
	Binds           []string                       `json:"Binds,omitempty"`
	PortBindings    map[string][]dockerPortBinding `json:"PortBindings,omitempty"`
	NetworkMode     string                         `json:"NetworkMode,omitempty"`
	Memory          int64                          `json:"Memory,omitempty"`
	MemorySwap      int64                          `json:"MemorySwap,omitempty"`
	NanoCPUs        int64                          `json:"NanoCpus,omitempty"`
	PidsLimit       int64                          `json:"PidsLimit,omitempty"`
	CapDrop         []string                       `json:"CapDrop,omitempty"`
	CapAdd          []string                       `json:"CapAdd,omitempty"`
	SecurityOpt     []string                       `json:"SecurityOpt,omitempty"`
	ReadonlyRootfs  bool                           `json:"ReadonlyRootfs,omitempty"`
	Privileged      bool                           `json:"Privileged"`
	RestartPolicy   dockerRestartPolicy            `json:"RestartPolicy"`
	AutoRemove      bool                           `json:"AutoRemove"`
	PublishAllPorts bool                           `json:"PublishAllPorts"`
}

// dockerCreateRequest is the container create document.
type dockerCreateRequest struct {
	Image        string              `json:"Image"`
	Cmd          []string            `json:"Cmd,omitempty"`
	Env          []string            `json:"Env,omitempty"`
	Labels       map[string]string   `json:"Labels,omitempty"`
	WorkingDir   string              `json:"WorkingDir,omitempty"`
	User         string              `json:"User,omitempty"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts,omitempty"`
	HostConfig   dockerHostConfig    `json:"HostConfig"`
}

// dockerCreateResponse carries the new container identifier.
type dockerCreateResponse struct {
	ID string `json:"Id"`
}

// Create builds a container from a resolved spec.
func (b *DockerBackend) Create(ctx context.Context, spec resolvedSpec) (Instance, error) {
	var out Instance
	if spec.Spec.Kind != KindContainer {
		return out, unsupportedErr(b.name, string(KindVM))
	}
	if err := checkSpecSupport(b.name, b.Capabilities(), spec); err != nil {
		return out, err
	}

	body := dockerCreateRequest{
		Image:      imageWithDigest(spec.Spec.Image, spec.Spec.ImageDigest),
		Cmd:        spec.Spec.Command,
		Env:        envPairs(spec.Spec.Env),
		Labels:     spec.Labels,
		WorkingDir: spec.Spec.WorkingDir,
		User:       spec.Spec.User,
		HostConfig: dockerHostConfig{
			Binds:          dockerBinds(spec.Mounts),
			Memory:         spec.Spec.Resources.MemoryBytes,
			MemorySwap:     spec.Spec.Resources.MemoryBytes,
			NanoCPUs:       nanoCPUs(spec.Spec.Resources.CPUCores),
			PidsLimit:      spec.PidsLimit,
			CapDrop:        spec.Profile.DropCapabilities,
			CapAdd:         spec.AddCapabilities,
			ReadonlyRootfs: spec.Spec.ReadOnlyRoot,
			Privileged:     spec.Spec.Privileged,
			RestartPolicy:  dockerRestartPolicy{Name: string(spec.Spec.Restart)},
		},
	}
	if spec.Profile.NoNewPrivileges {
		body.HostConfig.SecurityOpt = append(body.HostConfig.SecurityOpt, "no-new-privileges:true")
	}

	// The network half is taken from the resolved spec, never from the raw
	// request: host networking is already refused for tenant workloads by the
	// profile gate, so an empty resolved name can only mean "no network".
	switch spec.Spec.Network.Mode {
	case NetworkNone:
		body.HostConfig.NetworkMode = "none"
	case NetworkHost:
		body.HostConfig.NetworkMode = "host"
	default:
		body.HostConfig.NetworkMode = spec.NetworkName
	}

	if len(spec.Spec.Network.Ports) > 0 {
		body.ExposedPorts = make(map[string]struct{}, len(spec.Spec.Network.Ports))
		body.HostConfig.PortBindings = make(map[string][]dockerPortBinding, len(spec.Spec.Network.Ports))
		for _, port := range spec.Spec.Network.Ports {
			key := portKey(port)
			body.ExposedPorts[key] = struct{}{}
			body.HostConfig.PortBindings[key] = []dockerPortBinding{{
				HostIP:   port.HostIP,
				HostPort: strconv.Itoa(port.HostPort),
			}}
		}
	}

	createCtx, cancel := withTimeout(ctx, b.cfg.CreateTimeout)
	defer cancel()

	query := url.Values{"name": {spec.Qualified}}
	var created dockerCreateResponse
	if err := b.api.do(createCtx, "POST", "/containers/create", query, body, &created); err != nil {
		return out, err
	}

	return Instance{
		Ref:           spec.Spec.Ref,
		Backend:       b.name,
		Kind:          KindContainer,
		ID:            created.ID,
		QualifiedName: spec.Qualified,
		State:         StateCreated,
		Image:         spec.Spec.Image,
		ImageDigest:   spec.Spec.ImageDigest,
		CreatedAt:     time.Now().UTC(),
		Resources:     spec.Spec.Resources,
	}, nil
}

// containerPath builds a per-container route from a validated reference.
func (b *DockerBackend) containerPath(ref Ref, suffix string) (string, error) {
	qualified, err := ref.Qualified()
	if err != nil {
		return "", err
	}
	return "/containers/" + pathEscape(qualified) + suffix, nil
}

// Start starts an existing container.
func (b *DockerBackend) Start(ctx context.Context, ref Ref) error {
	p, err := b.containerPath(ref, "/start")
	if err != nil {
		return err
	}
	callCtx, cancel := withTimeout(ctx, b.cfg.RequestTimeout)
	defer cancel()
	return b.api.do(callCtx, "POST", p, nil, nil, nil)
}

// Stop stops a running container, allowing grace for a clean shutdown.
func (b *DockerBackend) Stop(ctx context.Context, ref Ref, grace time.Duration) error {
	p, err := b.containerPath(ref, "/stop")
	if err != nil {
		return err
	}
	callCtx, cancel := withTimeout(ctx, b.cfg.RequestTimeout+graceSeconds(grace))
	defer cancel()
	query := url.Values{"t": {strconv.Itoa(int(graceSeconds(grace).Seconds()))}}
	return b.api.do(callCtx, "POST", p, query, nil, nil)
}

// Restart restarts a container.
func (b *DockerBackend) Restart(ctx context.Context, ref Ref, grace time.Duration) error {
	p, err := b.containerPath(ref, "/restart")
	if err != nil {
		return err
	}
	callCtx, cancel := withTimeout(ctx, b.cfg.RequestTimeout+graceSeconds(grace))
	defer cancel()
	query := url.Values{"t": {strconv.Itoa(int(graceSeconds(grace).Seconds()))}}
	return b.api.do(callCtx, "POST", p, query, nil, nil)
}

// Remove destroys a container.
func (b *DockerBackend) Remove(ctx context.Context, ref Ref, opts RemoveOptions) error {
	p, err := b.containerPath(ref, "")
	if err != nil {
		return err
	}
	callCtx, cancel := withTimeout(ctx, b.cfg.RequestTimeout)
	defer cancel()
	query := url.Values{
		"force": {strconv.FormatBool(opts.Force)},
		"v":     {strconv.FormatBool(opts.RemoveVolumes)},
	}
	return b.api.do(callCtx, "DELETE", p, query, nil, nil)
}

// dockerNetwork is one attached network in an inspect document.
type dockerNetwork struct {
	IPAddress string `json:"IPAddress"`
}

// dockerInspect is the subset of the inspect document this backend reads.
type dockerInspect struct {
	ID      string `json:"Id"`
	Name    string `json:"Name"`
	Created string `json:"Created"`
	Image   string `json:"Image"`
	State   struct {
		Status    string `json:"Status"`
		ExitCode  int    `json:"ExitCode"`
		StartedAt string `json:"StartedAt"`
	} `json:"State"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		Memory    int64 `json:"Memory"`
		NanoCPUs  int64 `json:"NanoCpus"`
		PidsLimit int64 `json:"PidsLimit"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Ports    map[string][]dockerPortBinding `json:"Ports"`
		Networks map[string]dockerNetwork       `json:"Networks"`
	} `json:"NetworkSettings"`
}

// Inspect reports one container's current status.
func (b *DockerBackend) Inspect(ctx context.Context, ref Ref) (Instance, error) {
	var out Instance
	p, err := b.containerPath(ref, "/json")
	if err != nil {
		return out, err
	}

	callCtx, cancel := withTimeout(ctx, b.cfg.RequestTimeout)
	defer cancel()

	var doc dockerInspect
	if err := b.api.do(callCtx, "GET", p, nil, nil, &doc); err != nil {
		return out, err
	}
	return b.instanceFrom(doc, ref)
}

// instanceFrom converts an inspect document into an Instance and re-checks
// that the container really belongs to the requesting tenant. The qualified
// name already scopes the lookup; the label check is the second, independent
// barrier so a container adopted from elsewhere under a colliding name can
// never be returned to the wrong account.
func (b *DockerBackend) instanceFrom(doc dockerInspect, want Ref) (Instance, error) {
	var out Instance
	labels := doc.Config.Labels
	if labels[LabelTenant] != "" && want.TenantID != "" && labels[LabelTenant] != want.TenantID {
		return out, tenantErr()
	}

	created, _ := time.Parse(time.RFC3339Nano, doc.Created)
	started, _ := time.Parse(time.RFC3339Nano, doc.State.StartedAt)

	out = Instance{
		Ref:           want,
		Backend:       b.name,
		Kind:          KindContainer,
		ID:            doc.ID,
		QualifiedName: strings.TrimPrefix(doc.Name, "/"),
		State:         dockerState(doc.State.Status),
		Image:         doc.Config.Image,
		ImageDigest:   digestOf(doc.Image),
		CreatedAt:     created.UTC(),
		StartedAt:     started.UTC(),
		ExitCode:      doc.State.ExitCode,
		Ports:         portsFromBindings(doc.NetworkSettings.Ports),
		Resources: Resources{
			MemoryBytes: doc.HostConfig.Memory,
			CPUCores:    coresFromNano(doc.HostConfig.NanoCPUs),
			PidsLimit:   doc.HostConfig.PidsLimit,
		},
	}
	for _, network := range doc.NetworkSettings.Networks {
		if network.IPAddress != "" {
			out.Addresses = append(out.Addresses, network.IPAddress)
		}
	}
	return out, nil
}

// dockerListEntry is one row of the container list.
type dockerListEntry struct {
	ID      string              `json:"Id"`
	Names   []string            `json:"Names"`
	Image   string              `json:"Image"`
	ImageID string              `json:"ImageID"`
	State   string              `json:"State"`
	Created int64               `json:"Created"`
	Labels  map[string]string   `json:"Labels"`
	Ports   []dockerListPortRow `json:"Ports"`
}

// dockerListPortRow is one published port in a list row.
type dockerListPortRow struct {
	IP          string `json:"IP"`
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort"`
	Type        string `json:"Type"`
}

// List enumerates containers matching a tenant-scoped filter.
func (b *DockerBackend) List(ctx context.Context, filter Filter) ([]Instance, error) {
	if filter.Kind == KindVM {
		return nil, unsupportedErr(b.name, string(KindVM))
	}
	labels, err := listLabelFilters(filter)
	if err != nil {
		return nil, err
	}

	encoded, err := json.Marshal(map[string][]string{"label": labels})
	if err != nil {
		return nil, backendErr(b.name, "encode_filters", err)
	}

	callCtx, cancel := withTimeout(ctx, b.cfg.RequestTimeout)
	defer cancel()

	query := url.Values{"all": {"1"}, "filters": {string(encoded)}}
	var rows []dockerListEntry
	if err := b.api.do(callCtx, "GET", "/containers/json", query, nil, &rows); err != nil {
		return nil, err
	}

	out := make([]Instance, 0, len(rows))
	for _, row := range rows {
		name := ""
		if len(row.Names) > 0 {
			name = strings.TrimPrefix(row.Names[0], "/")
		}
		ref, ok := parseQualified(name)
		if !ok {
			continue
		}
		if filter.State != "" && dockerState(row.State) != filter.State {
			continue
		}
		instance := Instance{
			Ref:           ref,
			Backend:       b.name,
			Kind:          KindContainer,
			ID:            row.ID,
			QualifiedName: name,
			State:         dockerState(row.State),
			Image:         row.Image,
			ImageDigest:   digestOf(row.ImageID),
			CreatedAt:     time.Unix(row.Created, 0).UTC(),
		}
		for _, port := range row.Ports {
			if port.PublicPort == 0 {
				continue
			}
			instance.Ports = append(instance.Ports, PortMapping{
				HostIP:     port.IP,
				HostPort:   port.PublicPort,
				TargetPort: port.PrivatePort,
				Protocol:   port.Type,
			})
		}
		out = append(out, instance)
	}
	return out, nil
}

// Logs reads a bounded, tailed slice of a container's output. The request
// always carries an explicit tail and never sets follow, so a log read can
// neither stream forever nor return an unbounded backlog.
func (b *DockerBackend) Logs(ctx context.Context, ref Ref, opts LogOptions) ([]LogLine, error) {
	p, err := b.containerPath(ref, "/logs")
	if err != nil {
		return nil, err
	}
	opts = normalizeLogOptions(opts)

	query := url.Values{
		"stdout":     {strconv.FormatBool(opts.Stdout)},
		"stderr":     {strconv.FormatBool(opts.Stderr)},
		"tail":       {strconv.Itoa(opts.Tail)},
		"timestamps": {"1"},
		"follow":     {"0"},
	}
	if !opts.Since.IsZero() {
		query.Set("since", strconv.FormatInt(opts.Since.Unix(), 10))
	}

	callCtx, cancel := withTimeout(ctx, b.cfg.LogTimeout)
	defer cancel()

	resp, err := b.api.stream(callCtx, "GET", p, query, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	stdout, stderr, _, err := demuxStream(resp.Body, opts.MaxBytes)
	if err != nil {
		return nil, backendErr(b.name, "read_logs", err)
	}
	return mergeLogLines(
		splitLogLines(StreamStdout, stdout, opts.Tail),
		splitLogLines(StreamStderr, stderr, opts.Tail),
		opts.Tail,
	), nil
}

// dockerExecCreate is the exec create document.
type dockerExecCreate struct {
	AttachStdout bool     `json:"AttachStdout"`
	AttachStderr bool     `json:"AttachStderr"`
	Tty          bool     `json:"Tty"`
	Cmd          []string `json:"Cmd"`
	Env          []string `json:"Env,omitempty"`
	WorkingDir   string   `json:"WorkingDir,omitempty"`
	User         string   `json:"User,omitempty"`
}

// dockerExecStart is the exec start document.
type dockerExecStart struct {
	Detach bool `json:"Detach"`
	Tty    bool `json:"Tty"`
}

// dockerExecInspect carries the finished exit status.
type dockerExecInspect struct {
	ExitCode int  `json:"ExitCode"`
	Running  bool `json:"Running"`
}

// Exec runs an argv slice inside a container. The command is sent as a
// vector to the daemon, so nothing in it is ever parsed by a shell.
func (b *DockerBackend) Exec(ctx context.Context, ref Ref, req ExecRequest) (ExecResult, error) {
	var out ExecResult
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
	// Feeding stdin requires the daemon's hijacked bidirectional stream,
	// which this transport deliberately does not implement; the operation is
	// refused rather than silently discarding the input.
	if len(req.Stdin) > 0 {
		return out, unsupportedErr(b.name, "exec_stdin")
	}

	p, err := b.containerPath(ref, "/exec")
	if err != nil {
		return out, err
	}

	execCtx, cancel := withTimeout(ctx, b.cfg.ExecTimeout)
	defer cancel()

	create := dockerExecCreate{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          req.Argv,
		Env:          envPairs(req.Env),
		WorkingDir:   req.WorkingDir,
		User:         req.User,
	}
	var created dockerCreateResponse
	if err := b.api.do(execCtx, "POST", p, nil, create, &created); err != nil {
		return out, err
	}
	if created.ID == "" {
		return out, backendErr(b.name, "exec_create", nil)
	}

	execPath := "/exec/" + pathEscape(created.ID)
	resp, err := b.api.stream(execCtx, "POST", execPath+"/start", nil, dockerExecStart{}, nil)
	if err != nil {
		return out, err
	}
	limit := clampExecBytes(req.MaxOutputBytes)
	stdout, stderr, truncated, readErr := demuxStream(resp.Body, limit)
	resp.Body.Close()
	if readErr != nil {
		return out, backendErr(b.name, "exec_read", readErr)
	}

	var inspect dockerExecInspect
	if err := b.api.do(execCtx, "GET", execPath+"/json", nil, nil, &inspect); err != nil {
		return out, err
	}

	return ExecResult{
		ExitCode:  inspect.ExitCode,
		Stdout:    stdout,
		Stderr:    stderr,
		Truncated: truncated,
	}, nil
}

// dockerImageInspect is the subset of the image document this backend reads.
type dockerImageInspect struct {
	ID          string   `json:"Id"`
	RepoDigests []string `json:"RepoDigests"`
	Size        int64    `json:"Size"`
}

// PullImage fetches an image. When the request carries a digest the pull is
// pinned to that immutable content address, so a mutable tag cannot be
// swapped underneath a tenant between pull and run.
func (b *DockerBackend) PullImage(ctx context.Context, req ImageRequest) (ImageRef, error) {
	var out ImageRef
	if err := ValidateImageRef(req.Reference); err != nil {
		return out, err
	}
	if err := ValidateDigest(req.Digest); err != nil {
		return out, err
	}

	name, tag := splitImageRef(req.Reference)
	query := url.Values{"fromImage": {name}}
	if req.Digest != "" {
		query.Set("tag", req.Digest)
	} else {
		query.Set("tag", tag)
	}

	headers, err := registryAuthHeader(req.Auth)
	if err != nil {
		return out, err
	}

	pullCtx, cancel := withTimeout(ctx, b.cfg.PullTimeout)
	defer cancel()

	resp, err := b.api.stream(pullCtx, "POST", "/images/create", query, nil, headers)
	if err != nil {
		return out, err
	}
	// The daemon reports progress as a JSON stream and only finishes the
	// pull once the body is drained; the read is capped so a hostile registry
	// cannot make the daemon feed us an endless progress log.
	readCapped(resp.Body, DefaultMaxBodyBytes)
	resp.Body.Close()

	pulled := imageWithDigest(req.Reference, req.Digest)
	var doc dockerImageInspect
	if err := b.api.do(pullCtx, "GET", "/images/"+pathEscape(pulled)+"/json", nil, nil, &doc); err != nil {
		return out, err
	}

	out = ImageRef{
		Reference: req.Reference,
		Digest:    req.Digest,
		ID:        doc.ID,
		SizeBytes: doc.Size,
	}
	if out.Digest == "" && len(doc.RepoDigests) > 0 {
		out.Digest = digestOf(doc.RepoDigests[0])
	}
	return out, nil
}

// Snapshot is not offered: committing a container produces a new image, not
// a restorable point-in-time snapshot, and presenting it as one would give
// an operator a restore button that cannot roll anything back.
func (b *DockerBackend) Snapshot(ctx context.Context, ref Ref, name string) (Snapshot, error) {
	return Snapshot{}, unsupportedErr(b.name, string(CapSnapshot))
}

// Restore is not offered by the Engine API.
func (b *DockerBackend) Restore(ctx context.Context, ref Ref, name string) error {
	return unsupportedErr(b.name, string(CapRestore))
}

// ListSnapshots is not offered by the Engine API.
func (b *DockerBackend) ListSnapshots(ctx context.Context, ref Ref) ([]Snapshot, error) {
	return nil, unsupportedErr(b.name, string(CapListSnapshots))
}

// dockerState maps a daemon status string onto the package's state model.
func dockerState(status string) State {
	switch strings.ToLower(status) {
	case "created":
		return StateCreated
	case "running", "restarting":
		return StateRunning
	case "paused":
		return StatePaused
	case "exited", "removing":
		return StateStopped
	case "dead":
		return StateError
	default:
		return StateUnknown
	}
}

// dockerBinds renders resolved mounts as daemon bind strings. Every source
// has already been confined to the tenant storage root or a configured
// application data root, and every component has passed the allowlist, so no
// component can contain the separator the daemon splits on.
func dockerBinds(mounts []resolvedMount) []string {
	if len(mounts) == 0 {
		return nil
	}
	out := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		bind := mount.HostPath + ":" + mount.Target
		if mount.ReadOnly {
			bind += ":ro"
		} else {
			bind += ":rw"
		}
		out = append(out, bind)
	}
	return out
}

// envPairs renders an environment map as sorted KEY=VALUE strings so a
// create request is byte-identical for identical input.
func envPairs(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// portKey renders a container port as the daemon's "port/proto" key.
func portKey(p PortMapping) string {
	proto := p.Protocol
	if proto == "" {
		proto = "tcp"
	}
	return strconv.Itoa(p.TargetPort) + "/" + proto
}

// portsFromBindings converts an inspect port map into port mappings.
func portsFromBindings(bindings map[string][]dockerPortBinding) []PortMapping {
	if len(bindings) == 0 {
		return nil
	}
	keys := make([]string, 0, len(bindings))
	for k := range bindings {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []PortMapping
	for _, key := range keys {
		portText, proto, ok := strings.Cut(key, "/")
		if !ok {
			proto = "tcp"
			portText = key
		}
		target, err := strconv.Atoi(portText)
		if err != nil {
			continue
		}
		for _, binding := range bindings[key] {
			hostPort, err := strconv.Atoi(binding.HostPort)
			if err != nil {
				continue
			}
			out = append(out, PortMapping{
				HostIP:     binding.HostIP,
				HostPort:   hostPort,
				TargetPort: target,
				Protocol:   proto,
			})
		}
	}
	return out
}

// nanoCPUs converts a fractional core allowance into the daemon's nano-CPU
// unit.
func nanoCPUs(cores float64) int64 {
	if cores <= 0 {
		return 0
	}
	return int64(cores * 1e9)
}

// coresFromNano converts a nano-CPU allowance back into fractional cores.
func coresFromNano(nano int64) float64 {
	if nano <= 0 {
		return 0
	}
	return float64(nano) / 1e9
}

// graceSeconds bounds a shutdown grace period.
func graceSeconds(grace time.Duration) time.Duration {
	if grace <= 0 {
		return DefaultStopGrace
	}
	if grace > 5*time.Minute {
		return 5 * time.Minute
	}
	return grace.Round(time.Second)
}

// imageWithDigest pins a reference to an immutable digest when one is known.
func imageWithDigest(reference, digest string) string {
	if digest == "" {
		return reference
	}
	name, _ := splitImageRef(reference)
	return name + "@" + digest
}

// splitImageRef separates a reference into its name and tag. A colon that
// belongs to a registry port is left alone, since it appears before the last
// path separator.
func splitImageRef(reference string) (name, tag string) {
	slash := strings.LastIndex(reference, "/")
	colon := strings.LastIndex(reference, ":")
	if colon > slash {
		return reference[:colon], reference[colon+1:]
	}
	return reference, "latest"
}

// digestOf extracts the digest portion of a repository digest or content
// identifier, returning an empty string when there is none.
func digestOf(reference string) string {
	if at := strings.LastIndex(reference, "@"); at >= 0 {
		reference = reference[at+1:]
	}
	if strings.HasPrefix(reference, "sha256:") {
		return reference
	}
	return ""
}

// registryAuthHeader renders registry credentials into the daemon's
// base64 JSON header. The credentials are never logged and never echoed
// back; they exist only for the duration of the pull.
func registryAuthHeader(auth *RegistryAuth) (map[string]string, error) {
	if auth == nil {
		return nil, nil
	}
	if auth.ServerAddress != "" && hasUnsafeChars(auth.ServerAddress) {
		return nil, validationErr("registry_server", "charset")
	}
	if hasUnsafeChars(auth.Username) {
		return nil, validationErr("registry_username", "charset")
	}
	encoded, err := json.Marshal(map[string]string{
		"username":      auth.Username,
		"password":      auth.Password,
		"serveraddress": auth.ServerAddress,
	})
	if err != nil {
		return nil, validationErr("registry_auth", "encode")
	}
	return map[string]string{"X-Registry-Auth": base64.URLEncoding.EncodeToString(encoded)}, nil
}

// listLabelFilters builds the label selectors that scope a list to one
// tenant. Every listing is filtered by the managed label, so a container the
// panel does not own is never returned, and by the tenant label whenever a
// tenant was named.
func listLabelFilters(filter Filter) ([]string, error) {
	labels := []string{LabelManaged + "=true"}
	if filter.TenantID != "" {
		if err := ValidateTenantID(filter.TenantID); err != nil {
			return nil, err
		}
		labels = append(labels, LabelTenant+"="+filter.TenantID)
	}
	if filter.Class != "" {
		switch filter.Class {
		case ClassTenant, ClassAppManaged:
			labels = append(labels, LabelClass+"="+string(filter.Class))
		default:
			return nil, validationErr("class", "unknown")
		}
	}
	return labels, nil
}
