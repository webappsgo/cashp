// Package logging provides cashp's structured logging per AI.md PART 11:
// a level-controlled application logger writing to server.log, error.log,
// and optionally the console, plus a JSON-only append-only audit logger
// with daily rotation. Every handler runs the redaction hook, so secrets
// cannot reach a log file even when a caller passes one by mistake.
package logging

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/webappsgo/cashp/src/security"
)

// Log file names from the PART 11 log-file table.
const (
	// ServerLogName holds general application events.
	ServerLogName = "server.log"
	// ErrorLogName holds error-level events only.
	ErrorLogName = "error.log"
	// AuditLogName holds security-relevant events in JSON Lines format.
	AuditLogName = "audit.log"
	// DebugLogName holds verbose troubleshooting output, written only when Debug is set.
	DebugLogName = "debug.log"
)

// defaultMaxBytes is the 50MB size threshold that pairs with weekly
// rotation for every log except audit.log.
const defaultMaxBytes int64 = 50 * 1024 * 1024

// ErrNoDir is returned when Init is called without a log directory.
var ErrNoDir = errors.New("logging: Dir must not be empty")

// Options configures Init.
type Options struct {
	// Dir is the directory log files are written to.
	Dir string
	// Level is the minimum level recorded by the application logger.
	Level slog.Level
	// Debug additionally writes debug.log and forces the level down to debug.
	Debug bool
	// JSON selects JSON instead of plain text for the application log files.
	JSON bool
	// Console mirrors the application log to stderr.
	Console bool
}

// state holds the initialized loggers and the files they own.
type state struct {
	app     *slog.Logger
	audit   *slog.Logger
	closers []io.Closer
}

// current is the active logging state. Before Init it is a console-only
// fallback so early startup errors are never lost.
var (
	mu      sync.RWMutex
	current = fallbackState()
)

// Init opens the log files and installs the application and audit loggers.
// Calling it again closes the previously opened files and replaces them,
// which is how a config reload applies a new level or directory.
func Init(opts Options) error {
	if opts.Dir == "" {
		return ErrNoDir
	}

	if err := os.MkdirAll(opts.Dir, logDirMode); err != nil {
		return err
	}

	level := opts.Level
	if opts.Debug {
		level = slog.LevelDebug
	}

	next := &state{}

	serverWriter, err := newRotatingWriter(filepath.Join(opts.Dir, ServerLogName), rotateWeekly, defaultMaxBytes, 0)
	if err != nil {
		return err
	}
	next.closers = append(next.closers, serverWriter)

	errorWriter, err := newRotatingWriter(filepath.Join(opts.Dir, ErrorLogName), rotateWeekly, defaultMaxBytes, 0)
	if err != nil {
		next.closeAll()
		return err
	}
	next.closers = append(next.closers, errorWriter)

	auditWriter, err := newRotatingWriter(filepath.Join(opts.Dir, AuditLogName), rotateDaily, 0, 0)
	if err != nil {
		next.closeAll()
		return err
	}
	next.closers = append(next.closers, auditWriter)

	handlers := []slog.Handler{
		fileHandler(serverWriter, level, opts.JSON),
		fileHandler(errorWriter, slog.LevelError, opts.JSON),
	}

	if opts.Debug {
		debugWriter, err := newRotatingWriter(filepath.Join(opts.Dir, DebugLogName), rotateWeekly, defaultMaxBytes, 0)
		if err != nil {
			next.closeAll()
			return err
		}
		next.closers = append(next.closers, debugWriter)
		handlers = append(handlers, fileHandler(debugWriter, slog.LevelDebug, opts.JSON))
	}

	if opts.Console {
		handlers = append(handlers, fileHandler(os.Stderr, level, opts.JSON))
	}

	next.app = slog.New(newMultiHandler(handlers...))
	next.audit = slog.New(fileHandler(auditWriter, slog.LevelInfo, true))

	mu.Lock()
	previous := current
	current = next
	mu.Unlock()

	previous.closeAll()

	return nil
}

// L returns the application logger. It is safe to call before Init, in
// which case output goes to stderr.
func L() *slog.Logger {
	mu.RLock()
	defer mu.RUnlock()
	return current.app
}

// Audit returns the audit logger. Its output is JSON Lines only, appended
// to audit.log, and rotated daily with no retention by default.
func Audit() *slog.Logger {
	mu.RLock()
	defer mu.RUnlock()
	return current.audit
}

// Close closes every open log file and reverts to the console-only
// fallback logger. It is called on shutdown.
func Close() error {
	mu.Lock()
	previous := current
	current = fallbackState()
	mu.Unlock()

	return previous.closeAll()
}

// RequestLogAttrs builds the attribute set every error log line must
// carry. errorCode and httpStatus are omitted when unset, so the helper is
// equally usable for non-error request logging.
func RequestLogAttrs(requestID, errorCode string, httpStatus int) []slog.Attr {
	attrs := make([]slog.Attr, 0, 3)

	attrs = append(attrs, slog.String("request_id", requestID))
	if errorCode != "" {
		attrs = append(attrs, slog.String("error_code", errorCode))
	}
	if httpStatus != 0 {
		attrs = append(attrs, slog.Int("http_status", httpStatus))
	}

	return attrs
}

// LevelForStatus maps an HTTP status code to the level its log line must
// use: ERROR at 500 and above, WARN at 400 and above, INFO otherwise.
func LevelForStatus(httpStatus int) slog.Level {
	switch {
	case httpStatus >= 500:
		return slog.LevelError
	case httpStatus >= 400:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// LogRequestError records a request failure at the level implied by its
// status code, with the required request_id, error_code, and http_status
// attributes attached.
func LogRequestError(ctx context.Context, msg, requestID, errorCode string, httpStatus int) {
	L().LogAttrs(ctx, LevelForStatus(httpStatus), msg, RequestLogAttrs(requestID, errorCode, httpStatus)...)
}

// fileHandler builds a handler over w at the given level, JSON or text,
// with the redaction hook installed.
func fileHandler(w io.Writer, level slog.Level, asJSON bool) slog.Handler {
	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: redactAttr}
	if asJSON {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}

// fallbackState is the pre-Init logging state: application output to
// stderr, audit output discarded because no audit file exists yet.
func fallbackState() *state {
	return &state{
		app:   slog.New(fileHandler(os.Stderr, slog.LevelInfo, false)),
		audit: slog.New(fileHandler(io.Discard, slog.LevelInfo, true)),
	}
}

// closeAll closes every file the state owns, returning the first failure.
func (s *state) closeAll() error {
	var firstErr error
	for _, c := range s.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.closers = nil

	return firstErr
}

// multiHandler fans a record out to every underlying handler that accepts
// its level, letting one logger write server.log, error.log, debug.log,
// and the console at once.
type multiHandler struct {
	handlers []slog.Handler
}

// newMultiHandler wraps handlers into a single slog.Handler.
func newMultiHandler(handlers ...slog.Handler) slog.Handler {
	return &multiHandler{handlers: handlers}
}

// Enabled reports whether any underlying handler accepts the level.
func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle forwards the record to every handler that accepts its level.
func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	var firstErr error
	for _, h := range m.handlers {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// WithAttrs returns a multiHandler whose members all carry attrs.
func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		next[i] = h.WithAttrs(attrs)
	}

	return &multiHandler{handlers: next}
}

// WithGroup returns a multiHandler whose members all open the named group.
func (m *multiHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		next[i] = h.WithGroup(name)
	}

	return &multiHandler{handlers: next}
}

// MaskSecret re-exports the security package helper so log call sites can
// mask a value without importing both packages.
func MaskSecret(s string) string {
	return security.MaskSecret(s)
}
