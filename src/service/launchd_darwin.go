//go:build darwin

package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// launchdDaemonDir holds system-wide launchd daemons; launchdAgentSubdir is
// the per-user equivalent inside the caller's home directory (AI.md PART 25
// "launchd (macOS)").
const (
	launchdDaemonDir   = "/Library/LaunchDaemons"
	launchdAgentSubdir = "Library/LaunchAgents"
)

// launchdManager satisfies Manager.
var _ Manager = (*launchdManager)(nil)

// launchdManager drives launchctl for a system daemon or a user agent.
type launchdManager struct {
	data      TemplateData
	plistPath string
	userMode  bool
}

// newLaunchdManager builds a launchd manager for the system daemon, or for
// the per-user agent fallback when the caller is not root.
func newLaunchdManager(userMode bool) *launchdManager {
	plistPath := filepath.Join(launchdDaemonDir, PlistName+".plist")
	if userMode {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		plistPath = filepath.Join(home, launchdAgentSubdir, PlistName+".plist")
	}
	return &launchdManager{
		data:      DefaultTemplateData(userMode),
		plistPath: plistPath,
		userMode:  userMode,
	}
}

// Name returns the init system identifier.
func (m *launchdManager) Name() string {
	if m.userMode {
		return "launchd (agent)"
	}
	return "launchd"
}

// gate enforces elevation for system-scope operations.
func (m *launchdManager) gate(action string) error {
	if m.userMode {
		return nil
	}
	return requireElevation(action)
}

// domainTarget is the launchctl service target for signal delivery.
func (m *launchdManager) domainTarget() string {
	if m.userMode {
		return fmt.Sprintf("gui/%d/%s", os.Getuid(), PlistName)
	}
	return "system/" + PlistName
}

// Install writes the plist and loads it so it starts now and at boot.
func (m *launchdManager) Install(ctx context.Context) error {
	if err := m.gate("installing the launchd service"); err != nil {
		return err
	}
	content, err := RenderLaunchdPlist(m.data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.data.LogDir, stateDirMode); err != nil {
		return fmt.Errorf("create %s: %w", m.data.LogDir, err)
	}
	if err := writeServiceFile(m.plistPath, content, 0o644); err != nil {
		return err
	}
	return run(ctx, "launchctl", "load", "-w", m.plistPath)
}

// Uninstall unloads and removes the plist, then deletes all state and the
// service account after confirmation.
func (m *launchdManager) Uninstall(ctx context.Context, confirmed bool) error {
	if err := m.gate("uninstalling the launchd service"); err != nil {
		return err
	}
	if err := confirmUninstall(confirmed); err != nil {
		return err
	}
	// An already-unloaded service must not abort the uninstall.
	_ = run(ctx, "launchctl", "unload", "-w", m.plistPath)
	if err := removeServiceFile(m.plistPath); err != nil {
		return err
	}
	return purgeState(ctx, m.data)
}

// Start starts the service, loading the plist first when needed.
func (m *launchdManager) Start(ctx context.Context) error {
	if err := m.gate("starting the launchd service"); err != nil {
		return err
	}
	if !fileExists(m.plistPath) {
		return ErrNotInstalled
	}
	if !m.loaded(ctx) {
		if err := run(ctx, "launchctl", "load", m.plistPath); err != nil {
			return err
		}
	}
	return run(ctx, "launchctl", "start", PlistName)
}

// Stop stops the service without unloading it.
func (m *launchdManager) Stop(ctx context.Context) error {
	if err := m.gate("stopping the launchd service"); err != nil {
		return err
	}
	return run(ctx, "launchctl", "stop", PlistName)
}

// Restart stops and starts the service.
func (m *launchdManager) Restart(ctx context.Context) error {
	if err := m.gate("restarting the launchd service"); err != nil {
		return err
	}
	// A service that is not currently running still restarts cleanly.
	_ = run(ctx, "launchctl", "stop", PlistName)
	return run(ctx, "launchctl", "start", PlistName)
}

// Reload sends SIGHUP so the daemon rereads its configuration without a
// restart.
func (m *launchdManager) Reload(ctx context.Context) error {
	if err := m.gate("reloading the launchd service"); err != nil {
		return err
	}
	return run(ctx, "launchctl", "kill", "HUP", m.domainTarget())
}

// Enable loads the plist with the disabled override cleared so it starts at
// boot.
func (m *launchdManager) Enable(ctx context.Context) error {
	if err := m.gate("enabling the launchd service"); err != nil {
		return err
	}
	if !fileExists(m.plistPath) {
		return ErrNotInstalled
	}
	return run(ctx, "launchctl", "load", "-w", m.plistPath)
}

// Disable unloads the plist and marks it disabled, which stops the service
// and prevents it starting at boot while keeping the plist, data, config
// and service account intact.
func (m *launchdManager) Disable(ctx context.Context) error {
	if err := m.gate("disabling the launchd service"); err != nil {
		return err
	}
	if !fileExists(m.plistPath) {
		return ErrNotInstalled
	}
	return run(ctx, "launchctl", "unload", "-w", m.plistPath)
}

// Status reports installation, runtime and auto-start state.
func (m *launchdManager) Status(ctx context.Context) (Status, error) {
	status := Status{Manager: m.Name(), UserMode: m.userMode, State: StateStopped}
	if !fileExists(m.plistPath) {
		return status, nil
	}
	status.Installed = true

	out, err := output(ctx, "launchctl", "list", PlistName)
	if err != nil {
		return status, nil
	}
	// A listed job is registered with launchd, which is how launchd records
	// that it starts at boot.
	status.Enabled = true
	if pid := parseLaunchdPID(out); pid > 0 {
		status.State = StateRunning
		status.PID = pid
	}
	return status, nil
}

// loaded reports whether the job is currently registered with launchd.
func (m *launchdManager) loaded(ctx context.Context) bool {
	_, err := output(ctx, "launchctl", "list", PlistName)
	return err == nil
}

// parseLaunchdPID extracts the PID value from `launchctl list <label>`
// output, which prints lines such as `"PID" = 1234;`.
func parseLaunchdPID(out string) int {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `"PID"`) {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return 0
		}
		value := strings.TrimSpace(strings.Trim(strings.TrimSpace(line[eq+1:]), ";"))
		pid, err := strconv.Atoi(value)
		if err != nil || pid <= 0 {
			return 0
		}
		return pid
	}
	return 0
}
