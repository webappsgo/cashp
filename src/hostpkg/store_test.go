package hostpkg

import (
	"errors"
	"strings"
	"testing"
)

func TestNewStoreRequiresADatabase(t *testing.T) {
	if _, err := NewStore(nil); !errors.Is(err, ErrCommandFailed) {
		t.Fatalf("error = %v, want ErrCommandFailed", err)
	}
}

func TestStoreValidatesBeforeTouchingTheDatabase(t *testing.T) {
	// A zero Store has no handle, so any of these reaching the database would
	// panic; each must be refused by validation first.
	store := &Store{}
	ctx := t.Context()

	if err := store.RecordInstall(ctx, PackageRecord{Name: "nginx; rm -rf /"}); !errors.Is(err, ErrInvalidPackageName) {
		t.Errorf("RecordInstall error = %v, want ErrInvalidPackageName", err)
	}
	if err := store.RecordRemoval(ctx, "../etc/passwd"); !errors.Is(err, ErrInvalidPackageName) {
		t.Errorf("RecordRemoval error = %v, want ErrInvalidPackageName", err)
	}
	if _, err := store.Owned(ctx, "nginx redis"); !errors.Is(err, ErrInvalidPackageName) {
		t.Errorf("Owned error = %v, want ErrInvalidPackageName", err)
	}
	if err := store.RecordRepo(ctx, RepoRecord{ID: "Bad Repo"}); !errors.Is(err, ErrInvalidRepoName) {
		t.Errorf("RecordRepo error = %v, want ErrInvalidRepoName", err)
	}
	if _, err := store.RepoRecorded(ctx, "../docker"); !errors.Is(err, ErrInvalidRepoName) {
		t.Errorf("RepoRecorded error = %v, want ErrInvalidRepoName", err)
	}
}

func TestStoreFailureDoesNotLeakDriverDetail(t *testing.T) {
	raw := errors.New("dial tcp 10.0.0.5:5432: password=hunter2 dbname=cashp")

	err := storeFailure(raw)
	if !errors.Is(err, ErrCommandFailed) {
		t.Fatalf("error = %v, want ErrCommandFailed", err)
	}
	for _, secret := range []string{"hunter2", "10.0.0.5", "dbname"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error message leaked %q: %s", secret, err)
		}
	}
}

func TestMemoryRecorderOwnershipLifecycle(t *testing.T) {
	rec := NewMemoryRecorder()
	ctx := t.Context()

	owned, err := rec.Owned(ctx, "nginx")
	if err != nil || owned {
		t.Fatalf("Owned before install = %v, %v", owned, err)
	}

	if err := rec.RecordInstall(ctx, PackageRecord{Name: "nginx", Service: ServiceWebServer, Manager: ManagerAPT}); err != nil {
		t.Fatalf("RecordInstall: %v", err)
	}
	if owned, err := rec.Owned(ctx, "nginx"); err != nil || !owned {
		t.Fatalf("Owned after install = %v, %v", owned, err)
	}
	if names := rec.Packages(); len(names) != 1 || names[0] != "nginx" {
		t.Fatalf("Packages() = %v, want [nginx]", names)
	}

	if err := rec.RecordRemoval(ctx, "nginx"); err != nil {
		t.Fatalf("RecordRemoval: %v", err)
	}
	if owned, err := rec.Owned(ctx, "nginx"); err != nil || owned {
		t.Fatalf("Owned after removal = %v, %v", owned, err)
	}
	if names := rec.Packages(); len(names) != 0 {
		t.Fatalf("Packages() after removal = %v, want none", names)
	}

	// Reinstalling clears the removal marker.
	if err := rec.RecordInstall(ctx, PackageRecord{Name: "nginx"}); err != nil {
		t.Fatalf("RecordInstall: %v", err)
	}
	if owned, err := rec.Owned(ctx, "nginx"); err != nil || !owned {
		t.Fatalf("Owned after reinstall = %v, %v", owned, err)
	}
}

func TestMemoryRecorderRepoLifecycle(t *testing.T) {
	rec := NewMemoryRecorder()
	ctx := t.Context()

	recorded, err := rec.RepoRecorded(ctx, RepoDocker)
	if err != nil || recorded {
		t.Fatalf("RepoRecorded before add = %v, %v", recorded, err)
	}

	if err := rec.RecordRepo(ctx, RepoRecord{
		ID:             RepoDocker,
		Manager:        ManagerAPT,
		DefinitionPath: "/etc/apt/sources.list.d/docker.sources",
		Fingerprints:   []string{fingerprintDockerDeb},
	}); err != nil {
		t.Fatalf("RecordRepo: %v", err)
	}
	if recorded, err := rec.RepoRecorded(ctx, RepoDocker); err != nil || !recorded {
		t.Fatalf("RepoRecorded after add = %v, %v", recorded, err)
	}
}

func TestMemoryRecorderRejectsInvalidNames(t *testing.T) {
	rec := NewMemoryRecorder()
	ctx := t.Context()

	if err := rec.RecordInstall(ctx, PackageRecord{Name: "bad name"}); !errors.Is(err, ErrInvalidPackageName) {
		t.Errorf("RecordInstall error = %v, want ErrInvalidPackageName", err)
	}
	if err := rec.RecordRemoval(ctx, "bad;name"); !errors.Is(err, ErrInvalidPackageName) {
		t.Errorf("RecordRemoval error = %v, want ErrInvalidPackageName", err)
	}
	if err := rec.RecordRepo(ctx, RepoRecord{ID: "Bad Repo"}); !errors.Is(err, ErrInvalidRepoName) {
		t.Errorf("RecordRepo error = %v, want ErrInvalidRepoName", err)
	}
}
