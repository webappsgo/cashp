package hostpkg

import (
	"context"
	"strings"
)

// apkManager drives Alpine hosts. apk is non-interactive by default and is
// additionally run with --no-interactive so a future prompt cannot block a
// transaction, and with --no-cache so no index copy is left behind.
type apkManager struct {
	baseManager
}

// apkCommonFlags are the flags every mutating apk transaction carries.
var apkCommonFlags = []string{"--no-cache", "--no-interactive"}

// Kind identifies the manager.
func (m *apkManager) Kind() ManagerKind { return ManagerAPK }

// Refresh updates the apk index.
func (m *apkManager) Refresh(ctx context.Context) error {
	_, err := m.run(ctx, TimeoutRefresh, nil, "apk", "update", "--no-interactive")

	return err
}

// Install installs only the packages that are missing.
func (m *apkManager) Install(ctx context.Context, pkgs ...string) (InstallResult, error) {
	return idempotentInstall(ctx, m, pkgs, func(ctx context.Context, missing []string) error {
		args := append(append([]string{"add"}, apkCommonFlags...), missing...)
		_, err := m.run(ctx, TimeoutInstall, nil, "apk", args...)

		return err
	})
}

// Remove removes the named packages.
func (m *apkManager) Remove(ctx context.Context, pkgs ...string) error {
	set, err := prepare(pkgs)
	if err != nil {
		return err
	}

	args := append(append([]string{"del"}, apkCommonFlags...), set...)
	_, err = m.run(ctx, TimeoutInstall, nil, "apk", args...)

	return err
}

// Upgrade upgrades the named packages, or every installed package when none
// are named.
func (m *apkManager) Upgrade(ctx context.Context, pkgs ...string) error {
	args := append([]string{"upgrade"}, apkCommonFlags...)
	if len(pkgs) > 0 {
		set, err := prepare(pkgs)
		if err != nil {
			return err
		}
		args = append(args, set...)
	}
	_, err := m.run(ctx, TimeoutInstall, nil, "apk", args...)

	return err
}

// IsInstalled reports whether apk has the package in its installed database.
func (m *apkManager) IsInstalled(ctx context.Context, pkg string) (bool, error) {
	if err := ValidatePackageName(pkg); err != nil {
		return false, err
	}

	res, err := m.run(ctx, TimeoutQuery, nil, "apk", "info", "-e", pkg)
	if err != nil {
		// apk exits non-zero when the package is unknown, which is the
		// normal "not installed" answer.
		return false, nil
	}

	return strings.TrimSpace(res.Stdout) != "", nil
}

// AvailableVersion returns the highest version apk policy reports, which is
// the first indented version line of the policy output.
func (m *apkManager) AvailableVersion(ctx context.Context, pkg string) (string, error) {
	if err := ValidatePackageName(pkg); err != nil {
		return "", err
	}

	res, err := m.run(ctx, TimeoutQuery, nil, "apk", "policy", pkg)
	if err != nil {
		return "", err
	}

	return parseAPKPolicy(res.Stdout), nil
}

// parseAPKPolicy extracts the first version from "apk policy" output, whose
// shape is a package header line followed by indented "version:" lines.
func parseAPKPolicy(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if line == "" || !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasSuffix(trimmed, ":") {
			continue
		}
		version := strings.TrimSuffix(trimmed, ":")
		if version == "" || strings.Contains(version, "/") || strings.Contains(version, " ") {
			continue
		}
		return version
	}

	return ""
}

// SupportsHold reports that apk has no version-hold mechanism.
func (m *apkManager) SupportsHold() bool { return false }

// Hold is refused: apk has no equivalent of apt-mark hold.
func (m *apkManager) Hold(_ context.Context, _ string) error {
	return holdUnsupported(ManagerAPK)
}

// Unhold is refused for the same reason as Hold.
func (m *apkManager) Unhold(_ context.Context, _ string) error {
	return holdUnsupported(ManagerAPK)
}
