package orchestrator

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DefaultMaxRunOutputBytes caps what a single external command may return.
// virsh output for a list or a dumpxml is small; anything larger is either
// a malfunction or an attempt to exhaust memory on a shared node.
const DefaultMaxRunOutputBytes = 4 << 20

// RunResult is the captured outcome of one external command.
type RunResult struct {
	// Stdout is the captured standard output.
	Stdout []byte
	// Stderr is the captured standard error.
	Stderr []byte
	// ExitCode is the process exit status.
	ExitCode int
	// Truncated reports that output hit the configured cap.
	Truncated bool
}

// Runner executes one external command. It is an interface so the libvirt
// backend and the Podman CLI fallback can be exercised without a real
// binary on the host: the test suite substitutes FakeRunner and never
// spawns a process.
//
// Every implementation takes the program and its arguments as a separate
// argv slice. There is no variant that accepts a command string, because a
// command string is the only way a shell metacharacter could ever become
// executable.
type Runner interface {
	// Run executes bin with args, feeding stdin, and returns the captured
	// result. A non-zero exit status is reported in the result, not as an
	// error; err is reserved for a failure to run at all.
	Run(ctx context.Context, bin string, args []string, stdin []byte) (RunResult, error)
}

// ExecRunner is the real Runner. It spawns the process directly through
// exec.CommandContext with an argv slice — never through a shell — and
// bounds the captured output.
type ExecRunner struct {
	// MaxOutputBytes caps each captured stream. Zero uses the package
	// default.
	MaxOutputBytes int64
	// Env is the exact environment handed to the child. A nil Env runs the
	// child with an empty environment plus the minimal PATH below, so a
	// variable in the server's own environment can never influence a
	// hypervisor command.
	Env []string
}

// minimalChildEnv is the environment an external command runs with when the
// caller supplied none. It is deliberately tiny: the engines this package
// drives read configuration from their own files, not from the environment
// of whoever invoked them.
var minimalChildEnv = []string{
	"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	"LC_ALL=C",
}

// Run executes the command and captures its output.
func (r *ExecRunner) Run(ctx context.Context, bin string, args []string, stdin []byte) (RunResult, error) {
	var out RunResult

	if err := ValidateBinaryPath("binary", bin); err != nil {
		return out, err
	}
	for _, a := range args {
		if strings.ContainsRune(a, 0) {
			return out, validationErr("argv", "null_byte")
		}
	}

	limit := r.MaxOutputBytes
	if limit <= 0 {
		limit = DefaultMaxRunOutputBytes
	}

	// exec.CommandContext takes the program and each argument separately;
	// no shell is involved, so no argument can be reinterpreted as syntax.
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = r.Env
	if cmd.Env == nil {
		cmd.Env = minimalChildEnv
	}
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	stdout := &cappedBuffer{limit: limit}
	stderrBuf := &cappedBuffer{limit: limit}
	cmd.Stdout = stdout
	cmd.Stderr = stderrBuf

	err := cmd.Run()
	out.Stdout = stdout.Bytes()
	out.Stderr = stderrBuf.Bytes()
	out.Truncated = stdout.truncated || stderrBuf.truncated

	if err == nil {
		return out, nil
	}

	var exitErr *exec.ExitError
	if stderrors.As(err, &exitErr) {
		out.ExitCode = exitErr.ExitCode()
		return out, nil
	}
	if ctxErr := ctx.Err(); stderrors.Is(ctxErr, context.DeadlineExceeded) {
		return out, timeoutErr(BackendLibvirt, "exec", err)
	}
	// The wrapped cause carries the binary path for the log; the returned
	// message never does.
	return out, unavailableErr(BackendLibvirt, "binary", fmt.Errorf("run %s: %w", bin, err))
}

// cappedBuffer accumulates output up to a fixed ceiling and then discards
// the rest, recording that it did so.
type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int64
	truncated bool
}

// Write appends up to the remaining allowance and silently drops the rest.
func (c *cappedBuffer) Write(p []byte) (int, error) {
	remaining := c.limit - int64(c.buf.Len())
	if remaining <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		c.buf.Write(p[:remaining])
		c.truncated = true
		return len(p), nil
	}
	c.buf.Write(p)
	return len(p), nil
}

// Bytes returns the accumulated output.
func (c *cappedBuffer) Bytes() []byte { return c.buf.Bytes() }

// LookupBinary resolves an external program to an absolute path. It is used
// at construction time so a backend that cannot run is never registered,
// rather than failing on the first tenant request.
func LookupBinary(name string) (string, error) {
	if name == "" || hasUnsafeChars(name) {
		return "", validationErr("binary", "charset")
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", unavailableErr(BackendLibvirt, "binary", err)
	}
	if err := ValidateBinaryPath("binary", path); err != nil {
		return "", err
	}
	return path, nil
}

// withTimeout derives a bounded context from ctx. Every external command
// and every engine API call in this package runs under one: an operation
// without a deadline can pin a worker on a shared node indefinitely.
func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}
	return context.WithTimeout(ctx, timeout)
}
