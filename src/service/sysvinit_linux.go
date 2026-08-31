//go:build linux

package service

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/webappsgo/cashp/src/config"
)

// sysvRunlevelGlob matches the boot-time start symlinks that record whether
// a SysVinit service is enabled.
const sysvRunlevelGlob = "/etc/rc[2-5].d/S*" + config.InternalName

// sysvinitManager satisfies Manager.
var _ Manager = (*sysvinitManager)(nil)

// sysvinitManager drives a classic /etc/init.d script plus update-rc.d or
// chkconfig for boot registration.
type sysvinitManager struct {
	data       TemplateData
	scriptPath string
}

// newSysVInitManager builds a SysVinit manager for the system service.
func newSysVInitManager() *sysvinitManager {
	return &sysvinitManager{
		data:       DefaultTemplateData(false),
		scriptPath: filepath.Join(initScriptDir, config.InternalName),
	}
}

// Name returns the init system identifier.
func (m *sysvinitManager) Name() string { return "sysvinit" }

// script runs the init script with a verb.
func (m *sysvinitManager) script(ctx context.Context, verb string) error {
	if !fileExists(m.scriptPath) {
		return ErrNotInstalled
	}
	return run(ctx, m.scriptPath, verb)
}

// Install writes the init script, registers it for boot and starts it.
func (m *sysvinitManager) Install(ctx context.Context) error {
	if err := requireElevation("installing the SysVinit service"); err != nil {
		return err
	}
	content, err := RenderSysVInitScript(m.data)
	if err != nil {
		return err
	}
	if err := writeServiceFile(m.scriptPath, content, 0o755); err != nil {
		return err
	}
	if err := m.enableBoot(ctx); err != nil {
		return err
	}
	return m.script(ctx, "start")
}

// Uninstall stops and deregisters the service, removes the script, then
// deletes all state and the system account after confirmation.
func (m *sysvinitManager) Uninstall(ctx context.Context, confirmed bool) error {
	if err := requireElevation("uninstalling the SysVinit service"); err != nil {
		return err
	}
	if err := confirmUninstall(confirmed); err != nil {
		return err
	}
	// An already-stopped or already-deregistered service must not abort the
	// uninstall, so these two results are intentionally ignored.
	_ = m.script(ctx, "stop")
	_ = m.removeBoot(ctx)
	if err := removeServiceFile(m.scriptPath); err != nil {
		return err
	}
	return purgeState(ctx, m.data)
}

// Start starts the service.
func (m *sysvinitManager) Start(ctx context.Context) error {
	if err := requireElevation("starting the SysVinit service"); err != nil {
		return err
	}
	return m.script(ctx, "start")
}

// Stop stops the service.
func (m *sysvinitManager) Stop(ctx context.Context) error {
	if err := requireElevation("stopping the SysVinit service"); err != nil {
		return err
	}
	return m.script(ctx, "stop")
}

// Restart restarts the service.
func (m *sysvinitManager) Restart(ctx context.Context) error {
	if err := requireElevation("restarting the SysVinit service"); err != nil {
		return err
	}
	return m.script(ctx, "restart")
}

// Reload signals the running daemon to reread its configuration.
func (m *sysvinitManager) Reload(ctx context.Context) error {
	if err := requireElevation("reloading the SysVinit service"); err != nil {
		return err
	}
	return m.script(ctx, "reload")
}

// Enable registers the service for boot.
func (m *sysvinitManager) Enable(ctx context.Context) error {
	if err := requireElevation("enabling the SysVinit service"); err != nil {
		return err
	}
	return m.enableBoot(ctx)
}

// Disable stops the service and removes it from boot registration, keeping
// the script, data, config and system account intact.
func (m *sysvinitManager) Disable(ctx context.Context) error {
	if err := requireElevation("disabling the SysVinit service"); err != nil {
		return err
	}
	// Stopping an already-stopped service is not an error condition here.
	_ = m.script(ctx, "stop")
	return m.disableBoot(ctx)
}

// Status reports installation, runtime and auto-start state.
func (m *sysvinitManager) Status(ctx context.Context) (Status, error) {
	status := Status{Manager: m.Name(), State: StateStopped}
	if !fileExists(m.scriptPath) {
		return status, nil
	}
	status.Installed = true
	links, err := filepath.Glob(sysvRunlevelGlob)
	status.Enabled = err == nil && len(links) > 0
	if _, runErr := output(ctx, m.scriptPath, "status"); runErr == nil {
		status.State = StateRunning
		status.PID = readPIDFile(m.data.PIDFile)
	}
	return status, nil
}

// enableBoot registers the service with whichever boot manager the host
// provides.
func (m *sysvinitManager) enableBoot(ctx context.Context) error {
	switch {
	case hasBinary("update-rc.d"):
		return run(ctx, "update-rc.d", config.InternalName, "defaults")
	case hasBinary("chkconfig"):
		if err := run(ctx, "chkconfig", "--add", config.InternalName); err != nil {
			return err
		}
		return run(ctx, "chkconfig", config.InternalName, "on")
	default:
		return fmt.Errorf("cannot enable %s at boot: neither update-rc.d nor chkconfig is available", config.InternalName)
	}
}

// disableBoot removes the boot registration while keeping the script.
func (m *sysvinitManager) disableBoot(ctx context.Context) error {
	switch {
	case hasBinary("update-rc.d"):
		return run(ctx, "update-rc.d", config.InternalName, "disable")
	case hasBinary("chkconfig"):
		return run(ctx, "chkconfig", config.InternalName, "off")
	default:
		return fmt.Errorf("cannot disable %s at boot: neither update-rc.d nor chkconfig is available", config.InternalName)
	}
}

// removeBoot deletes the boot registration entirely, used during uninstall.
func (m *sysvinitManager) removeBoot(ctx context.Context) error {
	switch {
	case hasBinary("update-rc.d"):
		return run(ctx, "update-rc.d", "-f", config.InternalName, "remove")
	case hasBinary("chkconfig"):
		return run(ctx, "chkconfig", "--del", config.InternalName)
	default:
		return fmt.Errorf("cannot deregister %s from boot: neither update-rc.d nor chkconfig is available", config.InternalName)
	}
}
