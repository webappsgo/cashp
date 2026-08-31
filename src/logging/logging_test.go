package logging

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initForTest initializes logging into a temporary directory and restores
// the fallback state when the test ends.
func initForTest(t *testing.T, opts Options) string {
	t.Helper()

	dir := t.TempDir()
	opts.Dir = dir

	if err := Init(opts); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() {
		if err := Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})

	return dir
}

// readLog returns the contents of a log file in dir.
func readLog(t *testing.T, dir, name string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}

	return string(data)
}

func TestInitRequiresDir(t *testing.T) {
	if err := Init(Options{}); err == nil {
		t.Fatal("Init must reject an empty directory")
	}
}

func TestInitCreatesLogFiles(t *testing.T) {
	dir := initForTest(t, Options{Level: slog.LevelInfo, JSON: true})

	for _, name := range []string{ServerLogName, ErrorLogName, AuditLogName} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s to exist: %v", name, err)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, DebugLogName)); !os.IsNotExist(err) {
		t.Fatal("debug.log must not be created unless Debug is set")
	}
}

func TestInitDebugCreatesDebugLog(t *testing.T) {
	dir := initForTest(t, Options{Level: slog.LevelWarn, Debug: true, JSON: true})

	L().Debug("verbose detail")

	content := readLog(t, dir, DebugLogName)
	if !strings.Contains(content, "verbose detail") {
		t.Fatalf("debug.log missing the debug record: %q", content)
	}
}

func TestErrorLogOnlyReceivesErrors(t *testing.T) {
	dir := initForTest(t, Options{Level: slog.LevelInfo, JSON: true})

	L().Info("routine event")
	L().Error("failure event")

	server := readLog(t, dir, ServerLogName)
	if !strings.Contains(server, "routine event") || !strings.Contains(server, "failure event") {
		t.Fatalf("server.log must carry both records: %q", server)
	}

	errorLog := readLog(t, dir, ErrorLogName)
	if strings.Contains(errorLog, "routine event") {
		t.Fatalf("error.log must not carry info records: %q", errorLog)
	}
	if !strings.Contains(errorLog, "failure event") {
		t.Fatalf("error.log missing the error record: %q", errorLog)
	}
}

func TestLevelFiltering(t *testing.T) {
	dir := initForTest(t, Options{Level: slog.LevelWarn, JSON: true})

	L().Info("below threshold")
	L().Warn("at threshold")

	server := readLog(t, dir, ServerLogName)
	if strings.Contains(server, "below threshold") {
		t.Fatalf("records below the configured level must be dropped: %q", server)
	}
	if !strings.Contains(server, "at threshold") {
		t.Fatalf("server.log missing the warn record: %q", server)
	}
}

func TestAuditLogIsJSONLines(t *testing.T) {
	dir := initForTest(t, Options{Level: slog.LevelInfo, JSON: false})

	Audit().Info("admin.login",
		slog.String("category", "authentication"),
		slog.String("result", "success"),
		slog.String("actor_ip", "192.0.2.10"),
	)

	content := strings.TrimSpace(readLog(t, dir, AuditLogName))
	if content == "" {
		t.Fatal("audit.log is empty")
	}

	for _, line := range strings.Split(content, "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("audit line is not valid JSON: %q: %v", line, err)
		}
		if entry["msg"] != "admin.login" {
			t.Fatalf("msg = %v, want admin.login", entry["msg"])
		}
		if entry["result"] != "success" {
			t.Fatalf("result = %v, want success", entry["result"])
		}
	}
}

func TestAuditLogStaysJSONEvenWhenAppLogIsText(t *testing.T) {
	dir := initForTest(t, Options{Level: slog.LevelInfo, JSON: false})

	L().Info("text formatted")
	Audit().Info("audit.event")

	server := readLog(t, dir, ServerLogName)
	if strings.HasPrefix(strings.TrimSpace(server), "{") {
		t.Fatalf("server.log should be text formatted: %q", server)
	}

	audit := strings.TrimSpace(readLog(t, dir, AuditLogName))
	if !strings.HasPrefix(audit, "{") {
		t.Fatalf("audit.log must always be JSON: %q", audit)
	}
}

func TestRequestLogAttrs(t *testing.T) {
	attrs := RequestLogAttrs("req_123", "DB_TIMEOUT", 500)
	if len(attrs) != 3 {
		t.Fatalf("got %d attrs, want 3", len(attrs))
	}

	byKey := map[string]slog.Value{}
	for _, a := range attrs {
		byKey[a.Key] = a.Value
	}

	if byKey["request_id"].String() != "req_123" {
		t.Fatalf("request_id = %v", byKey["request_id"])
	}
	if byKey["error_code"].String() != "DB_TIMEOUT" {
		t.Fatalf("error_code = %v", byKey["error_code"])
	}
	if byKey["http_status"].Int64() != 500 {
		t.Fatalf("http_status = %v", byKey["http_status"])
	}

	if got := len(RequestLogAttrs("req_123", "", 0)); got != 1 {
		t.Fatalf("got %d attrs for a bare request log, want 1", got)
	}
}

func TestLevelForStatus(t *testing.T) {
	tests := []struct {
		status int
		want   slog.Level
	}{
		{200, slog.LevelInfo},
		{301, slog.LevelInfo},
		{400, slog.LevelWarn},
		{404, slog.LevelWarn},
		{429, slog.LevelWarn},
		{499, slog.LevelWarn},
		{500, slog.LevelError},
		{503, slog.LevelError},
	}

	for _, tc := range tests {
		if got := LevelForStatus(tc.status); got != tc.want {
			t.Fatalf("LevelForStatus(%d) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestLogRequestErrorCarriesRequiredAttrs(t *testing.T) {
	dir := initForTest(t, Options{Level: slog.LevelInfo, JSON: true})

	LogRequestError(context.Background(), "database unavailable", "req_abc", "DB_TIMEOUT", 503)

	content := readLog(t, dir, ErrorLogName)
	for _, want := range []string{`"request_id":"req_abc"`, `"error_code":"DB_TIMEOUT"`, `"http_status":503`, `"level":"ERROR"`} {
		if !strings.Contains(content, want) {
			t.Fatalf("error.log missing %s: %q", want, content)
		}
	}

	LogRequestError(context.Background(), "bad request", "req_def", "VALIDATION", 400)

	server := readLog(t, dir, ServerLogName)
	if !strings.Contains(server, `"level":"WARN"`) {
		t.Fatalf("a 4xx must log at WARN: %q", server)
	}
}

func TestLBeforeInitDoesNotPanic(t *testing.T) {
	if err := Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if L() == nil {
		t.Fatal("L() must never return nil")
	}
	if Audit() == nil {
		t.Fatal("Audit() must never return nil")
	}

	L().Info("pre-init message goes to stderr")
	Audit().Info("pre-init audit event is discarded")
}

func TestMaskSecretReExport(t *testing.T) {
	if got := MaskSecret("DB_PASSWORD=hunter2"); got != "DB_PASSWORD=xxxxx" {
		t.Fatalf("MaskSecret = %q", got)
	}
}
