package logging

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// rotateClock is a manually advanced clock for rotation tests.
type rotateClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now returns the current fake time.
func (c *rotateClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the fake clock forward.
func (c *rotateClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newTestWriter builds a rotatingWriter driven by a fake clock.
func newTestWriter(t *testing.T, interval rotateInterval, maxBytes int64, keep int) (*rotatingWriter, *rotateClock, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.log")
	clock := &rotateClock{now: time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)}

	w, err := newRotatingWriter(path, interval, maxBytes, keep)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	t.Cleanup(func() { w.Close() })

	w.nowFunc = clock.Now
	w.period = w.periodKey(clock.Now())

	return w, clock, path
}

func TestRotatingWriterAppends(t *testing.T) {
	w, _, path := newTestWriter(t, rotateNever, 0, 0)

	if _, err := w.Write([]byte("first\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := w.Write([]byte("second\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if string(data) != "first\nsecond\n" {
		t.Fatalf("log = %q, want both lines appended", data)
	}
}

func TestRotatingWriterDailyRotationKeepNone(t *testing.T) {
	w, clock, path := newTestWriter(t, rotateDaily, 0, 0)

	if _, err := w.Write([]byte("day one\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	clock.Advance(24 * time.Hour)

	if _, err := w.Write([]byte("day two\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if string(data) != "day two\n" {
		t.Fatalf("log = %q, want only the current day's line", data)
	}

	rotated, err := filepath.Glob(path + ".*")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(rotated) != 0 {
		t.Fatalf("keep none must delete rotated files, found %v", rotated)
	}
}

func TestRotatingWriterKeepsRetainedRotations(t *testing.T) {
	w, clock, path := newTestWriter(t, rotateDaily, 0, 1)

	for i := 0; i < 3; i++ {
		if _, err := w.Write([]byte("entry\n")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		clock.Advance(24 * time.Hour)
	}

	if _, err := w.Write([]byte("final\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	rotated, err := filepath.Glob(path + ".*")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(rotated) != 1 {
		t.Fatalf("keep 1 must retain exactly one rotated file, found %v", rotated)
	}
}

func TestRotatingWriterSizeRotation(t *testing.T) {
	w, _, path := newTestWriter(t, rotateNever, 10, 0)

	if _, err := w.Write([]byte("12345678\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := w.Write([]byte("overflow\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if string(data) != "overflow\n" {
		t.Fatalf("log = %q, want only the post-rotation line", data)
	}
}

func TestRotatingWriterNoRotationWithinPeriod(t *testing.T) {
	w, clock, path := newTestWriter(t, rotateDaily, 0, 0)

	if _, err := w.Write([]byte("morning\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	clock.Advance(6 * time.Hour)

	if _, err := w.Write([]byte("evening\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if string(data) != "morning\nevening\n" {
		t.Fatalf("log = %q, want no rotation inside the same day", data)
	}
}

func TestRotatingWriterWeeklyPeriodKey(t *testing.T) {
	w, _, _ := newTestWriter(t, rotateWeekly, 0, 0)

	monday := time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
	sunday := time.Date(2026, 3, 15, 23, 59, 59, 0, time.UTC)
	nextMonday := time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)

	if w.periodKey(monday) != w.periodKey(sunday) {
		t.Fatal("the same ISO week must map to one period")
	}
	if w.periodKey(monday) == w.periodKey(nextMonday) {
		t.Fatal("a new ISO week must map to a new period")
	}
}

func TestRotatingWriterCloseIsIdempotent(t *testing.T) {
	w, _, _ := newTestWriter(t, rotateNever, 0, 0)

	if err := w.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := w.Write([]byte("after close\n")); err == nil {
		t.Fatal("writing to a closed writer must fail")
	}
}

func TestRotatingWriterFilePermissions(t *testing.T) {
	_, _, path := newTestWriter(t, rotateNever, 0, 0)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat log: %v", err)
	}
	// The umask may narrow the mode further, but a log file must never be
	// readable or writable by other users.
	if perm := info.Mode().Perm(); perm&0o007 != 0 {
		t.Fatalf("log permissions = %v, want no access for other users", perm)
	}
}
