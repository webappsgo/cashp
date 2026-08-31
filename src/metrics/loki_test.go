package metrics

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRedactCredentials(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "password=hunter2", want: "password=xxxxx"},
		{in: `{"token": "abc123"}`, want: `{"token": "xxxxx"}`},
		{in: "api_key: 12345", want: "api_key: xxxxx"},
		{in: "login ok for alice", want: "login ok for alice"},
		{in: "session_id=deadbeef done", want: "session_id=xxxxx done"},
		{in: "Authorization: Bearer abc.def.ghi", want: "Authorization: xxxxx"},
		{in: "used Bearer abc.def.ghi to fetch", want: "used Bearer xxxxx to fetch"},
		{in: "private_key=-----BEGIN", want: "private_key=xxxxx"},
		{in: "secret = s3cr3t and more text", want: "secret = xxxxx and more text"},
		{in: "credentials: 'your-prometheus-tok'", want: "credentials: xxxxx"},
		{in: "nothing sensitive here at all", want: "nothing sensitive here at all"},
		{in: "passwd=abc token=def", want: "passwd=xxxxx token=xxxxx"},
		{in: "GET /server/metrics 200 in 1.2ms", want: "GET /server/metrics 200 in 1.2ms"},
		{in: "apikey=zzz", want: "apikey=xxxxx"},
		{in: "user_password: 'p@ss word' rejected", want: "user_password: xxxxx word' rejected"},
	}

	for _, tc := range cases {
		if got := Redact(tc.in); got != tc.want {
			t.Fatalf("Redact(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLogBufferBoundedByEntries(t *testing.T) {
	r := New(Options{LokiMaxEntries: 3})

	for i := 0; i < 10; i++ {
		r.Log("info", "line "+strconv.Itoa(i))
	}

	if got := r.LogCount(); got != 3 {
		t.Fatalf("LogCount = %d, want 3", got)
	}

	entries := r.logs.snapshot(time.Now())
	if entries[0].line != "line 7" || entries[2].line != "line 9" {
		t.Fatalf("oldest entries not dropped: %q .. %q", entries[0].line, entries[2].line)
	}
}

func TestLogBufferBoundedByAge(t *testing.T) {
	buf := newLogBuffer(100, time.Minute)

	now := time.Now()
	buf.append(logEntry{at: now.Add(-2 * time.Minute), line: "old"})
	buf.append(logEntry{at: now, line: "fresh"})

	entries := buf.snapshot(now)
	if len(entries) != 1 || entries[0].line != "fresh" {
		t.Fatalf("expired entry retained: %+v", entries)
	}
}

func TestLogBufferDefaults(t *testing.T) {
	buf := newLogBuffer(0, 0)

	if buf.maxEntries != DefaultLokiMaxEntries || buf.maxAge != DefaultLokiMaxAge {
		t.Fatalf("defaults = %d/%v", buf.maxEntries, buf.maxAge)
	}
}

func TestLokiStreamsGroupedByLabels(t *testing.T) {
	r := New(Options{AllowUnauthenticated: true})
	r.Log("info", "started", "component", "server")
	r.Log("info", "listening", "component", "server")
	r.Log("error", "db token=secret1 failed", "component", "store")

	rec := get(r.LokiHandler(), "/server/metrics/loki", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var payload struct {
		Streams []struct {
			Stream map[string]string `json:"stream"`
			Values [][2]string       `json:"values"`
		} `json:"streams"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(payload.Streams) != 2 {
		t.Fatalf("streams = %d, want 2", len(payload.Streams))
	}

	if len(payload.Streams[0].Values) != 2 {
		t.Fatalf("first stream values = %d, want 2", len(payload.Streams[0].Values))
	}

	if payload.Streams[0].Stream["level"] != "info" || payload.Streams[0].Stream["component"] != "server" {
		t.Fatalf("stream labels = %v", payload.Streams[0].Stream)
	}

	if _, err := strconv.ParseInt(payload.Streams[0].Values[0][0], 10, 64); err != nil {
		t.Fatalf("timestamp is not nanoseconds: %v", err)
	}

	if strings.Contains(rec.Body.String(), "secret1") {
		t.Fatalf("credential reached the loki stream:\n%s", rec.Body.String())
	}

	if !strings.HasSuffix(rec.Body.String(), "}\n") {
		t.Fatal("json response must end with a single trailing newline")
	}
}
