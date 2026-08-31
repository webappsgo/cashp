package hostpkg

import (
	"bytes"
	"context"
	"log/slog"
	"strings"

	"github.com/webappsgo/cashp/src/logging"
)

// Provisioner is the entry point for host package management: it resolves a
// managed service to its packages for the running distribution, adds the
// pinned third-party repository that service needs, installs the packages,
// and records every change to the audit log and the package inventory.
type Provisioner struct {
	// Distro is the detected host distribution.
	Distro *Distro
	// Manager is the package manager driver for that distribution.
	Manager Manager
	// Recorder persists what cashp installed.
	Recorder Recorder
	// FS is the rooted filesystem repository definitions are written through.
	FS *FileSystem
	// Fetcher downloads pinned signing keys.
	Fetcher Fetcher
	// Runner executes host commands such as the rpm key import.
	Runner Runner
}

// ProvisionerOptions overrides the production defaults, which is what the
// test suite uses to keep every write inside a temporary directory.
type ProvisionerOptions struct {
	// Runner executes host commands; nil uses the real exec runner.
	Runner Runner
	// Recorder persists ownership; nil uses an in-memory recorder.
	Recorder Recorder
	// FS is the rooted filesystem; nil writes to the real root.
	FS *FileSystem
	// Fetcher downloads signing keys; nil uses the HTTPS fetcher.
	Fetcher Fetcher
}

// NewProvisioner builds a provisioner for a detected distribution.
func NewProvisioner(d *Distro, opts ProvisionerOptions) (*Provisioner, error) {
	if d == nil {
		return nil, failUnavailable(ErrUnsupportedDistro, "host operating system is not supported")
	}

	runner := opts.Runner
	if runner == nil {
		runner = NewExecRunner()
	}
	manager, err := NewManager(d, runner)
	if err != nil {
		return nil, err
	}

	recorder := opts.Recorder
	if recorder == nil {
		recorder = NewMemoryRecorder()
	}
	fsys := opts.FS
	if fsys == nil {
		fsys = NewFileSystem("/")
	}
	fetcher := opts.Fetcher
	if fetcher == nil {
		fetcher = NewHTTPFetcher()
	}

	return &Provisioner{
		Distro:   d,
		Manager:  manager,
		Recorder: recorder,
		FS:       fsys,
		Fetcher:  fetcher,
		Runner:   runner,
	}, nil
}

// EnsureService installs the packages a managed service needs, adding the
// pinned third-party repository first when the distribution requires one.
// It is idempotent: a service whose packages are already installed reports
// no change and runs no transaction.
func (p *Provisioner) EnsureService(ctx context.Context, svc Service) (InstallResult, error) {
	packages, err := PackagesFor(svc, p.Distro)
	if err != nil {
		return InstallResult{}, err
	}

	if id, ok := RepoForService(svc, p.Distro); ok {
		if _, err := p.AddRepo(ctx, id); err != nil {
			return InstallResult{}, err
		}
	}
	if p.Distro.Family == FamilyAlpine {
		if _, err := p.EnsureAlpineCommunity(ctx); err != nil {
			return InstallResult{}, err
		}
	}

	return p.install(ctx, svc, packages)
}

// EnsurePHP installs one PHP-FPM version, adding the multi-version
// repository the distribution needs first. The resolved plan is returned so
// the caller can surface a degraded-support path to the operator.
func (p *Provisioner) EnsurePHP(ctx context.Context, version string) (PHPPlan, InstallResult, error) {
	plan, err := PHPFPMPlan(version, p.Distro)
	if err != nil {
		return PHPPlan{}, InstallResult{}, err
	}

	if plan.Repo != "" {
		if _, err := p.AddRepo(ctx, plan.Repo); err != nil {
			return plan, InstallResult{}, err
		}
	}

	res, err := p.install(ctx, ServicePHPFPM, plan.Packages)

	return plan, res, err
}

// install performs the package transaction and records ownership for every
// package that ends up installed.
func (p *Provisioner) install(ctx context.Context, svc Service, packages []string) (InstallResult, error) {
	res, err := p.Manager.Install(ctx, packages...)
	if err != nil {
		return InstallResult{}, err
	}

	for _, name := range res.Requested {
		version, verr := p.Manager.AvailableVersion(ctx, name)
		if verr != nil {
			version = ""
		}
		rec := PackageRecord{
			Name:         name,
			Service:      svc,
			Manager:      p.Manager.Kind(),
			Distribution: p.Distro.ID,
			Version:      version,
		}
		if err := p.Recorder.RecordInstall(ctx, rec); err != nil {
			return InstallResult{}, err
		}
	}

	logging.Audit().LogAttrs(ctx, slog.LevelInfo, "host package install",
		slog.String("service", string(svc)),
		slog.String("manager", string(p.Manager.Kind())),
		slog.String("distribution", p.Distro.ID),
		slog.String("packages", strings.Join(res.Requested, " ")),
		slog.String("installed", strings.Join(res.Installed, " ")),
		slog.Bool("changed", res.Changed),
	)

	return res, nil
}

// RemovePackages removes packages cashp installed. A package cashp does not
// own is refused: removing a package the operator installed themselves is
// destructive and is not sanctioned.
func (p *Provisioner) RemovePackages(ctx context.Context, packages ...string) error {
	set, err := prepare(packages)
	if err != nil {
		return err
	}

	for _, name := range set {
		owned, err := p.Recorder.Owned(ctx, name)
		if err != nil {
			return err
		}
		if !owned {
			return notOwned(name)
		}
	}

	if err := p.Manager.Remove(ctx, set...); err != nil {
		return err
	}

	for _, name := range set {
		if err := p.Recorder.RecordRemoval(ctx, name); err != nil {
			return err
		}
	}

	logging.Audit().LogAttrs(ctx, slog.LevelInfo, "host package remove",
		slog.String("manager", string(p.Manager.Kind())),
		slog.String("distribution", p.Distro.ID),
		slog.String("packages", strings.Join(set, " ")),
	)

	return nil
}

// Upgrade upgrades named packages only. An empty package list is refused so
// no code path can turn into a whole-system or distribution upgrade, which
// cashp never performs on an operator's host.
func (p *Provisioner) Upgrade(ctx context.Context, packages ...string) error {
	if len(packages) == 0 {
		return distroUpgradeRefused()
	}

	set, err := prepare(packages)
	if err != nil {
		return err
	}

	if err := p.Manager.Upgrade(ctx, set...); err != nil {
		return err
	}

	logging.Audit().LogAttrs(ctx, slog.LevelInfo, "host package upgrade",
		slog.String("manager", string(p.Manager.Kind())),
		slog.String("distribution", p.Distro.ID),
		slog.String("packages", strings.Join(set, " ")),
	)

	return nil
}

// RefreshIndex refreshes the package index.
func (p *Provisioner) RefreshIndex(ctx context.Context) error {
	return p.Manager.Refresh(ctx)
}

// AddRepo adds a pinned third-party repository and reports whether the host
// changed. Every signing key is downloaded and verified against its pinned
// fingerprint before a single byte is written, so a mismatch fails hard and
// leaves the host exactly as it was.
func (p *Provisioner) AddRepo(ctx context.Context, id RepoID) (bool, error) {
	plan, err := PlanRepo(id, p.Distro)
	if err != nil {
		return false, err
	}

	verified := make([][]byte, 0, len(plan.Keys))
	for _, key := range plan.Keys {
		material, err := p.Fetcher.Fetch(ctx, key.URL)
		if err != nil {
			return false, err
		}
		pinned, err := ExtractPinnedKey(material, key.Fingerprint)
		if err != nil {
			return false, err
		}
		if plan.ArmoredKeys {
			pinned = ArmorKey(pinned)
		}
		verified = append(verified, pinned)
	}

	changed := false
	for i, key := range plan.Keys {
		written, err := p.writeIfDifferent(key.Path, verified[i])
		if err != nil {
			return changed, err
		}
		if written {
			changed = true
		}
		if plan.Manager == ManagerDNF && written {
			if _, err := p.Runner.Run(ctx, Command{
				Name:    "rpm",
				Args:    []string{"--import", key.Path},
				Timeout: TimeoutKeyImport,
			}); err != nil {
				return changed, err
			}
		}
	}

	definitionWritten, err := p.writeIfDifferent(plan.DefinitionPath, []byte(plan.Definition))
	if err != nil {
		return changed, err
	}
	if definitionWritten {
		changed = true
	}

	fingerprints := make([]string, 0, len(plan.Keys))
	for _, key := range plan.Keys {
		fingerprints = append(fingerprints, key.Fingerprint)
	}
	if err := p.Recorder.RecordRepo(ctx, RepoRecord{
		ID:             plan.ID,
		Manager:        plan.Manager,
		DefinitionPath: plan.DefinitionPath,
		Fingerprints:   fingerprints,
	}); err != nil {
		return changed, err
	}

	if changed {
		if err := p.Manager.Refresh(ctx); err != nil {
			return changed, err
		}
	}

	logging.Audit().LogAttrs(ctx, slog.LevelInfo, "host repository add",
		slog.String("repository", string(plan.ID)),
		slog.String("manager", string(plan.Manager)),
		slog.String("distribution", p.Distro.ID),
		slog.String("fingerprints", strings.Join(fingerprints, " ")),
		slog.Bool("changed", changed),
	)

	return changed, nil
}

// EnsureAlpineCommunity enables Alpine's community repository, where most of
// the managed services live. It reports whether the file changed.
func (p *Provisioner) EnsureAlpineCommunity(ctx context.Context) (bool, error) {
	path, entry, err := AlpineCommunityRepository(p.Distro)
	if err != nil {
		return false, err
	}

	changed, err := p.FS.EnsureLine(path, entry)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}

	if err := p.Manager.Refresh(ctx); err != nil {
		return true, err
	}

	logging.Audit().LogAttrs(ctx, slog.LevelInfo, "host repository add",
		slog.String("repository", "alpine-community"),
		slog.String("manager", string(p.Manager.Kind())),
		slog.String("distribution", p.Distro.ID),
		slog.Bool("changed", true),
	)

	return true, nil
}

// writeIfDifferent writes content only when the file is missing or differs,
// which keeps repeated provisioning runs free of spurious changes.
func (p *Provisioner) writeIfDifferent(path string, content []byte) (bool, error) {
	exists, err := p.FS.Exists(path)
	if err != nil {
		return false, err
	}
	if exists {
		current, err := p.FS.ReadFile(path)
		if err != nil {
			return false, err
		}
		if bytes.Equal(current, content) {
			return false, nil
		}
	}

	if err := p.FS.WriteFile(path, content); err != nil {
		return false, err
	}

	return true, nil
}
