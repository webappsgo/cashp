package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServiceAccountIdentity(t *testing.T) {
	if ServiceAccountName != "cashp" {
		t.Errorf("ServiceAccountName = %q, want cashp", ServiceAccountName)
	}
	if ServiceAccountGecos != "cashp service account" {
		t.Errorf("ServiceAccountGecos = %q, want %q", ServiceAccountGecos, "cashp service account")
	}
}

func TestNologinShellIsANonLoginShell(t *testing.T) {
	shell := nologinShell()
	allowed := map[string]bool{}
	for _, candidate := range nologinShells {
		allowed[candidate] = true
	}
	if !allowed[shell] {
		t.Errorf("nologinShell() = %q, want one of %v", shell, nologinShells)
	}
}

func TestLookupServiceAccountMissing(t *testing.T) {
	// A name that cannot exist on a real host: account names may not contain
	// a space or a slash.
	if _, _, ok := lookupServiceAccount("no such/account name"); ok {
		t.Error("lookupServiceAccount must report a missing account as not found")
	}
}

func TestEnsureDirectoriesCreatesTheFullStateSet(t *testing.T) {
	root := t.TempDir()
	data := TemplateData{
		Name:      "cashp",
		ConfigDir: filepath.Join(root, "etc"),
		DataDir:   filepath.Join(root, "lib"),
		CacheDir:  filepath.Join(root, "cache"),
		LogDir:    filepath.Join(root, "log"),
		RunDir:    filepath.Join(root, "run"),
	}
	// -1/-1 means "leave ownership alone", which keeps this test from
	// touching any identity on the host.
	if err := ensureDirectories(data, -1, -1); err != nil {
		t.Fatalf("ensureDirectories: %v", err)
	}
	for _, dir := range []string{data.ConfigDir, data.DataDir, data.CacheDir, data.LogDir} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
		if info.Mode().Perm() != stateDirMode {
			t.Errorf("%s mode = %v, want %v", dir, info.Mode().Perm(), os.FileMode(stateDirMode))
		}
	}
	runInfo, err := os.Stat(data.RunDir)
	if err != nil {
		t.Fatalf("stat run dir: %v", err)
	}
	if runInfo.Mode().Perm() != runDirMode {
		t.Errorf("run dir mode = %v, want %v", runInfo.Mode().Perm(), os.FileMode(runDirMode))
	}
	// Re-running must be idempotent, because the server calls it on every
	// startup.
	if err := ensureDirectories(data, -1, -1); err != nil {
		t.Errorf("second ensureDirectories = %v, want nil", err)
	}
}
