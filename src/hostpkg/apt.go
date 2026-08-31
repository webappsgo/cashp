package hostpkg

import (
	"context"
	"strings"
)

// aptManager drives Debian and Ubuntu hosts. Every invocation is fully
// non-interactive: debconf, apt-listchanges and needrestart are all told not
// to prompt, and no debconf question can ever block the transaction.
type aptManager struct {
	baseManager
}

// aptNonInteractiveEnv is the environment that guarantees no prompt appears.
var aptNonInteractiveEnv = []string{
	"DEBIAN_FRONTEND=noninteractive",
	"DEBIAN_PRIORITY=critical",
	"APT_LISTCHANGES_FRONTEND=none",
	"NEEDRESTART_MODE=a",
	"NEEDRESTART_SUSPEND=1",
}

// Kind identifies the manager.
func (m *aptManager) Kind() ManagerKind { return ManagerAPT }

// Refresh updates the apt package index.
func (m *aptManager) Refresh(ctx context.Context) error {
	_, err := m.run(ctx, TimeoutRefresh, aptNonInteractiveEnv, "apt-get", "update", "-qq")

	return err
}

// Install installs only the packages that are missing.
func (m *aptManager) Install(ctx context.Context, pkgs ...string) (InstallResult, error) {
	return idempotentInstall(ctx, m, pkgs, func(ctx context.Context, missing []string) error {
		args := append([]string{"install", "-y", "--no-install-recommends"}, missing...)
		_, err := m.run(ctx, TimeoutInstall, aptNonInteractiveEnv, "apt-get", args...)

		return err
	})
}

// Remove removes packages while leaving their configuration in place, so a
// removal is never a silent purge of host configuration cashp did not write.
func (m *aptManager) Remove(ctx context.Context, pkgs ...string) error {
	set, err := prepare(pkgs)
	if err != nil {
		return err
	}

	args := append([]string{"remove", "-y"}, set...)
	_, err = m.run(ctx, TimeoutInstall, aptNonInteractiveEnv, "apt-get", args...)

	return err
}

// Upgrade upgrades the named packages, or every installed package when none
// are named. It never runs dist-upgrade, which would be a distribution
// upgrade rather than a package upgrade.
func (m *aptManager) Upgrade(ctx context.Context, pkgs ...string) error {
	if len(pkgs) == 0 {
		_, err := m.run(ctx, TimeoutInstall, aptNonInteractiveEnv, "apt-get", "upgrade", "-y")

		return err
	}

	set, err := prepare(pkgs)
	if err != nil {
		return err
	}

	args := append([]string{"install", "-y", "--only-upgrade"}, set...)
	_, err = m.run(ctx, TimeoutInstall, aptNonInteractiveEnv, "apt-get", args...)

	return err
}

// IsInstalled queries the dpkg database rather than parsing apt output.
func (m *aptManager) IsInstalled(ctx context.Context, pkg string) (bool, error) {
	if err := ValidatePackageName(pkg); err != nil {
		return false, err
	}

	res, err := m.run(ctx, TimeoutQuery, nil, "dpkg-query", "-W", "-f=${db:Status-Status}", pkg)
	if err != nil {
		// An unknown package makes dpkg-query exit non-zero, which is the
		// normal "not installed" answer rather than a host failure.
		return false, nil
	}

	return strings.TrimSpace(res.Stdout) == "installed", nil
}

// AvailableVersion returns the candidate version apt would install.
func (m *aptManager) AvailableVersion(ctx context.Context, pkg string) (string, error) {
	if err := ValidatePackageName(pkg); err != nil {
		return "", err
	}

	res, err := m.run(ctx, TimeoutQuery, nil, "apt-cache", "policy", pkg)
	if err != nil {
		return "", err
	}

	candidate := firstFieldAfter(res.Stdout, "Candidate:")
	if candidate == "" || candidate == "(none)" {
		return "", nil
	}

	return candidate, nil
}

// SupportsHold reports that apt can pin a package with apt-mark.
func (m *aptManager) SupportsHold() bool { return true }

// Hold pins a package at its installed version.
func (m *aptManager) Hold(ctx context.Context, pkg string) error {
	if err := ValidatePackageName(pkg); err != nil {
		return err
	}
	_, err := m.run(ctx, TimeoutQuery, aptNonInteractiveEnv, "apt-mark", "hold", pkg)

	return err
}

// Unhold releases a pin.
func (m *aptManager) Unhold(ctx context.Context, pkg string) error {
	if err := ValidatePackageName(pkg); err != nil {
		return err
	}
	_, err := m.run(ctx, TimeoutQuery, aptNonInteractiveEnv, "apt-mark", "unhold", pkg)

	return err
}
