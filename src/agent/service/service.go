// Package service installs and controls the agent as a native system
// service. It manages only the agent's own unit — the server's service is
// handled separately by src/service and is never touched from here.
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/agent/paths"
	"github.com/webappsgo/cashp/src/config"
)

// UnitName is the service identifier registered with the init system.
var UnitName = config.InternalName + "-agent"

// Description is the human-readable service description.
var Description = config.InternalName + " managed node agent"

// CommandTimeout bounds every init-system call so a wedged service manager
// cannot hang the agent binary.
const CommandTimeout = 30 * time.Second

// UnitFilePerm is the mode for a generated service definition. Unit files
// are read by the init system as root and carry no secrets, so they are
// world-readable, unlike the agent's config and token.
const UnitFilePerm = 0o644

// ErrUnsupportedPlatform is returned when no known service manager is
// present on this host.
var ErrUnsupportedPlatform = errors.New("no supported service manager found on this host")

// ErrNotInstalled is returned when an operation needs an installed service.
var ErrNotInstalled = errors.New("the agent service is not installed")

// ErrUnknownCommand is returned for a --service value outside the
// documented set.
var ErrUnknownCommand = errors.New("unknown service command")

// Commands are the values AI.md PART 33 documents for `--service`.
const (
	CommandInstall   = "install"
	CommandUninstall = "uninstall"
	CommandStart     = "start"
	CommandStop      = "stop"
	CommandRestart   = "restart"
	CommandStatus    = "status"
)

// Commands lists every accepted --service value, in help order.
func Commands() []string {
	return []string{
		CommandInstall,
		CommandUninstall,
		CommandStart,
		CommandStop,
		CommandRestart,
		CommandStatus,
	}
}

// State is the runtime state of the service.
type State string

// The states an agent service can be reported in.
const (
	StateRunning State = "running"
	StateStopped State = "stopped"
	StateNotFound State = "not installed"
	StateUnknown  State = "unknown"
)

// Status describes the installed and runtime state of the agent service.
type Status struct {
	Manager   string
	Installed bool
	Enabled   bool
	State     State
	Detail    string
}

// String renders the status for terminal output.
func (s Status) String() string {
	if !s.Installed {
		return fmt.Sprintf("%s: not installed", UnitName)
	}

	enabled := "disabled at boot"
	if s.Enabled {
		enabled = "enabled at boot"
	}
	line := fmt.Sprintf("%s: %s (%s, %s)", UnitName, s.State, s.Manager, enabled)
	if strings.TrimSpace(s.Detail) != "" {
		line += "\n" + strings.TrimSpace(s.Detail)
	}
	return line
}

// Options describe the agent installation the service should launch.
type Options struct {
	// BinaryPath is the absolute path to the agent executable.
	BinaryPath string
	// Overrides carry any non-default directory locations so the service
	// starts the agent with the same paths the operator configured.
	Overrides paths.Overrides
}

// Manager is the init-system-independent contract for the agent service.
type Manager interface {
	// Install writes the service definition, enables it at boot and starts
	// it.
	Install(ctx context.Context, opts Options) error
	// Uninstall stops the service and removes its definition. It never
	// deletes the agent's config, token, data or binary: an operator
	// removing the service is not necessarily discarding the enrollment.
	Uninstall(ctx context.Context) error
	// Start starts the service.
	Start(ctx context.Context) error
	// Stop stops the service.
	Stop(ctx context.Context) error
	// Restart restarts the service.
	Restart(ctx context.Context) error
	// Status reports the current installation and runtime state.
	Status(ctx context.Context) (Status, error)
	// Name returns the init system identifier, e.g. "systemd".
	Name() string
}

// Run executes one documented --service command against the host's service
// manager and returns the line to print. Installing and removing a system
// service requires root, which is checked before anything is written.
func Run(ctx context.Context, command string, opts Options) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(command))
	if !isCommand(normalized) {
		return "", fmt.Errorf("%w: %q (want one of: %s)", ErrUnknownCommand, command, strings.Join(Commands(), ", "))
	}

	if err := paths.RequireRoot(); err != nil {
		return "", err
	}

	manager, err := Detect()
	if err != nil {
		return "", err
	}

	switch normalized {
	case CommandInstall:
		resolved, err := resolveOptions(opts)
		if err != nil {
			return "", err
		}
		if err := manager.Install(ctx, resolved); err != nil {
			return "", err
		}
		return fmt.Sprintf("Installed and started %s (%s).", UnitName, manager.Name()), nil
	case CommandUninstall:
		if err := manager.Uninstall(ctx); err != nil {
			return "", err
		}
		return fmt.Sprintf("Removed %s. Configuration and enrollment were left in place.", UnitName), nil
	case CommandStart:
		if err := manager.Start(ctx); err != nil {
			return "", err
		}
		return fmt.Sprintf("Started %s.", UnitName), nil
	case CommandStop:
		if err := manager.Stop(ctx); err != nil {
			return "", err
		}
		return fmt.Sprintf("Stopped %s.", UnitName), nil
	case CommandRestart:
		if err := manager.Restart(ctx); err != nil {
			return "", err
		}
		return fmt.Sprintf("Restarted %s.", UnitName), nil
	default:
		status, err := manager.Status(ctx)
		if err != nil {
			return "", err
		}
		return status.String(), nil
	}
}

// isCommand reports whether value is a documented --service command.
func isCommand(value string) bool {
	for _, candidate := range Commands() {
		if value == candidate {
			return true
		}
	}
	return false
}

// resolveOptions fills in the binary path from the running executable when
// the caller did not supply one, and rejects a relative path: an init
// system needs an absolute command line.
func resolveOptions(opts Options) (Options, error) {
	binary := strings.TrimSpace(opts.BinaryPath)
	if binary == "" {
		executable, err := os.Executable()
		if err != nil {
			return Options{}, fmt.Errorf("cannot determine the agent binary path: %w", err)
		}
		binary = executable
	}

	resolved, err := filepath.Abs(binary)
	if err != nil {
		return Options{}, fmt.Errorf("cannot resolve %s: %w", binary, err)
	}
	if _, err := os.Stat(resolved); err != nil {
		return Options{}, fmt.Errorf("agent binary %s is not usable: %w", resolved, err)
	}

	opts.BinaryPath = resolved
	return opts, nil
}

// ExecArgs builds the command line the service runs, carrying through any
// directory overrides the operator configured.
func ExecArgs(opts Options) []string {
	args := []string{opts.BinaryPath}
	if strings.TrimSpace(opts.Overrides.Config) != "" {
		args = append(args, "--config", opts.Overrides.Config)
	}
	if strings.TrimSpace(opts.Overrides.Data) != "" {
		args = append(args, "--data", opts.Overrides.Data)
	}
	if strings.TrimSpace(opts.Overrides.Log) != "" {
		args = append(args, "--log", opts.Overrides.Log)
	}
	return args
}

// ExecLine renders ExecArgs as a shell-safe single command line for unit
// file formats that take one string rather than an argument vector.
func ExecLine(opts Options) string {
	quoted := make([]string, 0, len(ExecArgs(opts)))
	for _, arg := range ExecArgs(opts) {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

// shellQuote makes one argument safe to embed in a generated unit file.
// The values come from local flags rather than the network, but a path with
// a space would still produce a broken unit without this.
func shellQuote(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\n\"'\\$`") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// writeUnit writes a generated service definition, creating its directory.
func writeUnit(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), UnitFilePerm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// removeUnit deletes a service definition, treating an already-absent file
// as success so uninstall is idempotent.
func removeUnit(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// setExecutable makes a generated init script runnable by the init system.
func setExecutable(path string) error {
	if err := os.Chmod(path, 0o755); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// fileExists reports whether path is present.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// hasBinary reports whether a tool is on PATH.
func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// runCommand executes a service-manager command, discarding its output.
func runCommand(ctx context.Context, name string, args ...string) error {
	_, err := commandOutput(ctx, name, args...)
	return err
}

// commandOutput executes a service-manager command and returns its combined
// output. Every argument is passed as its own element: no shell is
// involved anywhere in this package.
func commandOutput(ctx context.Context, name string, args ...string) (string, error) {
	bounded, cancel := context.WithTimeout(ctx, CommandTimeout)
	defer cancel()

	binary, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s is not available on this host: %w", name, err)
	}

	buffer := &bytes.Buffer{}
	cmd := exec.CommandContext(bounded, binary, args...)
	cmd.Stdout = buffer
	cmd.Stderr = buffer

	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(buffer.String())
		if detail == "" {
			return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return buffer.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, detail)
	}
	return buffer.String(), nil
}
