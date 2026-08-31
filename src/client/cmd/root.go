// Package cmd implements the cashp-cli command tree, global flag handling
// and dispatch described in AI.md PART 33.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/client/api"
	"github.com/webappsgo/cashp/src/client/auth"
	"github.com/webappsgo/cashp/src/client/output"
	"github.com/webappsgo/cashp/src/client/paths"
	"github.com/webappsgo/cashp/src/client/settings"
	"github.com/webappsgo/cashp/src/client/term"
)

// Exit codes from AI.md PART 33 "Exit Codes".
const (
	ExitOK         = 0
	ExitGeneral    = 1
	ExitConfig     = 2
	ExitConnection = 3
	ExitAuth       = 4
	ExitNotFound   = 5
	ExitUsage      = 64
)

// BuildInfo carries the ldflags-injected build identity.
type BuildInfo struct {
	Version    string
	CommitID   string
	BuildEpoch string
	BuildDate  string
}

// ExecuteOptions configures one CLI run. The streams are injectable so the
// whole dispatcher can be driven from tests without a terminal.
type ExecuteOptions struct {
	Argv       []string
	Build      BuildInfo
	BinaryName string
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

// NewRoot builds the command tree. The administration groups are attached
// only when --admin was passed, so an ordinary invocation cannot reach them
// by accident.
func NewRoot(admin bool) *Command {
	root := &Command{Name: "cashp-cli", Summary: "cashp control panel client"}

	root.Subcommands = append(root.Subcommands, newAuthCommands()...)
	root.Subcommands = append(root.Subcommands, newServerCommands()...)
	for _, resource := range Resources {
		if resource.AdminOnly && !admin {
			continue
		}
		root.Subcommands = append(root.Subcommands, buildResourceCommand(resource))
	}
	if admin {
		for _, command := range newAdminCommands() {
			existing := root.Lookup(command.Name)
			if existing == nil {
				root.Subcommands = append(root.Subcommands, command)
				continue
			}
			for _, sub := range command.Subcommands {
				if existing.Lookup(sub.Name) == nil {
					existing.Subcommands = append(existing.Subcommands, sub)
				}
			}
		}
	}
	return root
}

// Execute runs one CLI invocation and returns the process exit code.
func Execute(opts ExecuteOptions) int {
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	stdin := opts.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}

	binaryName := opts.BinaryName
	if binaryName == "" {
		binaryName = displayName()
	}

	globals, positional, err := ParseGlobals(opts.Argv)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		fmt.Fprintf(stderr, "Run '%s --help' for usage.\n", binaryName)
		return ExitUsage
	}

	isTTY := false
	if file, ok := stdout.(*os.File); ok {
		isTTY = term.IsTTY(file)
	}

	if globals.Version {
		fmt.Fprintf(stdout, "%s %s (commit %s, built %s)\n",
			binaryName, opts.Build.Version, opts.Build.CommitID, opts.Build.BuildDate)
		return ExitOK
	}

	root := NewRoot(globals.Admin)

	if globals.ShellSet {
		action, shell := splitShellArgs(globals.Shell, positional)
		if err := HandleShell(stdout, binaryName, action, shell, root); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return ExitUsage
		}
		return ExitOK
	}

	if globals.Help && len(positional) == 0 {
		if err := PrintRootHelp(stdout, binaryName, opts.Build.Version, root); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return ExitGeneral
		}
		return ExitOK
	}

	configPath := paths.ResolveConfigPath(globals.Config)
	cfg, err := settings.Load(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return ExitConfig
	}
	applyGlobalsToConfig(globals, cfg)

	writer := output.New(output.Options{
		Out:     stdout,
		ErrOut:  stderr,
		Format:  resolveFormat(globals, cfg),
		Color:   output.ResolveColor(resolveColorFlag(globals, cfg), isTTY),
		Unicode: output.ResolveUnicode(cfg.TUI.Unicode, isTTY),
		Quiet:   globals.Quiet || cfg.Output.Quiet,
	})

	if err := paths.EnsureDirs(); err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return ExitConfig
	}

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx := &Context{
		Ctx:         rootCtx,
		Globals:     globals,
		Config:      cfg,
		ConfigPath:  configPath,
		Out:         writer,
		Version:     opts.Build.Version,
		BinaryName:  binaryName,
		Stdin:       stdin,
		Interactive: isTTY && term.Interactive(),
		Stdout:      stdout,
		Argv:        opts.Argv,
	}
	ctx.newClient = func() (*api.Client, error) { return buildClient(ctx) }

	if globals.UpdateSet {
		return report(ctx, HandleUpdate(ctx, globals.Update))
	}

	if len(positional) == 0 {
		if strings.TrimSpace(cfg.Server.Primary) == "" {
			return report(ctx, RunSetupWizard(ctx))
		}
		if ctx.Interactive {
			return report(ctx, RunInteractive(ctx, root))
		}
		if err := PrintRootHelp(stdout, binaryName, opts.Build.Version, root); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return ExitGeneral
		}
		return ExitOK
	}

	return report(ctx, Dispatch(ctx, root, positional))
}

// Dispatch walks the command tree and runs the selected leaf.
func Dispatch(ctx *Context, root *Command, args []string) error {
	if len(args) == 0 {
		return PrintRootHelp(ctx.Stdout, ctx.BinaryName, ctx.Version, root)
	}

	command := root
	parents := make([]string, 0, len(args))

	for len(args) > 0 {
		next := command.Lookup(args[0])
		if next == nil {
			break
		}
		if command != root {
			parents = append(parents, command.Name)
		}
		command = next
		args = args[1:]
	}

	if command == root {
		return usagef("unknown command: %s", args[0])
	}

	if len(args) > 0 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		return PrintCommandHelp(ctx.Stdout, ctx.BinaryName, parents, command)
	}
	if ctx.Globals.Help {
		return PrintCommandHelp(ctx.Stdout, ctx.BinaryName, parents, command)
	}

	if command.Run == nil {
		if len(args) > 0 {
			return usagef("unknown subcommand %q for %q", args[0], command.Name)
		}
		return PrintCommandHelp(ctx.Stdout, ctx.BinaryName, parents, command)
	}

	if command.NeedsClient {
		if _, err := ctx.APIClient(); err != nil {
			return err
		}
	}

	return command.Run(ctx, args)
}

// report turns a command error into an exit code, printing a user-safe
// message and discarding a credential the server has rejected.
func report(ctx *Context, err error) int {
	if err == nil {
		return ExitOK
	}

	var usageErr *UsageError
	if errors.As(err, &usageErr) {
		ctx.Out.Error("error: %s", usageErr.Message)
		ctx.Out.Warn("Run '%s --help' for usage.", ctx.BinaryName)
		return ExitUsage
	}

	if errors.Is(err, auth.ErrNoToken) {
		ctx.Out.Error("error: no API token found")
		ctx.Out.Warn("Run '%s login' or pass --token.", ctx.BinaryName)
		return ExitAuth
	}
	if errors.Is(err, paths.ErrInsecurePerms) {
		ctx.Out.Error("error: %s", err)
		ctx.Out.Warn("Fix it with: chmod 600 %s", ctx.ConfigPath)
		return ExitConfig
	}

	apiErr, ok := api.AsError(err)
	if !ok {
		ctx.Out.Error("error: %s", err)
		return ExitGeneral
	}

	if apiErr.IsTokenRejected() {
		// The credential is dead. Drop it so the next run asks for a fresh
		// one instead of replaying a revoked token, and never auto-retry.
		if clearErr := auth.Clear(); clearErr != nil {
			ctx.Out.Warn("warning: could not remove the stored token")
		}
		ctx.Out.Error("error: your API token is no longer valid")
		ctx.Out.Warn("Run '%s login' to authenticate again.", ctx.BinaryName)
		return ExitAuth
	}

	ctx.Out.Error("error: %s", apiErr.Error())
	switch apiErr.Kind {
	case api.KindConnection:
		ctx.Out.Warn("  Check your network connection and the server address.")
		ctx.Out.Warn("  Use --server URL to talk to a different server.")
	case api.KindAuth:
		ctx.Out.Warn("  Run '%s login' or pass --token.", ctx.BinaryName)
	case api.KindConfig:
		ctx.Out.Warn("  Check %s or pass the value on the command line.", ctx.ConfigPath)
	}
	return api.ExitCode(apiErr)
}

// buildClient assembles the API client from config, flags and the resolved
// credential. A missing credential is not fatal here: unauthenticated
// endpoints such as autodiscover and the health probes still work.
func buildClient(ctx *Context) (*api.Client, error) {
	server := firstNonEmpty(ctx.Globals.Server, ctx.Config.Server.Primary)
	if strings.TrimSpace(server) == "" {
		return nil, &api.Error{
			Kind:    api.KindConfig,
			Message: "no server configured; run '" + ctx.BinaryName + " login' or pass --server URL",
		}
	}
	if err := api.ValidateServerURL(server); err != nil {
		return nil, err
	}

	resolved, err := auth.Resolve(auth.Options{
		Flag:            ctx.Globals.Token,
		FlagFile:        ctx.Globals.TokenFile,
		ConfigToken:     ctx.Config.Auth.Token,
		ConfigTokenFile: ctx.Config.Auth.TokenFile,
	})
	if err != nil && !errors.Is(err, auth.ErrNoToken) {
		return nil, err
	}

	if !resolved.Empty() {
		if auth.IsAgentToken(resolved.Value) {
			return nil, &api.Error{
				Kind:    api.KindAuth,
				Message: "that is an agent token; agent tokens cannot be used with the CLI",
			}
		}
		if ctx.Globals.Debug {
			// Only the masked form is ever printed or logged.
			ctx.Out.Detail("using token %s from %s", resolved.Masked(), resolved.Source)
		}
	}

	return api.New(clientOptions(ctx, server, resolved.Value))
}

// clientOptions builds the transport options shared by every code path that
// constructs a client.
func clientOptions(ctx *Context, server, token string) api.Options {
	return api.Options{
		Primary:    server,
		Cluster:    ctx.Config.Server.Cluster,
		Token:      token,
		Version:    ctx.Version,
		APIVersion: ctx.Config.Server.APIVersion,
		Timeout:    parseDuration(ctx.Config.Server.Timeout, 30*time.Second),
		Retry:      ctx.Config.Server.Retry,
		RetryDelay: parseDuration(ctx.Config.Server.RetryDelay, time.Second),
		Debug:      ctx.Globals.Debug || ctx.Config.Debug,
		DebugLog: func(format string, args ...any) {
			ctx.Out.Detail(format, args...)
		},
	}
}

// applyGlobalsToConfig folds flag values into the loaded configuration so
// every later stage reads one source of truth.
func applyGlobalsToConfig(globals *Globals, cfg *settings.Config) {
	if server := strings.TrimSpace(globals.Server); server != "" && api.IsValidServerURL(server) {
		cfg.Server.Primary = server
	}
	if globals.Verbose {
		cfg.Output.Verbose = true
	}
	if globals.Debug {
		cfg.Debug = true
	}
}

// resolveFormat picks the output format from --json, then --output, then
// the configured default.
func resolveFormat(globals *Globals, cfg *settings.Config) string {
	if globals.JSON {
		return output.FormatJSON
	}
	if globals.Output != "" {
		return globals.Output
	}
	if cfg.Output.Format != "" {
		return cfg.Output.Format
	}
	return output.FormatTable
}

// resolveColorFlag prefers --color, falling back to the configured value.
func resolveColorFlag(globals *Globals, cfg *settings.Config) string {
	if strings.TrimSpace(globals.Color) != "" {
		return globals.Color
	}
	return cfg.Output.Color
}

// splitShellArgs resolves the --shell action and optional shell name,
// accepting both "--shell completions bash" and "--shell=completions bash".
func splitShellArgs(flagValue string, positional []string) (action string, shell string) {
	action = strings.TrimSpace(flagValue)
	remaining := positional
	if action == "" && len(remaining) > 0 {
		action = remaining[0]
		remaining = remaining[1:]
	}
	if len(remaining) > 0 {
		shell = remaining[0]
	}
	return action, shell
}

// parseDuration parses a config duration, falling back on an invalid value
// rather than failing the whole invocation.
func parseDuration(value string, fallback time.Duration) time.Duration {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

// displayName is the actual filename of this binary, so a renamed copy
// documents itself correctly.
func displayName() string {
	if len(os.Args) == 0 || os.Args[0] == "" {
		return "cashp-cli"
	}
	return filepath.Base(os.Args[0])
}
