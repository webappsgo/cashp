//go:build freebsd

package service

import (
	"context"
	"path/filepath"
	"strings"
)

// rcScriptDir is where FreeBSD rc.d scripts for installed software live.
const rcScriptDir = "/usr/local/etc/rc.d"

// Detect returns the rc.d manager, the native option on FreeBSD.
func Detect() (Manager, error) {
	if !hasBinary("service") || !hasBinary("sysrc") {
		return nil, ErrUnsupportedPlatform
	}
	return &rcManager{}, nil
}

// rcVariable is the rc.conf knob that enables the agent at boot.
var rcVariable = strings.ReplaceAll(UnitName, "-", "_") + "_enable"

// rcManager drives the agent through service(8) and sysrc(8).
type rcManager struct{}

// Name identifies the init system.
func (m *rcManager) Name() string { return "rc.d" }

// scriptPath is the generated rc.d script location.
func (m *rcManager) scriptPath() string {
	return filepath.Join(rcScriptDir, UnitName)
}

// Install writes the rc.d script, enables it in rc.conf and starts it.
func (m *rcManager) Install(ctx context.Context, opts Options) error {
	if err := writeUnit(m.scriptPath(), m.script(opts)); err != nil {
		return err
	}
	if err := setExecutable(m.scriptPath()); err != nil {
		return err
	}
	if err := runCommand(ctx, "sysrc", rcVariable+"=YES"); err != nil {
		return err
	}
	return runCommand(ctx, "service", UnitName, "start")
}

// Uninstall stops the service, clears its rc.conf entry and removes the
// script.
func (m *rcManager) Uninstall(ctx context.Context) error {
	if !fileExists(m.scriptPath()) {
		return ErrNotInstalled
	}

	// A stopped service and an absent rc.conf entry both make these calls
	// fail, which must not block removing the script.
	_ = runCommand(ctx, "service", UnitName, "stop")
	_ = runCommand(ctx, "sysrc", "-x", rcVariable)
	return removeUnit(m.scriptPath())
}

// Start starts the service.
func (m *rcManager) Start(ctx context.Context) error {
	if !fileExists(m.scriptPath()) {
		return ErrNotInstalled
	}
	return runCommand(ctx, "service", UnitName, "start")
}

// Stop stops the service.
func (m *rcManager) Stop(ctx context.Context) error {
	if !fileExists(m.scriptPath()) {
		return ErrNotInstalled
	}
	return runCommand(ctx, "service", UnitName, "stop")
}

// Restart restarts the service.
func (m *rcManager) Restart(ctx context.Context) error {
	if !fileExists(m.scriptPath()) {
		return ErrNotInstalled
	}
	return runCommand(ctx, "service", UnitName, "restart")
}

// Status reports installation and runtime state.
func (m *rcManager) Status(ctx context.Context) (Status, error) {
	status := Status{Manager: m.Name(), State: StateNotFound}
	if !fileExists(m.scriptPath()) {
		return status, nil
	}
	status.Installed = true

	// service(8) exits non-zero for a stopped service, so the output is
	// read regardless of the exit status.
	out, err := commandOutput(ctx, "service", UnitName, "status")
	switch {
	case err == nil && strings.Contains(out, "is running"):
		status.State = StateRunning
	case strings.Contains(out, "is not running"):
		status.State = StateStopped
	case err != nil:
		status.State = StateStopped
	default:
		status.State = StateUnknown
	}

	enabled, err := commandOutput(ctx, "sysrc", "-n", rcVariable)
	status.Enabled = err == nil && strings.EqualFold(strings.TrimSpace(enabled), "YES")
	return status, nil
}

// script renders the rc.d script.
func (m *rcManager) script(opts Options) string {
	args := ExecArgs(opts)
	arguments := ""
	if len(args) > 1 {
		quoted := make([]string, 0, len(args)-1)
		for _, arg := range args[1:] {
			quoted = append(quoted, shellQuote(arg))
		}
		arguments = strings.Join(quoted, " ")
	}

	lines := []string{
		"#!/bin/sh",
		"#",
		"# PROVIDE: " + UnitName,
		"# REQUIRE: NETWORKING",
		"# KEYWORD: shutdown",
		"",
		". /etc/rc.subr",
		"",
		"name=" + shellQuote(strings.ReplaceAll(UnitName, "-", "_")),
		"desc=" + shellQuote(Description),
		"rcvar=" + shellQuote(rcVariable),
		"pidfile=/var/run/" + UnitName + ".pid",
		"command=/usr/sbin/daemon",
		"command_args=" + shellQuote("-P ${pidfile} -r -f "+shellQuote(args[0])+" "+arguments),
		"",
		"load_rc_config $name",
		": ${" + rcVariable + ":=NO}",
		"",
		"run_rc_command \"$1\"",
		"",
	}
	return strings.Join(lines, "\n")
}
