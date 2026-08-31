//go:build windows

package service

import (
	"context"
	"strconv"
	"strings"

	"github.com/webappsgo/cashp/src/config"
)

// windowsServiceName is the service control manager key of the service.
const windowsServiceName = config.InternalName

// windowsManager satisfies Manager.
var _ Manager = (*windowsManager)(nil)

// windowsManager drives the service control manager through sc.exe. It
// deliberately uses no third-party bindings so the binary stays dependency
// free and CGO-less.
//
// The service runs as the Virtual Service Account NT SERVICE\cashp, which
// Windows creates and manages automatically. That account already carries
// minimal privileges, so no privilege drop is needed on Windows even though
// the Unix build runs permanently as root (AI.md PART 25 "Windows
// Service").
type windowsManager struct {
	data TemplateData
}

// newWindowsManager builds a Windows service manager.
func newWindowsManager() *windowsManager {
	return &windowsManager{data: DefaultTemplateData(false)}
}

// Name returns the init system identifier.
func (m *windowsManager) Name() string { return "windows-service" }

// Install registers the service with automatic start under the Virtual
// Service Account and starts it.
func (m *windowsManager) Install(ctx context.Context) error {
	if err := requireElevation("installing the Windows service"); err != nil {
		return err
	}
	if err := run(ctx, "sc.exe", "create", windowsServiceName,
		"binPath=", m.data.BinaryPath,
		"start=", "auto",
		"obj=", virtualServiceAccount,
		"DisplayName=", m.data.DisplayName); err != nil {
		return err
	}
	if err := run(ctx, "sc.exe", "description", windowsServiceName, m.data.Description); err != nil {
		return err
	}
	return run(ctx, "sc.exe", "start", windowsServiceName)
}

// Uninstall stops and deletes the service, then removes all state after
// confirmation. Deleting the service also removes its Virtual Service
// Account.
func (m *windowsManager) Uninstall(ctx context.Context, confirmed bool) error {
	if err := requireElevation("uninstalling the Windows service"); err != nil {
		return err
	}
	if err := confirmUninstall(confirmed); err != nil {
		return err
	}
	// An already-stopped service must not abort the uninstall.
	_ = run(ctx, "sc.exe", "stop", windowsServiceName)
	if err := run(ctx, "sc.exe", "delete", windowsServiceName); err != nil {
		return err
	}
	return purgeState(ctx, m.data)
}

// Start starts the service.
func (m *windowsManager) Start(ctx context.Context) error {
	if err := requireElevation("starting the Windows service"); err != nil {
		return err
	}
	return run(ctx, "sc.exe", "start", windowsServiceName)
}

// Stop stops the service.
func (m *windowsManager) Stop(ctx context.Context) error {
	if err := requireElevation("stopping the Windows service"); err != nil {
		return err
	}
	return run(ctx, "sc.exe", "stop", windowsServiceName)
}

// Restart stops and starts the service.
func (m *windowsManager) Restart(ctx context.Context) error {
	if err := requireElevation("restarting the Windows service"); err != nil {
		return err
	}
	// A service that is not currently running still restarts cleanly.
	_ = run(ctx, "sc.exe", "stop", windowsServiceName)
	return run(ctx, "sc.exe", "start", windowsServiceName)
}

// Reload restarts the service. Windows has no signal equivalent to SIGHUP,
// so a stop/start cycle is the supported way to pick up a changed
// configuration.
func (m *windowsManager) Reload(ctx context.Context) error {
	return m.Restart(ctx)
}

// Enable sets the start type back to automatic.
func (m *windowsManager) Enable(ctx context.Context) error {
	if err := requireElevation("enabling the Windows service"); err != nil {
		return err
	}
	return run(ctx, "sc.exe", "config", windowsServiceName, "start=", "auto")
}

// Disable stops the service and sets its start type to disabled, keeping
// the registration, data and config intact.
func (m *windowsManager) Disable(ctx context.Context) error {
	if err := requireElevation("disabling the Windows service"); err != nil {
		return err
	}
	// Stopping an already-stopped service is not an error condition here.
	_ = run(ctx, "sc.exe", "stop", windowsServiceName)
	return run(ctx, "sc.exe", "config", windowsServiceName, "start=", "disabled")
}

// Status reports installation, runtime and auto-start state.
func (m *windowsManager) Status(ctx context.Context) (Status, error) {
	status := Status{Manager: m.Name(), State: StateStopped}
	out, err := output(ctx, "sc.exe", "queryex", windowsServiceName)
	if err != nil {
		return status, nil
	}
	status.Installed = true
	if strings.Contains(scField(out, "STATE"), "RUNNING") {
		status.State = StateRunning
		if pid, convErr := strconv.Atoi(scField(out, "PID")); convErr == nil && pid > 0 {
			status.PID = pid
		}
	}
	if qc, qcErr := output(ctx, "sc.exe", "qc", windowsServiceName); qcErr == nil {
		startType := scField(qc, "START_TYPE")
		status.Enabled = strings.Contains(startType, "AUTO_START") || strings.Contains(startType, "DEMAND_START")
	}
	return status, nil
}

// scField extracts the value of a `NAME : VALUE` field from sc.exe output.
func scField(out, field string) string {
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, field) {
			continue
		}
		colon := strings.IndexByte(trimmed, ':')
		if colon < 0 {
			continue
		}
		return strings.TrimSpace(trimmed[colon+1:])
	}
	return ""
}
