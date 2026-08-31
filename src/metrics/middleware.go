package metrics

import (
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// Dynamic path segments are collapsed to :id so the path label stays low
// cardinality, as the PART 21 cardinality warning requires.
var (
	uuidPattern      = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	numericIDPattern = regexp.MustCompile(`/\d+(?:/|$)`)
)

// NormalizePath replaces the dynamic segments of a request path with :id so
// that user ids, record ids, and UUIDs never become label values.
func NormalizePath(path string) string {
	path = uuidPattern.ReplaceAllString(path, ":id")

	for {
		replaced := numericIDPattern.ReplaceAllStringFunc(path, func(match string) string {
			if match[len(match)-1] == '/' {
				return "/:id/"
			}

			return "/:id"
		})

		if replaced == path {
			return path
		}

		path = replaced
	}
}

// Middleware records the required HTTP metrics for every request it wraps:
// request count, duration, request and response size, and the in-flight
// gauge.
func (r *Registry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()

		active := r.Gauge(MetricHTTPActiveRequests)
		active.Inc()
		defer active.Dec()

		path := NormalizePath(req.URL.Path)

		if req.ContentLength > 0 {
			r.Histogram(MetricHTTPRequestSizeBytes, SizeBuckets, "method", req.Method, "path", path).Observe(float64(req.ContentLength))
		}

		recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, req)

		status := strconv.Itoa(recorder.status)

		r.Counter(MetricHTTPRequestsTotal, "method", req.Method, "path", path, "status", status).Inc()
		r.Histogram(MetricHTTPRequestDuration, DurationBuckets, "method", req.Method, "path", path).ObserveDuration(time.Since(start))
		r.Histogram(MetricHTTPResponseSizeBytes, SizeBuckets, "method", req.Method, "path", path).Observe(float64(recorder.size))
	})
}

// responseRecorder captures the status code and byte count of a response.
type responseRecorder struct {
	http.ResponseWriter

	status  int
	size    int
	written bool
}

// WriteHeader records the first status code written.
func (w *responseRecorder) WriteHeader(status int) {
	if !w.written {
		w.status = status
		w.written = true
	}

	w.ResponseWriter.WriteHeader(status)
}

// Write records the number of body bytes written.
func (w *responseRecorder) Write(b []byte) (int, error) {
	w.written = true

	n, err := w.ResponseWriter.Write(b)
	w.size += n

	return n, err
}

// Unwrap exposes the wrapped writer to http.ResponseController so flushing
// and hijacking keep working through the middleware.
func (w *responseRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
