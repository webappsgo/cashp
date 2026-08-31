package hostpkg

import (
	"context"
	"strings"
)

// pacmanManager drives Arch and its derivatives. --noconfirm makes every
// transaction non-interactive and --needed makes an install idempotent even
// before the installed-state check.
type pacmanManager struct {
	baseManager
}

// Kind identifies the manager.
func (m *pacmanManager) Kind() ManagerKind { return ManagerPacman }

// Refresh synchronizes the pacman databases without upgrading packages.
func (m *pacmanManager) Refresh(ctx context.Context) error {
	_, err := m.run(ctx, TimeoutRefresh, nil, "pacman", "-Sy", "--noconfirm")

	return err
}

// Install installs only the packages that are missing.
func (m *pacmanManager) Install(ctx context.Context, pkgs ...string) (InstallResult, error) {
	return idempotentInstall(ctx, m, pkgs, func(ctx context.Context, missing []string) error {
		args := append([]string{"-S", "--needed", "--noconfirm"}, missing...)
		_, err := m.run(ctx, TimeoutInstall, nil, "pacman", args...)

		return err
	})
}

// Remove removes the named packages, leaving their dependencies in place.
func (m *pacmanManager) Remove(ctx context.Context, pkgs ...string) error {
	set, err := prepare(pkgs)
	if err != nil {
		return err
	}

	args := append([]string{"-R", "--noconfirm"}, set...)
	_, err = m.run(ctx, TimeoutInstall, nil, "pacman", args...)

	return err
}

// Upgrade upgrades the named packages, or the whole system when none are
// named. On a rolling release a full -Syu is the supported upgrade path and
// is not a distribution upgrade.
func (m *pacmanManager) Upgrade(ctx context.Context, pkgs ...string) error {
	if len(pkgs) == 0 {
		_, err := m.run(ctx, TimeoutInstall, nil, "pacman", "-Syu", "--noconfirm")

		return err
	}

	set, err := prepare(pkgs)
	if err != nil {
		return err
	}

	args := append([]string{"-S", "--noconfirm"}, set...)
	_, err = m.run(ctx, TimeoutInstall, nil, "pacman", args...)

	return err
}

// IsInstalled queries the local pacman database.
func (m *pacmanManager) IsInstalled(ctx context.Context, pkg string) (bool, error) {
	if err := ValidatePackageName(pkg); err != nil {
		return false, err
	}

	res, err := m.run(ctx, TimeoutQuery, nil, "pacman", "-Q", pkg)
	if err != nil {
		// pacman exits non-zero for a package that is not installed, which
		// is the normal answer rather than a host failure.
		return false, nil
	}

	return strings.TrimSpace(res.Stdout) != "", nil
}

// AvailableVersion returns the version in the synchronized repositories.
func (m *pacmanManager) AvailableVersion(ctx context.Context, pkg string) (string, error) {
	if err := ValidatePackageName(pkg); err != nil {
		return "", err
	}

	res, err := m.run(ctx, TimeoutQuery, nil, "pacman", "-Si", pkg)
	if err != nil {
		return "", err
	}

	return parsePacmanInfoVersion(res.Stdout), nil
}

// parsePacmanInfoVersion extracts the Version field of "pacman -Si" output.
func parsePacmanInfoVersion(output string) string {
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "Version" {
			continue
		}
		return strings.TrimSpace(value)
	}

	return ""
}

// SupportsHold reports that pacman has no per-package hold command: pinning
// requires editing IgnorePkg in pacman.conf, which cashp does not do on the
// operator's behalf.
func (m *pacmanManager) SupportsHold() bool { return false }

// Hold is refused because pacman offers no hold command.
func (m *pacmanManager) Hold(_ context.Context, _ string) error {
	return holdUnsupported(ManagerPacman)
}

// Unhold is refused for the same reason as Hold.
func (m *pacmanManager) Unhold(_ context.Context, _ string) error {
	return holdUnsupported(ManagerPacman)
}
