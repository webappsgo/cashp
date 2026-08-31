//go:build linux

package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/webappsgo/cashp/src/config"
)

// runitServiceDefDir holds the service definition directories (AI.md PART
// 25 "runit (Linux)").
const runitServiceDefDir = "/etc/sv"

// runitLinkDirs are the supervised directories used by the distributions
// that ship runit, tried in this order.
var runitLinkDirs = []string{
	"/var/service",
	"/etc/service",
	"/service",
	"/etc/runit/runsvdir/default",
}

// runitManager satisfies Manager.
var _ Manager = (*runitManager)(nil)

// runitManager drives the sv(8) client and the supervised-directory symlink.
type runitManager struct {
	data    TemplateData
	defDir  string
	linkDir string
}

// newRunitManager builds a runit manager for the system service.
func newRunitManager() *runitManager {
	return &runitManager{
		data:    DefaultTemplateData(false),
		defDir:  filepath.Join(runitServiceDefDir, config.InternalName),
		linkDir: runitLinkDir(),
	}
}

// runitLinkDir returns the supervised directory present on this host,
// defaulting to the most common location when none exists yet.
func runitLinkDir() string {
	for _, dir := range runitLinkDirs {
		if fileExists(dir) {
			return dir
		}
	}
	return runitLinkDirs[0]
}

// linkPath is the symlink that enables the service at boot.
func (m *runitManager) linkPath() string {
	return filepath.Join(m.linkDir, config.InternalName)
}

// Name returns the init system identifier.
func (m *runitManager) Name() string { return "runit" }

// Install writes the run and log/run scripts, links the service into the
// supervised directory and brings it up.
func (m *runitManager) Install(ctx context.Context) error {
	if err := requireElevation("installing the runit service"); err != nil {
		return err
	}
	runScript, err := RenderRunitRun(m.data)
	if err != nil {
		return err
	}
	logScript, err := RenderRunitLogRun(m.data)
	if err != nil {
		return err
	}
	if err := writeServiceFile(filepath.Join(m.defDir, "run"), runScript, 0o755); err != nil {
		return err
	}
	if err := writeServiceFile(filepath.Join(m.defDir, "log", "run"), logScript, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(m.data.LogDir, stateDirMode); err != nil {
		return fmt.Errorf("create %s: %w", m.data.LogDir, err)
	}
	if err := m.link(); err != nil {
		return err
	}
	return run(ctx, "sv", "up", config.InternalName)
}

// Uninstall brings the service down, removes its definition and link, then
// deletes all state and the system account after confirmation.
func (m *runitManager) Uninstall(ctx context.Context, confirmed bool) error {
	if err := requireElevation("uninstalling the runit service"); err != nil {
		return err
	}
	if err := confirmUninstall(confirmed); err != nil {
		return err
	}
	// An already-stopped service must not abort the uninstall.
	_ = run(ctx, "sv", "down", config.InternalName)
	if err := m.unlink(); err != nil {
		return err
	}
	if err := os.RemoveAll(m.defDir); err != nil {
		return fmt.Errorf("remove %s: %w", m.defDir, err)
	}
	return purgeState(ctx, m.data)
}

// Start brings the service up.
func (m *runitManager) Start(ctx context.Context) error {
	if err := requireElevation("starting the runit service"); err != nil {
		return err
	}
	return run(ctx, "sv", "up", config.InternalName)
}

// Stop brings the service down.
func (m *runitManager) Stop(ctx context.Context) error {
	if err := requireElevation("stopping the runit service"); err != nil {
		return err
	}
	return run(ctx, "sv", "down", config.InternalName)
}

// Restart restarts the service.
func (m *runitManager) Restart(ctx context.Context) error {
	if err := requireElevation("restarting the runit service"); err != nil {
		return err
	}
	return run(ctx, "sv", "restart", config.InternalName)
}

// Reload sends SIGHUP so the daemon rereads its configuration without a
// restart.
func (m *runitManager) Reload(ctx context.Context) error {
	if err := requireElevation("reloading the runit service"); err != nil {
		return err
	}
	return run(ctx, "sv", "hup", config.InternalName)
}

// Enable links the service into the supervised directory so runsvdir starts
// it at boot.
func (m *runitManager) Enable(ctx context.Context) error {
	if err := requireElevation("enabling the runit service"); err != nil {
		return err
	}
	if !fileExists(filepath.Join(m.defDir, "run")) {
		return ErrNotInstalled
	}
	return m.link()
}

// Disable brings the service down and removes the supervised-directory
// link, keeping the definition, data, config and system account intact.
func (m *runitManager) Disable(ctx context.Context) error {
	if err := requireElevation("disabling the runit service"); err != nil {
		return err
	}
	// Stopping an already-stopped service is not an error condition here.
	_ = run(ctx, "sv", "down", config.InternalName)
	return m.unlink()
}

// Status reports installation, runtime and auto-start state.
func (m *runitManager) Status(ctx context.Context) (Status, error) {
	status := Status{Manager: m.Name(), State: StateStopped}
	if !fileExists(filepath.Join(m.defDir, "run")) {
		return status, nil
	}
	status.Installed = true
	status.Enabled = pathExists(m.linkPath())

	out, err := output(ctx, "sv", "status", config.InternalName)
	if err == nil && strings.HasPrefix(out, "run:") {
		status.State = StateRunning
		status.PID = parseRunitPID(out)
	}
	return status, nil
}

// link creates the supervised-directory symlink, tolerating an existing
// one.
func (m *runitManager) link() error {
	if err := os.MkdirAll(m.linkDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", m.linkDir, err)
	}
	if pathExists(m.linkPath()) {
		return nil
	}
	if err := os.Symlink(m.defDir, m.linkPath()); err != nil && !os.IsExist(err) {
		return fmt.Errorf("link %s: %w", m.linkPath(), err)
	}
	return nil
}

// unlink removes the supervised-directory symlink.
func (m *runitManager) unlink() error {
	if err := os.Remove(m.linkPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", m.linkPath(), err)
	}
	return nil
}

// parseRunitPID extracts the process ID from an `sv status` line such as
// "run: cashp: (pid 1234) 5s".
func parseRunitPID(out string) int {
	start := strings.Index(out, "(pid ")
	if start < 0 {
		return 0
	}
	rest := out[start+len("(pid "):]
	end := strings.IndexByte(rest, ')')
	if end < 0 {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(rest[:end]))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}
