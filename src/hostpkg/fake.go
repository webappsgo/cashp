package hostpkg

import (
	"context"
	"strings"
	"sync"
)

// FakeRunner is the in-memory Runner used by the test suite and by dry-run
// tooling. It records every argv it is handed and answers from a scripted
// response table, so nothing is ever executed on the host.
type FakeRunner struct {
	mu sync.Mutex
	// Calls records every command in the order it was received.
	Calls []Command
	// Responses maps a joined argv line to the result to return.
	Responses map[string]Result
	// Errors maps a joined argv line to an error to return.
	Errors map[string]error
	// Handler, when set, answers any command not in Responses or Errors.
	Handler func(cmd Command) (Result, error)
	// Default is returned when nothing else matches.
	Default Result
}

// NewFakeRunner returns an empty fake runner.
func NewFakeRunner() *FakeRunner {
	return &FakeRunner{
		Responses: map[string]Result{},
		Errors:    map[string]error{},
	}
}

// CommandLine joins a command's argv with spaces for use as a lookup key and
// for test assertions. It is a display form only and never executed.
func CommandLine(cmd Command) string {
	return strings.Join(append([]string{cmd.Name}, cmd.Args...), " ")
}

// Run records the command and returns the scripted result.
func (f *FakeRunner) Run(_ context.Context, cmd Command) (Result, error) {
	if err := validateCommand(cmd); err != nil {
		return Result{}, err
	}

	f.mu.Lock()
	f.Calls = append(f.Calls, cmd)
	f.mu.Unlock()

	key := CommandLine(cmd)
	if err, ok := f.Errors[key]; ok {
		return f.Responses[key], err
	}
	if res, ok := f.Responses[key]; ok {
		return res, nil
	}
	if f.Handler != nil {
		return f.Handler(cmd)
	}

	return f.Default, nil
}

// SetResponse scripts a successful result for an argv line.
func (f *FakeRunner) SetResponse(line string, res Result) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Responses == nil {
		f.Responses = map[string]Result{}
	}
	f.Responses[line] = res
}

// SetError scripts a failure for an argv line.
func (f *FakeRunner) SetError(line string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Errors == nil {
		f.Errors = map[string]error{}
	}
	f.Errors[line] = err
}

// Lines returns every recorded command as a joined argv line.
func (f *FakeRunner) Lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	lines := make([]string, 0, len(f.Calls))
	for _, call := range f.Calls {
		lines = append(lines, CommandLine(call))
	}

	return lines
}

// Reset clears the recorded calls, keeping the scripted responses.
func (f *FakeRunner) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = nil
}
