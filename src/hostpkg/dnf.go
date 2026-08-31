package hostpkg

import (
	"context"
	"strings"
)

// dnfManager drives the RHEL family and Fedora. Every transaction runs with
// --assumeyes and an explicitly disabled prompt, and gpg checking is never
// turned off to make an install succeed.
type dnfManager struct {
	baseManager
}

// dnfCommonFlags are the flags every dnf invocation carries.
var dnfCommonFlags = []string{"--assumeyes", "--setopt=assumeyes=1"}

// Kind identifies the manager.
func (m *dnfManager) Kind() ManagerKind { return ManagerDNF }

// Refresh rebuilds the dnf metadata cache.
func (m *dnfManager) Refresh(ctx context.Context) error {
	args := append(append([]string{}, dnfCommonFlags...), "makecache")
	_, err := m.run(ctx, TimeoutRefresh, nil, "dnf", args...)

	return err
}

// Install installs only the packages that are missing.
func (m *dnfManager) Install(ctx context.Context, pkgs ...string) (InstallResult, error) {
	return idempotentInstall(ctx, m, pkgs, func(ctx context.Context, missing []string) error {
		args := append(append([]string{}, dnfCommonFlags...), "install")
		args = append(args, missing...)
		_, err := m.run(ctx, TimeoutInstall, nil, "dnf", args...)

		return err
	})
}

// Remove removes the named packages.
func (m *dnfManager) Remove(ctx context.Context, pkgs ...string) error {
	set, err := prepare(pkgs)
	if err != nil {
		return err
	}

	args := append(append([]string{}, dnfCommonFlags...), "remove")
	args = append(args, set...)
	_, err = m.run(ctx, TimeoutInstall, nil, "dnf", args...)

	return err
}

// Upgrade upgrades the named packages, or every installed package when none
// are named. It never runs system-upgrade or distro-sync.
func (m *dnfManager) Upgrade(ctx context.Context, pkgs ...string) error {
	args := append(append([]string{}, dnfCommonFlags...), "upgrade")
	if len(pkgs) > 0 {
		set, err := prepare(pkgs)
		if err != nil {
			return err
		}
		args = append(args, set...)
	}
	_, err := m.run(ctx, TimeoutInstall, nil, "dnf", args...)

	return err
}

// IsInstalled queries the rpm database directly.
func (m *dnfManager) IsInstalled(ctx context.Context, pkg string) (bool, error) {
	if err := ValidatePackageName(pkg); err != nil {
		return false, err
	}

	res, err := m.run(ctx, TimeoutQuery, nil, "rpm", "--query", pkg)
	if err != nil {
		// rpm exits non-zero when the package is not installed, which is the
		// normal answer rather than a host failure.
		return false, nil
	}

	return strings.TrimSpace(res.Stdout) != "", nil
}

// AvailableVersion returns the newest version the enabled repositories offer.
func (m *dnfManager) AvailableVersion(ctx context.Context, pkg string) (string, error) {
	if err := ValidatePackageName(pkg); err != nil {
		return "", err
	}

	res, err := m.run(ctx, TimeoutQuery, nil, "dnf", "--quiet", "repoquery",
		"--queryformat=%{version}-%{release}", "--latest-limit=1", pkg)
	if err != nil {
		return "", err
	}

	for _, line := range strings.Split(res.Stdout, "\n") {
		if v := strings.TrimSpace(line); v != "" {
			return v, nil
		}
	}

	return "", nil
}

// SupportsHold reports that dnf can pin a version through versionlock.
func (m *dnfManager) SupportsHold() bool { return true }

// Hold pins a package at its installed version using the versionlock plugin.
func (m *dnfManager) Hold(ctx context.Context, pkg string) error {
	if err := ValidatePackageName(pkg); err != nil {
		return err
	}

	args := append(append([]string{}, dnfCommonFlags...), "versionlock", "add", pkg)
	_, err := m.run(ctx, TimeoutQuery, nil, "dnf", args...)

	return err
}

// Unhold releases a versionlock pin.
func (m *dnfManager) Unhold(ctx context.Context, pkg string) error {
	if err := ValidatePackageName(pkg); err != nil {
		return err
	}

	args := append(append([]string{}, dnfCommonFlags...), "versionlock", "delete", pkg)
	_, err := m.run(ctx, TimeoutQuery, nil, "dnf", args...)

	return err
}
