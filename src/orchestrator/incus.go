package orchestrator

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/security"
)

// incusHost is the placeholder authority in the request URL; the unix
// transport ignores it.
const incusHost = "incus"

// incusRoot is the API root every Incus route hangs off. The client is built
// with an empty prefix so operation URLs the server hands back can be used
// verbatim.
const incusRoot = "/1.0"

// incusConfigPrefix is the namespace Incus reserves for caller-defined
// configuration keys. Every cashp label lives under it.
const incusConfigPrefix = "user."

// IncusBackend drives Incus over its REST API on a local unix socket,
// covering both instance types it offers: system containers and virtual
// machines. No Incus client SDK is involved; the API is ordinary JSON over
// HTTP and its asynchronous operations are awaited explicitly.
type IncusBackend struct {
	cfg Config
	api *apiClient
}

// NewIncusBackend builds an Incus backend. A nil doer opens the configured
// socket.
func NewIncusBackend(cfg Config, doer Doer) (*IncusBackend, error) {
	full := cfg.withDefaults()
	if doer == nil {
		transport, err := NewUnixTransport(full.IncusSocket, full.RequestTimeout)
		if err != nil {
			return nil, err
		}
		doer = transport
	}
	return &IncusBackend{cfg: full, api: newAPIClient(doer, BackendIncus, incusHost, "")}, nil
}

// Name identifies the backend.
func (b *IncusBackend) Name() BackendName { return BackendIncus }

// Kinds reports that Incus manages both containers and virtual machines.
func (b *IncusBackend) Kinds() []Kind { return []Kind{KindContainer, KindVM} }

// Capabilities reports what Incus can do. It is the only backend here that
// covers both instance types and offers real snapshots with restore.
func (b *IncusBackend) Capabilities() CapabilitySet {
	return newCapabilitySet(
		CapCreate, CapStart, CapStop, CapRestart, CapRemove, CapInspect, CapList,
		CapLogs, CapExec, CapCPULimit, CapMemoryLimit, CapDiskLimit, CapPidsLimit,
		CapPortMapping, CapVolumeMount, CapDiskAttach, CapImagePull, CapDigestPin,
		CapRegistryAuth, CapSnapshot, CapRestore, CapListSnapshots,
	)
}

// incusResponse is the envelope every Incus route returns.
type incusResponse struct {
	Type       string          `json:"type"`
	StatusCode int             `json:"status_code"`
	Operation  string          `json:"operation"`
	Error      string          `json:"error"`
	Metadata   json.RawMessage `json:"metadata"`
}

// incusOperation is the operation document returned while waiting.
type incusOperation struct {
	ID         string          `json:"id"`
	Status     string          `json:"status"`
	StatusCode int             `json:"status_code"`
	Err        string          `json:"err"`
	Metadata   json.RawMessage `json:"metadata"`
}

// incusServer is the subset of the server document this backend reads.
type incusServer struct {
	Environment struct {
		ServerVersion string `json:"server_version"`
	} `json:"environment"`
}

// Probe checks that the Incus daemon answers on its socket.
func (b *IncusBackend) Probe(ctx context.Context) (BackendStatus, error) {
	status := BackendStatus{Name: BackendIncus, Kinds: b.Kinds()}

	probeCtx, cancel := withTimeout(ctx, b.cfg.ProbeTimeout)
	defer cancel()

	var server incusServer
	if err := b.call(probeCtx, "GET", incusRoot, nil, nil, &server); err != nil {
		status.Reason = "unreachable"
		return status, err
	}
	status.Available = true
	status.Version = server.Environment.ServerVersion
	return status, nil
}

// call issues a synchronous request and decodes the envelope metadata. An
// asynchronous response is awaited to completion first, so a caller never
// sees a half-finished operation reported as success.
func (b *IncusBackend) call(ctx context.Context, method, p string, q url.Values, body, out any) error {
	var envelope incusResponse
	if err := b.api.do(ctx, method, p, q, body, &envelope); err != nil {
		return err
	}
	if envelope.Type == "async" {
		metadata, err := b.wait(ctx, envelope.Operation)
		if err != nil {
			return err
		}
		return decodeMetadata(metadata, out)
	}
	return decodeMetadata(envelope.Metadata, out)
}

// wait blocks until an asynchronous operation finishes and returns its final
// metadata. The wait itself carries a deadline, so a stuck operation frees
// the caller instead of holding a request open forever.
func (b *IncusBackend) wait(ctx context.Context, operation string) (json.RawMessage, error) {
	if !strings.HasPrefix(operation, incusRoot+"/operations/") {
		return nil, backendErr(BackendIncus, "operation_path", nil)
	}

	seconds := int(b.cfg.RequestTimeout.Seconds())
	if deadline, ok := ctx.Deadline(); ok {
		remaining := int(time.Until(deadline).Seconds()) - 1
		if remaining > 0 {
			seconds = remaining
		}
	}
	if seconds < 1 {
		seconds = 1
	}

	query := url.Values{"timeout": {strconv.Itoa(seconds)}}
	var envelope incusResponse
	if err := b.api.do(ctx, "GET", operation+"/wait", query, nil, &envelope); err != nil {
		return nil, err
	}

	var op incusOperation
	if err := decodeMetadata(envelope.Metadata, &op); err != nil {
		return nil, err
	}
	switch {
	case op.Status == "Success":
		return op.Metadata, nil
	case op.Status == "Running" || op.Status == "Pending":
		return nil, timeoutErr(BackendIncus, "operation", nil)
	default:
		// The daemon's own message frequently quotes host paths and storage
		// pool layout, so it is kept in the logged cause only.
		return nil, backendErr(BackendIncus, "operation", stderrors.New(op.Err))
	}
}

// decodeMetadata unmarshals an envelope's metadata into out, tolerating an
// absent body for routes that return nothing useful.
func decodeMetadata(raw json.RawMessage, out any) error {
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return backendErr(BackendIncus, "decode_metadata", err)
	}
	return nil
}

// incusDevice is one device entry; Incus models every device as a flat
// string map.
type incusDevice map[string]string

// incusCreateRequest is the instance create document.
type incusCreateRequest struct {
	Name         string                 `json:"name"`
	Type         string                 `json:"type"`
	Architecture string                 `json:"architecture,omitempty"`
	Config       map[string]string      `json:"config,omitempty"`
	Devices      map[string]incusDevice `json:"devices,omitempty"`
	Source       incusSource            `json:"source"`
	Profiles     []string               `json:"profiles,omitempty"`
	Ephemeral    bool                   `json:"ephemeral"`
}

// incusSource describes where an instance's root filesystem comes from.
type incusSource struct {
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Alias       string `json:"alias,omitempty"`
}

// Create builds an instance from a resolved spec.
func (b *IncusBackend) Create(ctx context.Context, spec resolvedSpec) (Instance, error) {
	var out Instance
	if err := checkSpecSupport(BackendIncus, b.Capabilities(), spec); err != nil {
		return out, err
	}
	// Incus boots the image's own init; there is no entrypoint override, so a
	// command is refused rather than quietly dropped.
	if len(spec.Spec.Command) > 0 {
		return out, unsupportedErr(BackendIncus, "command_override")
	}
	if spec.Spec.ReadOnlyRoot {
		return out, unsupportedErr(BackendIncus, "readonly_root")
	}
	if spec.Spec.Network.Mode == NetworkHost {
		return out, unsupportedErr(BackendIncus, "host_network")
	}

	config := map[string]string{}
	for key, value := range spec.Labels {
		config[incusConfigPrefix+key] = value
	}
	for key, value := range spec.Spec.Env {
		config["environment."+key] = value
	}
	if spec.VCPUs > 0 {
		config["limits.cpu"] = strconv.Itoa(spec.VCPUs)
	}
	if spec.Spec.Resources.MemoryBytes > 0 {
		config["limits.memory"] = strconv.FormatInt(spec.Spec.Resources.MemoryBytes, 10)
	}
	if spec.PidsLimit > 0 && spec.Spec.Kind == KindContainer {
		config["limits.processes"] = strconv.FormatInt(spec.PidsLimit, 10)
	}
	if spec.Spec.Kind == KindContainer {
		config["security.privileged"] = strconv.FormatBool(spec.Spec.Privileged)
		config["security.nesting"] = "false"
	}
	if spec.Spec.Kind == KindVM {
		config["security.secureboot"] = strconv.FormatBool(spec.Spec.Firmware == FirmwareUEFI)
	}
	config["boot.autostart"] = strconv.FormatBool(spec.Spec.Restart == RestartAlways)

	devices, err := b.devicesFor(spec)
	if err != nil {
		return out, err
	}

	instanceType := "container"
	if spec.Spec.Kind == KindVM {
		instanceType = "virtual-machine"
	}

	source := incusSource{Type: "image"}
	if spec.Spec.ImageDigest != "" {
		source.Fingerprint = strings.TrimPrefix(spec.Spec.ImageDigest, "sha256:")
	} else {
		source.Alias = spec.Spec.Image
	}

	body := incusCreateRequest{
		Name:         spec.Qualified,
		Type:         instanceType,
		Architecture: spec.Spec.Architecture,
		Config:       config,
		Devices:      devices,
		Source:       source,
		Profiles:     []string{"default"},
	}

	createCtx, cancel := withTimeout(ctx, b.cfg.CreateTimeout)
	defer cancel()

	if err := b.call(createCtx, "POST", incusRoot+"/instances", nil, body, nil); err != nil {
		return out, err
	}

	return Instance{
		Ref:           spec.Spec.Ref,
		Backend:       BackendIncus,
		Kind:          spec.Spec.Kind,
		QualifiedName: spec.Qualified,
		State:         StateCreated,
		Image:         spec.Spec.Image,
		ImageDigest:   spec.Spec.ImageDigest,
		CreatedAt:     time.Now().UTC(),
		Resources:     spec.Spec.Resources,
	}, nil
}

// devicesFor renders the resolved network, mounts, disks, and port
// publications as Incus device entries.
func (b *IncusBackend) devicesFor(spec resolvedSpec) (map[string]incusDevice, error) {
	devices := map[string]incusDevice{}

	root := incusDevice{"type": "disk", "path": "/", "pool": "default"}
	if spec.Spec.Resources.DiskBytes > 0 {
		root["size"] = strconv.FormatInt(spec.Spec.Resources.DiskBytes, 10)
	}
	devices["root"] = root

	if spec.Spec.Network.Mode != NetworkNone && spec.NetworkName != "" {
		devices["eth0"] = incusDevice{
			"type":    "nic",
			"network": spec.NetworkName,
			"name":    "eth0",
		}
	}

	for i, mount := range spec.Mounts {
		device := incusDevice{
			"type":   "disk",
			"source": mount.HostPath,
			"path":   mount.Target,
		}
		if mount.ReadOnly {
			device["readonly"] = "true"
		}
		devices["vol"+strconv.Itoa(i)] = device
	}

	for i, disk := range spec.Disks {
		device := incusDevice{
			"type":   "disk",
			"source": disk.HostPath,
		}
		if disk.ReadOnly {
			device["readonly"] = "true"
		}
		devices["disk"+strconv.Itoa(i)] = device
	}

	for i, port := range spec.Spec.Network.Ports {
		if err := ValidatePort(port); err != nil {
			return nil, err
		}
		proto := port.Protocol
		if proto == "" {
			proto = "tcp"
		}
		hostIP := port.HostIP
		if hostIP == "" {
			hostIP = "0.0.0.0"
		}
		devices["port"+strconv.Itoa(i)] = incusDevice{
			"type":    "proxy",
			"listen":  proto + ":" + hostIP + ":" + strconv.Itoa(port.HostPort),
			"connect": proto + ":127.0.0.1:" + strconv.Itoa(port.TargetPort),
		}
	}
	return devices, nil
}

// instancePath builds a per-instance route from a validated reference.
func (b *IncusBackend) instancePath(ref Ref, suffix string) (string, error) {
	qualified, err := ref.Qualified()
	if err != nil {
		return "", err
	}
	return incusRoot + "/instances/" + pathEscape(qualified) + suffix, nil
}

// incusStateRequest changes an instance's run state.
type incusStateRequest struct {
	Action   string `json:"action"`
	Timeout  int    `json:"timeout"`
	Force    bool   `json:"force"`
	Stateful bool   `json:"stateful"`
}

// changeState issues one run-state transition.
func (b *IncusBackend) changeState(ctx context.Context, ref Ref, action string, grace time.Duration) error {
	p, err := b.instancePath(ref, "/state")
	if err != nil {
		return err
	}
	seconds := int(graceSeconds(grace).Seconds())

	callCtx, cancel := withTimeout(ctx, b.cfg.RequestTimeout+graceSeconds(grace))
	defer cancel()

	body := incusStateRequest{Action: action, Timeout: seconds}
	return b.call(callCtx, "PUT", p, nil, body, nil)
}

// Start starts an existing instance.
func (b *IncusBackend) Start(ctx context.Context, ref Ref) error {
	return b.changeState(ctx, ref, "start", 0)
}

// Stop stops a running instance.
func (b *IncusBackend) Stop(ctx context.Context, ref Ref, grace time.Duration) error {
	return b.changeState(ctx, ref, "stop", grace)
}

// Restart restarts an instance.
func (b *IncusBackend) Restart(ctx context.Context, ref Ref, grace time.Duration) error {
	return b.changeState(ctx, ref, "restart", grace)
}

// Remove destroys an instance. A forced removal stops it first, because
// Incus refuses to delete a running instance.
func (b *IncusBackend) Remove(ctx context.Context, ref Ref, opts RemoveOptions) error {
	if opts.Force {
		// Incus refuses to delete a running instance, so a forced removal
		// stops it first. A stop that fails because the instance is already
		// stopped must not block the delete, so the outcome is not fatal here;
		// a genuine problem resurfaces from the delete itself.
		if err := b.changeState(ctx, ref, "stop", 0); IsNotFound(err) {
			return err
		}
	}
	p, err := b.instancePath(ref, "")
	if err != nil {
		return err
	}
	callCtx, cancel := withTimeout(ctx, b.cfg.RequestTimeout)
	defer cancel()
	return b.call(callCtx, "DELETE", p, nil, nil, nil)
}

// incusInstance is the subset of the instance document this backend reads.
type incusInstance struct {
	Name         string                 `json:"name"`
	Type         string                 `json:"type"`
	Status       string                 `json:"status"`
	Architecture string                 `json:"architecture"`
	CreatedAt    time.Time              `json:"created_at"`
	LastUsedAt   time.Time              `json:"last_used_at"`
	Config       map[string]string      `json:"config"`
	Devices      map[string]incusDevice `json:"devices"`
	State        *incusInstanceState    `json:"state"`
}

// incusInstanceState carries the live runtime view of an instance.
type incusInstanceState struct {
	Status  string `json:"status"`
	Network map[string]struct {
		Addresses []struct {
			Family  string `json:"family"`
			Address string `json:"address"`
			Scope   string `json:"scope"`
		} `json:"addresses"`
	} `json:"network"`
}

// Inspect reports one instance's current status.
func (b *IncusBackend) Inspect(ctx context.Context, ref Ref) (Instance, error) {
	var out Instance
	p, err := b.instancePath(ref, "")
	if err != nil {
		return out, err
	}

	callCtx, cancel := withTimeout(ctx, b.cfg.RequestTimeout)
	defer cancel()

	var doc incusInstance
	if err := b.call(callCtx, "GET", p, url.Values{"recursion": {"1"}}, nil, &doc); err != nil {
		return out, err
	}
	return b.instanceFrom(doc, ref)
}

// instanceFrom converts an instance document into an Instance and re-checks
// tenant ownership against the configuration key the panel wrote at create.
func (b *IncusBackend) instanceFrom(doc incusInstance, want Ref) (Instance, error) {
	var out Instance
	owner := doc.Config[incusConfigPrefix+LabelTenant]
	if owner != "" && want.TenantID != "" && owner != want.TenantID {
		return out, tenantErr()
	}

	kind := KindContainer
	if doc.Type == "virtual-machine" {
		kind = KindVM
	}
	status := doc.Status
	if doc.State != nil && doc.State.Status != "" {
		status = doc.State.Status
	}

	out = Instance{
		Ref:           want,
		Backend:       BackendIncus,
		Kind:          kind,
		ID:            doc.Name,
		QualifiedName: doc.Name,
		State:         incusState(status),
		CreatedAt:     doc.CreatedAt.UTC(),
		StartedAt:     doc.LastUsedAt.UTC(),
		Resources:     incusResources(doc.Config, doc.Devices),
		Ports:         incusPorts(doc.Devices),
	}
	if doc.State != nil {
		names := make([]string, 0, len(doc.State.Network))
		for name := range doc.State.Network {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			for _, addr := range doc.State.Network[name].Addresses {
				if addr.Scope == "local" || addr.Address == "" {
					continue
				}
				out.Addresses = append(out.Addresses, addr.Address)
			}
		}
	}
	return out, nil
}

// List enumerates instances matching a tenant-scoped filter. Incus has no
// server-side label filter, so the tenant check is applied here — and it is
// applied to every row, so a workload belonging to another account can never
// appear in a tenant's listing.
func (b *IncusBackend) List(ctx context.Context, filter Filter) ([]Instance, error) {
	if filter.TenantID != "" {
		if err := ValidateTenantID(filter.TenantID); err != nil {
			return nil, err
		}
	}

	callCtx, cancel := withTimeout(ctx, b.cfg.RequestTimeout)
	defer cancel()

	var docs []incusInstance
	if err := b.call(callCtx, "GET", incusRoot+"/instances", url.Values{"recursion": {"2"}}, nil, &docs); err != nil {
		return nil, err
	}

	out := make([]Instance, 0, len(docs))
	for _, doc := range docs {
		if doc.Config[incusConfigPrefix+LabelManaged] != "true" {
			continue
		}
		ref, ok := parseQualified(doc.Name)
		if !ok {
			continue
		}
		if filter.TenantID != "" && ref.TenantID != filter.TenantID {
			continue
		}
		if filter.Class != "" && ref.Class != filter.Class {
			continue
		}
		instance, err := b.instanceFrom(doc, ref)
		if err != nil {
			continue
		}
		if filter.Kind != "" && instance.Kind != filter.Kind {
			continue
		}
		if filter.State != "" && instance.State != filter.State {
			continue
		}
		out = append(out, instance)
	}
	return out, nil
}

// Logs reads the instance console log, tailed and capped. Incus keeps
// per-instance log files rather than a demultiplexed stream, so the console
// log is the equivalent surface.
func (b *IncusBackend) Logs(ctx context.Context, ref Ref, opts LogOptions) ([]LogLine, error) {
	p, err := b.instancePath(ref, "/logs/console.log")
	if err != nil {
		return nil, err
	}
	opts = normalizeLogOptions(opts)

	callCtx, cancel := withTimeout(ctx, b.cfg.LogTimeout)
	defer cancel()

	data, err := b.readLogFile(callCtx, p, opts.MaxBytes)
	if err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return splitLogLines(StreamStdout, data, opts.Tail), nil
}

// readLogFile fetches a raw log file, bounded by a byte ceiling.
func (b *IncusBackend) readLogFile(ctx context.Context, p string, maxBytes int64) ([]byte, error) {
	resp, err := b.api.stream(ctx, "GET", p, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return readCapped(resp.Body, clampLogBytes(maxBytes)), nil
}

// incusExecRequest is the exec document.
type incusExecRequest struct {
	Command          []string          `json:"command"`
	Environment      map[string]string `json:"environment,omitempty"`
	WaitForWebsocket bool              `json:"wait-for-websocket"`
	RecordOutput     bool              `json:"record-output"`
	Interactive      bool              `json:"interactive"`
	Cwd              string            `json:"cwd,omitempty"`
	User             uint32            `json:"user,omitempty"`
}

// incusExecResult is the metadata a finished exec operation carries.
type incusExecResult struct {
	Return int               `json:"return"`
	Output map[string]string `json:"output"`
}

// Exec runs an argv slice inside an instance. The command is transmitted as
// a vector and Incus executes it directly, so no shell parses it.
func (b *IncusBackend) Exec(ctx context.Context, ref Ref, req ExecRequest) (ExecResult, error) {
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
	// Incus streams stdin over a websocket this transport does not open, and
	// its numeric user field cannot express a user name, so both are refused
	// instead of being dropped.
	if len(req.Stdin) > 0 {
		return out, unsupportedErr(BackendIncus, "exec_stdin")
	}
	uid, err := incusUser(req.User)
	if err != nil {
		return out, err
	}

	p, err := b.instancePath(ref, "/exec")
	if err != nil {
		return out, err
	}

	execCtx, cancel := withTimeout(ctx, b.cfg.ExecTimeout)
	defer cancel()

	body := incusExecRequest{
		Command:      req.Argv,
		Environment:  req.Env,
		RecordOutput: true,
		Cwd:          req.WorkingDir,
		User:         uid,
	}
	var result incusExecResult
	if err := b.call(execCtx, "POST", p, nil, body, &result); err != nil {
		return out, err
	}

	limit := clampExecBytes(req.MaxOutputBytes)
	out.ExitCode = result.Return
	if path := result.Output["1"]; path != "" {
		data, err := b.readRecordedOutput(execCtx, path, limit)
		if err != nil {
			return out, err
		}
		out.Stdout = data
	}
	if path := result.Output["2"]; path != "" {
		data, err := b.readRecordedOutput(execCtx, path, limit)
		if err != nil {
			return out, err
		}
		out.Stderr = data
	}
	out.Truncated = int64(len(out.Stdout)) >= limit || int64(len(out.Stderr)) >= limit
	return out, nil
}

// readRecordedOutput fetches one recorded exec output file. The path comes
// from the daemon rather than from a caller, and is still required to sit
// under the API root before it is requested.
func (b *IncusBackend) readRecordedOutput(ctx context.Context, p string, limit int64) ([]byte, error) {
	if !strings.HasPrefix(p, incusRoot+"/instances/") {
		return nil, backendErr(BackendIncus, "exec_output_path", nil)
	}
	resp, err := b.api.stream(ctx, "GET", p, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return readCapped(resp.Body, limit), nil
}

// incusImageSource describes a remote image to fetch.
type incusImageSource struct {
	Type        string `json:"type"`
	Mode        string `json:"mode"`
	Protocol    string `json:"protocol"`
	Server      string `json:"server,omitempty"`
	Alias       string `json:"alias,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Secret      string `json:"secret,omitempty"`
}

// incusImageRequest is the image import document.
type incusImageRequest struct {
	Source     incusImageSource  `json:"source"`
	Aliases    []incusImageAlias `json:"aliases,omitempty"`
	AutoUpdate bool              `json:"auto_update"`
	Public     bool              `json:"public"`
}

// incusImageAlias names a fetched image locally.
type incusImageAlias struct {
	Name string `json:"name"`
}

// incusImage is the subset of the image document this backend reads.
type incusImage struct {
	Fingerprint string `json:"fingerprint"`
	Size        int64  `json:"size"`
}

// PullImage fetches an image or template into the local store. A digest,
// when supplied, is used as the fingerprint so the fetch is pinned to
// immutable content.
func (b *IncusBackend) PullImage(ctx context.Context, req ImageRequest) (ImageRef, error) {
	var out ImageRef
	if err := ValidateImageRef(req.Reference); err != nil {
		return out, err
	}
	if err := ValidateDigest(req.Digest); err != nil {
		return out, err
	}

	source := incusImageSource{Type: "image", Mode: "pull", Protocol: "simplestreams"}
	if req.Digest != "" {
		source.Fingerprint = strings.TrimPrefix(req.Digest, "sha256:")
	} else {
		source.Alias = req.Reference
	}
	if req.Auth != nil {
		// The image server is operator- or tenant-supplied, so it goes through
		// the shared outbound guard before the daemon is asked to fetch from
		// it; that guard is what keeps an image pull from becoming an SSRF
		// probe of the host's internal network.
		if err := security.ValidateOutboundURL(req.Auth.ServerAddress); err != nil {
			return out, validationErr("registry_server", "outbound")
		}
		source.Server = req.Auth.ServerAddress
		source.Secret = req.Auth.Password
	}

	pullCtx, cancel := withTimeout(ctx, b.cfg.PullTimeout)
	defer cancel()

	body := incusImageRequest{Source: source, Aliases: []incusImageAlias{{Name: req.Reference}}}
	var image incusImage
	if err := b.call(pullCtx, "POST", incusRoot+"/images", nil, body, &image); err != nil {
		return out, err
	}

	out = ImageRef{Reference: req.Reference, ID: image.Fingerprint, SizeBytes: image.Size}
	if image.Fingerprint != "" {
		out.Digest = "sha256:" + image.Fingerprint
	} else {
		out.Digest = req.Digest
	}
	return out, nil
}

// incusSnapshotRequest creates a snapshot.
type incusSnapshotRequest struct {
	Name     string `json:"name"`
	Stateful bool   `json:"stateful"`
}

// incusSnapshot is the subset of the snapshot document this backend reads.
type incusSnapshot struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Stateful  bool      `json:"stateful"`
	Size      int64     `json:"size"`
}

// Snapshot captures a point-in-time snapshot of an instance.
func (b *IncusBackend) Snapshot(ctx context.Context, ref Ref, name string) (Snapshot, error) {
	var out Snapshot
	if err := ValidateSnapshotName(name); err != nil {
		return out, err
	}
	p, err := b.instancePath(ref, "/snapshots")
	if err != nil {
		return out, err
	}

	callCtx, cancel := withTimeout(ctx, b.cfg.SnapshotTimeout)
	defer cancel()

	if err := b.call(callCtx, "POST", p, nil, incusSnapshotRequest{Name: name}, nil); err != nil {
		return out, err
	}
	return Snapshot{Name: name, CreatedAt: time.Now().UTC()}, nil
}

// incusRestoreRequest reverts an instance to one of its snapshots.
type incusRestoreRequest struct {
	Restore string `json:"restore"`
}

// Restore reverts an instance to a snapshot.
func (b *IncusBackend) Restore(ctx context.Context, ref Ref, name string) error {
	if err := ValidateSnapshotName(name); err != nil {
		return err
	}
	p, err := b.instancePath(ref, "")
	if err != nil {
		return err
	}

	callCtx, cancel := withTimeout(ctx, b.cfg.SnapshotTimeout)
	defer cancel()

	return b.call(callCtx, "PUT", p, nil, incusRestoreRequest{Restore: name}, nil)
}

// ListSnapshots enumerates an instance's snapshots.
func (b *IncusBackend) ListSnapshots(ctx context.Context, ref Ref) ([]Snapshot, error) {
	p, err := b.instancePath(ref, "/snapshots")
	if err != nil {
		return nil, err
	}

	callCtx, cancel := withTimeout(ctx, b.cfg.RequestTimeout)
	defer cancel()

	var docs []incusSnapshot
	if err := b.call(callCtx, "GET", p, url.Values{"recursion": {"1"}}, nil, &docs); err != nil {
		return nil, err
	}

	out := make([]Snapshot, 0, len(docs))
	for _, doc := range docs {
		name := doc.Name
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		out = append(out, Snapshot{
			Name:      name,
			CreatedAt: doc.CreatedAt.UTC(),
			SizeBytes: doc.Size,
			Stateful:  doc.Stateful,
		})
	}
	return out, nil
}

// incusState maps an Incus status string onto the package's state model.
func incusState(status string) State {
	switch strings.ToLower(status) {
	case "running":
		return StateRunning
	case "stopped":
		return StateStopped
	case "frozen":
		return StatePaused
	case "starting", "stopping", "restarting":
		return StateRunning
	case "error":
		return StateError
	case "ready":
		return StateCreated
	default:
		return StateUnknown
	}
}

// incusResources reads the effective limits back out of an instance's
// configuration and root device.
func incusResources(config map[string]string, devices map[string]incusDevice) Resources {
	var out Resources
	if cores, err := strconv.Atoi(config["limits.cpu"]); err == nil {
		out.CPUCores = float64(cores)
	}
	if memory, err := strconv.ParseInt(config["limits.memory"], 10, 64); err == nil {
		out.MemoryBytes = memory
	}
	if pids, err := strconv.ParseInt(config["limits.processes"], 10, 64); err == nil {
		out.PidsLimit = pids
	}
	if root, ok := devices["root"]; ok {
		if size, err := strconv.ParseInt(root["size"], 10, 64); err == nil {
			out.DiskBytes = size
		}
	}
	return out
}

// incusPorts reads published ports back out of an instance's proxy devices.
func incusPorts(devices map[string]incusDevice) []PortMapping {
	names := make([]string, 0, len(devices))
	for name, device := range devices {
		if device["type"] == "proxy" {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	var out []PortMapping
	for _, name := range names {
		device := devices[name]
		proto, hostIP, hostPort, ok := parseProxyEndpoint(device["listen"])
		if !ok {
			continue
		}
		_, _, targetPort, ok := parseProxyEndpoint(device["connect"])
		if !ok {
			continue
		}
		out = append(out, PortMapping{
			HostIP:     hostIP,
			HostPort:   hostPort,
			TargetPort: targetPort,
			Protocol:   proto,
		})
	}
	return out
}

// parseProxyEndpoint splits a proxy device endpoint of the form
// "tcp:address:port" into its parts.
func parseProxyEndpoint(value string) (proto, address string, port int, ok bool) {
	parts := strings.Split(value, ":")
	if len(parts) < 3 {
		return "", "", 0, false
	}
	portValue, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return "", "", 0, false
	}
	return parts[0], strings.Join(parts[1:len(parts)-1], ":"), portValue, true
}

// incusUser converts an exec user into the numeric uid Incus expects. Only a
// numeric user is accepted, because the API has no field for a name and
// guessing one would run the command as the wrong account.
func incusUser(user string) (uint32, error) {
	if user == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(user, 10, 32)
	if err != nil {
		return 0, unsupportedErr(BackendIncus, "exec_user_name")
	}
	return uint32(value), nil
}
