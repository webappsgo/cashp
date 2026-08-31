package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFrozenIdentifiers checks the identifiers every path is built from.
func TestFrozenIdentifiers(t *testing.T) {
	if Org != "webappsgo" || Name != "cashp" {
		t.Fatalf("frozen identifiers changed: %s/%s", Org, Name)
	}
}

// TestPermissionsFollowStartupMode checks the 0755/0644 root split and the
// 0700/0600 user split, keyed off the mode locked at process start.
func TestPermissionsFollowStartupMode(t *testing.T) {
	if IsSystemMode() {
		if DirPerm() != 0755 {
			t.Errorf("system mode DirPerm() = %o, want 0755", DirPerm())
		}
		if FilePerm() != 0644 {
			t.Errorf("system mode FilePerm() = %o, want 0644", FilePerm())
		}
	} else {
		if DirPerm() != 0700 {
			t.Errorf("user mode DirPerm() = %o, want 0700", DirPerm())
		}
		if FilePerm() != 0600 {
			t.Errorf("user mode FilePerm() = %o, want 0600", FilePerm())
		}
	}

	if PIDPerm() != FilePerm() {
		t.Error("the PID file must use the same mode as other files")
	}
}

// TestModeIsLockedAtStart checks that changing HOME after start cannot move
// the resolved directories, which is the whole point of the lock.
func TestModeIsLockedAtStart(t *testing.T) {
	before := ConfigDir("")

	t.Setenv("HOME", t.TempDir())

	if after := ConfigDir(""); after != before {
		t.Fatalf("a late HOME change moved the config dir: %q then %q", before, after)
	}
}

// TestFlagBeatsEnvBeatsDefault checks the documented precedence for every
// directory flag.
func TestFlagBeatsEnvBeatsDefault(t *testing.T) {
	cases := []struct {
		name   string
		envKey string
		fn     func(string) string
	}{
		{"config", "CONFIG_DIR", ConfigDir},
		{"data", "DATA_DIR", DataDir},
		{"cache", "CACHE_DIR", CacheDir},
		{"log", "LOG_DIR", LogDir},
		{"pid", "PID_FILE", PIDFile},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envKey, "/from/env")

			if got := tc.fn("/from/flag"); got != "/from/flag" {
				t.Errorf("the flag must win, got %q", got)
			}
			if got := tc.fn(""); got != "/from/env" {
				t.Errorf("the environment variable must win over the default, got %q", got)
			}

			t.Setenv(tc.envKey, "")
			if got := tc.fn(""); got == "" || got == "/from/env" {
				t.Errorf("the default must be used when nothing is set, got %q", got)
			}
		})
	}
}

// TestDefaultsContainOrgAndName checks that every default path is scoped to
// this project.
func TestDefaultsContainOrgAndName(t *testing.T) {
	for _, path := range []string{ConfigDir(""), DataDir(""), CacheDir(""), LogDir("")} {
		if !strings.Contains(path, Org) || !strings.Contains(path, Name) {
			t.Errorf("default path %q must contain %s and %s", path, Org, Name)
		}
	}
}

// TestSystemModeNeverUsesHome checks the rule that a system install never
// derives a path from $HOME, because a service account's HOME points at the
// data directory.
func TestSystemModeNeverUsesHome(t *testing.T) {
	if !IsSystemMode() {
		t.Skip("this check only applies when the process started elevated")
	}

	dataDir := DataDir("")
	for _, path := range []string{ConfigDir(""), dataDir, CacheDir(""), LogDir(""), BackupDir("", dataDir)} {
		if startHome != "/" && startHome != "." && strings.HasPrefix(path, startHome) {
			t.Errorf("system path %q is derived from HOME %q", path, startHome)
		}
	}
}

// TestBackupDirFallback checks the flag and environment overrides and the
// data directory fallback.
func TestBackupDirFallback(t *testing.T) {
	dataDir := t.TempDir()

	if got := BackupDir("/from/flag", dataDir); got != "/from/flag" {
		t.Errorf("the flag must win, got %q", got)
	}

	t.Setenv("BACKUP_DIR", "/from/env")
	if got := BackupDir("", dataDir); got != "/from/env" {
		t.Errorf("the environment variable must win over detection, got %q", got)
	}

	t.Setenv("BACKUP_DIR", "")
	got := BackupDir("", dataDir)
	if got == "" {
		t.Fatal("BackupDir() must always resolve to a path")
	}
	if got != SystemBackupDir() && got != filepath.Join(dataDir, "backup") && got != UserBackupDir() {
		t.Errorf("BackupDir() = %q, which is none of the documented locations", got)
	}
}

// TestDatabaseDir checks the environment override and the native default.
func TestDatabaseDir(t *testing.T) {
	original := inContainer
	t.Cleanup(func() { inContainer = original })

	t.Setenv("DATABASE_DIR", "/from/env")
	if got := DatabaseDir("/var/lib/x"); got != "/from/env" {
		t.Errorf("DatabaseDir() = %q, want the environment value", got)
	}

	t.Setenv("DATABASE_DIR", "")

	inContainer = func() bool { return true }
	if got := DatabaseDir("/var/lib/x"); got != "/data/db/sqlite" {
		t.Errorf("in a container DatabaseDir() = %q, want /data/db/sqlite", got)
	}

	inContainer = func() bool { return false }
	if got := DatabaseDir("/var/lib/x"); got != "/var/lib/x/db" {
		t.Errorf("natively DatabaseDir() = %q, want /var/lib/x/db", got)
	}
}

// TestEnsureDirPermissions checks that EnsureDir creates the tree with the
// requested privilege level and verifies writability.
func TestEnsureDirPermissions(t *testing.T) {
	base := t.TempDir()

	userDir := filepath.Join(base, "user", "nested")
	if err := EnsureDir(userDir, false); err != nil {
		t.Fatalf("EnsureDir(user) failed: %v", err)
	}
	info, err := os.Stat(userDir)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	// The umask can only remove bits, so the check is that no group or
	// world access survived and the owner keeps full access.
	if info.Mode().Perm()&0077 != 0 || info.Mode().Perm()&0700 != 0700 {
		t.Errorf("user directory mode = %o, want owner-only 0700", info.Mode().Perm())
	}

	rootDir := filepath.Join(base, "root", "nested")
	if err := EnsureDir(rootDir, true); err != nil {
		t.Fatalf("EnsureDir(root) failed: %v", err)
	}
	info, err = os.Stat(rootDir)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if info.Mode().Perm()&0700 != 0700 {
		t.Errorf("root directory mode = %o, want at least owner access", info.Mode().Perm())
	}

	if entries, err := os.ReadDir(userDir); err != nil || len(entries) != 0 {
		t.Errorf("the write test file was not cleaned up: %v %v", entries, err)
	}
}

// TestEnsureAllSkipsEmpty checks that blank entries are ignored.
func TestEnsureAllSkipsEmpty(t *testing.T) {
	base := t.TempDir()

	if err := EnsureAll(filepath.Join(base, "a"), "", filepath.Join(base, "b")); err != nil {
		t.Fatalf("EnsureAll failed: %v", err)
	}
	for _, name := range []string{"a", "b"} {
		if _, err := os.Stat(filepath.Join(base, name)); err != nil {
			t.Errorf("directory %s was not created: %v", name, err)
		}
	}
}

// TestIsWritable checks both the existing-directory and the parent cases.
func TestIsWritable(t *testing.T) {
	base := t.TempDir()

	if !IsWritable(base) {
		t.Error("a temp directory must be writable")
	}
	if !IsWritable(filepath.Join(base, "not-created-yet")) {
		t.Error("a missing directory with a writable parent must report writable")
	}
	if IsWritable("") {
		t.Error("an empty path is never writable")
	}
	if IsWritable("/proc/definitely/not/writable") {
		t.Error("an unwritable path must report false")
	}
}
