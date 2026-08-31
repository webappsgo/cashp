package hostpkg

import (
	"context"
	"net/http"
	"strings"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// The package-manager abstraction. Every implementation is non-interactive
// and idempotent: installing a package that is already present succeeds and
// reports that nothing changed. Destructive operations IDEA.md does not
// sanction — notably a whole-system distribution upgrade — are refused.

// InstallResult describes what an install transaction actually did.
type InstallResult struct {
	// Requested is the validated, de-duplicated package set.
	Requested []string
	// Installed lists the packages this call installed.
	Installed []string
	// AlreadyPresent lists the packages that were already installed.
	AlreadyPresent []string
	// Changed reports whether the host was modified.
	Changed bool
}

// Manager is one host package manager.
type Manager interface {
	// Kind identifies the manager.
	Kind() ManagerKind
	// Refresh updates the package index.
	Refresh(ctx context.Context) error
	// Install installs the packages that are not already present.
	Install(ctx context.Context, pkgs ...string) (InstallResult, error)
	// Remove removes the named packages.
	Remove(ctx context.Context, pkgs ...string) error
	// Upgrade upgrades the named packages; an empty set upgrades every
	// installed package without ever performing a distribution upgrade.
	Upgrade(ctx context.Context, pkgs ...string) error
	// IsInstalled reports whether a package is installed.
	IsInstalled(ctx context.Context, pkg string) (bool, error)
	// AvailableVersion returns the candidate version from the index.
	AvailableVersion(ctx context.Context, pkg string) (string, error)
	// SupportsHold reports whether the manager can pin a package version.
	SupportsHold() bool
	// Hold pins a package at its installed version.
	Hold(ctx context.Context, pkg string) error
	// Unhold releases a pin.
	Unhold(ctx context.Context, pkg string) error
}

// NewManager returns the manager implementation for a distribution.
func NewManager(d *Distro, runner Runner) (Manager, error) {
	if d == nil {
		return nil, failUnavailable(ErrUnsupportedDistro, "host operating system is not supported")
	}
	if runner == nil {
		runner = NewExecRunner()
	}

	base := baseManager{runner: runner}
	switch d.Manager {
	case ManagerAPT:
		return &aptManager{baseManager: base}, nil
	case ManagerAPK:
		return &apkManager{baseManager: base}, nil
	case ManagerDNF:
		return &dnfManager{baseManager: base}, nil
	case ManagerPacman:
		return &pacmanManager{baseManager: base}, nil
	default:
		return nil, failUnavailable(ErrUnsupportedDistro, "host operating system is not supported")
	}
}

// baseManager carries the runner and the helpers shared by every manager.
type baseManager struct {
	runner Runner
}

// run executes one argv command with the given timeout and extra environment.
func (b *baseManager) run(ctx context.Context, timeout time.Duration, env []string, name string, args ...string) (Result, error) {
	return b.runner.Run(ctx, Command{
		Name:    name,
		Args:    args,
		Env:     append([]string{"LC_ALL=C", "LANG=C"}, env...),
		Timeout: timeout,
	})
}

// prepare validates and de-duplicates a package set before it can be turned
// into argv elements.
func prepare(pkgs []string) ([]string, error) {
	set := DedupePackages(pkgs)
	if err := ValidatePackageNames(set); err != nil {
		return nil, err
	}

	return set, nil
}

// idempotentInstall splits a package set into present and missing packages
// and only runs the transaction when something is actually missing.
func idempotentInstall(ctx context.Context, m Manager, pkgs []string, install func(context.Context, []string) error) (InstallResult, error) {
	set, err := prepare(pkgs)
	if err != nil {
		return InstallResult{}, err
	}

	res := InstallResult{Requested: set}
	missing := make([]string, 0, len(set))
	for _, pkg := range set {
		present, err := m.IsInstalled(ctx, pkg)
		if err != nil {
			return InstallResult{}, err
		}
		if present {
			res.AlreadyPresent = append(res.AlreadyPresent, pkg)
			continue
		}
		missing = append(missing, pkg)
	}
	if len(missing) == 0 {
		return res, nil
	}

	if err := install(ctx, missing); err != nil {
		return InstallResult{}, err
	}
	res.Installed = missing
	res.Changed = true

	return res, nil
}

// holdUnsupported is the typed refusal returned by managers with no
// version-hold concept, so a caller gets an explicit error instead of a
// silent no-op.
func holdUnsupported(kind ManagerKind) error {
	return fail(ErrHoldUnsupported, apperr.CodeConflict, http.StatusConflict,
		"package version pinning is not supported on this system").
		WithDetails(map[string]any{"package_manager": string(kind)})
}

// firstFieldAfter returns the trimmed remainder of the first line whose
// prefix matches, used to parse package manager query output.
func firstFieldAfter(output, prefix string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
	}

	return ""
}
