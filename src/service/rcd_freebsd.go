//go:build freebsd

package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/webappsgo/cashp/src/config"
)

// rcdScriptDir holds FreeBSD rc.d scripts for locally installed software
// (AI.md PART 25 "rc.d (FreeBSD)").
const rcdScriptDir = "/usr/local/etc/rc.d"

// rcdEnableVar is the rc.conf knob that records whether the service starts
// at boot.
const rcdEnableVar = config.InternalName + "_enable"

// rcdManager satisfies Manager.
var _ Manager = (*rcdManager)(nil)

// rcdManager drives service(8) and sysrc(8).
type rcdManager struct {
	data       TemplateData
	scriptPath string
}

// newRCDManager builds an rc.d manager for the system service.
func newRCDManager() *rcdManager {
	return &rcdManager{
		data:       DefaultTemplateData(false),
		scriptPath: filepath.Join(rcdScriptDir, config.InternalName),
	}
}

// Name returns the init system identifier.
func (m *rcdManager) Name() string { return "rc.d" }

// Install writes the rc.d script, enables it in rc.conf and starts it.
func (m *rcdManager) Install(ctx context.Context) error {
	if err := requireElevation("installing the rc.d service"); err != nil {
		return err
	}
	content, err := RenderRCDScript(m.data)
	if err != nil {
		return err
	}
	if err := writeServiceFile(m.scriptPath, content, 0o755); err != nil {
		return err
	}
	if err := m.setEnable(ctx, "YES"); err != nil {
		return err
	}
	return run(ctx, "service", config.InternalName, "start")
}

// Uninstall stops and disables the service, removes the script, then
// deletes all state and the system account after confirmation.
func (m *rcdManager) Uninstall(ctx context.Context, confirmed bool) error {
	if err := requireElevation("uninstalling the rc.d service"); err != nil {
		return err
	}
	if err := confirmUninstall(confirmed); err != nil {
		return err
	}
	// An already-stopped or already-disabled service must not abort the
	// uninstall, so these two results are intentionally ignored.
	_ = run(ctx, "service", config.InternalName, "stop")
	_ = m.setEnable(ctx, "NO")
	if err := removeServiceFile(m.scriptPath); err != nil {
		return err
	}
	return purgeState(ctx, m.data)
}

// Start starts the service.
func (m *rcdManager) Start(ctx context.Context) error {
	if err := requireElevation("starting the rc.d service"); err != nil {
		return err
	}
	return run(ctx, "service", config.InternalName, "start")
}

// Stop stops the service.
func (m *rcdManager) Stop(ctx context.Context) error {
	if err := requireElevation("stopping the rc.d service"); err != nil {
		return err
	}
	return run(ctx, "service", config.InternalName, "stop")
}

// Restart restarts the service.
func (m *rcdManager) Restart(ctx context.Context) error {
	if err := requireElevation("restarting the rc.d service"); err != nil {
		return err
	}
	return run(ctx, "service", config.InternalName, "restart")
}

// Reload signals the running daemon to reread its configuration through the
// script's reload command.
func (m *rcdManager) Reload(ctx context.Context) error {
	if err := requireElevation("reloading the rc.d service"); err != nil {
		return err
	}
	return run(ctx, "service", config.InternalName, "reload")
}

// Enable sets the rc.conf knob so the service starts at boot.
func (m *rcdManager) Enable(ctx context.Context) error {
	if err := requireElevation("enabling the rc.d service"); err != nil {
		return err
	}
	return m.setEnable(ctx, "YES")
}

// Disable stops the service and clears the rc.conf knob, keeping the
// script, data, config and system account intact.
func (m *rcdManager) Disable(ctx context.Context) error {
	if err := requireElevation("disabling the rc.d service"); err != nil {
		return err
	}
	// Stopping an already-stopped service is not an error condition here.
	_ = run(ctx, "service", config.InternalName, "stop")
	return m.setEnable(ctx, "NO")
}

// Status reports installation, runtime and auto-start state.
func (m *rcdManager) Status(ctx context.Context) (Status, error) {
	status := Status{Manager: m.Name(), State: StateStopped}
	if !fileExists(m.scriptPath) {
		return status, nil
	}
	status.Installed = true
	if value, err := output(ctx, "sysrc", "-n", rcdEnableVar); err == nil {
		status.Enabled = strings.EqualFold(strings.TrimSpace(value), "YES")
	}
	if _, err := output(ctx, "service", config.InternalName, "status"); err == nil {
		status.State = StateRunning
		status.PID = readPIDFile(m.data.PIDFile)
	}
	return status, nil
}

// setEnable writes the rc.conf knob through sysrc(8).
func (m *rcdManager) setEnable(ctx context.Context, value string) error {
	if !hasBinary("sysrc") {
		return fmt.Errorf("cannot set %s: sysrc(8) is not available", rcdEnableVar)
	}
	return run(ctx, "sysrc", rcdEnableVar+"="+value)
}
