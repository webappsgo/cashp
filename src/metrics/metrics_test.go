package metrics

import (
	"math"
	"strings"
	"testing"
	"time"
)

// newTestRegistry returns a registry with every service token set so the
// handlers under test are reachable.
func newTestRegistry(t *testing.T) *Registry {
	t.Helper()

	return New(Options{
		ServiceTokens: map[string]string{
			ServicePrometheus: "prom-token",
			ServiceGrafana:    "graf-token",
			ServiceLoki:       "loki-token",
		},
		RootAliasEnabled: true,
		APIVersion:       "v1",
		Version:          "1.0.0",
		Commit:           "abc1234",
		BuildDate:        "2026-01-15",
	})
}

func TestNewUsesDefaultNamespace(t *testing.T) {
	r := New(Options{})

	if r.Namespace() != DefaultNamespace {
		t.Fatalf("namespace = %q, want %q", r.Namespace(), DefaultNamespace)
	}

	if got := r.fullName(MetricHTTPRequestsTotal); got != DefaultNamespace+"_http_requests_total" {
		t.Fatalf("fullName = %q", got)
	}

	// An already-prefixed name must not be prefixed twice.
	if got := r.fullName(DefaultNamespace + "_custom_total"); got != DefaultNamespace+"_custom_total" {
		t.Fatalf("double prefix: %q", got)
	}
}

func TestCounterMath(t *testing.T) {
	r := New(Options{Namespace: "test"})

	c := r.Counter("widgets_total", "kind", "blue")
	c.Inc()
	c.Add(4.5)

	// Counters never decrease: a negative delta is ignored.
	c.Add(-10)

	if got := c.Value(); got != 5.5 {
		t.Fatalf("counter = %v, want 5.5", got)
	}

	if same := r.Counter("widgets_total", "kind", "blue"); same != c {
		t.Fatal("same name and labels must return the same counter")
	}

	if other := r.Counter("widgets_total", "kind", "red"); other.Value() != 0 {
		t.Fatal("a different label set must be a separate series")
	}
}

func TestGaugeMath(t *testing.T) {
	r := New(Options{Namespace: "test"})

	g := r.Gauge("connections")
	g.Set(10)
	g.Inc()
	g.Dec()
	g.Add(5)
	g.Sub(3)

	if got := g.Value(); got != 12 {
		t.Fatalf("gauge = %v, want 12", got)
	}

	g.SetToCurrentTime()

	if delta := math.Abs(g.Value() - float64(time.Now().Unix())); delta > 2 {
		t.Fatalf("SetToCurrentTime drifted by %v seconds", delta)
	}
}

func TestHistogramMath(t *testing.T) {
	r := New(Options{Namespace: "test"})

	h := r.Histogram("latency_seconds", []float64{0.1, 0.5, 1}, "route", "/x")
	h.Observe(0.05)
	h.Observe(0.4)
	h.Observe(2)
	h.ObserveDuration(500 * time.Millisecond)

	// NaN would poison the sum for every consumer and must be dropped.
	h.Observe(math.NaN())

	if got := h.Count(); got != 4 {
		t.Fatalf("count = %d, want 4", got)
	}

	if got := h.Sum(); math.Abs(got-2.95) > 1e-9 {
		t.Fatalf("sum = %v, want 2.95", got)
	}

	want := []uint64{1, 3, 3}
	for i, b := range h.Buckets() {
		if b.Count != want[i] {
			t.Fatalf("bucket le=%v count = %d, want %d", b.UpperBound, b.Count, want[i])
		}
	}
}

func TestHistogramBucketsSortedAndDeduped(t *testing.T) {
	h := newHistogram([]float64{1, 0.5, 1, math.Inf(1), math.NaN(), 0.1})

	want := []float64{0.1, 0.5, 1}
	got := h.Buckets()

	if len(got) != len(want) {
		t.Fatalf("bucket count = %d, want %d", len(got), len(want))
	}

	for i, b := range got {
		if b.UpperBound != want[i] {
			t.Fatalf("bound[%d] = %v, want %v", i, b.UpperBound, want[i])
		}
	}
}

func TestParseLabelsSortsPairs(t *testing.T) {
	labels := parseLabels([]string{"status", "200", "method", "GET"})

	if len(labels) != 2 || labels[0].Name != "method" || labels[1].Name != "status" {
		t.Fatalf("labels = %+v", labels)
	}

	if key := labelKey(labels); key != "method=GET,status=200" {
		t.Fatalf("labelKey = %q", key)
	}
}

func TestParseLabelsRejectsOddPairs(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("odd label list must panic")
		}
	}()

	parseLabels([]string{"method"})
}

func TestCollectIsSortedAndCarriesHelp(t *testing.T) {
	r := New(Options{Namespace: "test"})
	r.Counter("zebra_total").Inc()
	r.SetHelp("zebra_total", "Zebras seen")
	r.Gauge("alpha").Set(1)

	samples := r.Collect()

	var names []string
	for _, s := range samples {
		names = append(names, s.Name)
	}

	if !sortedStrings(names) {
		t.Fatalf("Collect output is not sorted: %v", names)
	}

	for _, s := range samples {
		if s.Name == "test_zebra_total" {
			if s.Help != "Zebras seen" {
				t.Fatalf("help = %q", s.Help)
			}
			if s.Type != TypeCounter {
				t.Fatalf("type = %q", s.Type)
			}
		}
	}
}

func TestBuiltinsRegistered(t *testing.T) {
	r := newTestRegistry(t)

	var body strings.Builder
	if err := WriteText(&body, r.Collect()); err != nil {
		t.Fatalf("WriteText: %v", err)
	}

	out := body.String()

	required := []string{
		"cashp_app_info{build_date=\"2026-01-15\",commit=\"abc1234\"",
		"cashp_app_uptime_seconds ",
		"cashp_app_start_timestamp ",
		"cashp_http_active_requests 0",
		"cashp_auth_sessions_active 0",
		"cashp_go_goroutines ",
	}

	for _, want := range required {
		if !strings.Contains(out, want) {
			t.Fatalf("exposition missing %q\n%s", want, out)
		}
	}
}

func TestInitAppInfoFillsUnknownLabels(t *testing.T) {
	r := New(Options{Namespace: "test"})

	var body strings.Builder
	if err := WriteText(&body, r.Collect()); err != nil {
		t.Fatalf("WriteText: %v", err)
	}

	if !strings.Contains(body.String(), `test_app_info{build_date="unknown",commit="unknown"`) {
		t.Fatalf("app_info missing unknown placeholders:\n%s", body.String())
	}
}

func TestRuntimeMetricsCanBeDisabled(t *testing.T) {
	r := New(Options{Namespace: "test", DisableRuntimeMetrics: true})

	for _, s := range r.Collect() {
		if s.Name == "test_go_goroutines" {
			t.Fatal("runtime metrics must not be collected when disabled")
		}
	}
}

// sortedStrings reports whether values are in non-decreasing order.
func sortedStrings(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i] < values[i-1] {
			return false
		}
	}

	return true
}
