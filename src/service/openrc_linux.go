//go:build linux

package service

import (
	"context"
	"path/filepath"

	"github.com/webappsgo/cashp/src/config"
)

// initScriptDir holds the OpenRC and SysVinit service scripts. Only one of
// the two init systems is ever installed on a given host (AI.md PART 25).
const initScriptDir = "/etc/init.d"

// openrcRunlevelDir holds the symlinks of the default runlevel, which is
// how OpenRC records that a service starts at boot.
const openrcRunlevelDir = "/etc/runlevels/default"

// openrcManager satisfies Manager.
var _ Manager = (*openrcManager)(nil)

// openrcManager drives rc-service and rc-update.
type openrcManager struct {
	data       TemplateData
	scriptPath string
}

// newOpenRCManager builds an OpenRC manager for the system service.
func newOpenRCManager() *openrcManager {
	return &openrcManager{
		data:       DefaultTemplateData(false),
		scriptPath: filepath.Join(initScriptDir, config.InternalName),
	}
}

// Name returns the init system identifier.
func (m *openrcManager) Name() string { return "openrc" }

// Install writes the init script, adds it to the default runlevel and
// starts it.
func (m *openrcManager) Install(ctx context.Context) error {
	if err := requireElevation("installing the OpenRC service"); err != nil {
		return err
	}
	content, err := RenderOpenRCScript(m.data)
	if err != nil {
		return err
	}
	if err := writeServiceFile(m.scriptPath, content, 0o755); err != nil {
		return err
	}
	if err := run(ctx, "rc-update", "add", config.InternalName, "default"); err != nil {
		return err
	}
	return run(ctx, "rc-service", config.InternalName, "start")
}

// Uninstall stops and removes the service, then deletes all state and the
// system account after confirmation.
func (m *openrcManager) Uninstall(ctx context.Context, confirmed bool) error {
	if err := requireElevation("uninstalling the OpenRC service"); err != nil {
		return err
	}
	if err := confirmUninstall(confirmed); err != nil {
		return err
	}
	// An already-stopped or already-removed service must not abort the
	// uninstall, so these two results are intentionally ignored.
	_ = run(ctx, "rc-service", config.InternalName, "stop")
	_ = run(ctx, "rc-update", "del", config.InternalName, "default")
	if err := removeServiceFile(m.scriptPath); err != nil {
		return err
	}
	return purgeState(ctx, m.data)
}

// Start starts the service.
func (m *openrcManager) Start(ctx context.Context) error {
	if err := requireElevation("starting the OpenRC service"); err != nil {
		return err
	}
	return run(ctx, "rc-service", config.InternalName, "start")
}

// Stop stops the service.
func (m *openrcManager) Stop(ctx context.Context) error {
	if err := requireElevation("stopping the OpenRC service"); err != nil {
		return err
	}
	return run(ctx, "rc-service", config.InternalName, "stop")
}

// Restart restarts the service.
func (m *openrcManager) Restart(ctx context.Context) error {
	if err := requireElevation("restarting the OpenRC service"); err != nil {
		return err
	}
	return run(ctx, "rc-service", config.InternalName, "restart")
}

// Reload signals the running daemon to reread its configuration through the
// script's reload command.
func (m *openrcManager) Reload(ctx context.Context) error {
	if err := requireElevation("reloading the OpenRC service"); err != nil {
		return err
	}
	return run(ctx, "rc-service", config.InternalName, "reload")
}

// Enable adds the service to the default runlevel.
func (m *openrcManager) Enable(ctx context.Context) error {
	if err := requireElevation("enabling the OpenRC service"); err != nil {
		return err
	}
	return run(ctx, "rc-update", "add", config.InternalName, "default")
}

// Disable stops the service and removes it from the default runlevel,
// keeping the script, data, config and system account intact.
func (m *openrcManager) Disable(ctx context.Context) error {
	if err := requireElevation("disabling the OpenRC service"); err != nil {
		return err
	}
	// Stopping an already-stopped service is not an error condition here.
	_ = run(ctx, "rc-service", config.InternalName, "stop")
	return run(ctx, "rc-update", "del", config.InternalName, "default")
}

// Status reports installation, runtime and auto-start state.
func (m *openrcManager) Status(ctx context.Context) (Status, error) {
	status := Status{Manager: m.Name(), State: StateStopped}
	if !fileExists(m.scriptPath) {
		return status, nil
	}
	status.Installed = true
	status.Enabled = fileExists(filepath.Join(openrcRunlevelDir, config.InternalName))
	if _, err := output(ctx, "rc-service", config.InternalName, "status"); err == nil {
		status.State = StateRunning
		status.PID = readPIDFile(m.data.PIDFile)
	}
	return status, nil
}
