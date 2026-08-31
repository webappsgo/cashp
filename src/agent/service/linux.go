//go:build linux

package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// systemdUnitDir is where generated system units are written.
const systemdUnitDir = "/etc/systemd/system"

// openRCScriptDir is where OpenRC init scripts live.
const openRCScriptDir = "/etc/init.d"

// Detect returns the manager for the init system running on this host,
// checked in the order AI.md documents for the server: systemd first, then
// OpenRC.
func Detect() (Manager, error) {
	switch {
	case hasSystemd():
		return &systemdManager{}, nil
	case hasOpenRC():
		return &openRCManager{}, nil
	default:
		return nil, ErrUnsupportedPlatform
	}
}

// hasSystemd reports whether systemd is the running init system, not merely
// installed.
func hasSystemd() bool {
	return hasBinary("systemctl") && fileExists("/run/systemd/system")
}

// hasOpenRC reports whether OpenRC drives this host.
func hasOpenRC() bool {
	return hasBinary("rc-service") && (fileExists("/sbin/openrc-run") || fileExists("/usr/sbin/openrc-run"))
}

// systemdManager drives the agent unit through systemctl.
type systemdManager struct{}

// Name identifies the init system.
func (m *systemdManager) Name() string { return "systemd" }

// unitPath is the generated unit file location.
func (m *systemdManager) unitPath() string {
	return filepath.Join(systemdUnitDir, UnitName+".service")
}

// Install writes the unit, reloads systemd and enables plus starts it.
func (m *systemdManager) Install(ctx context.Context, opts Options) error {
	if err := writeUnit(m.unitPath(), m.unitFile(opts)); err != nil {
		return err
	}
	if err := runCommand(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	return runCommand(ctx, "systemctl", "enable", "--now", UnitName)
}

// Uninstall stops and disables the unit, then removes the definition.
func (m *systemdManager) Uninstall(ctx context.Context) error {
	if !fileExists(m.unitPath()) {
		return ErrNotInstalled
	}

	// A unit that is already stopped or was never enabled makes these calls
	// fail, which must not block removing the file.
	_ = runCommand(ctx, "systemctl", "disable", "--now", UnitName)
	if err := removeUnit(m.unitPath()); err != nil {
		return err
	}
	return runCommand(ctx, "systemctl", "daemon-reload")
}

// Start starts the unit.
func (m *systemdManager) Start(ctx context.Context) error {
	if !fileExists(m.unitPath()) {
		return ErrNotInstalled
	}
	return runCommand(ctx, "systemctl", "start", UnitName)
}

// Stop stops the unit.
func (m *systemdManager) Stop(ctx context.Context) error {
	if !fileExists(m.unitPath()) {
		return ErrNotInstalled
	}
	return runCommand(ctx, "systemctl", "stop", UnitName)
}

// Restart restarts the unit.
func (m *systemdManager) Restart(ctx context.Context) error {
	if !fileExists(m.unitPath()) {
		return ErrNotInstalled
	}
	return runCommand(ctx, "systemctl", "restart", UnitName)
}

// Status reports installation and runtime state.
func (m *systemdManager) Status(ctx context.Context) (Status, error) {
	status := Status{Manager: m.Name(), State: StateNotFound}
	if !fileExists(m.unitPath()) {
		return status, nil
	}
	status.Installed = true

	// systemctl exits non-zero for inactive and disabled units, which is
	// information rather than failure, so the output is read either way.
	active, _ := commandOutput(ctx, "systemctl", "is-active", UnitName)
	switch strings.TrimSpace(active) {
	case "active", "activating":
		status.State = StateRunning
	case "":
		status.State = StateUnknown
	default:
		status.State = StateStopped
	}

	enabled, _ := commandOutput(ctx, "systemctl", "is-enabled", UnitName)
	status.Enabled = strings.TrimSpace(enabled) == "enabled"
	return status, nil
}

// unitFile renders the systemd unit. The agent needs full system access to
// collect accurate metrics, so it runs as root, but everything that can be
// restricted without blinding it is.
func (m *systemdManager) unitFile(opts Options) string {
	lines := []string{
		"[Unit]",
		"Description=" + Description,
		"After=network-online.target",
		"Wants=network-online.target",
		"",
		"[Service]",
		"Type=simple",
		"ExecStart=" + ExecLine(opts),
		"Restart=always",
		"RestartSec=10",
		"User=root",
		"NoNewPrivileges=yes",
		"PrivateTmp=yes",
		"ProtectHome=read-only",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}
	return strings.Join(lines, "\n")
}

// openRCManager drives the agent service through rc-service and rc-update.
type openRCManager struct{}

// Name identifies the init system.
func (m *openRCManager) Name() string { return "openrc" }

// scriptPath is the generated init script location.
func (m *openRCManager) scriptPath() string {
	return filepath.Join(openRCScriptDir, UnitName)
}

// Install writes the init script, adds it to the default runlevel and
// starts it.
func (m *openRCManager) Install(ctx context.Context, opts Options) error {
	if err := writeUnit(m.scriptPath(), m.script(opts)); err != nil {
		return err
	}
	if err := setExecutable(m.scriptPath()); err != nil {
		return err
	}
	if err := runCommand(ctx, "rc-update", "add", UnitName, "default"); err != nil {
		return err
	}
	return runCommand(ctx, "rc-service", UnitName, "start")
}

// Uninstall stops the service, removes it from the runlevel and deletes the
// init script.
func (m *openRCManager) Uninstall(ctx context.Context) error {
	if !fileExists(m.scriptPath()) {
		return ErrNotInstalled
	}

	// A stopped service and an unregistered runlevel entry both make these
	// calls fail, which must not block removing the script.
	_ = runCommand(ctx, "rc-service", UnitName, "stop")
	_ = runCommand(ctx, "rc-update", "del", UnitName, "default")
	return removeUnit(m.scriptPath())
}

// Start starts the service.
func (m *openRCManager) Start(ctx context.Context) error {
	if !fileExists(m.scriptPath()) {
		return ErrNotInstalled
	}
	return runCommand(ctx, "rc-service", UnitName, "start")
}

// Stop stops the service.
func (m *openRCManager) Stop(ctx context.Context) error {
	if !fileExists(m.scriptPath()) {
		return ErrNotInstalled
	}
	return runCommand(ctx, "rc-service", UnitName, "stop")
}

// Restart restarts the service.
func (m *openRCManager) Restart(ctx context.Context) error {
	if !fileExists(m.scriptPath()) {
		return ErrNotInstalled
	}
	return runCommand(ctx, "rc-service", UnitName, "restart")
}

// Status reports installation and runtime state.
func (m *openRCManager) Status(ctx context.Context) (Status, error) {
	status := Status{Manager: m.Name(), State: StateNotFound}
	if !fileExists(m.scriptPath()) {
		return status, nil
	}
	status.Installed = true

	// rc-service exits non-zero for a stopped service, so the output is
	// read regardless of the exit status.
	out, _ := commandOutput(ctx, "rc-service", UnitName, "status")
	switch {
	case strings.Contains(out, "started"):
		status.State = StateRunning
	case strings.Contains(out, "stopped") || strings.Contains(out, "crashed"):
		status.State = StateStopped
	default:
		status.State = StateUnknown
	}

	levels, _ := commandOutput(ctx, "rc-update", "show", "default")
	status.Enabled = strings.Contains(levels, UnitName)
	return status, nil
}

// script renders the OpenRC init script.
func (m *openRCManager) script(opts Options) string {
	args := ExecArgs(opts)
	command := shellQuote(args[0])
	arguments := ""
	if len(args) > 1 {
		quoted := make([]string, 0, len(args)-1)
		for _, arg := range args[1:] {
			quoted = append(quoted, shellQuote(arg))
		}
		arguments = strings.Join(quoted, " ")
	}

	lines := []string{
		"#!/sbin/openrc-run",
		"",
		fmt.Sprintf("description=%q", Description),
		"command=" + command,
		"command_args=" + shellQuote(arguments),
		"command_background=true",
		"pidfile=/run/" + UnitName + ".pid",
		"respawn_delay=10",
		"",
		"depend() {",
		"\tneed net",
		"}",
		"",
	}
	return strings.Join(lines, "\n")
}
