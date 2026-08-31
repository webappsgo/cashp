package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Rotation intervals supported by rotatingWriter, matching the rotation
// options in AI.md PART 11.
type rotateInterval int

const (
	// rotateNever disables time-based rotation; only maxBytes applies.
	rotateNever rotateInterval = iota
	// rotateDaily rotates at the start of each UTC day.
	rotateDaily
	// rotateWeekly rotates at the start of each ISO week.
	rotateWeekly
)

// logFileMode is the permission mask for log files. Logs may contain
// operational detail, so they are never world-readable.
const logFileMode os.FileMode = 0o640

// logDirMode is the permission mask for the log directory.
const logDirMode os.FileMode = 0o750

// rotatingWriter is an io.WriteCloser that rotates a log file on a time
// interval, a size threshold, or whichever comes first. Rotated files are
// deleted immediately when keep is zero, which is the project default
// (keep: none).
type rotatingWriter struct {
	mu       sync.Mutex
	path     string
	interval rotateInterval
	maxBytes int64
	keep     int
	file     *os.File
	size     int64
	period   string
	nowFunc  func() time.Time
}

// newRotatingWriter opens path for appending, creating the parent
// directory if needed. maxBytes of zero disables size-based rotation.
func newRotatingWriter(path string, interval rotateInterval, maxBytes int64, keep int) (*rotatingWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), logDirMode); err != nil {
		return nil, fmt.Errorf("logging: create log directory: %w", err)
	}

	w := &rotatingWriter{
		path:     path,
		interval: interval,
		maxBytes: maxBytes,
		keep:     keep,
		nowFunc:  time.Now,
	}

	if err := w.open(); err != nil {
		return nil, err
	}

	return w, nil
}

// Write appends p to the log, rotating first when the current period has
// elapsed or the size threshold would be exceeded.
func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return 0, os.ErrClosed
	}

	if w.shouldRotate(int64(len(p))) {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)

	return n, err
}

// Close flushes and closes the underlying file.
func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}

	err := w.file.Close()
	w.file = nil

	return err
}

// shouldRotate reports whether the next write of n bytes must be preceded
// by a rotation.
func (w *rotatingWriter) shouldRotate(n int64) bool {
	if w.interval != rotateNever && w.periodKey(w.nowFunc()) != w.period {
		return true
	}
	return w.maxBytes > 0 && w.size+n > w.maxBytes
}

// rotate closes the current file, moves it aside under a timestamped name,
// prunes old rotations, and opens a fresh file.
func (w *rotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("logging: close log for rotation: %w", err)
	}
	w.file = nil

	stamp := w.nowFunc().UTC().Format("20060102-150405")
	rotated := w.path + "." + stamp
	if err := os.Rename(w.path, rotated); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("logging: rotate log: %w", err)
	}

	if err := w.prune(); err != nil {
		return err
	}

	return w.open()
}

// prune enforces the retention policy. With keep at zero the just-rotated
// file is removed immediately, matching the default "keep: none".
func (w *rotatingWriter) prune() error {
	matches, err := filepath.Glob(w.path + ".*")
	if err != nil {
		return fmt.Errorf("logging: list rotated logs: %w", err)
	}
	if len(matches) <= w.keep {
		return nil
	}

	sort.Strings(matches)
	for _, old := range matches[:len(matches)-w.keep] {
		if err := os.Remove(old); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("logging: remove rotated log: %w", err)
		}
	}

	return nil
}

// open creates or reopens the active log file and records its current size
// and rotation period.
func (w *rotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logFileMode)
	if err != nil {
		return fmt.Errorf("logging: open log file: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("logging: stat log file: %w", err)
	}

	w.file = f
	w.size = info.Size()
	w.period = w.periodKey(w.nowFunc())

	return nil
}

// periodKey renders the rotation period t falls into. Two writes with the
// same key belong to the same log file.
func (w *rotatingWriter) periodKey(t time.Time) string {
	switch w.interval {
	case rotateDaily:
		return t.UTC().Format("2006-01-02")
	case rotateWeekly:
		year, week := t.UTC().ISOWeek()
		return fmt.Sprintf("%04d-W%02d", year, week)
	default:
		return ""
	}
}
