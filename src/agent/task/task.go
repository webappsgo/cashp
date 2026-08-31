// Package task validates and runs the work the panel assigns to this node.
//
// The panel is a remote party from the node's point of view, so every task
// crosses a trust boundary: nothing is executed unless it names an action
// from the compiled-in allowlist, is addressed to this exact agent, and
// carries arguments that pass their validator. Commands are always built
// as an argv slice and handed to exec.CommandContext — no shell is ever
// involved, so no value from the panel can be interpreted as syntax.
package task

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/agent/transport"
	"github.com/webappsgo/cashp/src/security"
)

// DefaultTimeout bounds a task that does not request one.
const DefaultTimeout = 2 * time.Minute

// MaxTimeout bounds a task that requests an unreasonable one.
const MaxTimeout = 30 * time.Minute

// MaxOutputBytes caps the output returned to the panel.
const MaxOutputBytes = 256 << 10

// MaxArgs caps how many arguments a task may carry.
const MaxArgs = 8

// Rejection reasons reported back to the panel.
var (
	// ErrNotForThisAgent means the task named a different agent.
	ErrNotForThisAgent = errors.New("task is addressed to a different agent")
	// ErrUnknownAction means the action is not on the allowlist.
	ErrUnknownAction = errors.New("task action is not allowed")
	// ErrBadArguments means an argument failed validation.
	ErrBadArguments = errors.New("task arguments are not acceptable")
	// ErrNoTaskID means the task carried no identifier.
	ErrNoTaskID = errors.New("task has no id")
	// ErrUnsupportedPlatform means the action exists but not on this OS.
	ErrUnsupportedPlatform = errors.New("task action is not supported on this platform")
)

// unitPattern is the only shape accepted for a service unit name.
var unitPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._-]{0,127}$`)

// envNamePattern restricts panel-supplied environment variables to a
// namespace of the agent's own, so a task can never set PATH, LD_PRELOAD
// or anything else that changes how a binary resolves or loads.
var envNamePattern = regexp.MustCompile(`^CASHP_TASK_[A-Z0-9_]{1,64}$`)

// envValuePattern keeps panel-supplied values printable and single-line.
var envValuePattern = regexp.MustCompile(`^[\x20-\x7E]{0,512}$`)

// Action is one allowlisted operation.
type Action struct {
	// Name is what the panel asks for.
	Name string
	// Summary documents the action in --status output.
	Summary string
	// Build turns validated arguments into a full argv slice. A nil Build
	// marks an action the agent answers itself without running anything.
	Build func(args []string) ([]string, error)
	// Answer handles an action that needs no external process.
	Answer func(args []string) (string, error)
	// MinArgs and MaxArgs bound the argument count.
	MinArgs int
	MaxArgs int
	// Platforms lists the GOOS values that support the action. An empty
	// list means every platform.
	Platforms []string
}

// Allowlist is the complete set of actions this agent will ever perform.
// Adding an entry here is the only way to widen what the panel can make a
// managed node do.
var Allowlist = []Action{
	{
		Name:    "agent.ping",
		Summary: "Answer with pong, proving the task path works",
		Answer:  func(args []string) (string, error) { return "pong", nil },
	},
	{
		Name:    "service.status",
		Summary: "Report the status of a system service",
		MinArgs: 1,
		MaxArgs: 1,
		Build: func(args []string) ([]string, error) {
			return serviceArgv("status", args[0])
		},
		Platforms: []string{"linux", "darwin", "freebsd", "windows"},
	},
	{
		Name:    "service.start",
		Summary: "Start a system service",
		MinArgs: 1,
		MaxArgs: 1,
		Build: func(args []string) ([]string, error) {
			return serviceArgv("start", args[0])
		},
		Platforms: []string{"linux", "darwin", "freebsd", "windows"},
	},
	{
		Name:    "service.stop",
		Summary: "Stop a system service",
		MinArgs: 1,
		MaxArgs: 1,
		Build: func(args []string) ([]string, error) {
			return serviceArgv("stop", args[0])
		},
		Platforms: []string{"linux", "darwin", "freebsd", "windows"},
	},
	{
		Name:    "service.restart",
		Summary: "Restart a system service",
		MinArgs: 1,
		MaxArgs: 1,
		Build: func(args []string) ([]string, error) {
			return serviceArgv("restart", args[0])
		},
		Platforms: []string{"linux", "freebsd"},
	},
	{
		Name:    "system.uptime",
		Summary: "Report how long the node has been running",
		Build: func(args []string) ([]string, error) {
			return []string{"uptime"}, nil
		},
		Platforms: []string{"linux", "darwin", "freebsd"},
	},
	{
		Name:    "system.disk",
		Summary: "Report filesystem usage",
		Build: func(args []string) ([]string, error) {
			return []string{"df", "-P", "-k"}, nil
		},
		Platforms: []string{"linux", "darwin", "freebsd"},
	},
}

// Lookup finds an allowlisted action by name.
func Lookup(name string) (Action, bool) {
	for _, action := range Allowlist {
		if action.Name == strings.TrimSpace(name) {
			return action, true
		}
	}
	return Action{}, false
}

// Runner executes a validated argv slice. It is a field on Executor so
// tests can drive the whole validation path without touching the system.
type Runner func(ctx context.Context, argv []string, env []string) (output string, exitCode int, err error)

// Executor validates and runs tasks on behalf of one registered agent.
type Executor struct {
	// AgentID is the identity the panel assigned at registration.
	AgentID string
	// Run executes a validated command; nil means the real system runner.
	Run Runner
}

// Validate checks a task against every rule before anything is executed.
func (e *Executor) Validate(task transport.Task) (Action, []string, error) {
	if strings.TrimSpace(task.ID) == "" {
		return Action{}, nil, ErrNoTaskID
	}

	// Constant-time comparison: the agent id is a capability-like value and
	// must not be discoverable by timing a stream of guesses.
	if !security.ConstantTimeEqualString(strings.TrimSpace(task.AgentID), strings.TrimSpace(e.AgentID)) {
		return Action{}, nil, ErrNotForThisAgent
	}

	action, ok := Lookup(task.Action)
	if !ok {
		return Action{}, nil, ErrUnknownAction
	}
	if !action.supports(runtime.GOOS) {
		return Action{}, nil, ErrUnsupportedPlatform
	}

	args := task.Args
	if len(args) > MaxArgs {
		return Action{}, nil, ErrBadArguments
	}
	if len(args) < action.MinArgs || len(args) > action.MaxArgs {
		return Action{}, nil, ErrBadArguments
	}
	for _, arg := range args {
		if !unitPattern.MatchString(arg) {
			return Action{}, nil, ErrBadArguments
		}
	}

	env, err := SafeEnv(task.Env)
	if err != nil {
		return Action{}, nil, err
	}
	return action, env, nil
}

// Execute validates and runs a task, always returning a result the caller
// can report. A rejected task is reported as rejected, never silently
// dropped, so the panel's audit trail stays complete.
func (e *Executor) Execute(ctx context.Context, task transport.Task) transport.TaskResult {
	result := transport.TaskResult{AgentID: e.AgentID, TaskID: strings.TrimSpace(task.ID)}

	action, env, err := e.Validate(task)
	if err != nil {
		result.Status = transport.TaskStatusRejected
		result.ExitCode = -1
		result.Error = err.Error()
		return result
	}

	timeout := Timeout(task.TimeoutMS)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if action.Answer != nil {
		output, answerErr := action.Answer(task.Args)
		if answerErr != nil {
			result.Status = transport.TaskStatusFailed
			result.ExitCode = 1
			result.Error = answerErr.Error()
			return result
		}
		result.Status = transport.TaskStatusSucceeded
		result.Output = Truncate(output)
		return result
	}

	argv, buildErr := action.Build(task.Args)
	if buildErr != nil || len(argv) == 0 {
		result.Status = transport.TaskStatusRejected
		result.ExitCode = -1
		result.Error = ErrBadArguments.Error()
		return result
	}

	runner := e.Run
	if runner == nil {
		runner = SystemRunner
	}

	output, exitCode, runErr := runner(runCtx, argv, env)
	result.ExitCode = exitCode
	result.Output = Truncate(output)
	if runErr != nil {
		result.Status = transport.TaskStatusFailed
		result.Error = runErr.Error()
		return result
	}
	result.Status = transport.TaskStatusSucceeded
	return result
}

// SystemRunner executes argv directly. There is no shell: argv[0] is
// resolved through PATH by exec.LookPath and the remaining entries are
// passed as separate arguments, so panel-supplied values can never be
// re-parsed as shell syntax.
func SystemRunner(ctx context.Context, argv []string, env []string) (string, int, error) {
	if len(argv) == 0 {
		return "", -1, ErrBadArguments
	}

	binary, err := exec.LookPath(argv[0])
	if err != nil {
		return "", -1, fmt.Errorf("%s is not available on this node", argv[0])
	}

	command := exec.CommandContext(ctx, binary, argv[1:]...)
	command.Env = env

	var buffer bytes.Buffer
	command.Stdout = &buffer
	command.Stderr = &buffer

	err = command.Run()
	output := buffer.String()

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return output, exitErr.ExitCode(), fmt.Errorf("%s exited with status %d", argv[0], exitErr.ExitCode())
	}
	if err != nil {
		return output, -1, fmt.Errorf("run %s: %w", argv[0], err)
	}
	return output, 0, nil
}

// SafeEnv filters panel-supplied environment variables down to the agent's
// own namespace. A task can never influence PATH, LD_PRELOAD or any other
// variable that changes how a binary is found or loaded.
func SafeEnv(supplied map[string]string) ([]string, error) {
	env := []string{"PATH=" + systemPath(), "LC_ALL=C"}
	for name, value := range supplied {
		if !envNamePattern.MatchString(name) || !envValuePattern.MatchString(value) {
			return nil, ErrBadArguments
		}
		env = append(env, name+"="+value)
	}
	return env, nil
}

// Timeout clamps a panel-supplied timeout into a sane range.
func Timeout(milliseconds int) time.Duration {
	if milliseconds <= 0 {
		return DefaultTimeout
	}
	requested := time.Duration(milliseconds) * time.Millisecond
	if requested > MaxTimeout {
		return MaxTimeout
	}
	return requested
}

// Truncate caps command output so a runaway process cannot flood the panel.
func Truncate(output string) string {
	if len(output) <= MaxOutputBytes {
		return output
	}
	return output[:MaxOutputBytes] + "\n[output truncated]"
}

// supports reports whether the action runs on the given platform.
func (a Action) supports(goos string) bool {
	if len(a.Platforms) == 0 {
		return true
	}
	for _, platform := range a.Platforms {
		if platform == goos {
			return true
		}
	}
	return false
}

// serviceArgv builds the platform's service-control command for a unit
// name that has already passed unitPattern.
func serviceArgv(verb, unit string) ([]string, error) {
	if !unitPattern.MatchString(unit) {
		return nil, ErrBadArguments
	}
	switch runtime.GOOS {
	case "linux":
		if verb == "status" {
			return []string{"systemctl", "status", "--no-pager", unit}, nil
		}
		return []string{"systemctl", verb, unit}, nil
	case "darwin":
		switch verb {
		case "status":
			return []string{"launchctl", "list", unit}, nil
		case "start":
			return []string{"launchctl", "start", unit}, nil
		case "stop":
			return []string{"launchctl", "stop", unit}, nil
		}
		return nil, ErrBadArguments
	case "freebsd":
		return []string{"service", unit, verb}, nil
	case "windows":
		switch verb {
		case "status":
			return []string{"sc", "query", unit}, nil
		case "start":
			return []string{"sc", "start", unit}, nil
		case "stop":
			return []string{"sc", "stop", unit}, nil
		}
		return nil, ErrBadArguments
	default:
		return nil, ErrUnsupportedPlatform
	}
}

// systemPath is the fixed search path used for task execution.
func systemPath() string {
	if runtime.GOOS == "windows" {
		return `C:\Windows\System32;C:\Windows`
	}
	return "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
}
