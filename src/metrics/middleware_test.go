package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizePath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "/api/v1/users", want: "/api/v1/users"},
		{in: "/api/v1/users/42", want: "/api/v1/users/:id"},
		{in: "/api/v1/users/42/tokens/7", want: "/api/v1/users/:id/tokens/:id"},
		{in: "/api/v1/users/3f0c1b2a-4d5e-6f70-8192-a3b4c5d6e7f8", want: "/api/v1/users/:id"},
		{in: "/server/healthz", want: "/server/healthz"},
	}

	for _, tc := range cases {
		if got := NormalizePath(tc.in); got != tc.want {
			t.Fatalf("NormalizePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMiddlewareRecordsHTTPMetrics(t *testing.T) {
	r := New(Options{Namespace: "cashp"})

	handler := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		if _, err := w.Write([]byte("hello")); err != nil {
			t.Errorf("write: %v", err)
		}
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/42", strings.NewReader("body"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}

	count := r.Counter(MetricHTTPRequestsTotal, "method", "POST", "path", "/api/v1/users/:id", "status", "201")
	if count.Value() != 1 {
		t.Fatalf("requests_total = %v, want 1", count.Value())
	}

	duration := r.Histogram(MetricHTTPRequestDuration, DurationBuckets, "method", "POST", "path", "/api/v1/users/:id")
	if duration.Count() != 1 {
		t.Fatalf("duration observations = %d, want 1", duration.Count())
	}

	requestSize := r.Histogram(MetricHTTPRequestSizeBytes, SizeBuckets, "method", "POST", "path", "/api/v1/users/:id")
	if requestSize.Sum() != 4 {
		t.Fatalf("request size sum = %v, want 4", requestSize.Sum())
	}

	responseSize := r.Histogram(MetricHTTPResponseSizeBytes, SizeBuckets, "method", "POST", "path", "/api/v1/users/:id")
	if responseSize.Sum() != 5 {
		t.Fatalf("response size sum = %v, want 5", responseSize.Sum())
	}

	if active := r.Gauge(MetricHTTPActiveRequests).Value(); active != 0 {
		t.Fatalf("active requests = %v, want 0 after completion", active)
	}
}
