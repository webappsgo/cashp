package paths

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// stubProcessChecks replaces the platform process checks for the duration
// of a test.
func stubProcessChecks(t *testing.T, running, ours bool) {
	t.Helper()

	originalRunning := processRunning
	originalOurs := processIsOurs
	originalContainer := inContainer

	processRunning = func(int) bool { return running }
	processIsOurs = func(int) bool { return ours }
	inContainer = func() bool { return false }

	t.Cleanup(func() {
		processRunning = originalRunning
		processIsOurs = originalOurs
		inContainer = originalContainer
	})
}

// writePID writes a PID file for a test.
func writePID(t *testing.T, path string, contents string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatalf("writing the pid file failed: %v", err)
	}
}

// TestCheckPIDFileMissing checks that no PID file means not running.
func TestCheckPIDFileMissing(t *testing.T) {
	stubProcessChecks(t, true, true)

	running, pid, err := CheckPIDFile(filepath.Join(t.TempDir(), "cashp.pid"))
	if err != nil {
		t.Fatalf("CheckPIDFile returned an error: %v", err)
	}
	if running || pid != 0 {
		t.Fatalf("a missing pid file must report not running, got running=%v pid=%d", running, pid)
	}
}

// TestCheckPIDFileRunning checks the live-instance case.
func TestCheckPIDFileRunning(t *testing.T) {
	stubProcessChecks(t, true, true)

	path := filepath.Join(t.TempDir(), "cashp.pid")
	writePID(t, path, "4242\n")

	running, pid, err := CheckPIDFile(path)
	if err != nil {
		t.Fatalf("CheckPIDFile returned an error: %v", err)
	}
	if !running || pid != 4242 {
		t.Fatalf("expected a live instance with pid 4242, got running=%v pid=%d", running, pid)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("a live pid file must not be removed")
	}
}

// TestCheckPIDFileStale checks that a PID with no live process is treated
// as stale and the file is removed.
func TestCheckPIDFileStale(t *testing.T) {
	stubProcessChecks(t, false, false)

	path := filepath.Join(t.TempDir(), "cashp.pid")
	writePID(t, path, "4242")

	running, _, err := CheckPIDFile(path)
	if err != nil {
		t.Fatalf("CheckPIDFile returned an error: %v", err)
	}
	if running {
		t.Fatal("a stale pid must report not running")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a stale pid file must be removed")
	}
}

// TestCheckPIDFileReusedPID checks that a live process belonging to another
// program is not mistaken for our server.
func TestCheckPIDFileReusedPID(t *testing.T) {
	stubProcessChecks(t, true, false)

	path := filepath.Join(t.TempDir(), "cashp.pid")
	writePID(t, path, "4242")

	running, _, err := CheckPIDFile(path)
	if err != nil {
		t.Fatalf("CheckPIDFile returned an error: %v", err)
	}
	if running {
		t.Fatal("a reused pid must report not running")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a reused pid file must be removed")
	}
}

// TestCheckPIDFileCorrupt checks that unreadable contents are discarded.
func TestCheckPIDFileCorrupt(t *testing.T) {
	stubProcessChecks(t, true, true)

	path := filepath.Join(t.TempDir(), "cashp.pid")
	writePID(t, path, "not-a-pid")

	running, _, err := CheckPIDFile(path)
	if err != nil {
		t.Fatalf("CheckPIDFile returned an error: %v", err)
	}
	if running {
		t.Fatal("a corrupt pid file must report not running")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a corrupt pid file must be removed")
	}
}

// TestWritePIDFile checks the happy path and the already-running refusal.
func TestWritePIDFile(t *testing.T) {
	stubProcessChecks(t, false, false)

	path := filepath.Join(t.TempDir(), "cashp.pid")
	if err := WritePIDFile(path); err != nil {
		t.Fatalf("WritePIDFile failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the pid file failed: %v", err)
	}
	if strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("the pid file holds %q, want %d", data, os.Getpid())
	}

	processRunning = func(int) bool { return true }
	processIsOurs = func(int) bool { return true }

	err = WritePIDFile(path)
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("a second instance must be refused, got %v", err)
	}
}

// TestRemovePIDFile checks removal and the tolerated missing file.
func TestRemovePIDFile(t *testing.T) {
	stubProcessChecks(t, false, false)

	path := filepath.Join(t.TempDir(), "cashp.pid")
	writePID(t, path, "4242")

	if err := RemovePIDFile(path); err != nil {
		t.Fatalf("RemovePIDFile failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the pid file was not removed")
	}
	if err := RemovePIDFile(path); err != nil {
		t.Errorf("removing a missing pid file must succeed, got %v", err)
	}
}

// TestContainerSkipsPIDFile checks the rule that a container never writes a
// PID file at all: the runtime supervises the process.
func TestContainerSkipsPIDFile(t *testing.T) {
	stubProcessChecks(t, true, true)
	inContainer = func() bool { return true }

	path := filepath.Join(t.TempDir(), "cashp.pid")

	if err := WritePIDFile(path); err != nil {
		t.Fatalf("WritePIDFile in a container failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a container must not create a pid file")
	}

	running, pid, err := CheckPIDFile(path)
	if err != nil || running || pid != 0 {
		t.Errorf("a container must never report a running pid, got %v %d %v", running, pid, err)
	}
	if err := EnsurePIDFile(filepath.Join(path, "nested", "cashp.pid"), true); err != nil {
		t.Errorf("EnsurePIDFile must be a no-op in a container, got %v", err)
	}
}

// TestEnsurePIDFileCreatesDirectory checks the native case.
func TestEnsurePIDFileCreatesDirectory(t *testing.T) {
	stubProcessChecks(t, false, false)

	path := filepath.Join(t.TempDir(), "run", "cashp.pid")
	if err := EnsurePIDFile(path, true); err != nil {
		t.Fatalf("EnsurePIDFile failed: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("the pid directory was not created: %v", err)
	}
}
