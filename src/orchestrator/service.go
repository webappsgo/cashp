package orchestrator

import (
	"context"
	"log/slog"
	"time"

	"github.com/webappsgo/cashp/src/logging"
)

// Lifecycle action names. They appear in the audit log and in the event
// table, so they are constants rather than literals scattered through the
// call sites.
const (
	actionCreate        = "orchestrator.create"
	actionStart         = "orchestrator.start"
	actionStop          = "orchestrator.stop"
	actionRestart       = "orchestrator.restart"
	actionRemove        = "orchestrator.remove"
	actionExec          = "orchestrator.exec"
	actionLogs          = "orchestrator.logs"
	actionPull          = "orchestrator.pull"
	actionSnapshot      = "orchestrator.snapshot"
	actionRestore       = "orchestrator.restore"
	outcomeOK           = "ok"
	outcomeError        = "error"
	auditRedactedDetail = "redacted"
)

// Service is the audited, account-scoped entry point to orchestration.
//
// Handlers call this rather than a backend directly. It is the layer that
// binds an action to the account that asked for it: the caller's account is
// taken from the actor, never from the request body, so a reference naming
// somebody else's account is rejected before any engine is contacted.
type Service struct {
	manager *Manager
	store   *Store
}

// NewService builds the facade over a manager and a store. The store is
// optional: a node that has not opened its database yet can still probe and
// list, it simply records nothing.
func NewService(manager *Manager, store *Store) (*Service, error) {
	if manager == nil {
		return nil, validationErr("manager", "required")
	}
	return &Service{manager: manager, store: store}, nil
}

// Manager exposes the backend registry for status and capability views.
func (s *Service) Manager() *Manager { return s.manager }

// authorize confirms the actor may act on the named workload and returns the
// reference with the account filled in from the actor rather than from the
// request.
//
// A global administrator may act on any account. Everyone else is pinned to
// their own: this is the check that turns a guessed workload name into a
// refusal instead of an escalation.
func (s *Service) authorize(actor Actor, ref Ref) (Ref, error) {
	if err := ValidateWorkloadName(ref.Name); err != nil {
		return Ref{}, err
	}
	switch ref.Class {
	case ClassTenant:
	case ClassAppManaged:
		// App-managed service containers belong to the platform, not to an
		// account, so only a global administrator may touch them.
		if actor.Role != RoleGlobalAdmin {
			return Ref{}, tenantErr()
		}
		ref.TenantID = SystemTenantID
		return ref, ref.Validate()
	default:
		return Ref{}, isolationErr(ref.Class, "class", "this class is not managed by the orchestrator")
	}

	if actor.Role == RoleGlobalAdmin {
		if ref.TenantID == "" {
			ref.TenantID = actor.TenantID
		}
		return ref, ref.Validate()
	}
	if actor.TenantID == "" {
		return Ref{}, tenantErr()
	}
	if ref.TenantID != "" && ref.TenantID != actor.TenantID {
		return Ref{}, tenantErr()
	}
	ref.TenantID = actor.TenantID
	return ref, ref.Validate()
}

// record writes one action to the append-only audit log and to the queryable
// event table. The detail string is written by this package, never by the
// caller, so nothing tenant-supplied reaches either sink.
func (s *Service) record(ctx context.Context, actor Actor, ref Ref, backend BackendName, action, detail string, err error) {
	outcome := outcomeOK
	if err != nil {
		outcome = outcomeError
	}
	qualified, qualErr := ref.Qualified()
	if qualErr != nil {
		qualified = ""
	}

	logging.Audit().LogAttrs(ctx, slog.LevelInfo, action,
		slog.String("actor_id", actor.UserID),
		slog.String("actor_role", actor.Role),
		slog.String("tenant_id", ref.TenantID),
		slog.String("workload", qualified),
		slog.String("backend", string(backend)),
		slog.String("outcome", outcome),
		slog.String("detail", detail),
		slog.String("request_id", actor.RequestID),
	)

	if s.store == nil || ref.TenantID == "" {
		return
	}
	// The event row mirrors the audit line for the panel. A failure to write
	// it must not turn a successful action into a reported failure, and the
	// audit log above already holds the durable copy.
	_, _ = s.store.SaveEvent(ctx, EventRecord{
		TenantID:      ref.TenantID,
		QualifiedName: qualified,
		Backend:       backend,
		Action:        action,
		ActorUserID:   actor.UserID,
		ActorRole:     actor.Role,
		RequestID:     actor.RequestID,
		Outcome:       outcome,
		Detail:        detail,
		CreatedAt:     time.Now().UTC(),
	})
}

// backendFor picks the backend for a workload kind, honouring an explicit
// preference when the caller expressed one.
func (s *Service) backendFor(kind Kind, preferred BackendName) (Backend, error) {
	return s.manager.Select(kind, preferred)
}

// Create builds a new workload for the actor's account.
//
// The reference's account is overwritten with the actor's before anything
// else happens, and the spec then passes through resolveSpec, which is where
// the class isolation profile is enforced.
func (s *Service) Create(ctx context.Context, actor Actor, spec Spec, preferred BackendName) (Instance, error) {
	var out Instance

	ref, err := s.authorize(actor, spec.Ref)
	if err != nil {
		return out, err
	}
	spec.Ref = ref

	backend, err := s.backendFor(spec.Kind, preferred)
	if err != nil {
		s.record(ctx, actor, ref, preferred, actionCreate, "backend_unavailable", err)
		return out, err
	}

	resolved, err := s.manager.Config().resolveSpec(spec)
	if err != nil {
		s.record(ctx, actor, ref, backend.Name(), actionCreate, "rejected", err)
		return out, err
	}

	instance, err := backend.Create(ctx, resolved)
	s.record(ctx, actor, ref, backend.Name(), actionCreate, string(spec.Kind), err)
	if err != nil {
		return out, err
	}
	s.persist(ctx, instance)
	return instance, nil
}

// persist writes the ownership record for a workload. A storage failure is
// not allowed to undo an action the engine already performed, so it is
// logged and the caller still sees the instance.
func (s *Service) persist(ctx context.Context, instance Instance) {
	if s.store == nil {
		return
	}
	rec := WorkloadRecord{
		Ref:           instance.Ref,
		QualifiedName: instance.QualifiedName,
		Backend:       instance.Backend,
		Kind:          instance.Kind,
		EngineID:      instance.ID,
		Image:         instance.Image,
		ImageDigest:   instance.ImageDigest,
		State:         instance.State,
		CPUMillicores: millicores(instance.Resources.CPUCores),
		MemoryBytes:   instance.Resources.MemoryBytes,
		DiskBytes:     instance.Resources.DiskBytes,
		CreatedAt:     instance.CreatedAt,
	}
	if err := s.store.SaveWorkload(ctx, rec); err != nil {
		logging.Audit().LogAttrs(ctx, slog.LevelWarn, "orchestrator.persist",
			slog.String("tenant_id", instance.Ref.TenantID),
			slog.String("workload", instance.QualifiedName),
			slog.String("outcome", outcomeError),
		)
	}
}

// resolve authorizes a reference and picks the backend that owns it.
func (s *Service) resolve(ctx context.Context, actor Actor, ref Ref, preferred BackendName) (Backend, Ref, error) {
	scoped, err := s.authorize(actor, ref)
	if err != nil {
		return nil, Ref{}, err
	}
	if preferred != "" {
		backend, err := s.manager.Backend(preferred)
		if err != nil {
			return nil, Ref{}, err
		}
		return backend, scoped, nil
	}
	// No preference was expressed, so the workload is located by asking each
	// registered backend for it. Each Inspect re-checks ownership itself, so
	// a match can only be returned to the account that owns it.
	for _, name := range s.manager.Names() {
		backend, err := s.manager.Backend(name)
		if err != nil {
			continue
		}
		if _, err := backend.Inspect(ctx, scoped); err == nil {
			return backend, scoped, nil
		}
	}
	return nil, Ref{}, notFoundErr()
}

// Start starts an existing workload.
func (s *Service) Start(ctx context.Context, actor Actor, ref Ref, preferred BackendName) error {
	backend, scoped, err := s.resolve(ctx, actor, ref, preferred)
	if err != nil {
		s.record(ctx, actor, ref, preferred, actionStart, "unresolved", err)
		return err
	}
	err = backend.Start(ctx, scoped)
	s.record(ctx, actor, scoped, backend.Name(), actionStart, "", err)
	return err
}

// Stop stops a running workload.
func (s *Service) Stop(ctx context.Context, actor Actor, ref Ref, preferred BackendName, grace time.Duration) error {
	backend, scoped, err := s.resolve(ctx, actor, ref, preferred)
	if err != nil {
		s.record(ctx, actor, ref, preferred, actionStop, "unresolved", err)
		return err
	}
	if grace <= 0 {
		grace = DefaultStopGrace
	}
	err = backend.Stop(ctx, scoped, grace)
	s.record(ctx, actor, scoped, backend.Name(), actionStop, "", err)
	return err
}

// Restart restarts a workload.
func (s *Service) Restart(ctx context.Context, actor Actor, ref Ref, preferred BackendName, grace time.Duration) error {
	backend, scoped, err := s.resolve(ctx, actor, ref, preferred)
	if err != nil {
		s.record(ctx, actor, ref, preferred, actionRestart, "unresolved", err)
		return err
	}
	if grace <= 0 {
		grace = DefaultStopGrace
	}
	err = backend.Restart(ctx, scoped, grace)
	s.record(ctx, actor, scoped, backend.Name(), actionRestart, "", err)
	return err
}

// Remove destroys a workload and marks its ownership record removed.
func (s *Service) Remove(ctx context.Context, actor Actor, ref Ref, preferred BackendName, opts RemoveOptions) error {
	backend, scoped, err := s.resolve(ctx, actor, ref, preferred)
	if err != nil {
		s.record(ctx, actor, ref, preferred, actionRemove, "unresolved", err)
		return err
	}
	err = backend.Remove(ctx, scoped, opts)
	s.record(ctx, actor, scoped, backend.Name(), actionRemove, "", err)
	if err != nil {
		return err
	}
	if s.store != nil {
		qualified, qualErr := scoped.Qualified()
		if qualErr == nil {
			// The engine has already destroyed the workload; a bookkeeping
			// failure here must not be reported as a failed removal.
			_ = s.store.MarkWorkloadRemoved(ctx, scoped.TenantID, qualified)
		}
	}
	return nil
}

// Inspect reports one workload's current status.
func (s *Service) Inspect(ctx context.Context, actor Actor, ref Ref, preferred BackendName) (Instance, error) {
	backend, scoped, err := s.resolve(ctx, actor, ref, preferred)
	if err != nil {
		return Instance{}, err
	}
	return backend.Inspect(ctx, scoped)
}

// List enumerates the actor's workloads across every registered backend.
func (s *Service) List(ctx context.Context, actor Actor, filter Filter) ([]Instance, error) {
	if actor.Role != RoleGlobalAdmin || filter.TenantID == "" {
		if actor.TenantID == "" {
			return nil, tenantErr()
		}
		filter.TenantID = actor.TenantID
	}
	if err := ValidateTenantID(filter.TenantID); err != nil {
		return nil, err
	}

	var out []Instance
	for _, name := range s.manager.Names() {
		backend, err := s.manager.Backend(name)
		if err != nil {
			continue
		}
		found, err := backend.List(ctx, filter)
		if err != nil {
			if IsUnavailable(err) || IsUnsupported(err) {
				continue
			}
			return nil, err
		}
		out = append(out, found...)
	}
	return out, nil
}

// Logs reads a bounded, tailed slice of a workload's output.
func (s *Service) Logs(ctx context.Context, actor Actor, ref Ref, preferred BackendName, opts LogOptions) ([]LogLine, error) {
	backend, scoped, err := s.resolve(ctx, actor, ref, preferred)
	if err != nil {
		return nil, err
	}
	lines, err := backend.Logs(ctx, scoped, normalizeLogOptions(opts))
	s.record(ctx, actor, scoped, backend.Name(), actionLogs, "", err)
	return lines, err
}

// Exec runs an argv slice inside a workload.
//
// The command and its captured output are never written to the audit log:
// they routinely carry credentials. The log records that an exec happened,
// who ran it, and where.
func (s *Service) Exec(ctx context.Context, actor Actor, ref Ref, preferred BackendName, req ExecRequest) (ExecResult, error) {
	backend, scoped, err := s.resolve(ctx, actor, ref, preferred)
	if err != nil {
		s.record(ctx, actor, ref, preferred, actionExec, "unresolved", err)
		return ExecResult{}, err
	}
	if err := ValidateArgv(req.Argv); err != nil {
		s.record(ctx, actor, scoped, backend.Name(), actionExec, "rejected", err)
		return ExecResult{}, err
	}
	result, err := backend.Exec(ctx, scoped, req)
	s.record(ctx, actor, scoped, backend.Name(), actionExec, auditRedactedDetail, err)
	return result, err
}

// PullImage makes an image available on the node. Only a global
// administrator may pull with registry credentials, because those
// credentials are the platform's, not an account's.
func (s *Service) PullImage(ctx context.Context, actor Actor, req ImageRequest, preferred BackendName) (ImageRef, error) {
	if req.Auth != nil && actor.Role != RoleGlobalAdmin {
		return ImageRef{}, tenantErr()
	}
	backend, err := s.backendFor(KindContainer, preferred)
	if err != nil {
		return ImageRef{}, err
	}
	ref := Ref{Class: ClassTenant, TenantID: actor.TenantID}
	detail := "unpinned"
	if req.Digest != "" {
		detail = "digest_pinned"
	}
	image, err := backend.PullImage(ctx, req)
	s.record(ctx, actor, ref, backend.Name(), actionPull, detail, err)
	return image, err
}

// Snapshot captures a snapshot of a workload and records it.
func (s *Service) Snapshot(ctx context.Context, actor Actor, ref Ref, preferred BackendName, name string) (Snapshot, error) {
	backend, scoped, err := s.resolve(ctx, actor, ref, preferred)
	if err != nil {
		s.record(ctx, actor, ref, preferred, actionSnapshot, "unresolved", err)
		return Snapshot{}, err
	}
	snapshot, err := backend.Snapshot(ctx, scoped, name)
	s.record(ctx, actor, scoped, backend.Name(), actionSnapshot, "", err)
	if err != nil {
		return Snapshot{}, err
	}
	if s.store != nil {
		qualified, qualErr := scoped.Qualified()
		if qualErr == nil {
			// The snapshot exists on the engine either way; failing to index
			// it must not fail the request.
			_, _ = s.store.SaveSnapshot(ctx, SnapshotRecord{
				QualifiedName: qualified,
				TenantID:      scoped.TenantID,
				Name:          snapshot.Name,
				Backend:       backend.Name(),
				SizeBytes:     snapshot.SizeBytes,
				Stateful:      snapshot.Stateful,
				CreatedAt:     snapshot.CreatedAt,
			})
		}
	}
	return snapshot, nil
}

// Restore reverts a workload to a snapshot.
func (s *Service) Restore(ctx context.Context, actor Actor, ref Ref, preferred BackendName, name string) error {
	backend, scoped, err := s.resolve(ctx, actor, ref, preferred)
	if err != nil {
		s.record(ctx, actor, ref, preferred, actionRestore, "unresolved", err)
		return err
	}
	err = backend.Restore(ctx, scoped, name)
	s.record(ctx, actor, scoped, backend.Name(), actionRestore, "", err)
	return err
}

// ListSnapshots enumerates a workload's snapshots.
func (s *Service) ListSnapshots(ctx context.Context, actor Actor, ref Ref, preferred BackendName) ([]Snapshot, error) {
	backend, scoped, err := s.resolve(ctx, actor, ref, preferred)
	if err != nil {
		return nil, err
	}
	return backend.ListSnapshots(ctx, scoped)
}

// Events reads the recorded lifecycle actions for one account.
func (s *Service) Events(ctx context.Context, actor Actor, limit int) ([]EventRecord, error) {
	if s.store == nil {
		return nil, nil
	}
	tenantID := actor.TenantID
	if tenantID == "" {
		return nil, tenantErr()
	}
	return s.store.ListEvents(ctx, tenantID, limit)
}
