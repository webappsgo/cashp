//go:build windows

package service

import (
	"context"
	"strings"
)

// Detect returns the Windows Service Control Manager driver.
func Detect() (Manager, error) {
	if !hasBinary("sc") {
		return nil, ErrUnsupportedPlatform
	}
	return &scManager{}, nil
}

// scManager drives the agent service through the Service Control Manager.
type scManager struct{}

// Name identifies the service manager.
func (m *scManager) Name() string { return "windows-scm" }

// Install registers the service to start automatically and starts it.
func (m *scManager) Install(ctx context.Context, opts Options) error {
	binPath := ExecLineWindows(opts)
	if err := runCommand(ctx, "sc", "create", UnitName,
		"binPath=", binPath,
		"start=", "auto",
		"DisplayName=", Description,
	); err != nil {
		return err
	}
	return runCommand(ctx, "sc", "start", UnitName)
}

// Uninstall stops the service and removes its registration.
func (m *scManager) Uninstall(ctx context.Context) error {
	if !m.registered(ctx) {
		return ErrNotInstalled
	}

	// A service that is already stopped makes this call fail, which must
	// not block deleting the registration.
	_ = runCommand(ctx, "sc", "stop", UnitName)
	return runCommand(ctx, "sc", "delete", UnitName)
}

// Start starts the service.
func (m *scManager) Start(ctx context.Context) error {
	if !m.registered(ctx) {
		return ErrNotInstalled
	}
	return runCommand(ctx, "sc", "start", UnitName)
}

// Stop stops the service.
func (m *scManager) Stop(ctx context.Context) error {
	if !m.registered(ctx) {
		return ErrNotInstalled
	}
	return runCommand(ctx, "sc", "stop", UnitName)
}

// Restart stops and starts the service: the Service Control Manager has no
// single restart verb.
func (m *scManager) Restart(ctx context.Context) error {
	if !m.registered(ctx) {
		return ErrNotInstalled
	}

	// A service that is not running makes the stop fail, which must not
	// prevent starting it.
	_ = runCommand(ctx, "sc", "stop", UnitName)
	return runCommand(ctx, "sc", "start", UnitName)
}

// Status reports installation and runtime state.
func (m *scManager) Status(ctx context.Context) (Status, error) {
	status := Status{Manager: m.Name(), State: StateNotFound}

	// sc exits non-zero for an unregistered service, which is information
	// rather than failure.
	out, err := commandOutput(ctx, "sc", "query", UnitName)
	if err != nil {
		return status, nil
	}
	status.Installed = true

	switch {
	case strings.Contains(out, "RUNNING"), strings.Contains(out, "START_PENDING"):
		status.State = StateRunning
	case strings.Contains(out, "STOPPED"), strings.Contains(out, "STOP_PENDING"):
		status.State = StateStopped
	default:
		status.State = StateUnknown
	}

	config, err := commandOutput(ctx, "sc", "qc", UnitName)
	status.Enabled = err == nil && strings.Contains(config, "AUTO_START")
	return status, nil
}

// registered reports whether the Service Control Manager knows the agent.
func (m *scManager) registered(ctx context.Context) bool {
	_, err := commandOutput(ctx, "sc", "query", UnitName)
	return err == nil
}

// ExecLineWindows renders the service command line in the quoting style the
// Service Control Manager expects, where the whole line is a single value.
func ExecLineWindows(opts Options) string {
	parts := make([]string, 0, len(ExecArgs(opts)))
	for _, arg := range ExecArgs(opts) {
		if strings.ContainsAny(arg, " \t") {
			parts = append(parts, `"`+strings.ReplaceAll(arg, `"`, `\"`)+`"`)
			continue
		}
		parts = append(parts, arg)
	}
	return strings.Join(parts, " ")
}
