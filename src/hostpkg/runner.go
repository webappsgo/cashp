package hostpkg

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// Every host command runs through this runner as an argv slice: there is no
// shell, no string interpolation of caller input, and no command line ever
// assembled from formatted text. The interface exists so the whole package
// is testable without shelling out, touching /etc, or requiring root.

// Command timeouts. A hung package manager is killed rather than waited on.
const (
	// TimeoutQuery bounds read-only queries such as "is this installed".
	TimeoutQuery = 60 * time.Second
	// TimeoutRefresh bounds a package index refresh.
	TimeoutRefresh = 5 * time.Minute
	// TimeoutInstall bounds an install, remove, or upgrade transaction.
	TimeoutInstall = 15 * time.Minute
	// TimeoutKeyImport bounds a key import or repository write.
	TimeoutKeyImport = 60 * time.Second
)

// maxCapturedOutput caps how much of a command's output is retained, so a
// runaway package manager cannot exhaust memory.
const maxCapturedOutput = 1 << 20

// commandNamePattern restricts a command to a bare binary name resolved from
// PATH; a path separator or shell metacharacter can never appear.
var commandNamePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,31}$`)

// Command is a single argv invocation.
type Command struct {
	// Name is the bare binary name, resolved from PATH.
	Name string
	// Args are the argv elements after the binary name.
	Args []string
	// Env holds additional KEY=VALUE entries layered on the process
	// environment, used for non-interactive frontends.
	Env []string
	// Timeout bounds the run; zero means the runner's default.
	Timeout time.Duration
}

// Result is the captured outcome of a command.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner executes host commands. ExecRunner is the real implementation and
// FakeRunner is the test double.
type Runner interface {
	Run(ctx context.Context, cmd Command) (Result, error)
}

// ExecRunner runs commands with exec.CommandContext.
type ExecRunner struct {
	// DefaultTimeout applies when a Command sets no timeout.
	DefaultTimeout time.Duration
}

// NewExecRunner returns a runner with a sane default timeout.
func NewExecRunner() *ExecRunner {
	return &ExecRunner{DefaultTimeout: TimeoutInstall}
}

// Run executes cmd as an argv slice under a bounded context. Package manager
// output is captured for parsing but never surfaced in an API-visible error
// message, because it can contain filesystem paths.
func (r *ExecRunner) Run(ctx context.Context, cmd Command) (Result, error) {
	if err := validateCommand(cmd); err != nil {
		return Result{}, err
	}

	timeout := cmd.Timeout
	if timeout <= 0 {
		timeout = r.DefaultTimeout
	}
	if timeout <= 0 {
		timeout = TimeoutInstall
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// #nosec G204 -- Name and Args are allowlist-validated and passed as an
	// argv slice; no shell is involved at any point.
	c := exec.CommandContext(runCtx, cmd.Name, cmd.Args...)
	c.Env = append(os.Environ(), cmd.Env...)

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	err := c.Run()
	// ProcessState is nil when the binary could not be started at all, which
	// is reported as -1 rather than panicking.
	exitCode := -1
	if c.ProcessState != nil {
		exitCode = c.ProcessState.ExitCode()
	}
	res := Result{
		Stdout:   truncateOutput(stdout.String()),
		Stderr:   truncateOutput(stderr.String()),
		ExitCode: exitCode,
	}

	switch {
	case err == nil:
		return res, nil
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return res, fail(ErrCommandTimeout, apperr.CodeTimeout, http.StatusGatewayTimeout,
			"package manager did not finish in time")
	case errors.Is(runCtx.Err(), context.Canceled):
		return res, failUnavailable(ErrCommandFailed, "package operation was cancelled")
	default:
		return res, failUnavailable(ErrCommandFailed, "package manager reported a failure").
			WithDetails(map[string]any{"command": cmd.Name, "exit_code": res.ExitCode})
	}
}

// validateCommand enforces the argv allowlist before anything is executed.
func validateCommand(cmd Command) error {
	if !commandNamePattern.MatchString(cmd.Name) {
		return failValidation(ErrInvalidCommand, "unsupported package manager command")
	}
	for _, arg := range cmd.Args {
		if arg == "" || strings.ContainsAny(arg, "\x00\n\r") {
			return failValidation(ErrInvalidCommand, "unsupported package manager argument")
		}
	}
	for _, env := range cmd.Env {
		if !strings.Contains(env, "=") || strings.ContainsAny(env, "\x00\n\r") {
			return failValidation(ErrInvalidCommand, "unsupported package manager environment")
		}
	}

	return nil
}

// truncateOutput caps captured output at maxCapturedOutput bytes.
func truncateOutput(s string) string {
	if len(s) <= maxCapturedOutput {
		return s
	}

	return s[:maxCapturedOutput]
}
