package hostpkg

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// provisionHarness is a provisioner wired entirely to in-memory doubles and a
// temporary root, so no test ever executes a command or writes under /etc.
type provisionHarness struct {
	p        *Provisioner
	runner   *FakeRunner
	fetcher  *StaticFetcher
	recorder *MemoryRecorder
	root     string
}

// newHarness builds a provisioner for a fixture distribution.
func newHarness(t *testing.T, family Family) *provisionHarness {
	t.Helper()

	root := t.TempDir()
	runner := NewFakeRunner()
	notInstalled(runner)
	fetcher := NewStaticFetcher()
	recorder := NewMemoryRecorder()

	p, err := NewProvisioner(distroFor(t, family), ProvisionerOptions{
		Runner:   runner,
		Recorder: recorder,
		FS:       NewFileSystem(root),
		Fetcher:  fetcher,
	})
	if err != nil {
		t.Fatalf("NewProvisioner: %v", err)
	}

	return &provisionHarness{p: p, runner: runner, fetcher: fetcher, recorder: recorder, root: root}
}

// fileCount counts the regular files written under the temporary root.
func fileCount(t *testing.T, root string) int {
	t.Helper()

	count := 0
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			count++
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walk root: %v", err)
	}

	return count
}

// readUnderRoot reads a host path from inside the temporary root.
func readUnderRoot(t *testing.T, root, path string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, strings.TrimPrefix(path, "/")))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(data)
}

// countLinesWithPrefix counts recorded commands starting with a prefix.
func countLinesWithPrefix(runner *FakeRunner, prefix string) int {
	count := 0
	for _, line := range runner.Lines() {
		if strings.HasPrefix(line, prefix) {
			count++
		}
	}

	return count
}

func TestNewProvisionerRejectsNilDistro(t *testing.T) {
	if _, err := NewProvisioner(nil, ProvisionerOptions{Runner: NewFakeRunner()}); !errors.Is(err, ErrUnsupportedDistro) {
		t.Fatalf("error = %v, want ErrUnsupportedDistro", err)
	}
}

func TestEnsureServiceAddsPinnedRepositoryThenInstalls(t *testing.T) {
	h := newHarness(t, FamilyRHEL)
	h.fetcher.Set("https://download.docker.com/linux/centos/gpg", readKeyFixture(t))

	res, err := h.p.EnsureService(t.Context(), ServiceDocker)
	if err != nil {
		t.Fatalf("EnsureService: %v", err)
	}
	if !res.Changed {
		t.Error("the first install reported no change")
	}

	// The verified key is written as ASCII armor and imported into rpm.
	key := readUnderRoot(t, h.root, "/etc/pki/rpm-gpg/RPM-GPG-KEY-docker-ce")
	if !strings.HasPrefix(key, armorHeader) {
		t.Errorf("rpm key was not written as ASCII armor: %.40q", key)
	}
	if !hasLine(h.runner, "rpm --import /etc/pki/rpm-gpg/RPM-GPG-KEY-docker-ce") {
		t.Errorf("the key was never imported: %v", h.runner.Lines())
	}

	// The repository definition is cashp's own file, with verification on.
	definition := readUnderRoot(t, h.root, "/etc/yum.repos.d/docker-ce.repo")
	if !strings.Contains(definition, "[docker-ce-stable]") || !strings.Contains(definition, "gpgcheck=1") {
		t.Errorf("unexpected repository definition:\n%s", definition)
	}

	// The transaction is a single argv, never a shell string.
	installs := 0
	for _, cmd := range h.runner.Calls {
		if cmd.Name == "dnf" && len(cmd.Args) > 2 && cmd.Args[2] == "install" {
			installs++
			for _, arg := range cmd.Args {
				if strings.ContainsAny(arg, ";&|`$") {
					t.Errorf("shell metacharacter reached argv: %q", arg)
				}
			}
		}
	}
	if installs != 1 {
		t.Errorf("install transactions = %d, want 1", installs)
	}

	wantPackages, err := PackagesFor(ServiceDocker, h.p.Distro)
	if err != nil {
		t.Fatalf("PackagesFor: %v", err)
	}
	for _, name := range wantPackages {
		owned, err := h.recorder.Owned(t.Context(), name)
		if err != nil || !owned {
			t.Errorf("package %q was not recorded as owned: %v", name, err)
		}
	}

	recorded, err := h.recorder.RepoRecorded(t.Context(), RepoDocker)
	if err != nil || !recorded {
		t.Errorf("repository was not recorded: %v, %v", recorded, err)
	}
}

func TestAddRepoIsIdempotent(t *testing.T) {
	h := newHarness(t, FamilyRHEL)
	h.fetcher.Set("https://download.docker.com/linux/centos/gpg", readKeyFixture(t))

	changed, err := h.p.AddRepo(t.Context(), RepoDocker)
	if err != nil || !changed {
		t.Fatalf("first AddRepo = %v, %v, want changed", changed, err)
	}
	files := fileCount(t, h.root)
	h.runner.Reset()

	changed, err = h.p.AddRepo(t.Context(), RepoDocker)
	if err != nil {
		t.Fatalf("second AddRepo: %v", err)
	}
	if changed {
		t.Error("re-adding an unchanged repository reported a change")
	}
	if got := fileCount(t, h.root); got != files {
		t.Errorf("file count changed from %d to %d", files, got)
	}
	if n := countLinesWithPrefix(h.runner, "rpm --import"); n != 0 {
		t.Errorf("the key was re-imported %d times", n)
	}
	if n := countLinesWithPrefix(h.runner, "dnf --assumeyes --setopt=assumeyes=1 makecache"); n != 0 {
		t.Errorf("the index was refreshed %d times with nothing to refresh", n)
	}
}

func TestAddRepoFingerprintMismatchLeavesHostUnchanged(t *testing.T) {
	h := newHarness(t, FamilyDebian)

	// Debian pins Docker's apt key; serving the RPM key instead is exactly the
	// wrong-key case a pinned fingerprint exists to catch.
	h.fetcher.Set("https://download.docker.com/linux/debian/gpg", readKeyFixture(t))

	changed, err := h.p.AddRepo(t.Context(), RepoDocker)
	if !errors.Is(err, ErrKeyFingerprintMismatch) {
		t.Fatalf("error = %v, want ErrKeyFingerprintMismatch", err)
	}
	if changed {
		t.Error("a rejected repository reported a change")
	}

	if got := fileCount(t, h.root); got != 0 {
		t.Errorf("%d files were written despite the mismatch", got)
	}
	if exists, _ := h.p.FS.Exists("/etc/apt/keyrings/docker.gpg"); exists {
		t.Error("the unverified key was installed")
	}
	if exists, _ := h.p.FS.Exists("/etc/apt/sources.list.d/docker.sources"); exists {
		t.Error("the repository definition was written")
	}
	if len(h.runner.Calls) != 0 {
		t.Errorf("commands ran despite the mismatch: %v", h.runner.Lines())
	}
	if recorded, _ := h.recorder.RepoRecorded(t.Context(), RepoDocker); recorded {
		t.Error("the rejected repository was recorded as added")
	}
}

func TestAddRepoFailsWhenTheKeyCannotBeFetched(t *testing.T) {
	h := newHarness(t, FamilyDebian)

	if _, err := h.p.AddRepo(t.Context(), RepoDocker); !errors.Is(err, ErrCommandFailed) {
		t.Fatalf("error = %v, want ErrCommandFailed", err)
	}
	if got := fileCount(t, h.root); got != 0 {
		t.Errorf("%d files were written for a repository whose key never arrived", got)
	}
}

func TestEnsureServiceOnAlpineEnablesCommunity(t *testing.T) {
	h := newHarness(t, FamilyAlpine)

	if _, err := h.p.EnsureService(t.Context(), ServiceWebServer); err != nil {
		t.Fatalf("EnsureService: %v", err)
	}

	repositories := readUnderRoot(t, h.root, "/etc/apk/repositories")
	if !strings.Contains(repositories, "https://dl-cdn.alpinelinux.org/alpine/v3.21/community") {
		t.Errorf("community repository was not enabled:\n%s", repositories)
	}

	// A second run must not append the entry again.
	changed, err := h.p.EnsureAlpineCommunity(t.Context())
	if err != nil || changed {
		t.Fatalf("EnsureAlpineCommunity = %v, %v, want unchanged", changed, err)
	}
	if again := readUnderRoot(t, h.root, "/etc/apk/repositories"); again != repositories {
		t.Errorf("repositories file changed on the second run:\n%s", again)
	}
}

func TestEnsurePHPOnAlpineNeedsNoRepository(t *testing.T) {
	h := newHarness(t, FamilyAlpine)

	plan, res, err := h.p.EnsurePHP(t.Context(), "8.3")
	if err != nil {
		t.Fatalf("EnsurePHP: %v", err)
	}
	if plan.Repo != "" {
		t.Errorf("plan.Repo = %q, want no repository on Alpine", plan.Repo)
	}
	if !res.Changed {
		t.Error("the first PHP install reported no change")
	}
	for _, name := range plan.Packages {
		owned, err := h.recorder.Owned(t.Context(), name)
		if err != nil || !owned {
			t.Errorf("package %q was not recorded as owned: %v", name, err)
		}
	}
}

func TestRemovePackagesRefusesPackagesCashpDoesNotOwn(t *testing.T) {
	h := newHarness(t, FamilyDebian)

	err := h.p.RemovePackages(t.Context(), "nginx")
	if !errors.Is(err, ErrNotOwned) {
		t.Fatalf("error = %v, want ErrNotOwned", err)
	}
	if n := countLinesWithPrefix(h.runner, "apt-get"); n != 0 {
		t.Errorf("apt-get ran %d times for a package cashp does not own", n)
	}

	// After cashp installs it, removal is allowed.
	if _, err := h.p.EnsureService(t.Context(), ServiceWebServer); err != nil {
		t.Fatalf("EnsureService: %v", err)
	}
	h.runner.Reset()

	if err := h.p.RemovePackages(t.Context(), "nginx"); err != nil {
		t.Fatalf("RemovePackages: %v", err)
	}
	removed := false
	for _, line := range h.runner.Lines() {
		if strings.HasPrefix(line, "apt-get") && strings.Contains(line, " remove ") {
			removed = true
		}
	}
	if !removed {
		t.Errorf("no removal transaction ran: %v", h.runner.Lines())
	}

	// Ownership is released, so a second removal is refused again.
	if err := h.p.RemovePackages(t.Context(), "nginx"); !errors.Is(err, ErrNotOwned) {
		t.Fatalf("second removal error = %v, want ErrNotOwned", err)
	}
}

func TestRemovePackagesRejectsInvalidNames(t *testing.T) {
	h := newHarness(t, FamilyDebian)

	if err := h.p.RemovePackages(t.Context(), "nginx; rm -rf /"); !errors.Is(err, ErrInvalidPackageName) {
		t.Fatalf("error = %v, want ErrInvalidPackageName", err)
	}
	if err := h.p.RemovePackages(t.Context()); !errors.Is(err, ErrNoPackages) {
		t.Fatalf("error = %v, want ErrNoPackages", err)
	}
	if len(h.runner.Calls) != 0 {
		t.Errorf("commands ran for an invalid removal: %v", h.runner.Lines())
	}
}

func TestUpgradeRefusesAWholeSystemUpgrade(t *testing.T) {
	h := newHarness(t, FamilyDebian)

	if err := h.p.Upgrade(t.Context()); !errors.Is(err, ErrDistroUpgradeRefused) {
		t.Fatalf("error = %v, want ErrDistroUpgradeRefused", err)
	}
	if len(h.runner.Calls) != 0 {
		t.Errorf("commands ran for a refused upgrade: %v", h.runner.Lines())
	}

	if err := h.p.Upgrade(t.Context(), "nginx"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	upgraded := false
	for _, line := range h.runner.Lines() {
		if strings.HasPrefix(line, "apt-get") && strings.Contains(line, "nginx") {
			upgraded = true
		}
	}
	if !upgraded {
		t.Errorf("no targeted upgrade ran: %v", h.runner.Lines())
	}
}

func TestRefreshIndex(t *testing.T) {
	h := newHarness(t, FamilyDebian)

	if err := h.p.RefreshIndex(t.Context()); err != nil {
		t.Fatalf("RefreshIndex: %v", err)
	}
	if n := countLinesWithPrefix(h.runner, "apt-get"); n == 0 {
		t.Errorf("no refresh ran: %v", h.runner.Lines())
	}
}

func TestEnsureServiceRejectsAnUnknownService(t *testing.T) {
	h := newHarness(t, FamilyDebian)

	if _, err := h.p.EnsureService(t.Context(), "not-a-service"); !errors.Is(err, ErrServiceUnknown) {
		t.Fatalf("error = %v, want ErrServiceUnknown", err)
	}
	if len(h.runner.Calls) != 0 {
		t.Errorf("commands ran for an unknown service: %v", h.runner.Lines())
	}
}
