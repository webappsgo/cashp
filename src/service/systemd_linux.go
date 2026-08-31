//go:build linux

package service

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/webappsgo/cashp/src/config"
)

// systemdUnitName is the unit file name of the cashp service.
const systemdUnitName = config.InternalName + ".service"

// systemdSystemUnitDir is where system units are installed (AI.md PART 25
// "systemd (Linux)").
const systemdSystemUnitDir = "/etc/systemd/system"

// systemdManager satisfies Manager.
var _ Manager = (*systemdManager)(nil)

// systemdManager drives systemctl for a system or per-user unit.
type systemdManager struct {
	data     TemplateData
	unitPath string
	userMode bool
}

// newSystemdManager builds a systemd manager for the system unit, or for
// the per-user fallback unit when the caller is not root.
func newSystemdManager(userMode bool) *systemdManager {
	unitPath := filepath.Join(systemdSystemUnitDir, systemdUnitName)
	if userMode {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		unitPath = filepath.Join(home, ".config", "systemd", "user", systemdUnitName)
	}
	return &systemdManager{
		data:     DefaultTemplateData(userMode),
		unitPath: unitPath,
		userMode: userMode,
	}
}

// Name returns the init system identifier.
func (m *systemdManager) Name() string {
	if m.userMode {
		return "systemd (user)"
	}
	return "systemd"
}

// systemctl runs systemctl against the right scope.
func (m *systemdManager) systemctl(ctx context.Context, args ...string) error {
	return run(ctx, "systemctl", m.args(args)...)
}

// systemctlOutput runs systemctl and returns its trimmed output.
func (m *systemdManager) systemctlOutput(ctx context.Context, args ...string) (string, error) {
	return output(ctx, "systemctl", m.args(args)...)
}

// args prefixes --user for per-user units.
func (m *systemdManager) args(args []string) []string {
	if m.userMode {
		return append([]string{"--user"}, args...)
	}
	return args
}

// gate enforces elevation for system-scope operations.
func (m *systemdManager) gate(action string) error {
	if m.userMode {
		return nil
	}
	return requireElevation(action)
}

// Install writes the unit, reloads systemd, enables and starts the service.
func (m *systemdManager) Install(ctx context.Context) error {
	if err := m.gate("installing the systemd service"); err != nil {
		return err
	}
	content, err := RenderSystemdUnit(m.data)
	if err != nil {
		return err
	}
	if err := writeServiceFile(m.unitPath, content, 0o644); err != nil {
		return err
	}
	if err := m.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err := m.systemctl(ctx, "enable", systemdUnitName); err != nil {
		return err
	}
	return m.systemctl(ctx, "start", systemdUnitName)
}

// Uninstall stops and disables the service, removes the unit, and deletes
// all state and the system account after confirmation.
func (m *systemdManager) Uninstall(ctx context.Context, confirmed bool) error {
	if err := m.gate("uninstalling the systemd service"); err != nil {
		return err
	}
	if err := confirmUninstall(confirmed); err != nil {
		return err
	}
	// A service that is already stopped or disabled must not abort the
	// uninstall, so these two results are intentionally ignored.
	_ = m.systemctl(ctx, "stop", systemdUnitName)
	_ = m.systemctl(ctx, "disable", systemdUnitName)
	if err := removeServiceFile(m.unitPath); err != nil {
		return err
	}
	if err := m.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	return purgeState(ctx, m.data)
}

// Start starts the service.
func (m *systemdManager) Start(ctx context.Context) error {
	if err := m.gate("starting the systemd service"); err != nil {
		return err
	}
	return m.systemctl(ctx, "start", systemdUnitName)
}

// Stop stops the service.
func (m *systemdManager) Stop(ctx context.Context) error {
	if err := m.gate("stopping the systemd service"); err != nil {
		return err
	}
	return m.systemctl(ctx, "stop", systemdUnitName)
}

// Restart restarts the service.
func (m *systemdManager) Restart(ctx context.Context) error {
	if err := m.gate("restarting the systemd service"); err != nil {
		return err
	}
	return m.systemctl(ctx, "restart", systemdUnitName)
}

// Reload reloads the configuration through the unit's ExecReload without a
// restart.
func (m *systemdManager) Reload(ctx context.Context) error {
	if err := m.gate("reloading the systemd service"); err != nil {
		return err
	}
	return m.systemctl(ctx, "reload", systemdUnitName)
}

// Enable turns on automatic start at boot.
func (m *systemdManager) Enable(ctx context.Context) error {
	if err := m.gate("enabling the systemd service"); err != nil {
		return err
	}
	return m.systemctl(ctx, "enable", systemdUnitName)
}

// Disable stops the service and removes it from automatic start, keeping
// the unit file, data, config and system account intact.
func (m *systemdManager) Disable(ctx context.Context) error {
	if err := m.gate("disabling the systemd service"); err != nil {
		return err
	}
	// Stopping an already-stopped service is not an error condition here.
	_ = m.systemctl(ctx, "stop", systemdUnitName)
	return m.systemctl(ctx, "disable", systemdUnitName)
}

// Status reports installation, runtime and auto-start state.
func (m *systemdManager) Status(ctx context.Context) (Status, error) {
	status := Status{Manager: m.Name(), UserMode: m.userMode, State: StateStopped}
	if !fileExists(m.unitPath) {
		return status, nil
	}
	status.Installed = true

	// systemctl signals state through both output and exit status; the
	// output alone is authoritative here.
	active, _ := m.systemctlOutput(ctx, "is-active", systemdUnitName)
	if active == "active" || active == "activating" {
		status.State = StateRunning
	}
	enabled, _ := m.systemctlOutput(ctx, "is-enabled", systemdUnitName)
	status.Enabled = strings.HasPrefix(enabled, "enabled")

	mainPID, err := m.systemctlOutput(ctx, "show", "-p", "MainPID", "--value", systemdUnitName)
	if err == nil {
		if pid, convErr := strconv.Atoi(mainPID); convErr == nil && pid > 0 {
			status.PID = pid
		}
	}
	return status, nil
}
