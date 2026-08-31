// Package agentlog builds the agent's logger. The agent writes agent.log
// in the shared log directory, alongside — never on top of — the server's
// own log files, so both binaries can run on the same host.
package agentlog

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/webappsgo/cashp/src/agent/paths"
)

// LogFilePerm keeps the agent log readable only by its owner: task output
// and node identifiers are not for every local account to read.
const LogFilePerm = 0o600

// LogDirPerm matches the permission gate the rest of the agent enforces.
const LogDirPerm = 0o700

// Options configures the agent logger.
type Options struct {
	// Dir is the directory agent.log is written to. When empty the logger
	// is console-only, which is what --status and one-shot commands use.
	Dir string
	// Level is the configured level name: debug, info, warn or error.
	Level string
	// Debug forces the level down to debug regardless of Level.
	Debug bool
	// Console mirrors output to stderr, which is what a foreground run and
	// a systemd-supervised run both want.
	Console bool
}

// New builds the logger and returns a closer for the log file. The closer
// is never nil, so callers can defer it unconditionally.
func New(opts Options) (*slog.Logger, io.Closer, error) {
	level := ParseLevel(opts.Level)
	if opts.Debug {
		level = slog.LevelDebug
	}

	writers := []io.Writer{}
	closer := io.Closer(noopCloser{})

	if strings.TrimSpace(opts.Dir) != "" {
		if err := os.MkdirAll(opts.Dir, LogDirPerm); err != nil {
			return nil, closer, fmt.Errorf("create %s: %w", opts.Dir, err)
		}

		path := filepath.Join(opts.Dir, paths.LogFileName)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, LogFilePerm)
		if err != nil {
			return nil, closer, fmt.Errorf("open %s: %w", path, err)
		}
		writers = append(writers, file)
		closer = file
	}

	if opts.Console || len(writers) == 0 {
		writers = append(writers, os.Stderr)
	}

	handler := slog.NewTextHandler(io.MultiWriter(writers...), &slog.HandlerOptions{Level: level})
	return slog.New(handler), closer, nil
}

// ParseLevel maps a configured level name onto a slog level, defaulting to
// info for anything unrecognised.
func ParseLevel(name string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// noopCloser stands in when there is no log file to close.
type noopCloser struct{}

// Close does nothing and always succeeds.
func (noopCloser) Close() error { return nil }
