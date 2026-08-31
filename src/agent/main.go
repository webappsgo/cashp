// Command cashp-agent is the managed-node agent described in AI.md PART
// 33. It runs directly on a managed host, enrolls with the panel using an
// agent token, and from then on reports metrics and executes the tasks the
// panel queues for it. Every connection is outbound: the agent opens no
// listening socket and accepts no unauthenticated command.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/webappsgo/cashp/src/agent/shell"
	"github.com/webappsgo/cashp/src/common/version"
)

// Build information injected at link time, matching the pattern the server
// binary uses in src/main.go.
var (
	Version    = "devel"
	CommitID   = "unknown"
	BuildEpoch = "0"
	BuildDate  = "unknown"
)

// Exit codes. They follow the CLI's convention so a wrapper script can
// treat all three binaries alike.
const (
	// ExitOK reports success, and for --status a healthy agent.
	ExitOK = 0
	// ExitError reports a failure, and for --status an unhealthy agent.
	ExitError = 1
	// ExitAuth reports a credential the panel refused.
	ExitAuth = 4
	// ExitUsage reports a command line that could not be parsed.
	ExitUsage = 64
)

func main() {
	version.Set(Version, CommitID, BuildEpoch, BuildDate)
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
}

// Run executes one invocation and returns the process exit code. It takes
// its writers as arguments so the tests can drive the whole binary without
// touching the real standard streams.
func Run(args []string, stdout, stderr io.Writer) int {
	binaryName := version.BinaryName()

	opts, err := ParseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %s\n", binaryName, usageMessage(err))
		fmt.Fprintf(stderr, "Run '%s --help' for usage.\n", binaryName)
		return ExitUsage
	}

	switch {
	case opts.Help:
		PrintHelp(stdout, binaryName)
		return ExitOK
	case opts.Version:
		fmt.Fprintf(stdout, "%s %s (commit %s, built %s)\n", binaryName, Version, CommitID, BuildDate)
		return ExitOK
	case opts.ShellSet:
		if err := shell.Handle(stdout, binaryName, opts.ShellAction, opts.ShellName); err != nil {
			fmt.Fprintf(stderr, "%s: %s\n", binaryName, err)
			return ExitUsage
		}
		return ExitOK
	}

	return Dispatch(opts, stdout, stderr)
}

// usageMessage strips the sentinel prefix so the message reads naturally.
func usageMessage(err error) string {
	text := err.Error()
	if errors.Is(err, ErrUsage) {
		text = strings.TrimSpace(strings.TrimPrefix(text, ErrUsage.Error()+":"))
	}
	return text
}

// PrintHelp writes the documented --help output.
func PrintHelp(out io.Writer, binaryName string) {
	fmt.Fprintf(out, `%s %s - Agent for %s

Usage:
  %s [flags]
  %s [command]

Commands:
status                                 - Show agent status
test                                   - Test server connection
register                               - Interactive registration

Flags:
-h, --help                             - Show help
-v, --version                          - Show version
--shell completions [SHELL]            - Print shell completions (auto-detect if SHELL omitted)
--shell init [SHELL]                   - Print shell init command (auto-detect if SHELL omitted)
--shell help                           - Show shell integration help

--config DIR                           - Config directory
--data DIR                             - Data directory
--log DIR                              - Log directory
--server URL                           - Server URL to connect to
--token TOKEN                          - Authentication token
--org SLUG                             - Owning organization (org-scoped tokens)

--mode {production|development|debug}  - Application mode
--debug                                - Enable debug mode
--color {auto|yes|no}                  - Color output (default: auto)
--lang CODE                            - Language for output (default: auto)
--status                               - Show agent health

--service CMD                          - Service management (install|uninstall|start|stop|restart|status)
--update [CMD]                         - Check/perform self-update

Shells: %s
`, binaryName, Version, version.ProjectName,
		binaryName, binaryName, strings.Join(shell.SupportedShells, ", "))
}
