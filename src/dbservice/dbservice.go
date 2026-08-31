package dbservice

import (
	"context"
	"encoding/hex"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/backup"
	"github.com/webappsgo/cashp/src/logging"
	"github.com/webappsgo/cashp/src/security"
)

// Defaults for a first run. Every one of them is overridable through Options,
// and none of them requires configuration for the service to work.
const (
	// defaultHostIP publishes managed engines on loopback only. A managed
	// database is reached from the tenant network or through a cashp proxy,
	// never directly from the public internet.
	defaultHostIP = "127.0.0.1"
	// defaultPortFrom is the first host port managed engines are published on.
	defaultPortFrom = 25000
	// defaultPortTo is the last host port managed engines are published on.
	defaultPortTo = 25999
	// defaultMaxInstances is the per-tenant instance ceiling applied when the
	// caller does not supply one.
	defaultMaxInstances = 10
	// defaultReadyAttempts is how many times a freshly started engine is
	// probed before provisioning gives up.
	defaultReadyAttempts = 60
	// defaultReadyInterval is the pause between readiness probes.
	defaultReadyInterval = 2 * time.Second
	// defaultStopTimeout is how long an engine is given to shut down cleanly
	// before the backend is asked to stop it forcefully.
	defaultStopTimeout = 30 * time.Second
	// defaultRotationInterval is how old a tenant credential may get before
	// the scheduled rotation sweep replaces it.
	defaultRotationInterval = 90 * 24 * time.Hour
	// containerPrefix names managed containers so they are recognisable on the
	// host and cannot collide with a tenant-defined container.
	containerPrefix = "cashp-db-"
	// volumePrefix names managed data volumes.
	volumePrefix = "cashp-db-"
	// networkPrefix names the per-tenant isolated network.
	networkPrefix = "cashp-tenant-"
)

// Options configures the managed-database service. Store, Orchestrator and
// SecretKey are required; everything else has a working default.
type Options struct {
	// Store persists instances, credentials, databases and backup records.
	Store Store
	// Orchestrator runs the instances. It is the narrow interface declared in
	// this package, satisfied by cashp's container/VM orchestration layer.
	Orchestrator Orchestrator
	// Backups is the repository native dumps are stored in. When it is nil the
	// backup and restore operations report that they are not configured rather
	// than silently doing nothing.
	Backups BackupRepository
	// SecretKey is the AES-256 key credentials are encrypted with. It must be
	// exactly security.SecretLen bytes.
	SecretKey []byte
	// HostIP is the address managed engines are published on.
	HostIP string
	// PortFrom is the first host port of the managed range.
	PortFrom int
	// PortTo is the last host port of the managed range.
	PortTo int
	// DefaultLimits is the resource envelope applied when a request omits one.
	DefaultLimits ResourceLimits
	// MaxInstancesPerTenant caps how many live instances a tenant may hold.
	MaxInstancesPerTenant int
	// Retention is the policy stored dumps are pruned under. It is the same
	// GFS policy the rest of cashp's backups use.
	Retention backup.RetentionPolicy
	// RotationInterval is how old a tenant credential may get before the
	// scheduled sweep rotates it.
	RotationInterval time.Duration
	// ReadyAttempts bounds how many probes provisioning waits through for a
	// new engine to answer.
	ReadyAttempts int
	// ReadyInterval is the pause between readiness probes.
	ReadyInterval time.Duration
	// BackupSchedule is the cron expression the scheduled backup sweep runs
	// on; empty takes the package default.
	BackupSchedule string
	// HealthSchedule is the cron expression the health sweep runs on.
	HealthSchedule string
	// RotationSchedule is the cron expression the rotation sweep runs on.
	RotationSchedule string
	// Now supplies the current time; nil uses time.Now. Tests inject a fixed
	// clock through it.
	Now func() time.Time
	// NewID supplies record identifiers; nil uses a CSPRNG-backed generator.
	NewID func() string
	// Logger receives operational messages; nil uses the package logger.
	Logger *slog.Logger
}

// Service is the managed-database layer. It provisions, runs, backs up and
// tears down the database instances cashp offers to its tenants, on top of the
// orchestration backend and the backup repository wired in at construction.
type Service struct {
	store            Store
	orch             Orchestrator
	backups          BackupRepository
	key              []byte
	hostIP           string
	portFrom         int
	portTo           int
	defaultLimits    ResourceLimits
	maxInstances     int
	retention        backup.RetentionPolicy
	rotationInterval time.Duration
	readyAttempts    int
	readyInterval    time.Duration
	backupSchedule   string
	healthSchedule   string
	rotationSchedule string
	now              func() time.Time
	newID            func() string
	log              *slog.Logger
}

// New builds a Service from Options, applying every default and rejecting a
// configuration that cannot work.
func New(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, ErrNotConfigured("the database store")
	}
	if opts.Orchestrator == nil {
		return nil, ErrNotConfigured("the orchestration backend")
	}
	if len(opts.SecretKey) != security.SecretLen {
		return nil, ErrNotConfigured("the credential encryption key")
	}
	s := &Service{
		store:            opts.Store,
		orch:             opts.Orchestrator,
		backups:          opts.Backups,
		key:              append([]byte(nil), opts.SecretKey...),
		hostIP:           opts.HostIP,
		portFrom:         opts.PortFrom,
		portTo:           opts.PortTo,
		defaultLimits:    opts.DefaultLimits,
		maxInstances:     opts.MaxInstancesPerTenant,
		retention:        opts.Retention,
		rotationInterval: opts.RotationInterval,
		readyAttempts:    opts.ReadyAttempts,
		readyInterval:    opts.ReadyInterval,
		backupSchedule:   opts.BackupSchedule,
		healthSchedule:   opts.HealthSchedule,
		rotationSchedule: opts.RotationSchedule,
		now:              opts.Now,
		newID:            opts.NewID,
		log:              opts.Logger,
	}
	if s.hostIP == "" {
		s.hostIP = defaultHostIP
	}
	if s.portFrom <= 0 {
		s.portFrom = defaultPortFrom
	}
	if s.portTo < s.portFrom {
		s.portTo = defaultPortTo
	}
	if s.portTo < s.portFrom {
		return nil, ErrNotConfigured("the managed database port range")
	}
	if s.maxInstances <= 0 {
		s.maxInstances = defaultMaxInstances
	}
	if s.rotationInterval <= 0 {
		s.rotationInterval = defaultRotationInterval
	}
	if s.readyAttempts <= 0 {
		s.readyAttempts = defaultReadyAttempts
	}
	if s.readyInterval <= 0 {
		s.readyInterval = defaultReadyInterval
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.newID == nil {
		s.newID = newIdentifier
	}
	if s.log == nil {
		s.log = logging.L()
	}
	return s, nil
}

// newIdentifier returns a random 128 bit hex identifier. It falls back to the
// clock only if the CSPRNG is unavailable, which cannot happen on a supported
// platform but must not panic if it does.
func newIdentifier() string {
	raw, err := security.RandomSecret(16)
	if err != nil {
		return "db" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	}
	return hex.EncodeToString(raw)
}

// Provision creates, starts and prepares a new managed instance, then returns
// the tenant-visible record. Every credential it generates is encrypted before
// it is stored and none of them is ever logged.
func (s *Service) Provision(ctx context.Context, req ProvisionRequest) (*Instance, error) {
	if err := ValidateTenantID(req.TenantID); err != nil {
		return nil, err
	}
	if err := ValidateInstanceName(req.Name); err != nil {
		return nil, err
	}
	a, err := adapterFor(req.Engine)
	if err != nil {
		return nil, err
	}
	version, err := resolveVersion(a, req.Version)
	if err != nil {
		return nil, err
	}
	if req.Database != "" {
		if !a.capabilities().NamedDatabases {
			return nil, ErrUnsupported(req.Engine, "named databases")
		}
		if err := ValidateIdentifier(req.Engine, "database", req.Database); err != nil {
			return nil, err
		}
	}
	if req.Username != "" {
		if !a.capabilities().Users {
			return nil, ErrUnsupported(req.Engine, "user accounts")
		}
		if err := ValidateIdentifier(req.Engine, "username", req.Username); err != nil {
			return nil, err
		}
	}
	if err := s.checkInstanceQuota(ctx, req.TenantID); err != nil {
		return nil, err
	}
	if err := s.checkNameFree(ctx, req.TenantID, req.Name); err != nil {
		return nil, err
	}

	inst, err := s.createInstance(ctx, a, req.TenantID, req.Name, version, RolePrimary, "", req.Limits)
	if err != nil {
		return nil, err
	}
	password, err := s.issueAdminCredential(ctx, inst)
	if err != nil {
		return nil, err
	}
	if err := s.bringUp(ctx, a, inst, password, nil); err != nil {
		s.rollback(ctx, inst)
		return nil, err
	}
	c := s.contextFor(a, inst, password)
	if req.Database != "" {
		owner := req.Username
		if owner == "" {
			owner = inst.AdminUser
		}
		if err := s.createDatabaseIn(ctx, a, c, inst, req.Database, owner); err != nil {
			s.rollback(ctx, inst)
			return nil, err
		}
	}
	if req.Username != "" {
		_, err := s.CreateUser(ctx, CreateUserRequest{
			TenantID:   req.TenantID,
			InstanceID: inst.ID,
			Username:   req.Username,
			Database:   req.Database,
			Grant:      GrantReadWrite,
			Actor:      req.Actor,
		})
		if err != nil {
			s.rollback(ctx, inst)
			return nil, err
		}
	}
	s.audit(req.Actor, "database.instance.provisioned", inst, slog.String("engine_version", version))
	return s.reload(ctx, inst)
}

// createInstance writes the pending instance record and allocates its host
// port, container name, volume name and tenant network.
func (s *Service) createInstance(ctx context.Context, a adapter, tenantID, name, version string,
	role Role, primaryID string, limits ResourceLimits) (*Instance, error) {
	port, err := s.allocatePort(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	id := s.newID()
	inst := &Instance{
		ID:            id,
		TenantID:      tenantID,
		Name:          name,
		Engine:        a.engine(),
		EngineVersion: version,
		Role:          role,
		PrimaryID:     primaryID,
		State:         StatePending,
		ContainerName: containerPrefix + id,
		VolumeName:    volumePrefix + id,
		Network:       networkPrefix + tenantID,
		Host:          s.hostIP,
		Port:          port,
		AdminUser:     a.adminUser(),
		Limits:        s.limitsOrDefault(limits),
		Health:        HealthUnknown,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.store.CreateInstance(ctx, inst); err != nil {
		return nil, err
	}
	return inst, nil
}

// bringUp creates the volume and the container, drops the engine's bootstrap
// files, starts it, waits for its first successful health probe and runs the
// engine's post-start commands.
func (s *Service) bringUp(ctx context.Context, a adapter, inst *Instance, password string, replicaOf *engineCtx) error {
	if err := s.createVolume(ctx, inst); err != nil {
		return err
	}
	return s.createContainer(ctx, a, inst, password, replicaOf)
}

// createVolume creates the instance's data volume and records the name the
// backend gave it.
func (s *Service) createVolume(ctx context.Context, inst *Instance) error {
	volume, err := s.orch.CreateVolume(ctx, VolumeSpec{
		Name: inst.VolumeName,
		Labels: map[string]string{
			"cashp.tenant":   inst.TenantID,
			"cashp.instance": inst.ID,
			"cashp.managed":  "database",
		},
		SizeBytes: inst.Limits.DiskBytes,
	})
	if err != nil {
		return ErrInternal(err, "That instance could not be created.")
	}
	if volume != "" {
		inst.VolumeName = volume
	}
	return nil
}

// createContainer builds the container for an instance whose volume already
// exists, starts it, waits for the engine's first healthy answer and runs the
// engine's post-start commands. Version changes reuse it so an upgrade never
// duplicates the provisioning path.
func (s *Service) createContainer(ctx context.Context, a adapter, inst *Instance, password string, replicaOf *engineCtx) error {
	volume := inst.VolumeName
	p := specParams{
		Instance:      inst,
		AdminUser:     inst.AdminUser,
		AdminPassword: password,
		Volume:        volume,
		Network:       inst.Network,
		HostIP:        s.hostIP,
		HostPort:      inst.Port,
		ReplicaOf:     replicaOf,
	}
	containerID, err := s.orch.Create(ctx, a.containerSpec(p))
	if err != nil {
		return ErrInternal(err, "That instance could not be created.")
	}
	inst.ContainerID = containerID
	for _, f := range a.bootstrapFiles(p) {
		if err := s.writeFile(ctx, inst, f); err != nil {
			return err
		}
	}
	if err := s.orch.Start(ctx, containerID); err != nil {
		return ErrInternal(err, "That instance could not be started.")
	}
	inst.State = StateRunning
	inst.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateInstance(ctx, inst); err != nil {
		return err
	}
	c := s.contextFor(a, inst, password)
	if err := s.waitReady(ctx, a, c); err != nil {
		return err
	}
	cmds, err := a.postStartCommands(c)
	if err != nil {
		return err
	}
	if _, err := s.runAll(ctx, inst, cmds); err != nil {
		return err
	}
	return nil
}

// rollback tears down a half-provisioned instance so a failed Provision never
// leaves an orphaned container, volume or half-usable record behind.
func (s *Service) rollback(ctx context.Context, inst *Instance) {
	if inst.ContainerID != "" {
		if err := s.orch.Remove(ctx, inst.ContainerID, true); err != nil {
			s.log.Warn("managed database rollback could not remove container",
				slog.String("instance_id", inst.ID))
		}
	}
	if inst.VolumeName != "" {
		if err := s.orch.RemoveVolume(ctx, inst.VolumeName); err != nil {
			s.log.Warn("managed database rollback could not remove volume",
				slog.String("instance_id", inst.ID))
		}
	}
	now := s.now().UTC()
	inst.State = StateDestroyed
	inst.Health = HealthUnknown
	inst.ContainerID = ""
	inst.DestroyedAt = now
	inst.UpdatedAt = now
	if err := s.store.UpdateInstance(ctx, inst); err != nil {
		s.log.Warn("managed database rollback could not update the instance record",
			slog.String("instance_id", inst.ID))
	}
}

// Get returns one of the tenant's instances.
func (s *Service) Get(ctx context.Context, tenantID, instanceID string) (*Instance, error) {
	return s.live(ctx, tenantID, instanceID)
}

// List returns the tenant's live instances.
func (s *Service) List(ctx context.Context, tenantID string) ([]*Instance, error) {
	return s.store.ListInstances(ctx, tenantID)
}

// Start starts a stopped instance.
func (s *Service) Start(ctx context.Context, tenantID, instanceID, actor string) (*Instance, error) {
	inst, err := s.live(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	if inst.State == StateRunning {
		return inst, nil
	}
	if err := s.orch.Start(ctx, inst.ContainerID); err != nil {
		return nil, s.markFailed(ctx, inst, ErrInternal(err, "That instance could not be started."))
	}
	inst.State = StateRunning
	inst.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateInstance(ctx, inst); err != nil {
		return nil, err
	}
	s.audit(actor, "database.instance.started", inst)
	return inst, nil
}

// Stop stops a running instance without touching its data.
func (s *Service) Stop(ctx context.Context, tenantID, instanceID, actor string) (*Instance, error) {
	inst, err := s.live(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	if inst.State == StateStopped {
		return inst, nil
	}
	if err := s.orch.Stop(ctx, inst.ContainerID, defaultStopTimeout); err != nil {
		return nil, s.markFailed(ctx, inst, ErrInternal(err, "That instance could not be stopped."))
	}
	now := s.now().UTC()
	inst.State = StateStopped
	inst.Health = HealthUnknown
	inst.HealthDetail = ""
	inst.HealthCheckedAt = now
	inst.UpdatedAt = now
	if err := s.store.UpdateInstance(ctx, inst); err != nil {
		return nil, err
	}
	s.audit(actor, "database.instance.stopped", inst)
	return inst, nil
}

// Restart stops and starts an instance, waiting for it to answer its health
// probe again before reporting success.
func (s *Service) Restart(ctx context.Context, tenantID, instanceID, actor string) (*Instance, error) {
	inst, err := s.live(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	if err := s.orch.Stop(ctx, inst.ContainerID, defaultStopTimeout); err != nil {
		return nil, s.markFailed(ctx, inst, ErrInternal(err, "That instance could not be stopped."))
	}
	if err := s.orch.Start(ctx, inst.ContainerID); err != nil {
		return nil, s.markFailed(ctx, inst, ErrInternal(err, "That instance could not be started."))
	}
	inst.State = StateRunning
	inst.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateInstance(ctx, inst); err != nil {
		return nil, err
	}
	a, c, err := s.engineContext(ctx, inst)
	if err != nil {
		return nil, err
	}
	if err := s.waitReady(ctx, a, c); err != nil {
		return nil, err
	}
	s.audit(actor, "database.instance.restarted", inst)
	return inst, nil
}

// Destroy removes an instance and every byte of its data. It refuses to run
// without an explicit confirmation flag, so it can never be reached by an
// accidental or replayed request, and it is always audit logged.
func (s *Service) Destroy(ctx context.Context, req DestroyRequest) error {
	inst, err := s.live(ctx, req.TenantID, req.InstanceID)
	if err != nil {
		return err
	}
	if !req.Confirm {
		return ErrConfirmationRequired(inst.Name)
	}
	replicas, err := s.store.ListReplicas(ctx, inst.TenantID, inst.ID)
	if err != nil {
		return err
	}
	if len(replicas) > 0 {
		return ErrConflict("Remove this instance's replicas before destroying it.")
	}
	if inst.ContainerID != "" {
		if err := s.orch.Remove(ctx, inst.ContainerID, true); err != nil {
			return ErrInternal(err, "That instance could not be removed.")
		}
	}
	if inst.VolumeName != "" {
		if err := s.orch.RemoveVolume(ctx, inst.VolumeName); err != nil {
			return ErrInternal(err, "That instance's storage could not be removed.")
		}
	}
	if err := s.revokeAllCredentials(ctx, inst); err != nil {
		return err
	}
	now := s.now().UTC()
	inst.State = StateDestroyed
	inst.Health = HealthUnknown
	inst.HealthDetail = ""
	inst.ContainerID = ""
	inst.DestroyedAt = now
	inst.UpdatedAt = now
	if err := s.store.UpdateInstance(ctx, inst); err != nil {
		return err
	}
	s.audit(req.Actor, "database.instance.destroyed", inst, slog.Bool("confirmed", true))
	return nil
}

// live loads an instance that still exists, translating a destroyed record
// into the same not-found answer another tenant's record produces.
func (s *Service) live(ctx context.Context, tenantID, instanceID string) (*Instance, error) {
	inst, err := s.store.GetInstance(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	if !inst.Alive() {
		return nil, ErrNotFound("database instance")
	}
	return inst, nil
}

// running loads an instance that must currently be up for the operation to be
// possible.
func (s *Service) running(ctx context.Context, tenantID, instanceID string) (*Instance, error) {
	inst, err := s.live(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	if inst.State != StateRunning {
		return nil, ErrUnavailable("That instance is not running.")
	}
	return inst, nil
}

// reload re-reads an instance after a sequence of writes so the caller always
// sees the persisted record rather than a partially mutated copy.
func (s *Service) reload(ctx context.Context, inst *Instance) (*Instance, error) {
	return s.store.GetInstance(ctx, inst.TenantID, inst.ID)
}

// markFailed records a lifecycle failure on the instance and returns the
// original error so the caller's message is preserved.
func (s *Service) markFailed(ctx context.Context, inst *Instance, cause error) error {
	inst.State = StateFailed
	inst.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateInstance(ctx, inst); err != nil {
		return err
	}
	return cause
}

// limitsOrDefault fills any unset field of a requested envelope from the
// configured defaults, so an instance always runs under a limit.
func (s *Service) limitsOrDefault(l ResourceLimits) ResourceLimits {
	if l.CPUCores <= 0 {
		l.CPUCores = s.defaultLimits.CPUCores
	}
	if l.MemoryBytes <= 0 {
		l.MemoryBytes = s.defaultLimits.MemoryBytes
	}
	if l.DiskBytes <= 0 {
		l.DiskBytes = s.defaultLimits.DiskBytes
	}
	if l.PidsLimit <= 0 {
		l.PidsLimit = s.defaultLimits.PidsLimit
	}
	return l
}

// SetLimits changes an instance's resource envelope and applies it to the
// running container.
func (s *Service) SetLimits(ctx context.Context, tenantID, instanceID string, limits ResourceLimits, actor string) (*Instance, error) {
	inst, err := s.live(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	applied := s.limitsOrDefault(limits)
	if applied.IsZero() {
		return nil, ErrValidation("A managed database must run under a resource limit.")
	}
	if inst.ContainerID != "" {
		if err := s.orch.UpdateLimits(ctx, inst.ContainerID, applied); err != nil {
			return nil, ErrInternal(err, "Those resource limits could not be applied.")
		}
	}
	inst.Limits = applied
	inst.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateInstance(ctx, inst); err != nil {
		return nil, err
	}
	s.audit(actor, "database.instance.limits_changed", inst)
	return inst, nil
}

// checkInstanceQuota refuses a new instance once the tenant is at its ceiling.
func (s *Service) checkInstanceQuota(ctx context.Context, tenantID string) error {
	live, err := s.store.ListInstances(ctx, tenantID)
	if err != nil {
		return err
	}
	if len(live) >= s.maxInstances {
		return ErrQuota("database instances", int64(s.maxInstances), int64(len(live)), 1)
	}
	return nil
}

// checkNameFree refuses a duplicate instance name inside one tenant.
func (s *Service) checkNameFree(ctx context.Context, tenantID, name string) error {
	existing, err := s.store.GetInstanceByName(ctx, tenantID, name)
	if err == nil && existing != nil {
		return ErrConflict("You already have a database instance with that name.")
	}
	if IsNotFound(err) {
		return nil
	}
	return err
}

// allocatePort picks the lowest free host port in the configured range. Ports
// in use by any tenant are excluded, which is why this is the one place a
// cross-tenant read is required.
func (s *Service) allocatePort(ctx context.Context) (int, error) {
	all, err := s.store.ListAllInstances(ctx)
	if err != nil {
		return 0, err
	}
	used := make(map[int]bool, len(all))
	for _, inst := range all {
		used[inst.Port] = true
	}
	for port := s.portFrom; port <= s.portTo; port++ {
		if !used[port] {
			return port, nil
		}
	}
	return 0, ErrQuota("database ports", int64(s.portTo-s.portFrom+1), int64(len(used)), 1)
}

// audit writes one entry to the append-only audit trail. Only identifiers and
// non-sensitive attributes are recorded: no password, no DSN and no command
// line ever reaches this log.
func (s *Service) audit(actor, action string, inst *Instance, extra ...slog.Attr) {
	attrs := []any{
		slog.String("action", action),
		slog.String("actor", actor),
		slog.String("tenant_id", inst.TenantID),
		slog.String("instance_id", inst.ID),
		slog.String("instance", inst.Name),
		slog.String("engine", string(inst.Engine)),
	}
	for _, a := range extra {
		attrs = append(attrs, a)
	}
	logging.Audit().Info("managed database", attrs...)
}

// sortedNames returns names in a stable order so command plans and their tests
// are deterministic.
func sortedNames(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
