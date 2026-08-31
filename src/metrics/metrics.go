// Package metrics implements cashp's Prometheus-compatible metrics support
// per AI.md PART 21. It provides the counter/gauge/histogram registry, the
// Prometheus text exposition format writer, the Grafana dashboard and Loki
// stream services, and the mandatory per-service bearer token authentication
// in front of every metrics route. Tokens are accepted from the
// Authorization header only — query-string tokens are FORBIDDEN — and every
// alias path is served by the SAME handler instance, never a redirect.
package metrics

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultNamespace is the metric name prefix required by the PART 21 naming
// convention ("{project_name}_").
const DefaultNamespace = "cashp"

// Service names of the three metrics services defined by PART 21. Each has
// its own bearer token so rotating one never breaks the others.
const (
	ServicePrometheus = "prometheus"
	ServiceGrafana    = "grafana"
	ServiceLoki       = "loki"
)

// services is the canonical service list, in mount order.
var services = []string{ServicePrometheus, ServiceGrafana, ServiceLoki}

// Options configures a Registry. The zero value is usable: it yields the
// cashp namespace, runtime metrics enabled, no root alias, and every service
// disabled (403) because no tokens are set.
type Options struct {
	// ServiceTokens maps a service name (prometheus, grafana, loki) to its
	// bearer token. An empty or missing token disables that service.
	ServiceTokens map[string]string

	// RootAliasEnabled mounts the /metrics root alias
	// (server.metrics.root.enabled, default true in server.yml).
	RootAliasEnabled bool

	// APIVersion is the current API version segment used by the
	// /api/{api_version}/server/metrics routes. Empty omits those routes.
	APIVersion string

	// Namespace is the metric name prefix. Empty means DefaultNamespace.
	Namespace string

	// AllowUnauthenticated is the firewalled escape hatch
	// (server.metrics.auth.allow_unauthenticated, default false). It skips
	// token checks for ALL metrics services and is for internal networks
	// only.
	AllowUnauthenticated bool

	// Version, Commit, and BuildDate populate the app_info labels.
	Version   string
	Commit    string
	BuildDate string

	// DisableRuntimeMetrics turns off the Go runtime metrics that
	// server.metrics.include_runtime enables by default.
	DisableRuntimeMetrics bool

	// IncludeSystemMetrics turns on the CPU, memory, and disk metrics
	// gated by server.metrics.include_system.
	IncludeSystemMetrics bool

	// DataDir is the path reported by the system_disk_* metrics.
	DataDir string

	// LokiMaxEntries and LokiMaxAge bound the Loki service's buffer
	// (server.metrics.loki.max_entries, server.metrics.loki.max_age).
	LokiMaxEntries int
	LokiMaxAge     time.Duration

	// Logf receives the startup notice naming each service disabled by an
	// empty token. It must write to the log files, never the console.
	Logf func(format string, args ...any)
}

// family is one metric family: a name, a type, help text, and every child
// series keyed by its label set.
type family struct {
	name    string
	typ     string
	buckets []float64

	mu       sync.Mutex
	order    []string
	children map[string]*childSeries
}

// childSeries is one label set within a family.
type childSeries struct {
	labels []Label
	value  instance
}

// Registry holds every metric this process exports and owns the HTTP
// handlers that expose them.
type Registry struct {
	opts      Options
	namespace string
	start     time.Time

	mu       sync.RWMutex
	families map[string]*family
	help     map[string]string

	logs *logBuffer

	promHandler    http.Handler
	grafanaHandler http.Handler
	lokiHandler    http.Handler

	disabled []string

	sysMu   sync.Mutex
	lastCPU cpuTimes
}

// New returns a Registry configured by opts, pre-registers the metrics PART
// 21 marks REQUIRED, builds one handler per service, and logs once for every
// service an empty token disables.
func New(opts Options) *Registry {
	r := &Registry{
		opts:      opts,
		namespace: strings.TrimSuffix(strings.TrimSpace(opts.Namespace), "_"),
		start:     time.Now(),
		families:  make(map[string]*family),
		help:      make(map[string]string),
	}

	if r.namespace == "" {
		r.namespace = DefaultNamespace
	}

	r.logs = newLogBuffer(opts.LokiMaxEntries, opts.LokiMaxAge)

	r.registerBuiltins()

	r.promHandler = r.authenticate(ServicePrometheus, http.HandlerFunc(r.servePrometheus))
	r.grafanaHandler = r.authenticate(ServiceGrafana, http.HandlerFunc(r.serveGrafana))
	r.lokiHandler = r.authenticate(ServiceLoki, http.HandlerFunc(r.serveLoki))

	for _, service := range services {
		if strings.TrimSpace(opts.ServiceTokens[service]) == "" {
			r.disabled = append(r.disabled, service)
		}
	}

	r.logDisabled()

	return r
}

// DisabledServices returns the services whose empty token disables them.
// Their endpoints answer 403 with an empty body.
func (r *Registry) DisabledServices() []string {
	out := make([]string, len(r.disabled))
	copy(out, r.disabled)

	return out
}

// logDisabled emits the startup notice for every disabled service exactly
// once. Tokens are never included — there is nothing to redact because the
// value is empty by definition.
func (r *Registry) logDisabled() {
	if r.opts.Logf == nil || len(r.disabled) == 0 {
		return
	}

	for _, service := range r.disabled {
		r.opts.Logf("metrics: service %q disabled: empty bearer token in server.metrics.auth.tokens; its endpoints return 403", service)
	}
}

// Namespace returns the metric name prefix in use, without the trailing
// underscore.
func (r *Registry) Namespace() string {
	return r.namespace
}

// Counter returns the counter for name and the given label pairs, creating
// it on first use. labels is a flat sequence of name/value pairs:
// Counter("http_requests_total", "method", "GET", "status", "200").
func (r *Registry) Counter(name string, labels ...string) *Counter {
	if c, ok := r.metric(name, TypeCounter, nil, labels).(*Counter); ok {
		return c
	}

	// The family is already registered as another type. Hand back a
	// detached counter: it is not exported, but a naming mistake can never
	// panic the server through a nil metric.
	return &Counter{}
}

// Gauge returns the gauge for name and the given label pairs, creating it on
// first use.
func (r *Registry) Gauge(name string, labels ...string) *Gauge {
	if g, ok := r.metric(name, TypeGauge, nil, labels).(*Gauge); ok {
		return g
	}

	// See Counter: a type clash yields a detached metric, never nil.
	return &Gauge{}
}

// Histogram returns the histogram for name and the given label pairs,
// creating it on first use. buckets is the upper-bound list and is only read
// when the family is created; later calls reuse the family's buckets.
func (r *Registry) Histogram(name string, buckets []float64, labels ...string) *Histogram {
	if h, ok := r.metric(name, TypeHistogram, buckets, labels).(*Histogram); ok {
		return h
	}

	// See Counter: a type clash yields a detached metric, never nil.
	return newHistogram(buckets)
}

// SetHelp sets the help text emitted on the "# HELP" line for name. It may
// be called before or after the family itself exists.
func (r *Registry) SetHelp(name, help string) {
	full := r.fullName(name)

	r.mu.Lock()
	r.help[full] = help
	r.mu.Unlock()
}

// Help returns the help text registered for name.
func (r *Registry) Help(name string) string {
	full := r.fullName(name)

	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.help[full]
}

// Declare registers a family's type, help text, and histogram buckets ahead
// of its first observation, so the first caller cannot fix the wrong type or
// bucket list for a specification-defined metric.
func (r *Registry) Declare(name, typ, help string, buckets []float64) {
	r.SetHelp(name, help)
	r.family(r.fullName(name), typ, buckets)
}

// metric resolves one child series, creating the family and the child as
// needed.
func (r *Registry) metric(name, typ string, buckets []float64, labels []string) instance {
	parsed := parseLabels(labels)
	f := r.family(r.fullName(name), typ, buckets)

	key := labelKey(parsed)

	f.mu.Lock()
	defer f.mu.Unlock()

	if child, ok := f.children[key]; ok {
		return child.value
	}

	var value instance
	switch f.typ {
	case TypeCounter:
		value = &Counter{}
	case TypeHistogram:
		value = newHistogram(f.buckets)
	default:
		value = &Gauge{}
	}

	f.children[key] = &childSeries{labels: parsed, value: value}
	f.order = append(f.order, key)

	return value
}

// family returns the named family, creating it with typ and buckets when it
// does not exist yet. An existing family keeps its original type: metric
// types are fixed by the specification and must never change at runtime.
func (r *Registry) family(full, typ string, buckets []float64) *family {
	r.mu.RLock()
	f, ok := r.families[full]
	r.mu.RUnlock()

	if ok {
		return f
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if f, ok := r.families[full]; ok {
		return f
	}

	f = &family{
		name:     full,
		typ:      typ,
		buckets:  append([]float64(nil), buckets...),
		children: make(map[string]*childSeries),
	}
	r.families[full] = f

	return f
}

// fullName prefixes name with the namespace unless it is already prefixed.
func (r *Registry) fullName(name string) string {
	prefix := r.namespace + "_"
	if strings.HasPrefix(name, prefix) {
		return name
	}

	return prefix + name
}

// Collect refreshes the self-observed metrics (uptime, Go runtime, system)
// and returns every sample, sorted by metric name then by label set so the
// exposition output is stable between scrapes.
func (r *Registry) Collect() []Sample {
	r.refresh()

	r.mu.RLock()
	names := make([]string, 0, len(r.families))
	byName := make(map[string]*family, len(r.families))
	helps := make(map[string]string, len(r.help))
	for name, f := range r.families {
		names = append(names, name)
		byName[name] = f
	}
	for name, h := range r.help {
		helps[name] = h
	}
	r.mu.RUnlock()

	sort.Strings(names)

	out := make([]Sample, 0, len(names))
	for _, name := range names {
		f := byName[name]

		help := helps[name]

		f.mu.Lock()
		keys := append([]string(nil), f.order...)
		children := make(map[string]*childSeries, len(f.children))
		for k, v := range f.children {
			children[k] = v
		}
		f.mu.Unlock()

		sort.Strings(keys)

		for _, key := range keys {
			child := children[key]
			sample := child.value.snapshot()
			sample.Name = f.name
			sample.Help = help
			sample.Labels = child.labels
			out = append(out, sample)
		}
	}

	return out
}

// parseLabels turns a flat name/value sequence into labels sorted by name. A
// malformed pair list is a programming error at a fixed call site, so it
// panics rather than silently recording a wrong series.
func parseLabels(pairs []string) []Label {
	if len(pairs) == 0 {
		return nil
	}

	if len(pairs)%2 != 0 {
		panic("metrics: labels must be name/value pairs")
	}

	out := make([]Label, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		if pairs[i] == "" {
			panic("metrics: label name must not be empty")
		}
		out = append(out, Label{Name: pairs[i], Value: pairs[i+1]})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// labelKey builds the map key identifying one label set.
func labelKey(labels []Label) string {
	if len(labels) == 0 {
		return ""
	}

	var b strings.Builder
	for i, l := range labels {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(l.Name)
		b.WriteByte('=')
		b.WriteString(l.Value)
	}

	return b.String()
}
