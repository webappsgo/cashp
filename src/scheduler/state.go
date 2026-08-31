package scheduler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Task execution statuses recorded in persistent state, matching the
// last_status values described in AI.md PART 19 § Scheduler State.
const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

// stateFileName is the persistent scheduler state file written under
// Options.StateDir. It mirrors the scheduler table of server.db so state
// survives a restart even before the database package is wired in.
const stateFileName = "scheduler.json"

// TaskState is the persisted record for one task. Field names match the
// scheduler state columns in AI.md PART 19 § Scheduler State (Persistent).
type TaskState struct {
	TaskID     string    `json:"task_id"`
	TaskName   string    `json:"task_name"`
	Schedule   string    `json:"schedule"`
	LastRun    time.Time `json:"last_run"`
	LastStatus string    `json:"last_status"`
	LastError  string    `json:"last_error"`
	NextRun    time.Time `json:"next_run"`
	RunCount   int64     `json:"run_count"`
	FailCount  int64     `json:"fail_count"`
	Enabled    bool      `json:"enabled"`
	LockedBy   string    `json:"locked_by"`
	LockedAt   time.Time `json:"locked_at"`
}

// clone returns a copy of the state so callers never observe a record that
// is being mutated by the scheduler loop.
func (t *TaskState) clone() TaskState {
	return *t
}

// state is the on-disk document holding every task record.
type state struct {
	Updated time.Time             `json:"updated"`
	Tasks   map[string]*TaskState `json:"tasks"`
}

// newState returns an empty state document.
func newState() *state {
	return &state{Tasks: make(map[string]*TaskState)}
}

// statePath returns the state file path inside the given state directory.
func statePath(dir string) string {
	return filepath.Join(dir, stateFileName)
}

// loadState reads persistent scheduler state. A missing file yields an
// empty state, which is the normal first-run condition. A corrupt file is
// reported so the caller can log it and continue from empty state rather
// than losing the scheduler entirely.
func loadState(path string) (*state, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newState(), nil
		}
		return newState(), fmt.Errorf("scheduler: read state %s: %w", path, err)
	}
	s := newState()
	if err := json.Unmarshal(data, s); err != nil {
		return newState(), fmt.Errorf("scheduler: parse state %s: %w", path, err)
	}
	if s.Tasks == nil {
		s.Tasks = make(map[string]*TaskState)
	}
	return s, nil
}

// saveState writes scheduler state atomically: the document is written to a
// temporary file in the same directory and renamed over the target so a
// crash mid-write can never truncate existing state.
func saveState(path string, s *state) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("scheduler: create state dir %s: %w", dir, err)
	}
	s.Updated = time.Now().UTC()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("scheduler: encode state: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, stateFileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("scheduler: create temp state: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("scheduler: write temp state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("scheduler: sync temp state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("scheduler: close temp state: %w", err)
	}
	if err := os.Chmod(tmpName, 0o640); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("scheduler: chmod temp state: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("scheduler: replace state %s: %w", path, err)
	}
	return nil
}

// names returns the task identifiers held in state, sorted for stable
// iteration.
func (s *state) names() []string {
	out := make([]string, 0, len(s.Tasks))
	for name := range s.Tasks {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
