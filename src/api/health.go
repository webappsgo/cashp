package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Health status values (AI.md PART 13 § Health Status Values & HTTP Codes).
const (
	// StatusHealthy means every check passed.
	StatusHealthy = "healthy"
	// StatusDegraded means a non-critical check failed but the server serves.
	StatusDegraded = "degraded"
	// StatusRestartRequired means a config change needs a restart.
	StatusRestartRequired = "restart_required"
	// StatusUnhealthy means a critical check failed.
	StatusUnhealthy = "unhealthy"
	// StatusMaintenance means maintenance mode is active.
	StatusMaintenance = "maintenance"
	// StatusShuttingDown means a graceful shutdown is in progress.
	StatusShuttingDown = "shutting_down"
)

// CheckOK and CheckError are the only component-check values a health
// response may carry; details would leak internal information.
const (
	// CheckOK marks a passing component check.
	CheckOK = "ok"
	// CheckError marks a failing component check.
	CheckError = "error"
)

// HealthResponse is the canonical health payload. Field order matches
// AI.md PART 13 exactly and every value must be safe for an unauthenticated
// viewer on the public internet.
type HealthResponse struct {
	Project        ProjectInfo  `json:"project"`
	Status         string       `json:"status"`
	PendingRestart bool         `json:"pending_restart,omitempty"`
	RestartReason  []string     `json:"restart_reason,omitempty"`
	Version        string       `json:"version"`
	GoVersion      string       `json:"go_version"`
	Build          BuildInfo    `json:"build"`
	Uptime         string       `json:"uptime"`
	Mode           string       `json:"mode"`
	Timestamp      time.Time    `json:"timestamp"`
	Cluster        ClusterInfo  `json:"cluster"`
	Features       FeaturesInfo `json:"features"`
	Checks         ChecksInfo   `json:"checks"`
	Stats          StatsInfo    `json:"stats"`
}

// ProjectInfo carries the public branding identity of the instance.
type ProjectInfo struct {
	Name        string `json:"name"`
	Tagline     string `json:"tagline"`
	Description string `json:"description"`
}

// BuildInfo carries the build-time identity injected into package main.
type BuildInfo struct {
	Commit string `json:"commit"`
	Date   string `json:"date"`
}

// ClusterInfo carries public cluster topology. Node entries are public URLs
// only — never private addresses.
type ClusterInfo struct {
	Enabled   bool     `json:"enabled"`
	Status    string   `json:"status,omitempty"`
	Primary   string   `json:"primary,omitempty"`
	Nodes     []string `json:"nodes,omitempty"`
	NodeCount int      `json:"node_count,omitempty"`
	Role      string   `json:"role,omitempty"`
}

// TorInfo describes the Tor hidden service (AI.md PART 32.1).
type TorInfo struct {
	Enabled  bool   `json:"enabled"`
	Running  bool   `json:"running"`
	Status   string `json:"status"`
	Hostname string `json:"hostname"`
}

// I2PInfo describes the opt-in I2P eepsite (AI.md PART 32.2).
type I2PInfo struct {
	Enabled  bool   `json:"enabled"`
	Running  bool   `json:"running"`
	Status   string `json:"status"`
	Hostname string `json:"hostname"`
	Provider string `json:"provider"`
}

// FeaturesInfo lists public feature state. multi_user, organizations, and
// custom_domains are non-negotiable for cashp (IDEA.md project variables),
// so they always report their actual state.
type FeaturesInfo struct {
	Tor           TorInfo
	I2P           I2PInfo
	GeoIP         bool
	MultiUser     bool
	Organizations bool
	CustomDomains bool
	// Extra holds app-specific public feature flags.
	Extra map[string]any
}

// MarshalJSON emits the feature block in canonical order with app-specific
// flags appended in a stable order.
func (f FeaturesInfo) MarshalJSON() ([]byte, error) {
	pairs := []orderedPair{
		{"tor", f.Tor},
		{"i2p", f.I2P},
		{"geoip", f.GeoIP},
		{"multi_user", f.MultiUser},
		{"organizations", f.Organizations},
		{"custom_domains", f.CustomDomains},
	}
	return marshalOrdered(append(pairs, extraPairs(f.Extra)...))
}

// ChecksInfo carries component health as "ok" or "error" only.
type ChecksInfo struct {
	Database  string
	Cache     string
	Disk      string
	Scheduler string
	Cluster   string
	Tor       string
	I2P       string
	// Extra holds app-specific component checks.
	Extra map[string]string
}

// MarshalJSON emits checks in canonical order, omitting optional components
// that are not enabled.
func (c ChecksInfo) MarshalJSON() ([]byte, error) {
	pairs := []orderedPair{
		{"database", c.Database},
		{"cache", c.Cache},
		{"disk", c.Disk},
		{"scheduler", c.Scheduler},
	}
	for _, opt := range []orderedPair{{"cluster", c.Cluster}, {"tor", c.Tor}, {"i2p", c.I2P}} {
		if opt.Value.(string) != "" {
			pairs = append(pairs, opt)
		}
	}
	extra := make(map[string]any, len(c.Extra))
	for k, v := range c.Extra {
		extra[k] = v
	}
	return marshalOrdered(append(pairs, extraPairs(extra)...))
}

// StatsInfo carries public-safe aggregate counters.
type StatsInfo struct {
	RequestsTotal int64
	Requests24h   int64
	ActiveConns   int
	// Extra holds app-specific public aggregates.
	Extra map[string]any
}

// MarshalJSON emits statistics in canonical order.
func (s StatsInfo) MarshalJSON() ([]byte, error) {
	pairs := []orderedPair{
		{"requests_total", s.RequestsTotal},
		{"requests_24h", s.Requests24h},
		{"active_connections", s.ActiveConns},
	}
	return marshalOrdered(append(pairs, extraPairs(s.Extra)...))
}

// orderedPair is one key/value entry of an ordered JSON object.
type orderedPair struct {
	Key   string
	Value any
}

// extraPairs turns an app-specific map into pairs sorted by key so output
// is deterministic across restarts.
func extraPairs(extra map[string]any) []orderedPair {
	if len(extra) == 0 {
		return nil
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]orderedPair, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, orderedPair{k, extra[k]})
	}
	return pairs
}

// marshalOrdered encodes pairs as a JSON object preserving their order.
func marshalOrdered(pairs []orderedPair) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, p := range pairs {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(p.Key)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		val, err := json.Marshal(p.Value)
		if err != nil {
			return nil, err
		}
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// Check is one component probe contributing to the overall health status.
type Check struct {
	// Name is the health field name, such as "database" or "storage".
	Name string
	// Critical marks probes whose failure makes the instance unhealthy;
	// non-critical failures downgrade the instance to degraded.
	Critical bool
	// Probe returns nil when the component is healthy. Its error is used
	// only to decide ok/error — the error text never reaches the response.
	Probe func(ctx context.Context) error
}

// HealthOptions wires the health endpoint to the rest of the process. Every
// hook is optional; a nil hook simply contributes nothing.
type HealthOptions struct {
	Project        ProjectInfo
	Build          Build
	Mode           string
	Started        time.Time
	Timeout        time.Duration
	Checks         []Check
	Cluster        func() ClusterInfo
	Features       func() FeaturesInfo
	Stats          func() StatsInfo
	PendingRestart func() (bool, []string)
	Maintenance    func() bool
	ShuttingDown   func() bool
	// Now overrides the clock; used by tests.
	Now func() time.Time
}

// Health serves the PART 13 health payload. One instance is mounted at
// every health route — /server/healthz, the optional /healthz root alias,
// /api/{api_version}/server/healthz, and /api/healthz — so the alias is
// never a redirect and never a forked code path.
type Health struct {
	opts HealthOptions
}

// NewHealth builds the health handler.
func NewHealth(opts HealthOptions) *Health {
	if opts.Started.IsZero() {
		opts.Started = time.Now()
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Health{opts: opts}
}

// Snapshot runs every component probe and assembles the health payload.
func (h *Health) Snapshot(ctx context.Context) HealthResponse {
	ctx, cancel := context.WithTimeout(ctx, h.opts.Timeout)
	defer cancel()

	checks := ChecksInfo{Database: CheckOK, Cache: CheckOK, Disk: CheckOK, Scheduler: CheckOK}
	criticalFailed := false
	nonCriticalFailed := false
	for _, check := range h.opts.Checks {
		result := CheckOK
		if check.Probe != nil {
			if err := check.Probe(ctx); err != nil {
				result = CheckError
				if check.Critical {
					criticalFailed = true
				} else {
					nonCriticalFailed = true
				}
			}
		}
		setCheck(&checks, check.Name, result)
	}

	resp := HealthResponse{
		Project:   h.opts.Project,
		Version:   h.opts.Build.Version,
		GoVersion: runtime.Version(),
		Build: BuildInfo{
			Commit: h.opts.Build.CommitID,
			Date:   h.opts.Build.DateString(),
		},
		Uptime:    FormatUptime(h.opts.Now().Sub(h.opts.Started)),
		Mode:      h.opts.Mode,
		Timestamp: h.opts.Now().UTC(),
		Checks:    checks,
	}
	if h.opts.Cluster != nil {
		resp.Cluster = h.opts.Cluster()
	}
	if h.opts.Features != nil {
		resp.Features = h.opts.Features()
	}
	if h.opts.Stats != nil {
		resp.Stats = h.opts.Stats()
	}
	if h.opts.PendingRestart != nil {
		pending, reasons := h.opts.PendingRestart()
		resp.PendingRestart = pending
		if pending {
			resp.RestartReason = reasons
		}
	}

	resp.Status = overallStatus(h.opts, criticalFailed, nonCriticalFailed, resp.PendingRestart)
	return resp
}

// overallStatus resolves the single status value from the probe outcomes
// and the process lifecycle flags, most severe first.
func overallStatus(opts HealthOptions, criticalFailed, nonCriticalFailed, pendingRestart bool) string {
	if opts.ShuttingDown != nil && opts.ShuttingDown() {
		return StatusShuttingDown
	}
	if opts.Maintenance != nil && opts.Maintenance() {
		return StatusMaintenance
	}
	if criticalFailed {
		return StatusUnhealthy
	}
	if nonCriticalFailed {
		return StatusDegraded
	}
	if pendingRestart {
		return StatusRestartRequired
	}
	return StatusHealthy
}

// setCheck stores a probe result in the canonical field for its name, or in
// the app-specific map when the name is not one of the global components.
func setCheck(c *ChecksInfo, name, result string) {
	switch strings.ToLower(name) {
	case "database":
		c.Database = result
	case "cache":
		c.Cache = result
	case "disk":
		c.Disk = result
	case "scheduler":
		c.Scheduler = result
	case "cluster":
		c.Cluster = result
	case "tor":
		c.Tor = result
	case "i2p":
		c.I2P = result
	default:
		if c.Extra == nil {
			c.Extra = map[string]string{}
		}
		c.Extra[name] = result
	}
}

// StatusCode maps a health status to its HTTP status code.
func StatusCode(status string) int {
	switch status {
	case StatusUnhealthy, StatusMaintenance, StatusShuttingDown:
		return http.StatusServiceUnavailable
	default:
		return http.StatusOK
	}
}

// ServeHTTP renders the health payload in the negotiated format. The JSON
// body is a bare object with no {ok,data} envelope on every route and in
// every state, because probes and load balancers expect a flat document.
func (h *Health) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	snapshot := h.Snapshot(r.Context())
	status := StatusCode(snapshot.Status)
	w.Header().Set("Cache-Control", "no-store")
	Write(w, r, status, Body{
		JSON:  snapshot,
		Text:  snapshot.RenderText(),
		HTML:  snapshot.RenderHTML(),
		Title: snapshot.Project.Name + " - Health Status",
	})
}

// RenderText renders the health payload as the canonical dot-notation text
// document described in AI.md PART 13.
func (hr HealthResponse) RenderText() string {
	var b strings.Builder
	b.WriteString("# 1. Project\n")
	fmt.Fprintf(&b, "project.name: %s\n", hr.Project.Name)
	fmt.Fprintf(&b, "project.tagline: %s\n", hr.Project.Tagline)
	fmt.Fprintf(&b, "project.description: %s\n", hr.Project.Description)

	b.WriteString("\n# 2. Status\n")
	fmt.Fprintf(&b, "status: %s\n", hr.Status)
	if hr.PendingRestart {
		fmt.Fprintf(&b, "pending_restart: true\n")
		fmt.Fprintf(&b, "restart_reason: %s\n", strings.Join(hr.RestartReason, ", "))
	}

	b.WriteString("\n# 3. Version & Build\n")
	fmt.Fprintf(&b, "version: %s\n", hr.Version)
	fmt.Fprintf(&b, "go_version: %s\n", hr.GoVersion)
	fmt.Fprintf(&b, "build.commit: %s\n", hr.Build.Commit)
	fmt.Fprintf(&b, "build.date: %s\n", hr.Build.Date)

	b.WriteString("\n# 4. Runtime\n")
	fmt.Fprintf(&b, "uptime: %s\n", hr.Uptime)
	fmt.Fprintf(&b, "mode: %s\n", hr.Mode)
	fmt.Fprintf(&b, "timestamp: %s\n", hr.Timestamp.UTC().Format(time.RFC3339))

	b.WriteString("\n# 5. Cluster\n")
	fmt.Fprintf(&b, "cluster.enabled: %t\n", hr.Cluster.Enabled)
	fmt.Fprintf(&b, "cluster.status: %s\n", hr.Cluster.Status)
	fmt.Fprintf(&b, "cluster.primary: %s\n", hr.Cluster.Primary)
	fmt.Fprintf(&b, "cluster.nodes: %s\n", strings.Join(hr.Cluster.Nodes, ", "))
	fmt.Fprintf(&b, "cluster.node_count: %d\n", hr.Cluster.NodeCount)
	fmt.Fprintf(&b, "cluster.role: %s\n", hr.Cluster.Role)

	b.WriteString("\n# 6. Features\n")
	fmt.Fprintf(&b, "features.tor.enabled: %t\n", hr.Features.Tor.Enabled)
	fmt.Fprintf(&b, "features.tor.running: %t\n", hr.Features.Tor.Running)
	fmt.Fprintf(&b, "features.tor.status: %s\n", hr.Features.Tor.Status)
	fmt.Fprintf(&b, "features.tor.hostname: %s\n", hr.Features.Tor.Hostname)
	fmt.Fprintf(&b, "features.i2p.enabled: %t\n", hr.Features.I2P.Enabled)
	fmt.Fprintf(&b, "features.i2p.running: %t\n", hr.Features.I2P.Running)
	fmt.Fprintf(&b, "features.i2p.status: %s\n", hr.Features.I2P.Status)
	fmt.Fprintf(&b, "features.i2p.hostname: %s\n", hr.Features.I2P.Hostname)
	fmt.Fprintf(&b, "features.i2p.provider: %s\n", hr.Features.I2P.Provider)
	fmt.Fprintf(&b, "features.geoip: %t\n", hr.Features.GeoIP)
	fmt.Fprintf(&b, "features.multi_user: %t\n", hr.Features.MultiUser)
	fmt.Fprintf(&b, "features.organizations: %t\n", hr.Features.Organizations)
	fmt.Fprintf(&b, "features.custom_domains: %t\n", hr.Features.CustomDomains)
	for _, p := range extraPairs(hr.Features.Extra) {
		fmt.Fprintf(&b, "features.%s: %v\n", p.Key, p.Value)
	}

	b.WriteString("\n# 7. Checks\n")
	fmt.Fprintf(&b, "checks.database: %s\n", hr.Checks.Database)
	fmt.Fprintf(&b, "checks.cache: %s\n", hr.Checks.Cache)
	fmt.Fprintf(&b, "checks.disk: %s\n", hr.Checks.Disk)
	fmt.Fprintf(&b, "checks.scheduler: %s\n", hr.Checks.Scheduler)
	for _, opt := range []struct{ name, value string }{
		{"cluster", hr.Checks.Cluster},
		{"tor", hr.Checks.Tor},
		{"i2p", hr.Checks.I2P},
	} {
		if opt.value != "" {
			fmt.Fprintf(&b, "checks.%s: %s\n", opt.name, opt.value)
		}
	}
	extraChecks := make(map[string]any, len(hr.Checks.Extra))
	for k, v := range hr.Checks.Extra {
		extraChecks[k] = v
	}
	for _, p := range extraPairs(extraChecks) {
		fmt.Fprintf(&b, "checks.%s: %v\n", p.Key, p.Value)
	}

	b.WriteString("\n# 8. Stats\n")
	fmt.Fprintf(&b, "stats.requests_total: %d\n", hr.Stats.RequestsTotal)
	fmt.Fprintf(&b, "stats.requests_24h: %d\n", hr.Stats.Requests24h)
	fmt.Fprintf(&b, "stats.active_connections: %d\n", hr.Stats.ActiveConns)
	for _, p := range extraPairs(hr.Stats.Extra) {
		fmt.Fprintf(&b, "stats.%s: %v\n", p.Key, p.Value)
	}

	return b.String()
}

// FormatUptime renders a duration as "2d 5h 30m", the form used by the
// health payload.
func FormatUptime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}
