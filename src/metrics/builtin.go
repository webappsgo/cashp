package metrics

import (
	"runtime"
	"time"
)

// Metric names required by AI.md PART 21, without the namespace prefix. The
// registry adds "{project_name}_" to every one of them.
const (
	MetricAppInfo           = "app_info"
	MetricAppUptimeSeconds  = "app_uptime_seconds"
	MetricAppStartTimestamp = "app_start_timestamp"

	MetricHTTPRequestsTotal        = "http_requests_total"
	MetricHTTPRequestDuration      = "http_request_duration_seconds"
	MetricHTTPRequestSizeBytes     = "http_request_size_bytes"
	MetricHTTPResponseSizeBytes    = "http_response_size_bytes"
	MetricHTTPActiveRequests       = "http_active_requests"
	MetricDBQueriesTotal           = "db_queries_total"
	MetricDBQueryDurationSeconds   = "db_query_duration_seconds"
	MetricDBConnectionsOpen        = "db_connections_open"
	MetricDBConnectionsInUse       = "db_connections_in_use"
	MetricDBErrorsTotal            = "db_errors_total"
	MetricCacheHitsTotal           = "cache_hits_total"
	MetricCacheMissesTotal         = "cache_misses_total"
	MetricCacheEvictionsTotal      = "cache_evictions_total"
	MetricCacheSize                = "cache_size"
	MetricCacheBytes               = "cache_bytes"
	MetricSchedulerTasksTotal      = "scheduler_tasks_total"
	MetricSchedulerTaskDuration    = "scheduler_task_duration_seconds"
	MetricSchedulerTasksRunning    = "scheduler_tasks_running"
	MetricSchedulerLastRun         = "scheduler_last_run_timestamp"
	MetricAuthAttemptsTotal        = "auth_attempts_total"
	MetricAuthSessionsActive       = "auth_sessions_active"
	MetricUsersTotal               = "users_total"
	MetricUsersActive              = "users_active"
	MetricAPITokensActive          = "api_tokens_active"
	MetricSystemCPUUsagePercent    = "system_cpu_usage_percent"
	MetricSystemMemoryUsagePercent = "system_memory_usage_percent"
	MetricSystemMemoryUsedBytes    = "system_memory_used_bytes"
	MetricSystemMemoryTotalBytes   = "system_memory_total_bytes"
	MetricSystemDiskUsagePercent   = "system_disk_usage_percent"
	MetricSystemDiskUsedBytes      = "system_disk_used_bytes"
	MetricSystemDiskTotalBytes     = "system_disk_total_bytes"
	MetricGoGoroutines             = "go_goroutines"
	MetricGoMemAllocBytes          = "go_mem_alloc_bytes"
	MetricGoMemSysBytes            = "go_mem_sys_bytes"
	MetricGoGCRunsTotal            = "go_gc_runs_total"
	MetricGoGCPauseTotalSeconds    = "go_gc_pause_total_seconds"
	MetricClusterNodesTotal        = "cluster_nodes_total"
	MetricClusterNodesHealthy      = "cluster_nodes_healthy"
	MetricClusterIsPrimary         = "cluster_is_primary"
	MetricClusterSyncLagSeconds    = "cluster_sync_lag_seconds"
	MetricClusterElectionsTotal    = "cluster_elections_total"
	MetricTorEnabled               = "tor_enabled"
	MetricTorRunning               = "tor_running"
	MetricTorCircuitEstablished    = "tor_circuit_established"
	MetricTorRequestsTotal         = "tor_requests_total"
	MetricRatelimitRequestsTotal   = "ratelimit_requests_total"
	MetricRatelimitBlockedTotal    = "ratelimit_blocked_total"
)

// Histogram bucket sets fixed by PART 21.
var (
	// DurationBuckets is used by http_request_duration_seconds.
	DurationBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

	// SizeBuckets is used by http_request_size_bytes and
	// http_response_size_bytes.
	SizeBuckets = []float64{100, 1000, 10000, 100000, 1000000, 10000000}

	// DBDurationBuckets is used by db_query_duration_seconds.
	DBDurationBuckets = []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1}

	// SchedulerDurationBuckets is used by scheduler_task_duration_seconds.
	SchedulerDurationBuckets = []float64{0.1, 0.5, 1, 5, 10, 30, 60, 300, 600}
)

// builtin describes one specification-defined metric family.
type builtin struct {
	name    string
	typ     string
	help    string
	buckets []float64
}

// builtins is every metric family PART 21 defines, with the exact name,
// type, help text, and buckets from the Complete Metrics Reference.
var builtins = []builtin{
	{MetricAppInfo, TypeGauge, "Application information", nil},
	{MetricAppUptimeSeconds, TypeGauge, "Application uptime in seconds", nil},
	{MetricAppStartTimestamp, TypeGauge, "Application start timestamp", nil},

	{MetricHTTPRequestsTotal, TypeCounter, "Total number of HTTP requests", nil},
	{MetricHTTPRequestDuration, TypeHistogram, "HTTP request duration in seconds", DurationBuckets},
	{MetricHTTPRequestSizeBytes, TypeHistogram, "HTTP request size in bytes", SizeBuckets},
	{MetricHTTPResponseSizeBytes, TypeHistogram, "HTTP response size in bytes", SizeBuckets},
	{MetricHTTPActiveRequests, TypeGauge, "Number of active HTTP requests", nil},

	{MetricDBQueriesTotal, TypeCounter, "Total number of database queries", nil},
	{MetricDBQueryDurationSeconds, TypeHistogram, "Database query duration in seconds", DBDurationBuckets},
	{MetricDBConnectionsOpen, TypeGauge, "Number of open database connections", nil},
	{MetricDBConnectionsInUse, TypeGauge, "Number of database connections in use", nil},
	{MetricDBErrorsTotal, TypeCounter, "Total number of database errors", nil},

	{MetricCacheHitsTotal, TypeCounter, "Total number of cache hits", nil},
	{MetricCacheMissesTotal, TypeCounter, "Total number of cache misses", nil},
	{MetricCacheEvictionsTotal, TypeCounter, "Total number of cache evictions", nil},
	{MetricCacheSize, TypeGauge, "Current cache size (items)", nil},
	{MetricCacheBytes, TypeGauge, "Current cache size (bytes)", nil},

	{MetricSchedulerTasksTotal, TypeCounter, "Total number of scheduled tasks executed", nil},
	{MetricSchedulerTaskDuration, TypeHistogram, "Scheduled task duration in seconds", SchedulerDurationBuckets},
	{MetricSchedulerTasksRunning, TypeGauge, "Number of currently running scheduled tasks", nil},
	{MetricSchedulerLastRun, TypeGauge, "Timestamp of last task run", nil},

	{MetricAuthAttemptsTotal, TypeCounter, "Total authentication attempts", nil},
	{MetricAuthSessionsActive, TypeGauge, "Number of active sessions", nil},

	{MetricUsersTotal, TypeGauge, "Total number of registered users", nil},
	{MetricUsersActive, TypeGauge, "Number of users active in last 24 hours", nil},
	{MetricAPITokensActive, TypeGauge, "Number of active API tokens", nil},

	{MetricSystemCPUUsagePercent, TypeGauge, "Current CPU usage percentage", nil},
	{MetricSystemMemoryUsagePercent, TypeGauge, "Current memory usage percentage", nil},
	{MetricSystemMemoryUsedBytes, TypeGauge, "Memory used in bytes", nil},
	{MetricSystemMemoryTotalBytes, TypeGauge, "Total memory in bytes", nil},
	{MetricSystemDiskUsagePercent, TypeGauge, "Disk usage percentage", nil},
	{MetricSystemDiskUsedBytes, TypeGauge, "Disk space used in bytes", nil},
	{MetricSystemDiskTotalBytes, TypeGauge, "Total disk space in bytes", nil},

	{MetricGoGoroutines, TypeGauge, "Number of goroutines", nil},
	{MetricGoMemAllocBytes, TypeGauge, "Bytes allocated and in use", nil},
	{MetricGoMemSysBytes, TypeGauge, "Total bytes obtained from system", nil},
	{MetricGoGCRunsTotal, TypeCounter, "Total number of GC runs", nil},
	{MetricGoGCPauseTotalSeconds, TypeCounter, "Total time spent in GC pauses", nil},

	{MetricClusterNodesTotal, TypeGauge, "Total nodes in cluster", nil},
	{MetricClusterNodesHealthy, TypeGauge, "Healthy nodes in cluster", nil},
	{MetricClusterIsPrimary, TypeGauge, "1 if this node is primary", nil},
	{MetricClusterSyncLagSeconds, TypeGauge, "Replication lag from primary in seconds", nil},
	{MetricClusterElectionsTotal, TypeCounter, "Total primary elections", nil},

	{MetricTorEnabled, TypeGauge, "1 if Tor is enabled", nil},
	{MetricTorRunning, TypeGauge, "1 if Tor process is running", nil},
	{MetricTorCircuitEstablished, TypeGauge, "1 if circuit established", nil},
	{MetricTorRequestsTotal, TypeCounter, "Total requests via Tor hidden service", nil},

	{MetricRatelimitRequestsTotal, TypeCounter, "Total rate-limited requests", nil},
	{MetricRatelimitBlockedTotal, TypeCounter, "Requests blocked by rate limiter", nil},
}

// registerBuiltins declares every specification-defined family and creates
// the label-less REQUIRED series so a fresh process still exposes them.
func (r *Registry) registerBuiltins() {
	for _, b := range builtins {
		r.Declare(b.name, b.typ, b.help, b.buckets)
	}

	r.Gauge(MetricHTTPActiveRequests)
	r.Gauge(MetricAuthSessionsActive)
	r.Gauge(MetricAppUptimeSeconds)
	r.Gauge(MetricAppStartTimestamp).Set(float64(r.start.Unix()))

	r.InitAppInfo(r.opts.Version, r.opts.Commit, r.opts.BuildDate)
}

// InitAppInfo publishes the app_info series. The value is always 1 and the
// build details ride on the labels, as PART 21 requires.
func (r *Registry) InitAppInfo(version, commit, buildDate string) {
	r.Gauge(
		MetricAppInfo,
		"version", orUnknown(version),
		"commit", orUnknown(commit),
		"build_date", orUnknown(buildDate),
		"go_version", runtime.Version(),
	).Set(1)
}

// Uptime returns how long this registry has been running.
func (r *Registry) Uptime() time.Duration {
	return time.Since(r.start)
}

// refresh updates the metrics the registry observes about itself right
// before every collection, so a scrape never reports a stale uptime,
// goroutine count, or memory figure.
func (r *Registry) refresh() {
	r.Gauge(MetricAppUptimeSeconds).Set(r.Uptime().Seconds())

	if !r.opts.DisableRuntimeMetrics {
		r.refreshRuntime()
	}

	if r.opts.IncludeSystemMetrics {
		r.refreshSystem()
	}
}

// refreshRuntime publishes the Go runtime metrics gated by
// server.metrics.include_runtime.
func (r *Registry) refreshRuntime() {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)

	r.Gauge(MetricGoGoroutines).Set(float64(runtime.NumGoroutine()))
	r.Gauge(MetricGoMemAllocBytes).Set(float64(stats.Alloc))
	r.Gauge(MetricGoMemSysBytes).Set(float64(stats.Sys))

	// GC totals are read as absolutes from the runtime, so only the growth
	// since the previous refresh is added to the counters.
	gcRuns := r.Counter(MetricGoGCRunsTotal)
	if delta := float64(stats.NumGC) - gcRuns.Value(); delta > 0 {
		gcRuns.Add(delta)
	}

	gcPause := r.Counter(MetricGoGCPauseTotalSeconds)
	if delta := float64(stats.PauseTotalNs)/float64(time.Second) - gcPause.Value(); delta > 0 {
		gcPause.Add(delta)
	}
}

// orUnknown replaces an unset build detail with a stable placeholder so
// app_info never carries an empty label value.
func orUnknown(v string) string {
	if v == "" {
		return "unknown"
	}

	return v
}
