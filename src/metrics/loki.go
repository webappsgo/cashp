package metrics

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Loki service defaults matching server.metrics.loki in server.yml.
const (
	DefaultLokiMaxEntries = 1000
	DefaultLokiMaxAge     = time.Hour
)

// redactedValue is the placeholder every credential is replaced with, the
// same masking the rest of the project uses.
const redactedValue = "xxxxx"

// credentialPattern matches "key: value" and "key=value" for every
// credential-bearing key name, so a log line can never carry a secret into
// the Loki stream. An optional "Bearer " prefix is consumed with the value.
var credentialPattern = regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(?:password|passwd|token|secret|api[_-]?key|apikey|authorization|credentials?|private[_-]?key|session[_-]?id))("?\s*[:=]\s*)("?)((?:bearer\s+)?[^\s",}]+)`)

// bearerPattern matches a bare "Bearer <token>" that no key name precedes.
var bearerPattern = regexp.MustCompile(`(?i)\bbearer\s+([^\s",}]+)`)

// logEntry is one buffered structured log line.
type logEntry struct {
	at     time.Time
	line   string
	labels []Label
}

// logBuffer is the bounded ring of recent log entries the loki service
// serves. It is bounded by both entry count and entry age.
type logBuffer struct {
	maxEntries int
	maxAge     time.Duration

	mu      sync.Mutex
	entries []logEntry
}

// newLogBuffer returns a buffer using the configured bounds, falling back to
// the server.yml defaults when they are unset.
func newLogBuffer(maxEntries int, maxAge time.Duration) *logBuffer {
	if maxEntries <= 0 {
		maxEntries = DefaultLokiMaxEntries
	}
	if maxAge <= 0 {
		maxAge = DefaultLokiMaxAge
	}

	return &logBuffer{maxEntries: maxEntries, maxAge: maxAge}
}

// append records one entry, dropping the oldest once the buffer is full.
func (b *logBuffer) append(e logEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.entries = append(b.entries, e)

	if overflow := len(b.entries) - b.maxEntries; overflow > 0 {
		b.entries = append(b.entries[:0], b.entries[overflow:]...)
	}
}

// snapshot returns the entries still inside the age bound, oldest first, and
// drops the expired ones.
func (b *logBuffer) snapshot(now time.Time) []logEntry {
	b.mu.Lock()
	defer b.mu.Unlock()

	cutoff := now.Add(-b.maxAge)

	kept := b.entries[:0]
	for _, e := range b.entries {
		if e.at.Before(cutoff) {
			continue
		}
		kept = append(kept, e)
	}
	b.entries = kept

	out := make([]logEntry, len(b.entries))
	copy(out, b.entries)

	return out
}

// Log records one structured log line for the loki service. labels is a flat
// name/value sequence and must stay low cardinality. The message is redacted
// before it is stored, so credentials never reach the buffer at all.
func (r *Registry) Log(level, message string, labels ...string) {
	parsed := parseLabels(labels)
	if level != "" {
		parsed = append(parsed, Label{Name: "level", Value: level})
		sort.Slice(parsed, func(i, j int) bool { return parsed[i].Name < parsed[j].Name })
	}

	r.logs.append(logEntry{at: time.Now(), line: Redact(message), labels: parsed})
}

// LogCount returns how many entries the loki service currently holds.
func (r *Registry) LogCount() int {
	return len(r.logs.snapshot(time.Now()))
}

// Redact masks every credential value in s, preserving the key name and
// replacing the value with xxxxx.
func Redact(s string) string {
	s = bearerPattern.ReplaceAllString(s, "Bearer "+redactedValue)

	return credentialPattern.ReplaceAllString(s, "${1}${2}${3}"+redactedValue)
}

// lokiStream is one Loki push-API stream: a label set and its lines.
type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"`
}

// lokiPayload is the Loki push-API document served by the loki service.
type lokiPayload struct {
	Streams []lokiStream `json:"streams"`
}

// serveLoki writes the recent structured log entries as Loki streams.
func (r *Registry) serveLoki(w http.ResponseWriter, _ *http.Request) {
	entries := r.logs.snapshot(time.Now())

	index := make(map[string]int)
	payload := lokiPayload{Streams: []lokiStream{}}

	for _, e := range entries {
		key := labelKey(e.labels)

		at, ok := index[key]
		if !ok {
			stream := make(map[string]string, len(e.labels))
			for _, l := range e.labels {
				stream[l.Name] = l.Value
			}

			payload.Streams = append(payload.Streams, lokiStream{Stream: stream, Values: [][2]string{}})
			at = len(payload.Streams) - 1
			index[key] = at
		}

		timestamp := strconv.FormatInt(e.at.UnixNano(), 10)
		payload.Streams[at].Values = append(payload.Streams[at].Values, [2]string{timestamp, e.line})
	}

	writeJSON(w, payload)
}

// writeJSON writes v as JSON with the project's two-space indent and single
// trailing newline.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	// A write error means the client hung up mid-response; there is no
	// recovery beyond stopping.
	_ = encoder.Encode(v)
}
