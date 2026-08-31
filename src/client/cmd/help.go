package cmd

import (
	"fmt"
	"io"
	"strings"
)

// helpColumn is where flag and command descriptions start.
const helpColumn = 40

// PrintRootHelp writes the top-level help text. The binary name shown is
// always the actual filename so a renamed binary documents itself.
func PrintRootHelp(out io.Writer, binaryName, version string, root *Command) error {
	var builder strings.Builder

	fmt.Fprintf(&builder, "%s %s - CLI for cashp\n\n", binaryName, version)
	builder.WriteString("Usage:\n")
	fmt.Fprintf(&builder, "  %s <command> [subcommand] [args] [flags]\n", binaryName)
	fmt.Fprintf(&builder, "  %s\n\n", binaryName)

	builder.WriteString("Flags:\n")
	for _, line := range rootFlagHelp() {
		builder.WriteString(line)
		builder.WriteString("\n")
	}

	builder.WriteString("\nAdministration (requires an admin token):\n")
	builder.WriteString(helpLine("--admin <command>", "Run a command in the administration namespace"))
	builder.WriteString("\n")

	builder.WriteString("\nCommands:\n")
	for _, command := range root.SortedSubcommands() {
		builder.WriteString(helpLine("  "+command.Name, command.Summary))
		builder.WriteString("\n")
	}

	fmt.Fprintf(&builder, "\nShells: %s\n", strings.Join(SupportedShells, ", "))
	builder.WriteString("\nRun without arguments to start interactive mode.\n")
	fmt.Fprintf(&builder, "Run '%s <command> --help' for detailed help on any command.\n", binaryName)

	_, err := io.WriteString(out, builder.String())
	return err
}

// rootFlagHelp returns the global flag help lines.
func rootFlagHelp() []string {
	return []string{
		helpLine("-h, --help", "Show help"),
		helpLine("-v, --version", "Show version"),
		helpLine("--shell completions [SHELL]", "Print shell completions (auto-detected if omitted)"),
		helpLine("--shell init [SHELL]", "Print the shell init line for your rc file"),
		helpLine("--shell help", "Show shell integration help"),
		"",
		helpLine("--server URL", "Server URL (default: from cli.yml)"),
		helpLine("--token TOKEN", "API token for authentication"),
		helpLine("--token-file FILE", "Read the API token from a file"),
		helpLine("--user NAME", "Target user or org (auto-detect, @user, +org)"),
		helpLine("--config NAME", "Config file or profile name (default: cli.yml)"),
		helpLine("--output FORMAT", "Output format: table, json, yaml, plain, csv"),
		helpLine("--json", "Shorthand for --output json"),
		helpLine("--limit N", "Maximum number of items to list"),
		helpLine("--color auto|yes|no", "Colour output (default: auto)"),
		helpLine("--lang CODE", "Language for output (default: auto)"),
		helpLine("--quiet", "Suppress informational output"),
		helpLine("--verbose", "Show additional detail"),
		helpLine("--debug", "Show request tracing on stderr"),
		helpLine("--yes", "Assume yes for confirmation prompts"),
		helpLine("--update [check|yes]", "Check for or install a newer CLI"),
	}
}

// PrintCommandHelp writes help for one command or command group.
func PrintCommandHelp(out io.Writer, binaryName string, parents []string, command *Command) error {
	var builder strings.Builder

	path := command.Path(parents)
	fmt.Fprintf(&builder, "%s %s - %s\n\n", binaryName, path, command.Summary)

	builder.WriteString("Usage:\n")
	if command.IsLeaf() {
		fmt.Fprintf(&builder, "  %s %s %s\n", binaryName, path, command.Args)
	} else {
		fmt.Fprintf(&builder, "  %s %s <subcommand> [args] [flags]\n", binaryName, path)
	}

	if command.Long != "" {
		builder.WriteString("\n")
		builder.WriteString(command.Long)
		builder.WriteString("\n")
	}

	if !command.IsLeaf() {
		builder.WriteString("\nSubcommands:\n")
		for _, sub := range command.SortedSubcommands() {
			builder.WriteString(helpLine("  "+sub.Name+" "+sub.Args, sub.Summary))
			builder.WriteString("\n")
		}
	}

	fmt.Fprintf(&builder, "\nRun '%s --help' for global flags.\n", binaryName)

	_, err := io.WriteString(out, builder.String())
	return err
}

// helpLine pads a term so descriptions line up in a narrow terminal.
func helpLine(term, description string) string {
	trimmed := strings.TrimRight(term, " ")
	if len([]rune(trimmed)) >= helpColumn {
		return trimmed + "  " + description
	}
	padding := helpColumn - len([]rune(trimmed))
	return trimmed + strings.Repeat(" ", padding) + description
}
