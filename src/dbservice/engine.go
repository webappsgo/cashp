package dbservice

import (
	"io"
	"sort"
	"time"
)

// Engine is one of the database engines cashp manages as an app-managed
// container (IDEA.md -> Service hosting model). Tenants never define these
// containers: cashp owns the image, the version and the lifecycle so every
// node in a cluster runs an identical, cashp-controlled deployment.
type Engine string

// The managed engines. This list is closed: an engine is only registered
// once its whole lifecycle is implemented.
const (
	// EnginePostgres is PostgreSQL, the managed relational default.
	EnginePostgres Engine = "postgres"
	// EngineMariaDB is MariaDB, the MySQL-protocol relational engine.
	EngineMariaDB Engine = "mariadb"
	// EngineMongoDB is MongoDB, the managed document engine.
	EngineMongoDB Engine = "mongodb"
	// EngineValkey is Valkey, the managed key-value engine.
	EngineValkey Engine = "valkey"
)

// UpgradeStrategy is how an engine moves an instance between versions.
type UpgradeStrategy string

const (
	// StrategyInPlace restarts the instance on the new image and lets the
	// engine migrate its own on-disk format.
	StrategyInPlace UpgradeStrategy = "in_place"
	// StrategyDumpRestore dumps every database, recreates the instance on the
	// new version and restores the dump. PostgreSQL major versions require
	// this because their on-disk format is not forward compatible.
	StrategyDumpRestore UpgradeStrategy = "dump_restore"
)

// Capabilities is what one engine can actually do. Any operation whose
// capability is false returns the typed ErrUnsupported rather than silently
// doing nothing.
type Capabilities struct {
	// NamedDatabases means the engine has named databases a tenant can
	// create and drop. Valkey does not: it exposes a fixed set of numbered
	// logical keyspaces instead.
	NamedDatabases bool
	// Users means the engine has its own user accounts.
	Users bool
	// Grants means per-user privileges can be granted and revoked.
	Grants bool
	// Replicas means the engine supports attaching a read replica.
	Replicas bool
	// InPlaceUpgrade means a version change can reuse the existing data
	// directory.
	InPlaceUpgrade bool
	// PerDatabaseDump means a backup can target one named database instead
	// of the whole instance.
	PerDatabaseDump bool
	// OnlineRestore means a restore can stream into the running instance. An
	// engine without it is restored offline by replacing its snapshot file.
	OnlineRestore bool
}

// EngineInfo is the admin-panel view of one managed engine.
type EngineInfo struct {
	// Engine is the engine identifier.
	Engine Engine `json:"engine"`
	// DisplayName is the human-readable engine name.
	DisplayName string `json:"display_name"`
	// Versions are the versions this build offers, newest last.
	Versions []string `json:"versions"`
	// DefaultVersion is the version chosen when a request omits one.
	DefaultVersion string `json:"default_version"`
	// Port is the port the engine listens on inside its container.
	Port int `json:"port"`
	// Scheme is the URI scheme of a connection string for this engine.
	Scheme string `json:"scheme"`
	// UpgradeStrategy is how a version change is carried out.
	UpgradeStrategy UpgradeStrategy `json:"upgrade_strategy"`
	// Capabilities is the engine's capability matrix.
	Capabilities Capabilities `json:"capabilities"`
}

// engineDisplayNames maps each engine to its product name.
var engineDisplayNames = map[Engine]string{
	EnginePostgres: "PostgreSQL",
	EngineMariaDB:  "MariaDB",
	EngineMongoDB:  "MongoDB",
	EngineValkey:   "Valkey",
}

// EngineDisplayName returns the product name of an engine, falling back to
// the raw identifier for an unknown one so error text stays readable.
func EngineDisplayName(e Engine) string {
	if name, ok := engineDisplayNames[e]; ok {
		return name
	}
	return string(e)
}

// registry holds every implemented engine adapter.
var registry = map[Engine]adapter{
	EnginePostgres: postgresAdapter{},
	EngineMariaDB:  mariadbAdapter{},
	EngineMongoDB:  mongodbAdapter{},
	EngineValkey:   valkeyAdapter{},
}

// Engines lists every managed engine in a stable order.
func Engines() []Engine {
	out := make([]Engine, 0, len(registry))
	for e := range registry {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// EngineInfos returns the capability matrix and version list of every
// managed engine, for the admin panel and the API.
func EngineInfos() []EngineInfo {
	engines := Engines()
	out := make([]EngineInfo, 0, len(engines))
	for _, e := range engines {
		info, err := InfoFor(e)
		if err != nil {
			continue
		}
		out = append(out, info)
	}
	return out
}

// InfoFor returns one engine's descriptor.
func InfoFor(e Engine) (EngineInfo, error) {
	a, err := adapterFor(e)
	if err != nil {
		return EngineInfo{}, err
	}
	versions := a.versions()
	return EngineInfo{
		Engine:          a.engine(),
		DisplayName:     EngineDisplayName(a.engine()),
		Versions:        append([]string(nil), versions...),
		DefaultVersion:  versions[len(versions)-1],
		Port:            a.defaultPort(),
		Scheme:          a.scheme(),
		UpgradeStrategy: a.upgradeStrategy(),
		Capabilities:    a.capabilities(),
	}, nil
}

// adapterFor resolves an engine to its adapter.
func adapterFor(e Engine) (adapter, error) {
	a, ok := registry[e]
	if !ok {
		return nil, ErrUnknownEngine(string(e))
	}
	return a, nil
}

// resolveVersion validates a requested version and checks it against the
// engine's offered list. An empty request selects the engine default.
func resolveVersion(a adapter, requested string) (string, error) {
	versions := a.versions()
	if requested == "" {
		return versions[len(versions)-1], nil
	}
	if err := ValidateVersion(requested); err != nil {
		return "", err
	}
	for _, v := range versions {
		if v == requested {
			return v, nil
		}
	}
	return "", ErrValidation("That version of " + EngineDisplayName(a.engine()) + " is not offered by this server.")
}

// versionIndex reports the position of a version in the engine's ordered
// list, or -1 when it is not offered.
func versionIndex(a adapter, version string) int {
	for i, v := range a.versions() {
		if v == version {
			return i
		}
	}
	return -1
}

// engineCtx is everything an adapter needs to build a command for one
// instance: where the engine listens inside its container and which
// administrative account to authenticate as.
type engineCtx struct {
	// Instance is the target instance.
	Instance *Instance
	// AdminUser is the instance's administrative account.
	AdminUser string
	// AdminPassword is that account's current password in the clear. It is
	// only ever placed in an exec environment or a 0600 credential file,
	// never in an argv slice and never in a log line.
	AdminPassword string
	// Host is the address the engine listens on from inside its own
	// container, always loopback.
	Host string
	// Port is the in-container engine port.
	Port int
}

// fileDrop is a file an adapter needs written inside the container, used for
// credential material that must stay off the command line and for engine
// configuration that carries a password.
type fileDrop struct {
	// Path is the absolute in-container path.
	Path string
	// Mode is the file mode, always owner-only for credential material.
	Mode uint32
	// UID is the owning user id inside the container.
	UID int
	// GID is the owning group id inside the container.
	GID int
	// Content is the file body.
	Content []byte
}

// command is one engine operation: optional files to drop first, the exec
// itself, and files to remove afterwards.
type command struct {
	// Files are written before Exec runs.
	Files []fileDrop
	// Exec is the argv-only command to run inside the container.
	Exec ExecRequest
	// Cleanup lists paths removed after Exec finishes, successfully or not.
	Cleanup []string
}

// execTarget names which end of a primary/replica pair a command runs on.
type execTarget string

const (
	// targetPrimary runs the command inside the primary instance.
	targetPrimary execTarget = "primary"
	// targetReplica runs the command inside the replica instance.
	targetReplica execTarget = "replica"
)

// replicaCommand is one step of a replication plan together with the end of
// the pair it must run on.
type replicaCommand struct {
	// Target is the instance the command runs in.
	Target execTarget
	// Cmd is the command itself.
	Cmd command
}

// offlineRestore describes a restore performed by replacing a snapshot file
// on a stopped instance rather than streaming into a running one.
type offlineRestore struct {
	// Path is the absolute in-container path of the snapshot file.
	Path string
	// Mode is the mode the snapshot file is written with.
	Mode uint32
	// UID is the account the engine runs as, which must own the snapshot.
	UID int
	// GID is the group the engine runs as.
	GID int
}

// restorePlan is either an online restore command or an offline file
// replacement. Exactly one field is set.
type restorePlan struct {
	// Online streams the archive into the running engine.
	Online *command
	// Offline replaces a snapshot file while the instance is stopped.
	Offline *offlineRestore
}

// specParams is the input an adapter needs to build a container spec.
type specParams struct {
	// Instance is the instance being created.
	Instance *Instance
	// AdminUser is the bootstrap administrative account name.
	AdminUser string
	// AdminPassword is the bootstrap password, delivered through the
	// container environment and never through the command line.
	AdminPassword string
	// Volume is the backend volume name holding the instance data.
	Volume string
	// Network is the per-tenant isolated network the container joins.
	Network string
	// HostIP is the host address the engine port is published on.
	HostIP string
	// HostPort is the host port the engine is published on.
	HostPort int
	// ReplicaOf is set when the container is being created as a replica of
	// an existing primary.
	ReplicaOf *engineCtx
}

// adapter is the per-engine implementation. Every managed engine implements
// the whole interface: there is no partially implemented engine in the
// registry, and an operation an engine genuinely cannot perform returns
// ErrUnsupported from the method rather than being left unimplemented.
type adapter interface {
	// engine is the adapter's engine identifier.
	engine() Engine
	// versions are the offered versions, oldest first.
	versions() []string
	// image is the fully qualified container image for a version.
	image(version string) string
	// defaultPort is the in-container engine port.
	defaultPort() int
	// scheme is the connection-string URI scheme.
	scheme() string
	// dataPath is the in-container path the data volume mounts at.
	dataPath() string
	// adminUser is the administrative account name cashp bootstraps inside
	// the instance. It is owned by cashp and never issued to a tenant.
	adminUser() string
	// capabilities is the engine capability matrix.
	capabilities() Capabilities
	// upgradeStrategy is how a version change is carried out.
	upgradeStrategy() UpgradeStrategy
	// containerSpec builds the full container definition.
	containerSpec(p specParams) ContainerSpec
	// bootstrapFiles are files written into the created container before it
	// is first started, for engines whose configuration carries a secret.
	bootstrapFiles(p specParams) []fileDrop
	// postStartCommands are commands run once the engine answers its health
	// probe for the first time, such as initiating a replica set.
	postStartCommands(c engineCtx) ([]command, error)
	// healthCommand probes the engine at protocol level.
	healthCommand(c engineCtx) command
	// parseHealth turns a health probe result into a health state and a
	// short, non-sensitive detail string.
	parseHealth(res ExecResult) (HealthState, string)
	// createDatabase creates a named database owned by owner.
	createDatabase(c engineCtx, name, owner string) ([]command, error)
	// dropDatabase drops a named database.
	dropDatabase(c engineCtx, name string) ([]command, error)
	// listDatabases lists the tenant-visible databases.
	listDatabases(c engineCtx) ([]command, error)
	// parseDatabaseList extracts database names from a listDatabases result.
	parseDatabaseList(res ExecResult) ([]string, error)
	// createUser creates a least-privilege account with no grants at all.
	createUser(c engineCtx, username, database, password string) ([]command, error)
	// setPassword rotates an existing account's password.
	setPassword(c engineCtx, username, database, password string) ([]command, error)
	// dropUser removes an account.
	dropUser(c engineCtx, username, database string) ([]command, error)
	// grant gives an account the requested access to one database.
	grant(c engineCtx, username, database string, level GrantLevel) ([]command, error)
	// revoke removes an account's access to one database.
	revoke(c engineCtx, username, database string) ([]command, error)
	// dump streams a native backup of one database, or of the whole
	// instance when database is empty, into out.
	dump(c engineCtx, database string, out io.Writer) ([]command, error)
	// restore consumes a native dump previously produced by dump.
	restore(c engineCtx, database string, in io.Reader) (restorePlan, error)
	// replicaNeedsSeed reports whether a new replica must be seeded from a
	// dump of the primary before replication is attached.
	replicaNeedsSeed() bool
	// attachReplica is the ordered plan that joins a replica to its primary.
	attachReplica(replica, primary engineCtx, databases []string) ([]replicaCommand, error)
	// detachReplica is the ordered plan that removes a replica from its
	// primary's topology.
	detachReplica(replica, primary engineCtx, databases []string) ([]replicaCommand, error)
}

// droppedCapabilities are the Linux capabilities removed from every managed
// database container. The engines keep only what their entrypoints need to
// prepare a data directory; nothing here is required by any of them.
var droppedCapabilities = []string{
	"AUDIT_WRITE", "MKNOD", "NET_ADMIN", "NET_RAW", "SYS_ADMIN",
	"SYS_MODULE", "SYS_PTRACE", "SYS_RAWIO", "SYS_TIME",
}

// baseContainerSpec builds the parts of a container definition that are
// identical for every engine: the data volume, the loopback-only published
// port, the tenant attribution labels, the resource envelope and the
// hardening flags. Adapters add their own image, environment and arguments.
func baseContainerSpec(p specParams, a adapter, image string) ContainerSpec {
	return ContainerSpec{
		Name:  p.Instance.ContainerName,
		Image: image,
		Mounts: []Mount{{
			Volume: p.Volume,
			Target: a.dataPath(),
		}},
		Ports: []PortMap{{
			HostIP:        p.HostIP,
			HostPort:      p.HostPort,
			ContainerPort: a.defaultPort(),
			Protocol:      "tcp",
		}},
		Network: p.Network,
		Labels: map[string]string{
			"cashp.tenant":   p.Instance.TenantID,
			"cashp.instance": p.Instance.ID,
			"cashp.engine":   string(a.engine()),
			"cashp.role":     string(p.Instance.Role),
			"cashp.managed":  "database",
		},
		Limits:          p.Instance.Limits,
		RestartPolicy:   "unless-stopped",
		CapDrop:         append([]string(nil), droppedCapabilities...),
		NoNewPrivileges: true,
	}
}

// execTimeouts bound engine commands so a wedged instance cannot hold a
// request or a scheduled sweep open indefinitely.
const (
	// healthTimeout bounds a health probe.
	healthTimeout = 10 * time.Second
	// adminTimeout bounds a management statement such as CREATE DATABASE.
	adminTimeout = 30 * time.Second
	// transferTimeout bounds a dump or restore stream.
	transferTimeout = 6 * time.Hour
)
