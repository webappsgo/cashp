// Package service implements privilege-escalation detection, system
// user/group provisioning, and native service-manager integration for every
// init system cashp supports (AI.md PART 24 and PART 25).
//
// PERMANENT ROOT EXCEPTION: cashp is the documented exception to the
// "drop privileges after binding" rule (IDEA.md "Security decisions &
// exceptions", AI.md PART 25 "Service Templates" exception clause). The
// server manages libvirt/KVM virtual machines, Docker/Incus/Podman
// containers, mail, DNS and firewall services on the host, so it needs
// sustained root privileges for its entire lifetime. No privilege drop is
// implemented anywhere in this package for the Unix server process, and
// every generated service file states this explicitly. Least privilege is
// enforced inside the application through strict RBAC and per-tenant
// isolation instead. On Windows the service runs as the Virtual Service
// Account NT SERVICE\cashp, which is already minimal-privilege, so no drop
// is needed there either.
package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// ErrNotInstalled is returned by operations that require an installed
// service unit when no unit is present on the host.
var ErrNotInstalled = errors.New("service is not installed")

// ErrUnsupportedPlatform is returned by Detect when the host runs an
// operating system or init system this build has no implementation for.
var ErrUnsupportedPlatform = errors.New("no supported service manager found on this host")

// ErrNotConfirmed is returned by Uninstall when the destructive-action
// confirmation prompt was declined or could not be answered.
var ErrNotConfirmed = errors.New("uninstall cancelled: destructive action not confirmed")

// State describes the runtime state of the installed service.
type State string

// Service runtime states reported by Status.
const (
	StateRunning State = "running"
	StateStopped State = "stopped"
	StateUnknown State = "unknown"
)

// Status is the snapshot reported by Manager.Status, mirroring the
// "Current status" block of `cashp --service --help` (AI.md PART 24).
type Status struct {
	// Manager is the init system backing this status (systemd, openrc, ...).
	Manager string
	// Installed reports whether the service unit/script exists on disk.
	Installed bool
	// State is the current runtime state of the service.
	State State
	// Enabled reports whether the service starts automatically at boot.
	Enabled bool
	// PID is the main process ID when running, or 0.
	PID int
	// UserMode reports whether this is a per-user service rather than a
	// system-wide one.
	UserMode bool
}

// String renders the status block exactly as `--service --help` documents
// it in AI.md PART 24 "Service Help Output".
func (s Status) String() string {
	installed := "not installed"
	if s.Installed {
		installed = "installed"
	}
	autostart := "disabled"
	if s.Enabled {
		autostart = "enabled"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  Service:    %s\n", installed)
	fmt.Fprintf(&b, "  State:      %s\n", s.State)
	fmt.Fprintf(&b, "  Auto-start: %s\n", autostart)
	if s.PID > 0 {
		fmt.Fprintf(&b, "  PID:        %d\n", s.PID)
	}
	return b.String()
}

// Manager is the init-system-independent contract used by the CLI to manage
// the cashp service.
type Manager interface {
	// Install writes the service definition, enables it at boot and starts
	// it. It never creates the system user: the server binary does that
	// itself during normal startup (AI.md PART 24 "Service Installation
	// Logic").
	Install(ctx context.Context) error
	// Uninstall stops and disables the service, removes the service
	// definition, and deletes config/data/cache/log/run directories plus the
	// system user. It never deletes the binary. confirmed must be true, or
	// the caller is prompted first.
	Uninstall(ctx context.Context, confirmed bool) error
	// Start starts the service.
	Start(ctx context.Context) error
	// Stop stops the service.
	Stop(ctx context.Context) error
	// Restart restarts the service.
	Restart(ctx context.Context) error
	// Reload reloads configuration without a full restart where the platform
	// supports it.
	Reload(ctx context.Context) error
	// Enable enables automatic start at boot without touching runtime state.
	Enable(ctx context.Context) error
	// Disable stops the service and removes it from automatic start. It
	// never deletes data, config or the system user (AI.md PART 24 "Service
	// Disable Logic").
	Disable(ctx context.Context) error
	// Status reports the current installation and runtime state.
	Status(ctx context.Context) (Status, error)
	// Name returns the init system identifier, e.g. "systemd".
	Name() string
}

// uninstallNotice is printed after a successful uninstall: the binary is
// deliberately left in place (AI.md PART 24 "Service Uninstall Logic").
func uninstallNotice(binaryPath string) string {
	return fmt.Sprintf("Service uninstalled. Delete binary manually: rm %s", binaryPath)
}

// confirmPrompt is the destructive-action question asked before an
// uninstall removes data, config and the system user.
const confirmPrompt = "This will delete ALL data, configs, and the system user. Continue? [y/N] "

// confirmDestructive asks the destructive-action question and reports
// whether the answer was an explicit yes. Anything else — including EOF on a
// non-interactive stdin — counts as no.
func confirmDestructive(in io.Reader, out io.Writer, prompt string) (bool, error) {
	if _, err := io.WriteString(out, prompt); err != nil {
		return false, err
	}
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// run executes a command, discarding stdout and folding stderr into the
// returned error so callers surface actionable messages.
func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
	}
	return nil
}

// output executes a command and returns its trimmed standard output. A
// non-zero exit status is returned alongside whatever output was produced,
// because several init systems signal state through exit codes.
func output(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return strings.TrimSpace(stdout.String()), err
}

// hasBinary reports whether an executable is present in PATH.
func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
