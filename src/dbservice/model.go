package dbservice

import (
	"time"
)

// State is the lifecycle state of a managed instance.
type State string

const (
	// StatePending is a record created before its container exists.
	StatePending State = "pending"
	// StateRunning is a started instance.
	StateRunning State = "running"
	// StateStopped is a created instance that is not running.
	StateStopped State = "stopped"
	// StateUpgrading is an instance mid version change.
	StateUpgrading State = "upgrading"
	// StateFailed is an instance whose last lifecycle operation failed.
	StateFailed State = "failed"
	// StateDestroyed is a tombstoned instance. Rows are never deleted, so a
	// destroyed instance stays auditable and its name is never silently
	// reused by a different record.
	StateDestroyed State = "destroyed"
)

// Role distinguishes a primary instance from a replica of one.
type Role string

const (
	// RolePrimary is a read-write instance.
	RolePrimary Role = "primary"
	// RoleReplica is a read-only copy following a primary.
	RoleReplica Role = "replica"
)

// HealthState is the outcome of a protocol-level health probe.
type HealthState string

const (
	// HealthUnknown means no probe has run yet, or the instance is stopped.
	HealthUnknown HealthState = "unknown"
	// HealthHealthy means the engine answered its own protocol probe.
	HealthHealthy HealthState = "healthy"
	// HealthDegraded means the engine answered but reported a problem, for
	// example a replica that has fallen out of its topology.
	HealthDegraded HealthState = "degraded"
	// HealthUnhealthy means the engine did not answer.
	HealthUnhealthy HealthState = "unhealthy"
)

// CredentialRole is what an issued account is for.
type CredentialRole string

const (
	// RoleAdmin is the instance's administrative account, owned by cashp and
	// never handed to a tenant.
	RoleAdmin CredentialRole = "admin"
	// RoleApp is a tenant-facing least-privilege application account.
	RoleApp CredentialRole = "app"
	// RoleReplication is the account a replica authenticates with.
	RoleReplication CredentialRole = "replication"
)

// GrantLevel is the privilege set an application account receives on a
// database. The default is deliberately the narrowest useful level.
type GrantLevel string

const (
	// GrantReadOnly allows reads only.
	GrantReadOnly GrantLevel = "read_only"
	// GrantReadWrite allows reads and writes but no schema changes beyond the
	// account's own objects. This is the default for a new account.
	GrantReadWrite GrantLevel = "read_write"
	// GrantOwner allows full control of one database and nothing outside it.
	GrantOwner GrantLevel = "owner"
)

// Valid reports whether a grant level is one this package issues.
func (g GrantLevel) Valid() bool {
	switch g {
	case GrantReadOnly, GrantReadWrite, GrantOwner:
		return true
	default:
		return false
	}
}

// Instance is one managed database instance. It is always tenant-scoped:
// there is no query in this package that reads or writes an instance without
// a tenant identifier.
type Instance struct {
	// ID is the cashp identifier of the instance.
	ID string `json:"id"`
	// TenantID is the owning tenant.
	TenantID string `json:"tenant_id"`
	// Name is the tenant-chosen instance name, unique inside the tenant.
	Name string `json:"name"`
	// Engine is the database engine.
	Engine Engine `json:"engine"`
	// EngineVersion is the running engine version.
	EngineVersion string `json:"engine_version"`
	// Role is primary or replica.
	Role Role `json:"role"`
	// PrimaryID is the instance this one replicates, empty for a primary.
	PrimaryID string `json:"primary_id,omitempty"`
	// State is the lifecycle state.
	State State `json:"state"`
	// ContainerID is the orchestrator's container identifier. It is internal
	// and is never rendered into a tenant-visible payload.
	ContainerID string `json:"-"`
	// ContainerName is the container's name on the tenant network, which is
	// also how a replica addresses its primary. Internal only.
	ContainerName string `json:"-"`
	// VolumeName is the backend volume holding the data. Internal only.
	VolumeName string `json:"-"`
	// Network is the per-tenant isolated network. Internal only.
	Network string `json:"-"`
	// Host is the address the instance is reachable at.
	Host string `json:"host"`
	// Port is the published port.
	Port int `json:"port"`
	// AdminUser is the administrative account name inside the instance.
	AdminUser string `json:"-"`
	// Limits is the resource envelope in force.
	Limits ResourceLimits `json:"limits"`
	// Health is the last observed health state.
	Health HealthState `json:"health"`
	// HealthDetail is a short, non-sensitive explanation of Health.
	HealthDetail string `json:"health_detail,omitempty"`
	// HealthCheckedAt is when Health was last refreshed.
	HealthCheckedAt time.Time `json:"health_checked_at,omitempty"`
	// CreatedAt is when the instance record was created.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when the record last changed.
	UpdatedAt time.Time `json:"updated_at"`
	// DestroyedAt is when the instance was destroyed, zero while it lives.
	DestroyedAt time.Time `json:"destroyed_at,omitempty"`
	// RowVersion backs optimistic locking on concurrent lifecycle writes.
	RowVersion int64 `json:"-"`
}

// Alive reports whether the instance still exists as far as the tenant is
// concerned.
func (i *Instance) Alive() bool {
	return i != nil && i.State != StateDestroyed
}

// Credential is an account issued inside a managed instance. The password is
// only ever held encrypted at rest and is never written to a log.
type Credential struct {
	// ID is the credential identifier.
	ID string `json:"id"`
	// TenantID is the owning tenant.
	TenantID string `json:"tenant_id"`
	// InstanceID is the instance the account lives in.
	InstanceID string `json:"instance_id"`
	// Username is the account name inside the instance.
	Username string `json:"username"`
	// Role is what the account is for.
	Role CredentialRole `json:"role"`
	// Database is the database the account is scoped to, empty for the
	// administrative and replication accounts.
	Database string `json:"database,omitempty"`
	// Grant is the privilege level the account holds on Database.
	Grant GrantLevel `json:"grant,omitempty"`
	// Secret is the AES-256-GCM ciphertext of the password. It never leaves
	// this package in a payload and is never logged.
	Secret []byte `json:"-"`
	// CreatedAt is when the account was issued.
	CreatedAt time.Time `json:"created_at"`
	// RotatedAt is when the password was last rotated.
	RotatedAt time.Time `json:"rotated_at,omitempty"`
	// RevokedAt is when the account was revoked, zero while it is usable.
	RevokedAt time.Time `json:"revoked_at,omitempty"`
}

// Database is a named database inside an instance.
type Database struct {
	// ID is the record identifier.
	ID string `json:"id"`
	// TenantID is the owning tenant.
	TenantID string `json:"tenant_id"`
	// InstanceID is the instance the database lives in.
	InstanceID string `json:"instance_id"`
	// Name is the database name.
	Name string `json:"name"`
	// Owner is the account that owns it.
	Owner string `json:"owner"`
	// CreatedAt is when it was created.
	CreatedAt time.Time `json:"created_at"`
	// DroppedAt is when it was dropped, zero while it exists.
	DroppedAt time.Time `json:"dropped_at,omitempty"`
}

// BackupRecord is one native engine dump stored in the backup repository.
type BackupRecord struct {
	// ID is the record identifier.
	ID string `json:"id"`
	// TenantID is the owning tenant.
	TenantID string `json:"tenant_id"`
	// InstanceID is the instance the dump came from.
	InstanceID string `json:"instance_id"`
	// ArtifactID identifies the stored artifact in the backup repository.
	ArtifactID string `json:"artifact_id"`
	// Engine is the engine that produced the dump.
	Engine Engine `json:"engine"`
	// EngineVersion is the version that produced it, which bounds where it
	// can be restored.
	EngineVersion string `json:"engine_version"`
	// Database is the dumped database, empty for a whole-instance dump.
	Database string `json:"database,omitempty"`
	// SizeBytes is the stored artifact size.
	SizeBytes int64 `json:"size_bytes"`
	// Checksum is the repository's content checksum.
	Checksum string `json:"checksum"`
	// Encrypted records that the repository stored the artifact encrypted.
	Encrypted bool `json:"encrypted"`
	// CreatedAt is when the dump was taken.
	CreatedAt time.Time `json:"created_at"`
	// DeletedAt is when retention removed it, zero while it is restorable.
	DeletedAt time.Time `json:"deleted_at,omitempty"`
}

// Health is a point-in-time health report for one instance.
type Health struct {
	// InstanceID is the probed instance.
	InstanceID string `json:"instance_id"`
	// Engine is the probed engine.
	Engine Engine `json:"engine"`
	// State is the probe outcome.
	State HealthState `json:"state"`
	// Detail is a short, non-sensitive explanation.
	Detail string `json:"detail,omitempty"`
	// CheckedAt is when the probe ran.
	CheckedAt time.Time `json:"checked_at"`
}

// Usage reports what one instance is consuming against its limits.
type Usage struct {
	// InstanceID is the measured instance.
	InstanceID string `json:"instance_id"`
	// Engine is the measured engine.
	Engine Engine `json:"engine"`
	// Limits is the configured envelope.
	Limits ResourceLimits `json:"limits"`
	// DiskUsedBytes is the current volume consumption.
	DiskUsedBytes int64 `json:"disk_used_bytes"`
	// MemoryUsedBytes is current resident memory, zero when the backend does
	// not report it.
	MemoryUsedBytes int64 `json:"memory_used_bytes"`
	// CPUPercent is current cpu utilisation, zero when unreported.
	CPUPercent float64 `json:"cpu_percent"`
	// Running is whether the instance was running when measured.
	Running bool `json:"running"`
	// MeasuredAt is when the measurement was taken.
	MeasuredAt time.Time `json:"measured_at"`
}

// QuotaReport aggregates a tenant's managed-database consumption so the
// billing layer can compare it against the tenant's plan.
type QuotaReport struct {
	// TenantID is the measured tenant.
	TenantID string `json:"tenant_id"`
	// Instances is the number of live instances.
	Instances int `json:"instances"`
	// Databases is the number of live databases across those instances.
	Databases int `json:"databases"`
	// CPUCores is the sum of configured cpu limits.
	CPUCores float64 `json:"cpu_cores"`
	// MemoryBytes is the sum of configured memory limits.
	MemoryBytes int64 `json:"memory_bytes"`
	// DiskLimitBytes is the sum of configured disk limits.
	DiskLimitBytes int64 `json:"disk_limit_bytes"`
	// DiskUsedBytes is the sum of measured volume consumption.
	DiskUsedBytes int64 `json:"disk_used_bytes"`
	// PerInstance is the per-instance breakdown.
	PerInstance []Usage `json:"per_instance"`
	// MeasuredAt is when the report was assembled.
	MeasuredAt time.Time `json:"measured_at"`
}

// ConnectionInfo is the masked connection descriptor safe to render in any
// API or UI payload. The password is replaced with security.MaskedValue and
// the DSN carries the same masked value, so a screenshot or a support
// transcript never leaks a working credential.
type ConnectionInfo struct {
	// Engine is the target engine.
	Engine Engine `json:"engine"`
	// Scheme is the connection-string URI scheme.
	Scheme string `json:"scheme"`
	// Host is the address to connect to.
	Host string `json:"host"`
	// Port is the port to connect to.
	Port int `json:"port"`
	// Username is the account name.
	Username string `json:"username"`
	// Password is always masked.
	Password string `json:"password"`
	// Database is the default database, empty for engines without one.
	Database string `json:"database,omitempty"`
	// DSN is the connection string with its password masked.
	DSN string `json:"dsn"`
}

// UserCredential is the one-time full credential handed to the owning tenant
// when an account is created or rotated. It is returned exactly once, at the
// moment of issuance, and is never persisted in the clear or logged.
type UserCredential struct {
	// TenantID is the owning tenant.
	TenantID string `json:"tenant_id"`
	// InstanceID is the instance the account lives in.
	InstanceID string `json:"instance_id"`
	// Username is the account name.
	Username string `json:"username"`
	// Password is the plaintext password, shown once.
	Password string `json:"password"`
	// Database is the database the account is scoped to.
	Database string `json:"database,omitempty"`
	// Grant is the privilege level granted.
	Grant GrantLevel `json:"grant,omitempty"`
	// DSN is the full connection string, shown only to the owning tenant.
	DSN string `json:"dsn"`
}

// ProvisionRequest creates a new managed instance.
type ProvisionRequest struct {
	// TenantID is the owning tenant. Required.
	TenantID string `json:"tenant_id"`
	// Name is the tenant-chosen instance name. Required.
	Name string `json:"name"`
	// Engine is the engine to run. Required.
	Engine Engine `json:"engine"`
	// Version is the engine version; empty selects the engine default.
	Version string `json:"version"`
	// Limits is the requested resource envelope; zero fields take the
	// package defaults.
	Limits ResourceLimits `json:"limits"`
	// Database, when set, creates an initial database of that name.
	Database string `json:"database,omitempty"`
	// Username, when set together with Database, creates an initial
	// least-privilege account with GrantReadWrite on it.
	Username string `json:"username,omitempty"`
	// Actor is the account performing the action, recorded in the audit log.
	Actor string `json:"-"`
}

// DestroyRequest deletes an instance and its data. Confirm must be true:
// without it the call is refused, so destruction is never reachable by a
// mistaken or replayed request.
type DestroyRequest struct {
	// TenantID is the owning tenant. Required.
	TenantID string `json:"tenant_id"`
	// InstanceID is the instance to destroy. Required.
	InstanceID string `json:"instance_id"`
	// Confirm must be true for the request to proceed.
	Confirm bool `json:"confirm"`
	// Actor is the account performing the action, recorded in the audit log.
	Actor string `json:"-"`
}

// UpgradeRequest moves an instance to a different engine version.
type UpgradeRequest struct {
	// TenantID is the owning tenant. Required.
	TenantID string `json:"tenant_id"`
	// InstanceID is the instance to upgrade. Required.
	InstanceID string `json:"instance_id"`
	// TargetVersion is the version to move to. Required.
	TargetVersion string `json:"target_version"`
	// Actor is the account performing the action, recorded in the audit log.
	Actor string `json:"-"`
}

// CreateUserRequest issues a new least-privilege account inside an instance.
type CreateUserRequest struct {
	// TenantID is the owning tenant. Required.
	TenantID string `json:"tenant_id"`
	// InstanceID is the instance. Required.
	InstanceID string `json:"instance_id"`
	// Username is the account to create. Required.
	Username string `json:"username"`
	// Database is the database to scope the account to. Required for every
	// engine with named databases.
	Database string `json:"database,omitempty"`
	// Grant is the privilege level; empty defaults to GrantReadWrite.
	Grant GrantLevel `json:"grant,omitempty"`
	// Actor is the account performing the action, recorded in the audit log.
	Actor string `json:"-"`
}

// GrantRequest changes one account's access to one database.
type GrantRequest struct {
	// TenantID is the owning tenant. Required.
	TenantID string `json:"tenant_id"`
	// InstanceID is the instance. Required.
	InstanceID string `json:"instance_id"`
	// Username is the account. Required.
	Username string `json:"username"`
	// Database is the database. Required.
	Database string `json:"database"`
	// Grant is the privilege level to apply. Ignored by Revoke.
	Grant GrantLevel `json:"grant,omitempty"`
	// Actor is the account performing the action, recorded in the audit log.
	Actor string `json:"-"`
}

// DropRequest removes a database or an account. Confirm guards the database
// form for the same reason DestroyRequest guards an instance.
type DropRequest struct {
	// TenantID is the owning tenant. Required.
	TenantID string `json:"tenant_id"`
	// InstanceID is the instance. Required.
	InstanceID string `json:"instance_id"`
	// Name is the database or account name. Required.
	Name string `json:"name"`
	// Confirm must be true to drop a database.
	Confirm bool `json:"confirm"`
	// Actor is the account performing the action, recorded in the audit log.
	Actor string `json:"-"`
}

// BackupRequest takes a native dump of an instance or of one database.
type BackupRequest struct {
	// TenantID is the owning tenant. Required.
	TenantID string `json:"tenant_id"`
	// InstanceID is the instance. Required.
	InstanceID string `json:"instance_id"`
	// Database, when set, dumps that database instead of the whole instance.
	Database string `json:"database,omitempty"`
	// Actor is the account performing the action, recorded in the audit log.
	Actor string `json:"-"`
}

// RestoreRequest restores a stored dump back into an instance.
type RestoreRequest struct {
	// TenantID is the owning tenant. Required.
	TenantID string `json:"tenant_id"`
	// InstanceID is the instance to restore into. Required.
	InstanceID string `json:"instance_id"`
	// BackupID is the backup record to restore. Required.
	BackupID string `json:"backup_id"`
	// Confirm must be true: a restore overwrites live data.
	Confirm bool `json:"confirm"`
	// Actor is the account performing the action, recorded in the audit log.
	Actor string `json:"-"`
}

// ReplicaRequest adds or removes a read replica of an instance.
type ReplicaRequest struct {
	// TenantID is the owning tenant. Required.
	TenantID string `json:"tenant_id"`
	// InstanceID is the primary instance. Required.
	InstanceID string `json:"instance_id"`
	// ReplicaID is the replica to remove. Required by RemoveReplica only.
	ReplicaID string `json:"replica_id,omitempty"`
	// Name is the name of the replica to create. Required by AddReplica.
	Name string `json:"name,omitempty"`
	// Actor is the account performing the action, recorded in the audit log.
	Actor string `json:"-"`
}
