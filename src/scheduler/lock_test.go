package scheduler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileLockerSingleHolder(t *testing.T) {
	l := NewFileLocker(filepath.Join(t.TempDir(), "locks"))
	ctx := context.Background()

	ok, err := l.Acquire(ctx, TaskBackupDaily, "node-a", time.Minute)
	if err != nil || !ok {
		t.Fatalf("node-a acquire = %t, %v; want true, nil", ok, err)
	}
	ok, err = l.Acquire(ctx, TaskBackupDaily, "node-b", time.Minute)
	if err != nil {
		t.Fatalf("node-b acquire error: %v", err)
	}
	if ok {
		t.Fatal("node-b must not acquire a lock held by node-a")
	}
	// The owner may re-acquire, which refreshes its claim.
	if ok, err := l.Acquire(ctx, TaskBackupDaily, "node-a", time.Minute); err != nil || !ok {
		t.Fatalf("owner re-acquire = %t, %v; want true, nil", ok, err)
	}
	if err := l.Release(ctx, TaskBackupDaily, "node-b"); err != nil {
		t.Fatalf("foreign release error: %v", err)
	}
	if ok, _ := l.Acquire(ctx, TaskBackupDaily, "node-b", time.Minute); ok {
		t.Fatal("foreign release must be a no-op")
	}
	if err := l.Release(ctx, TaskBackupDaily, "node-a"); err != nil {
		t.Fatalf("owner release error: %v", err)
	}
	if ok, err := l.Acquire(ctx, TaskBackupDaily, "node-b", time.Minute); err != nil || !ok {
		t.Fatalf("acquire after release = %t, %v; want true, nil", ok, err)
	}
}

func TestFileLockerStealsExpiredLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	l := NewFileLocker(dir)
	ctx := context.Background()

	if ok, err := l.Acquire(ctx, TaskCVEUpdate, "dead-node", time.Minute); err != nil || !ok {
		t.Fatalf("initial acquire = %t, %v", ok, err)
	}
	// Rewind the recorded time past the TTL, simulating a node that died
	// while holding the lock.
	path := l.lockPath(TaskCVEUpdate)
	rec := lockRecord{Task: TaskCVEUpdate, NodeID: "dead-node", LockedAt: time.Now().UTC().Add(-10 * time.Minute)}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	if err := os.WriteFile(path, data, 0o640); err != nil {
		t.Fatalf("rewrite lock: %v", err)
	}
	if ok, err := l.Acquire(ctx, TaskCVEUpdate, "live-node", 5*time.Minute); err != nil || !ok {
		t.Fatalf("expired lock takeover = %t, %v; want true, nil", ok, err)
	}
}

func TestFileLockerCorruptLockIsRecoverable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	l := NewFileLocker(dir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(l.lockPath(TaskGeoIPUpdate), []byte("garbage"), 0o640); err != nil {
		t.Fatalf("write garbage lock: %v", err)
	}
	ok, err := l.Acquire(context.Background(), TaskGeoIPUpdate, "node-a", time.Minute)
	if err != nil || !ok {
		t.Fatalf("acquire over corrupt lock = %t, %v; want true, nil", ok, err)
	}
}

func TestFileLockerSanitisesTaskName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	l := NewFileLocker(dir)
	if ok, err := l.Acquire(context.Background(), "../../escape me", "node-a", time.Minute); err != nil || !ok {
		t.Fatalf("acquire = %t, %v", ok, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one lock file, got %d", len(entries))
	}
	if entries[0].Name() != "______escape_me.lock" {
		t.Errorf("lock file name = %q", entries[0].Name())
	}
}

func TestFileLockerRespectsCancelledContext(t *testing.T) {
	l := NewFileLocker(filepath.Join(t.TempDir(), "locks"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Acquire(ctx, TaskSSLRenewal, "node-a", time.Minute); err == nil {
		t.Error("expected a context error from Acquire")
	}
	if err := l.Release(ctx, TaskSSLRenewal, "node-a"); err == nil {
		t.Error("expected a context error from Release")
	}
}

func TestNoopLockerAlwaysGrants(t *testing.T) {
	var l Locker = noopLocker{}
	ok, err := l.Acquire(context.Background(), TaskSSLRenewal, "node-a", time.Minute)
	if err != nil || !ok {
		t.Fatalf("noop acquire = %t, %v; want true, nil", ok, err)
	}
	if err := l.Release(context.Background(), TaskSSLRenewal, "node-a"); err != nil {
		t.Fatalf("noop release error: %v", err)
	}
}
