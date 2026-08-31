package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultLockTTL is the cluster task lock timeout from AI.md PART 19
// § Task Locking: a lock older than this is considered abandoned by a dead
// node and may be taken over.
const DefaultLockTTL = 5 * time.Minute

// Locker guards cluster-wide tasks so exactly one node executes them. The
// scheduler owns no storage of its own for this: the database package
// supplies a database-backed implementation (advisory lock) once it exists,
// and FileLocker is used until then and for single-node installs.
type Locker interface {
	// Acquire attempts to take the named task lock for nodeID. It reports
	// false without an error when another live node holds the lock.
	Acquire(ctx context.Context, task, nodeID string, ttl time.Duration) (bool, error)
	// Release drops the named task lock if nodeID still owns it. Releasing a
	// lock owned by another node is a no-op, not an error.
	Release(ctx context.Context, task, nodeID string) error
}

// lockRecord is the payload written by FileLocker.
type lockRecord struct {
	Task     string    `json:"task"`
	NodeID   string    `json:"node_id"`
	LockedAt time.Time `json:"locked_at"`
}

// FileLocker implements Locker with one lock file per task, created with
// O_EXCL so acquisition is atomic on a local filesystem and on any shared
// filesystem that honours exclusive create.
type FileLocker struct {
	dir string
}

// NewFileLocker returns a Locker storing lock files in dir.
func NewFileLocker(dir string) *FileLocker {
	return &FileLocker{dir: dir}
}

// lockPath returns the lock file path for a task, sanitising the task name
// so it can never escape the lock directory.
func (l *FileLocker) lockPath(task string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			return r
		default:
			return '_'
		}
	}, task)
	return filepath.Join(l.dir, safe+".lock")
}

// Acquire takes the task lock for nodeID, stealing a lock whose age exceeds
// ttl (the holder is presumed dead) and refreshing a lock this node already
// owns.
func (l *FileLocker) Acquire(ctx context.Context, task, nodeID string, ttl time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if ttl <= 0 {
		ttl = DefaultLockTTL
	}
	if err := os.MkdirAll(l.dir, 0o750); err != nil {
		return false, fmt.Errorf("scheduler: create lock dir %s: %w", l.dir, err)
	}
	path := l.lockPath(task)
	rec := lockRecord{Task: task, NodeID: nodeID, LockedAt: time.Now().UTC()}
	data, err := json.Marshal(rec)
	if err != nil {
		return false, fmt.Errorf("scheduler: encode lock: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err == nil {
		defer f.Close()
		if _, err := f.Write(data); err != nil {
			return false, fmt.Errorf("scheduler: write lock %s: %w", path, err)
		}
		return true, nil
	}
	if !os.IsExist(err) {
		return false, fmt.Errorf("scheduler: create lock %s: %w", path, err)
	}
	existing, err := l.read(path)
	if err != nil {
		return false, err
	}
	// A lock held by this node or older than the TTL is taken over; the
	// rewrite refreshes locked_at so a long task keeps its claim visible.
	if existing.NodeID != nodeID && time.Since(existing.LockedAt) < ttl {
		return false, nil
	}
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return false, fmt.Errorf("scheduler: refresh lock %s: %w", path, err)
	}
	return true, nil
}

// Release removes the task lock when nodeID still owns it.
func (l *FileLocker) Release(ctx context.Context, task, nodeID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := l.lockPath(task)
	existing, err := l.read(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if existing.NodeID != nodeID {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("scheduler: remove lock %s: %w", path, err)
	}
	return nil
}

// read loads a lock record from disk. An unreadable or malformed record is
// reported as an expired lock so a corrupt file cannot wedge a task
// forever.
func (l *FileLocker) read(path string) (lockRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return lockRecord{}, err
	}
	var rec lockRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return lockRecord{}, nil
	}
	return rec, nil
}

// noopLocker grants every lock. It is used when cluster mode is off, where
// the single node always owns every task.
type noopLocker struct{}

// Acquire always succeeds.
func (noopLocker) Acquire(ctx context.Context, task, nodeID string, ttl time.Duration) (bool, error) {
	return true, ctx.Err()
}

// Release always succeeds.
func (noopLocker) Release(ctx context.Context, task, nodeID string) error {
	return ctx.Err()
}
