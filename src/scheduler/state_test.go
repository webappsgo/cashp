package scheduler

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := statePath(dir)
	now := time.Date(2026, time.March, 10, 2, 0, 0, 0, time.UTC)

	s := newState()
	s.Tasks[TaskBackupDaily] = &TaskState{
		TaskID:     TaskBackupDaily,
		TaskName:   "Backup Daily",
		Schedule:   "0 2 * * *",
		LastRun:    now,
		LastStatus: StatusSuccess,
		NextRun:    now.Add(24 * time.Hour),
		RunCount:   342,
		FailCount:  2,
		Enabled:    true,
		LockedBy:   "node-a",
		LockedAt:   now,
	}
	if err := saveState(path, s); err != nil {
		t.Fatalf("saveState error: %v", err)
	}

	loaded, err := loadState(path)
	if err != nil {
		t.Fatalf("loadState error: %v", err)
	}
	got, ok := loaded.Tasks[TaskBackupDaily]
	if !ok {
		t.Fatal("task missing after reload")
	}
	if got.RunCount != 342 || got.FailCount != 2 || got.LastStatus != StatusSuccess {
		t.Errorf("counters not preserved: %+v", got)
	}
	if !got.LastRun.Equal(now) || !got.NextRun.Equal(now.Add(24*time.Hour)) {
		t.Errorf("timestamps not preserved: %+v", got)
	}
	if got.LockedBy != "node-a" || !got.LockedAt.Equal(now) {
		t.Errorf("lock fields not preserved: %+v", got)
	}
	if names := loaded.names(); len(names) != 1 || names[0] != TaskBackupDaily {
		t.Errorf("names = %v", names)
	}
}

func TestLoadStateMissingFileIsEmpty(t *testing.T) {
	s, err := loadState(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("loadState error: %v", err)
	}
	if len(s.Tasks) != 0 {
		t.Errorf("expected empty state, got %d tasks", len(s.Tasks))
	}
}

func TestLoadStateCorruptFileReportsAndResets(t *testing.T) {
	dir := t.TempDir()
	path := statePath(dir)
	if err := os.WriteFile(path, []byte("{not json"), 0o640); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}
	s, err := loadState(path)
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if s == nil || len(s.Tasks) != 0 {
		t.Fatalf("expected empty state on corruption, got %+v", s)
	}
}

func TestSaveStateOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := statePath(dir)
	s := newState()
	s.Tasks["a"] = &TaskState{TaskID: "a", RunCount: 1, Enabled: true}
	if err := saveState(path, s); err != nil {
		t.Fatalf("first saveState error: %v", err)
	}
	s.Tasks["a"].RunCount = 9
	if err := saveState(path, s); err != nil {
		t.Fatalf("second saveState error: %v", err)
	}
	loaded, err := loadState(path)
	if err != nil {
		t.Fatalf("loadState error: %v", err)
	}
	if loaded.Tasks["a"].RunCount != 9 {
		t.Errorf("RunCount = %d, want 9", loaded.Tasks["a"].RunCount)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir error: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only the state file to remain, got %d entries", len(entries))
	}
}

func TestSaveStateCreatesDirectory(t *testing.T) {
	path := statePath(filepath.Join(t.TempDir(), "nested", "state"))
	if err := saveState(path, newState()); err != nil {
		t.Fatalf("saveState error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file missing: %v", err)
	}
}

func TestTaskStateClone(t *testing.T) {
	orig := &TaskState{TaskID: "x", RunCount: 3}
	c := orig.clone()
	c.RunCount = 99
	if orig.RunCount != 3 {
		t.Errorf("clone must not alias the original")
	}
}
