package metrics

import (
	"net/http"
)

// grafanaSchemaVersion is the dashboard schema version this definition is
// written against.
const grafanaSchemaVersion = 39

// grafanaDatasourceVar is the template variable every panel points at, so
// the dashboard imports against any Prometheus datasource.
const grafanaDatasourceVar = "${datasource}"

// grafanaDatasource references the dashboard's datasource template variable.
type grafanaDatasource struct {
	Type string `json:"type"`
	UID  string `json:"uid"`
}

// grafanaTarget is one query inside a panel.
type grafanaTarget struct {
	Datasource   grafanaDatasource `json:"datasource"`
	Expr         string            `json:"expr"`
	LegendFormat string            `json:"legendFormat"`
	RefID        string            `json:"refId"`
}

// grafanaGridPos places a panel on the dashboard grid.
type grafanaGridPos struct {
	H int `json:"h"`
	W int `json:"w"`
	X int `json:"x"`
	Y int `json:"y"`
}

// grafanaFieldDefaults carries the unit of a panel's values.
type grafanaFieldDefaults struct {
	Unit string `json:"unit"`
}

// grafanaFieldConfig is the panel field configuration block.
type grafanaFieldConfig struct {
	Defaults  grafanaFieldDefaults `json:"defaults"`
	Overrides []any                `json:"overrides"`
}

// grafanaPanel is a single dashboard panel.
type grafanaPanel struct {
	ID          int                `json:"id"`
	Type        string             `json:"type"`
	Title       string             `json:"title"`
	Description string             `json:"description"`
	Datasource  grafanaDatasource  `json:"datasource"`
	GridPos     grafanaGridPos     `json:"gridPos"`
	FieldConfig grafanaFieldConfig `json:"fieldConfig"`
	Targets     []grafanaTarget    `json:"targets"`
}

// grafanaTemplateVar is a dashboard template variable.
type grafanaTemplateVar struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Label       string `json:"label"`
	Query       string `json:"query"`
	Refresh     int    `json:"refresh"`
	Hide        int    `json:"hide"`
	IncludeAll  bool   `json:"includeAll"`
	Multi       bool   `json:"multi"`
	SkipURLSync bool   `json:"skipUrlSync"`
	Options     []any  `json:"options"`
}

// grafanaTemplating holds the dashboard's variable list.
type grafanaTemplating struct {
	List []grafanaTemplateVar `json:"list"`
}

// grafanaAnnotations holds the dashboard's annotation queries.
type grafanaAnnotations struct {
	List []any `json:"list"`
}

// grafanaTimeRange is the dashboard's default time window.
type grafanaTimeRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// grafanaDashboard is a complete, importable Grafana dashboard definition.
type grafanaDashboard struct {
	Annotations   grafanaAnnotations `json:"annotations"`
	Editable      bool               `json:"editable"`
	GraphTooltip  int                `json:"graphTooltip"`
	Links         []any              `json:"links"`
	Panels        []grafanaPanel     `json:"panels"`
	Refresh       string             `json:"refresh"`
	SchemaVersion int                `json:"schemaVersion"`
	Tags          []string           `json:"tags"`
	Templating    grafanaTemplating  `json:"templating"`
	Time          grafanaTimeRange   `json:"time"`
	Timezone      string             `json:"timezone"`
	Title         string             `json:"title"`
	UID           string             `json:"uid"`
	Version       int                `json:"version"`
	WeekStart     string             `json:"weekStart"`
}

// panelSpec describes one panel before it is laid out on the grid.
type panelSpec struct {
	title       string
	description string
	unit        string
	targets     [][2]string
}

// GrafanaDashboard builds the importable dashboard JSON covering every
// metric category this specification requires. The datasource is left as a
// template variable so it imports against any Prometheus datasource.
func (r *Registry) GrafanaDashboard() any {
	ns := r.namespace

	specs := []panelSpec{
		{
			title:       "HTTP request rate",
			description: "Requests per second by method, path, and status.",
			unit:        "reqps",
			targets:     [][2]string{{"sum by (method, path, status) (rate(" + ns + "_" + MetricHTTPRequestsTotal + "[5m]))", "{{method}} {{path}} {{status}}"}},
		},
		{
			title:       "HTTP request duration",
			description: "95th and 50th percentile request latency.",
			unit:        "s",
			targets: [][2]string{
				{"histogram_quantile(0.95, sum by (le, method, path) (rate(" + ns + "_" + MetricHTTPRequestDuration + "_bucket[5m])))", "p95 {{method}} {{path}}"},
				{"histogram_quantile(0.50, sum by (le, method, path) (rate(" + ns + "_" + MetricHTTPRequestDuration + "_bucket[5m])))", "p50 {{method}} {{path}}"},
			},
		},
		{
			title:       "HTTP payload size",
			description: "95th percentile request and response body size.",
			unit:        "bytes",
			targets: [][2]string{
				{"histogram_quantile(0.95, sum by (le) (rate(" + ns + "_" + MetricHTTPRequestSizeBytes + "_bucket[5m])))", "request p95"},
				{"histogram_quantile(0.95, sum by (le) (rate(" + ns + "_" + MetricHTTPResponseSizeBytes + "_bucket[5m])))", "response p95"},
			},
		},
		{
			title:       "HTTP active requests",
			description: "Requests currently being processed.",
			unit:        "short",
			targets:     [][2]string{{ns + "_" + MetricHTTPActiveRequests, "in flight"}},
		},
		{
			title:       "Database query rate",
			description: "Queries per second by operation and table.",
			unit:        "ops",
			targets:     [][2]string{{"sum by (operation, table) (rate(" + ns + "_" + MetricDBQueriesTotal + "[5m]))", "{{operation}} {{table}}"}},
		},
		{
			title:       "Database query duration",
			description: "95th percentile query latency by operation.",
			unit:        "s",
			targets:     [][2]string{{"histogram_quantile(0.95, sum by (le, operation) (rate(" + ns + "_" + MetricDBQueryDurationSeconds + "_bucket[5m])))", "p95 {{operation}}"}},
		},
		{
			title:       "Database connections",
			description: "Open and in-use pool connections.",
			unit:        "short",
			targets: [][2]string{
				{ns + "_" + MetricDBConnectionsOpen, "open"},
				{ns + "_" + MetricDBConnectionsInUse, "in use"},
			},
		},
		{
			title:       "Database errors",
			description: "Database errors per second by classification.",
			unit:        "ops",
			targets:     [][2]string{{"sum by (operation, error_type) (rate(" + ns + "_" + MetricDBErrorsTotal + "[5m]))", "{{operation}} {{error_type}}"}},
		},
		{
			title:       "Cache hit ratio",
			description: "Hits as a fraction of all cache lookups.",
			unit:        "percentunit",
			targets:     [][2]string{{"sum by (cache) (rate(" + ns + "_" + MetricCacheHitsTotal + "[5m])) / clamp_min(sum by (cache) (rate(" + ns + "_" + MetricCacheHitsTotal + "[5m]) + rate(" + ns + "_" + MetricCacheMissesTotal + "[5m])), 1)", "{{cache}}"}},
		},
		{
			title:       "Cache size",
			description: "Cached items and bytes held per cache.",
			unit:        "short",
			targets: [][2]string{
				{ns + "_" + MetricCacheSize, "{{cache}} items"},
				{ns + "_" + MetricCacheBytes, "{{cache}} bytes"},
			},
		},
		{
			title:       "Cache evictions",
			description: "Evictions per second per cache.",
			unit:        "ops",
			targets:     [][2]string{{"sum by (cache) (rate(" + ns + "_" + MetricCacheEvictionsTotal + "[5m]))", "{{cache}}"}},
		},
		{
			title:       "Scheduler task results",
			description: "Scheduled task executions per second by result.",
			unit:        "ops",
			targets:     [][2]string{{"sum by (task, status) (rate(" + ns + "_" + MetricSchedulerTasksTotal + "[15m]))", "{{task}} {{status}}"}},
		},
		{
			title:       "Scheduler task duration",
			description: "95th percentile scheduled task duration.",
			unit:        "s",
			targets:     [][2]string{{"histogram_quantile(0.95, sum by (le, task) (rate(" + ns + "_" + MetricSchedulerTaskDuration + "_bucket[15m])))", "p95 {{task}}"}},
		},
		{
			title:       "Scheduler freshness",
			description: "Seconds since each task last ran, and instances running now.",
			unit:        "s",
			targets: [][2]string{
				{"time() - " + ns + "_" + MetricSchedulerLastRun, "{{task}} age"},
				{ns + "_" + MetricSchedulerTasksRunning, "{{task}} running"},
			},
		},
		{
			title:       "Authentication attempts",
			description: "Authentication attempts per second by method and result.",
			unit:        "ops",
			targets:     [][2]string{{"sum by (method, status) (rate(" + ns + "_" + MetricAuthAttemptsTotal + "[5m]))", "{{method}} {{status}}"}},
		},
		{
			title:       "System CPU and memory",
			description: "Host CPU and memory utilisation.",
			unit:        "percent",
			targets: [][2]string{
				{ns + "_" + MetricSystemCPUUsagePercent, "cpu"},
				{ns + "_" + MetricSystemMemoryUsagePercent, "memory"},
			},
		},
		{
			title:       "System memory bytes",
			description: "Memory used against total system memory.",
			unit:        "bytes",
			targets: [][2]string{
				{ns + "_" + MetricSystemMemoryUsedBytes, "used"},
				{ns + "_" + MetricSystemMemoryTotalBytes, "total"},
			},
		},
		{
			title:       "System disk usage",
			description: "Disk utilisation of the data directory.",
			unit:        "percent",
			targets:     [][2]string{{ns + "_" + MetricSystemDiskUsagePercent, "{{path}}"}},
		},
		{
			title:       "Go runtime",
			description: "Goroutines and heap allocation.",
			unit:        "short",
			targets: [][2]string{
				{ns + "_" + MetricGoGoroutines, "goroutines"},
				{ns + "_" + MetricGoMemAllocBytes, "heap bytes"},
			},
		},
		{
			title:       "Business totals",
			description: "Registered users, recently active users, active sessions, and active API tokens.",
			unit:        "short",
			targets: [][2]string{
				{ns + "_" + MetricUsersTotal, "users"},
				{ns + "_" + MetricUsersActive, "active users"},
				{ns + "_" + MetricAuthSessionsActive, "active sessions"},
				{ns + "_" + MetricAPITokensActive, "api tokens"},
			},
		},
		{
			title:       "Application uptime",
			description: "Seconds since the running build started.",
			unit:        "s",
			targets:     [][2]string{{ns + "_" + MetricAppUptimeSeconds, "uptime"}},
		},
	}

	return grafanaDashboard{
		Annotations:   grafanaAnnotations{List: []any{}},
		Editable:      true,
		GraphTooltip:  1,
		Links:         []any{},
		Panels:        buildGrafanaPanels(specs),
		Refresh:       "30s",
		SchemaVersion: grafanaSchemaVersion,
		Tags:          []string{ns, "metrics"},
		Templating: grafanaTemplating{List: []grafanaTemplateVar{{
			Type:    "datasource",
			Name:    "datasource",
			Label:   "Datasource",
			Query:   "prometheus",
			Refresh: 1,
			Options: []any{},
		}}},
		Time:      grafanaTimeRange{From: "now-6h", To: "now"},
		Timezone:  "",
		Title:     ns + " metrics",
		UID:       ns + "-metrics",
		Version:   1,
		WeekStart: "",
	}
}

// buildGrafanaPanels lays the panel specs out two per row on the 24-column
// Grafana grid.
func buildGrafanaPanels(specs []panelSpec) []grafanaPanel {
	datasource := grafanaDatasource{Type: "prometheus", UID: grafanaDatasourceVar}
	refIDs := []string{"A", "B", "C", "D", "E", "F"}

	panels := make([]grafanaPanel, 0, len(specs))
	for i, spec := range specs {
		targets := make([]grafanaTarget, 0, len(spec.targets))
		for j, t := range spec.targets {
			refID := "A"
			if j < len(refIDs) {
				refID = refIDs[j]
			}

			targets = append(targets, grafanaTarget{
				Datasource:   datasource,
				Expr:         t[0],
				LegendFormat: t[1],
				RefID:        refID,
			})
		}

		panels = append(panels, grafanaPanel{
			ID:          i + 1,
			Type:        "timeseries",
			Title:       spec.title,
			Description: spec.description,
			Datasource:  datasource,
			GridPos:     grafanaGridPos{H: 8, W: 12, X: (i % 2) * 12, Y: (i / 2) * 8},
			FieldConfig: grafanaFieldConfig{Defaults: grafanaFieldDefaults{Unit: spec.unit}, Overrides: []any{}},
			Targets:     targets,
		})
	}

	return panels
}

// serveGrafana writes the importable dashboard definition.
func (r *Registry) serveGrafana(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, r.GrafanaDashboard())
}
