package metrics

import (
	"math"
	"strings"
	"testing"
)

func TestWriteTextCounterAndGauge(t *testing.T) {
	samples := []Sample{
		{Name: "cashp_http_requests_total", Help: "Total number of HTTP requests", Type: TypeCounter, Labels: []Label{{"method", "GET"}, {"path", "/api/v1/users"}, {"status", "200"}}, Value: 1523},
		{Name: "cashp_http_requests_total", Help: "Total number of HTTP requests", Type: TypeCounter, Labels: []Label{{"method", "POST"}, {"path", "/api/v1/users"}, {"status", "201"}}, Value: 42},
		{Name: "cashp_http_active_requests", Help: "Number of active HTTP requests", Type: TypeGauge, Value: 3},
	}

	var out strings.Builder
	if err := WriteText(&out, samples); err != nil {
		t.Fatalf("WriteText: %v", err)
	}

	want := "# HELP cashp_http_requests_total Total number of HTTP requests\n" +
		"# TYPE cashp_http_requests_total counter\n" +
		"cashp_http_requests_total{method=\"GET\",path=\"/api/v1/users\",status=\"200\"} 1523\n" +
		"cashp_http_requests_total{method=\"POST\",path=\"/api/v1/users\",status=\"201\"} 42\n" +
		"\n" +
		"# HELP cashp_http_active_requests Number of active HTTP requests\n" +
		"# TYPE cashp_http_active_requests gauge\n" +
		"cashp_http_active_requests 3\n"

	if out.String() != want {
		t.Fatalf("exposition mismatch:\ngot:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestWriteTextHistogram(t *testing.T) {
	r := New(Options{Namespace: "cashp"})

	h := r.Histogram("job_duration_seconds", []float64{0.1, 1}, "task", "cleanup")
	h.Observe(0.5)
	h.Observe(0.25)
	h.Observe(4)
	r.SetHelp("job_duration_seconds", "Job duration in seconds")

	var out strings.Builder
	if err := WriteText(&out, r.Collect()); err != nil {
		t.Fatalf("WriteText: %v", err)
	}

	want := []string{
		"# TYPE cashp_job_duration_seconds histogram",
		`cashp_job_duration_seconds_bucket{task="cleanup",le="0.1"} 0`,
		`cashp_job_duration_seconds_bucket{task="cleanup",le="1"} 2`,
		`cashp_job_duration_seconds_bucket{task="cleanup",le="+Inf"} 3`,
		`cashp_job_duration_seconds_sum{task="cleanup"} 4.75`,
		`cashp_job_duration_seconds_count{task="cleanup"} 3`,
	}

	for _, line := range want {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("missing %q in:\n%s", line, out.String())
		}
	}
}

func TestWriteTextEscapesLabelValues(t *testing.T) {
	samples := []Sample{{
		Name:   "cashp_app_info",
		Help:   "Application\ninformation",
		Type:   TypeGauge,
		Labels: []Label{{"version", `1.0 "beta"\x`}},
		Value:  1,
	}}

	var out strings.Builder
	if err := WriteText(&out, samples); err != nil {
		t.Fatalf("WriteText: %v", err)
	}

	if !strings.Contains(out.String(), `# HELP cashp_app_info Application\ninformation`) {
		t.Fatalf("help not escaped:\n%s", out.String())
	}

	if !strings.Contains(out.String(), `version="1.0 \"beta\"\\x"`) {
		t.Fatalf("label value not escaped:\n%s", out.String())
	}
}

func TestFormatFloatSpecialValues(t *testing.T) {
	cases := []struct {
		value float64
		want  string
	}{
		{value: math.Inf(1), want: "+Inf"},
		{value: math.Inf(-1), want: "-Inf"},
		{value: math.NaN(), want: "NaN"},
		{value: 1705312200, want: "1.7053122e+09"},
		{value: 0.5, want: "0.5"},
	}

	for _, tc := range cases {
		if got := formatFloat(tc.value); got != tc.want {
			t.Fatalf("formatFloat(%v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
