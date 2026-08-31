package scheduler

import "time"

// Built-in task identifiers required by AI.md PART 19 § Built-in Tasks
// (Required). Other packages bind their implementations against these exact
// names.
const (
	TaskSSLRenewal       = "ssl_renewal"
	TaskGeoIPUpdate      = "geoip_update"
	TaskBlocklistUpdate  = "blocklist_update"
	TaskCVEUpdate        = "cve_update"
	TaskUpdateCheck      = "update_check"
	TaskSessionCleanup   = "session_cleanup"
	TaskTokenCleanup     = "token_cleanup"
	TaskLogRotation      = "log_rotation"
	TaskBackupDaily      = "backup_daily"
	TaskBackupHourly     = "backup_hourly"
	TaskHealthcheckSelf  = "healthcheck_self"
	TaskTorHealth        = "tor_health"
	TaskI2PHealth        = "i2p_health"
	TaskClusterHeartbeat = "cluster_heartbeat"
)

// BuiltinSpec describes one required built-in task: its identifier, default
// schedule and execution properties. The implementation itself lives in the
// package that owns the work (tls, geoip, backup, update, ...) and is
// attached with Scheduler.Bind.
type BuiltinSpec struct {
	// Name is the unique task identifier.
	Name string
	// Title is the human-readable name shown in the admin panel.
	Title string
	// Schedule is the default schedule expression from AI.md PART 19.
	Schedule string
	// Description explains what the task does.
	Description string
	// ClusterWide marks a global task that must run on exactly one cluster
	// node (AI.md PART 19 § Cluster Mode Task Distribution).
	ClusterWide bool
	// CatchUp marks a task that runs on startup when its window was missed
	// inside the catch-up window.
	CatchUp bool
	// DefaultEnabled is the task's enabled state on first run.
	DefaultEnabled bool
	// Required is true for tasks that are not skippable and must have a
	// bound implementation before the scheduler will start.
	Required bool
	// Conditional names the runtime condition under which an otherwise
	// mandatory task applies (Tor installed, I2P opt-in, cluster mode). Such
	// a task is not Required because the condition may not hold, but leaving
	// it unbound is logged at startup rather than passing silently.
	Conditional string
	// RetryOnFail enables retry with exponential backoff after a failure.
	RetryOnFail bool
	// RetryDelay is the base delay before the first retry.
	RetryDelay time.Duration
}

// builtinSpecs is the authoritative built-in task table from AI.md PART 19
// § Built-in Tasks (Required), with global/local classification from
// § Cluster Mode Task Distribution and retry settings from § Task
// Configuration.
var builtinSpecs = []BuiltinSpec{
	{
		Name:           TaskSSLRenewal,
		Title:          "SSL Renewal",
		Schedule:       "0 3 * * *",
		Description:    "Renew Let's Encrypt certificates 7 days before expiry",
		ClusterWide:    true,
		CatchUp:        true,
		DefaultEnabled: true,
		Required:       true,
	},
	{
		Name:           TaskGeoIPUpdate,
		Title:          "GeoIP Update",
		Schedule:       "0 3 * * 0",
		Description:    "Download and update the ip-location-db GeoIP databases",
		ClusterWide:    true,
		CatchUp:        true,
		DefaultEnabled: true,
	},
	{
		Name:           TaskBlocklistUpdate,
		Title:          "Blocklist Update",
		Schedule:       "0 4 * * *",
		Description:    "Download and update IP and domain blocklists",
		ClusterWide:    true,
		CatchUp:        true,
		DefaultEnabled: true,
		RetryOnFail:    true,
		RetryDelay:     time.Hour,
	},
	{
		Name:           TaskCVEUpdate,
		Title:          "CVE Update",
		Schedule:       "0 5 * * *",
		Description:    "Download and update CVE and security databases",
		ClusterWide:    true,
		CatchUp:        true,
		DefaultEnabled: true,
		RetryOnFail:    true,
		RetryDelay:     time.Hour,
	},
	{
		Name:           TaskUpdateCheck,
		Title:          "Update Check",
		Schedule:       "0 6 * * *",
		Description:    "Check the release channel for a newer version; notify only unless update.auto_install is true",
		ClusterWide:    true,
		CatchUp:        true,
		DefaultEnabled: true,
	},
	{
		Name:           TaskSessionCleanup,
		Title:          "Session Cleanup",
		Schedule:       "@every 15m",
		Description:    "Remove expired sessions",
		CatchUp:        true,
		DefaultEnabled: true,
		Required:       true,
	},
	{
		Name:           TaskTokenCleanup,
		Title:          "Token Cleanup",
		Schedule:       "@every 15m",
		Description:    "Remove expired tokens",
		CatchUp:        true,
		DefaultEnabled: true,
		Required:       true,
	},
	{
		Name:           TaskLogRotation,
		Title:          "Log Rotation",
		Schedule:       "0 0 * * *",
		Description:    "Rotate and compress old logs using each log's server.logs policy",
		CatchUp:        true,
		DefaultEnabled: true,
		Required:       true,
	},
	{
		Name:           TaskBackupDaily,
		Title:          "Backup Daily",
		Schedule:       "0 2 * * *",
		Description:    "Full backup plus daily incremental, verified after creation",
		ClusterWide:    true,
		CatchUp:        true,
		DefaultEnabled: true,
	},
	{
		Name:           TaskBackupHourly,
		Title:          "Backup Hourly",
		Schedule:       "@hourly",
		Description:    "Hourly incremental backup since the daily full backup",
		ClusterWide:    true,
		DefaultEnabled: false,
	},
	{
		Name:           TaskHealthcheckSelf,
		Title:          "Health Check",
		Schedule:       "@every 5m",
		Description:    "Self-health verification",
		DefaultEnabled: true,
		Required:       true,
	},
	{
		Name:           TaskTorHealth,
		Title:          "Tor Health",
		Schedule:       "@every 10m",
		Description:    "Check Tor connectivity and restart the service if unhealthy",
		DefaultEnabled: true,
		Conditional:    "Tor is installed",
	},
	{
		Name:           TaskI2PHealth,
		Title:          "I2P Health",
		Schedule:       "@every 10m",
		Description:    "Check the I2P provider and restart it if unhealthy",
		DefaultEnabled: true,
		Conditional:    "I2P opt-in is enabled",
	},
	{
		Name:           TaskClusterHeartbeat,
		Title:          "Cluster Heartbeat",
		Schedule:       "@every 30s",
		Description:    "Publish this node's cluster heartbeat",
		DefaultEnabled: true,
		Conditional:    "cluster mode is enabled",
	},
}

// Builtins returns a copy of the built-in task table so callers can
// enumerate names, default schedules and descriptions without mutating the
// package's own registry.
func Builtins() []BuiltinSpec {
	out := make([]BuiltinSpec, len(builtinSpecs))
	copy(out, builtinSpecs)
	return out
}

// Builtin returns the specification for one built-in task.
func Builtin(name string) (BuiltinSpec, bool) {
	for _, spec := range builtinSpecs {
		if spec.Name == name {
			return spec, true
		}
	}
	return BuiltinSpec{}, false
}

// task converts a built-in specification into the Task shape the scheduler
// stores, without an implementation — Bind attaches that later.
func (b BuiltinSpec) task() Task {
	return Task{
		Name:        b.Name,
		Title:       b.Title,
		Schedule:    b.Schedule,
		Description: b.Description,
		CatchUp:     b.CatchUp,
		ClusterWide: b.ClusterWide,
		Disabled:    !b.DefaultEnabled,
		MaxRetries:  DefaultMaxRetries,
		RetryDelay:  b.RetryDelay,
	}
}
