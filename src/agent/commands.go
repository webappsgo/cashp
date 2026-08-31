package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	agentbanner "github.com/webappsgo/cashp/src/agent/banner"
	"github.com/webappsgo/cashp/src/agent/paths"
	"github.com/webappsgo/cashp/src/agent/reporter"
	agentservice "github.com/webappsgo/cashp/src/agent/service"
	"github.com/webappsgo/cashp/src/agent/settings"
	"github.com/webappsgo/cashp/src/agent/transport"
	"github.com/webappsgo/cashp/src/agent/updater"
	"github.com/webappsgo/cashp/src/client/term"
	"github.com/webappsgo/cashp/src/common/display"
)

// Dispatch routes a parsed command line to its implementation and returns
// the process exit code.
func Dispatch(opts *Options, stdout, stderr io.Writer) int {
	ctx, cancel := SignalContext()
	defer cancel()

	if opts.Service != "" {
		return RunService(ctx, opts, stdout, stderr)
	}

	// Only the long-running foreground command opens agent.log; one-shot
	// commands stay on stderr so they never create files as a side effect.
	foreground := opts.Command == "" && !opts.Status && !opts.UpdateSet
	runtime, err := NewRuntime(opts, foreground)
	if err != nil {
		return Fail(stderr, err)
	}
	defer runtime.Close()

	switch {
	case opts.UpdateSet:
		return RunUpdate(ctx, runtime, stdout, stderr)
	case opts.Status, opts.Command == CommandStatus:
		return RunStatus(ctx, runtime, stdout, stderr)
	case opts.Command == CommandTest:
		return RunTest(ctx, runtime, stdout, stderr)
	case opts.Command == CommandRegister:
		return RunRegister(ctx, runtime, stdout, stderr)
	}

	if strings.TrimSpace(opts.Server) != "" && strings.TrimSpace(opts.Token) != "" {
		return RunConnect(ctx, runtime, stdout, stderr)
	}
	return RunForeground(ctx, runtime, stdout, stderr)
}

// SignalContext cancels on the signals a supervisor uses to stop a daemon.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Fail reports an error and maps it onto an exit code. Token material is
// masked on the way out so a mistyped credential never lands in a log.
func Fail(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "%s: %s\n", AppName(), ScrubTokens(err.Error()))
	if errors.Is(err, transport.ErrUnauthorized) {
		return ExitAuth
	}
	return ExitError
}

// Mark renders a pass or fail indicator for the connection checks.
func Mark(ok bool) string {
	if display.EmojiEnabled() {
		if ok {
			return "✅"
		}
		return "❌"
	}
	if ok {
		return "[OK]"
	}
	return "[FAIL]"
}

// RunStatus prints the health summary. It exits 0 when the agent is
// registered and the panel answers, and 1 otherwise.
func RunStatus(ctx context.Context, runtime *Runtime, stdout, stderr io.Writer) int {
	rep, err := reporter.New(reporter.Options{
		Config:    runtime.Config,
		Client:    runtime.Client,
		Overrides: runtime.Overrides,
		Version:   Version,
		Logger:    runtime.Logger,
	})
	if err != nil {
		return Fail(stderr, err)
	}

	state := rep.State()
	identity := rep.Identity()
	reachable := runtime.Client.Ping(ctx) == nil

	status := "Not registered"
	switch {
	case state.Registered() && reachable:
		status = "Connected"
	case state.Registered():
		status = "Registered (server unreachable)"
	}

	lastReport := state.LastHeartbeat
	if strings.TrimSpace(lastReport) == "" {
		lastReport = "never"
	}

	fmt.Fprintf(stdout, "Agent: %s v%s\n", AppName(), Version)
	fmt.Fprintf(stdout, "Hostname: %s\n", identity.Hostname)
	fmt.Fprintf(stdout, "Server: %s\n", runtime.Client.ActiveServer())
	fmt.Fprintf(stdout, "Scope: %s\n", runtime.Client.Scope())
	if state.Registered() {
		fmt.Fprintf(stdout, "Agent ID: %s\n", state.AgentID)
		fmt.Fprintf(stdout, "Registered: %s\n", state.RegisteredAt)
	}
	fmt.Fprintf(stdout, "Status: %s\n", status)
	fmt.Fprintf(stdout, "Last Report: %s\n", lastReport)
	fmt.Fprintf(stdout, "Tasks Completed: %d\n", state.TasksCompleted)

	if state.Registered() && reachable {
		return ExitOK
	}
	return ExitError
}

// RunTest verifies the connection, the credential and the enrollment.
func RunTest(ctx context.Context, runtime *Runtime, stdout, stderr io.Writer) int {
	server := runtime.Client.ActiveServer()
	fmt.Fprintf(stdout, "Testing connection to %s...\n", server)

	if _, err := runtime.Client.Autodiscover(ctx); err != nil {
		fmt.Fprintf(stdout, "%s Connection failed\n", Mark(false))
		return Fail(stderr, err)
	}
	fmt.Fprintf(stdout, "%s Connection successful\n", Mark(true))

	if err := runtime.Client.Ping(ctx); err != nil {
		fmt.Fprintf(stdout, "%s Authentication rejected\n", Mark(false))
		return Fail(stderr, err)
	}
	fmt.Fprintf(stdout, "%s Authentication valid\n", Mark(true))

	rep, err := reporter.New(reporter.Options{
		Config:    runtime.Config,
		Client:    runtime.Client,
		Overrides: runtime.Overrides,
		Version:   Version,
		Logger:    runtime.Logger,
	})
	if err != nil {
		return Fail(stderr, err)
	}

	if !rep.State().Registered() {
		fmt.Fprintf(stdout, "%s Agent not registered\n", Mark(false))
		return ExitError
	}
	fmt.Fprintf(stdout, "%s Agent registered\n", Mark(true))
	return ExitOK
}

// RunRegister enrolls this node, asking for the identity details the panel
// will display when the terminal is interactive.
func RunRegister(ctx context.Context, runtime *Runtime, stdout, stderr io.Writer) int {
	if err := paths.RequireRoot(); err != nil {
		return Fail(stderr, err)
	}

	rep, err := reporter.New(reporter.Options{
		Config:    runtime.Config,
		Client:    runtime.Client,
		Overrides: runtime.Overrides,
		Version:   Version,
		Logger:    runtime.Logger,
	})
	if err != nil {
		return Fail(stderr, err)
	}

	if err := Prompt(runtime, rep.Identity().Hostname, stdout); err != nil {
		return Fail(stderr, err)
	}

	state, err := rep.Register(ctx, runtime.Options.Force)
	if err != nil {
		return Fail(stderr, err)
	}

	token, err := ResolveToken(runtime.Config, runtime.Overrides, runtime.Options.Token)
	if err != nil {
		return Fail(stderr, err)
	}
	if err := SaveEnrollment(runtime.Config, runtime.Overrides, token); err != nil {
		return Fail(stderr, err)
	}

	fmt.Fprintf(stdout, "%s Agent registered as %q\n", Mark(true), state.Name)
	fmt.Fprintf(stdout, "Config saved to: %s\n", paths.ConfigFile(runtime.Overrides))
	return ExitOK
}

// RunConnect is the one-liner the panel hands the operator: connect,
// validate the token, register, save the configuration, and install the
// service so the node keeps reporting across reboots.
func RunConnect(ctx context.Context, runtime *Runtime, stdout, stderr io.Writer) int {
	if err := paths.RequireRoot(); err != nil {
		return Fail(stderr, err)
	}

	server := runtime.Client.ActiveServer()
	fmt.Fprintf(stdout, "Connecting to %s...\n", server)

	if _, err := runtime.Client.Autodiscover(ctx); err != nil {
		return Fail(stderr, err)
	}
	fmt.Fprintf(stdout, "%s Connection successful\n", Mark(true))

	if err := runtime.Client.Ping(ctx); err != nil {
		return Fail(stderr, err)
	}
	fmt.Fprintf(stdout, "%s Token validated\n", Mark(true))

	rep, err := reporter.New(reporter.Options{
		Config:    runtime.Config,
		Client:    runtime.Client,
		Overrides: runtime.Overrides,
		Version:   Version,
		Logger:    runtime.Logger,
	})
	if err != nil {
		return Fail(stderr, err)
	}

	state, err := rep.Register(ctx, false)
	if err != nil {
		return Fail(stderr, err)
	}
	fmt.Fprintf(stdout, "%s Agent registered as %q\n", Mark(true), state.Name)

	token, err := ResolveToken(runtime.Config, runtime.Overrides, runtime.Options.Token)
	if err != nil {
		return Fail(stderr, err)
	}
	if err := SaveEnrollment(runtime.Config, runtime.Overrides, token); err != nil {
		return Fail(stderr, err)
	}
	fmt.Fprintf(stdout, "\nConfig saved to: %s\n", paths.ConfigFile(runtime.Overrides))

	fmt.Fprintln(stdout, "Installing service...")
	line, err := agentservice.Run(ctx, agentservice.CommandInstall, agentservice.Options{Overrides: runtime.Overrides})
	if err != nil {
		return Fail(stderr, err)
	}
	fmt.Fprintf(stdout, "%s %s\n", Mark(true), line)

	fmt.Fprintf(stdout, "\nAgent is now sending data to server for %s scope.\n", runtime.Client.Scope())
	return ExitOK
}

// RunForeground runs the reporting loops until the process is signalled.
func RunForeground(ctx context.Context, runtime *Runtime, stdout, stderr io.Writer) int {
	if err := paths.RequireRoot(); err != nil {
		return Fail(stderr, err)
	}

	rep, err := reporter.New(reporter.Options{
		Config:    runtime.Config,
		Client:    runtime.Client,
		Overrides: runtime.Overrides,
		Version:   Version,
		Logger:    runtime.Logger,
	})
	if err != nil {
		return Fail(stderr, err)
	}

	identity := rep.Identity()
	connected := runtime.Client.Ping(ctx) == nil

	agentbanner.PrintTo(stdout, agentbanner.Config{
		AppName:   AppName(),
		Version:   Version,
		AppMode:   string(runtime.Mode),
		Debug:     runtime.Debug,
		Server:    runtime.Client.ActiveServer(),
		Hostname:  identity.Hostname,
		Tags:      identity.Tags,
		Connected: connected,
	}, runtime.Color)

	if err := rep.Run(ctx); err != nil {
		return Fail(stderr, err)
	}
	return ExitOK
}

// RunUpdate performs the documented --update flow.
func RunUpdate(ctx context.Context, runtime *Runtime, stdout, stderr io.Writer) int {
	result, err := updater.Run(ctx, updater.Options{
		Client:  runtime.Client,
		Version: Version,
		Mode:    runtime.Options.UpdateMode,
	})
	if err != nil {
		return Fail(stderr, err)
	}

	fmt.Fprintln(stdout, result.Message())
	return ExitOK
}

// RunService manages the agent's own system service. It needs no panel
// connection, so it deliberately runs before the transport is built.
func RunService(ctx context.Context, opts *Options, stdout, stderr io.Writer) int {
	line, err := agentservice.Run(ctx, opts.Service, agentservice.Options{Overrides: opts.Directories()})
	if err != nil {
		return Fail(stderr, err)
	}

	fmt.Fprintln(stdout, line)
	return ExitOK
}

// Prompt asks for the identity fields the panel displays, but only when a
// human is present and the value is not already configured.
func Prompt(runtime *Runtime, hostname string, stdout io.Writer) error {
	if !term.IsTTY(os.Stdin) {
		return nil
	}

	reader := bufio.NewReader(os.Stdin)
	if strings.TrimSpace(runtime.Config.Identity.DisplayName) == "" {
		fmt.Fprintf(stdout, "Display name [%s]: ", hostname)
		answer, err := ReadLine(reader)
		if err != nil {
			return err
		}
		if answer != "" {
			runtime.Config.Identity.DisplayName = answer
		}
	}

	if len(runtime.Config.Identity.Tags) == 0 {
		fmt.Fprint(stdout, "Tags (comma separated, optional): ")
		answer, err := ReadLine(reader)
		if err != nil {
			return err
		}
		runtime.Config.Identity.Tags = settings.SplitList(answer)
	}
	return nil
}

// ReadLine reads one trimmed answer, treating end of input as an empty one.
func ReadLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
